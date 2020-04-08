package volumes

import (
	"context"
	"fmt"
	"time"

	log "github.com/hashicorp/go-hclog"
	memdb "github.com/hashicorp/go-memdb"
	multierror "github.com/hashicorp/go-multierror"
	"github.com/hashicorp/nomad/nomad/structs"
)

// VolumeReaper will poll periodically on the leader and also accept
// messages on a channel.  When polled, trigger a VolumeReap, which
// reaps claims for all terminated or nil allocs.  This covers 'nomad
// job stop', 'nomad job stop -purge', and normal alloc termination.
type VolumeReaper struct {
	srv    *Server
	logger log.Logger

	syncPeriod time.Duration
	updateCh   <-chan *structs.CSIVolume // TODO: this almost certainly be buffered

	ctx    context.Context
	exitFn context.CancelFunc
}

func (vr *VolumeReaper) VolumeReap(vol *structs.CSIVolume) {
	vr.updateCh <- vol

}

// TODO: need to wire this all up for step-down, etc.
func (vr *VolumeReaper) Run() {
	timer := time.NewTimer(0) // ensure we sync immediately in first pass
	for {
		select {
		case <-timer.C:
			timer.Reset(vr.syncPeriod)
		case vol := c.updateCh:
			vr.volumeReapImpl(vol)
		case <-vr.ctx.Done():
			return
		}
	}
}

func (vr *VolumeReaper) volumeReapImpl(vol *structs.CSIVolume) error {

	// TODO: assumption here is we've denormalized first, is that likely?

	ws := memdb.NewWatchSet()
	var result *multierror.Error
	nodeClaims := map[string]int{} // node IDs -> count

	for _, alloc := range vol.ReadAllocs {
		if alloc == nil || alloc.Terminated() {
			claim, ok := vol.ReadClaims[alloc.ID]
			if !ok {
				err := fmt.Errorf(
					"alloc read claims corrupt: %s missing from read claims", alloc.ID)
				result = multierror.Append(result, err)
				continue
			}
			// only overwrite the PastClaim if this is new,
			// so that we can track state between subsequent
			// calls of VolumeReap
			if _, exists := vol.PastClaims[alloc.ID]; !exists {
				claim.State = CSIVolumeClaimStateTaken
				vol.PastClaims[alloc.ID] = claim
			}
			delete(vol.ReadClaims, claim)
		} else {
			nodeClaims[alloc.NodeID]++
		}
	}

	for _, alloc := range vol.WriteAllocs {
		if alloc == nil || alloc.Terminated() {
			claim, ok := vol.WriteClaims[alloc.ID]
			if !ok {
				err := fmt.Errorf(
					"alloc read claims corrupt: %s missing from write claims", alloc.ID)
				result = multierror.Append(result, err)
				continue
			}
			// only overwrite the PastClaim if this is new,
			// so that we can track state between subsequent
			// calls of VolumeReap
			if _, exists := vol.PastClaims[alloc.ID]; !exists {
				claim.State = CSIVolumeClaimStateTaken
				vol.PastClaims[alloc.ID] = claim
			}
			delete(vol.WriteClaims, claim)
		} else {
			nodeClaims[alloc.NodeID]++
		}
	}

	// TODO: partition these? is this likely to be a problem in practice?
	for _, claim := range vol.PastClaims {
		for {
			switch claim.State {
			case CSIVolumeClaimStateTaken:
				err = vr.nodeDetach(vol, claim, &nodeClaims)
				if err != nil {
					result = multierror.Append(result, err)
					break
				}
			case CSIVolumeClaimStateNodeDetached:
				err = vr.controllerDetach(vol, claim, &nodeClaims)
				if err != nil {
					result = multierror.Append(result, err)
					break
				}
			case CSIVolumeClaimStateReadyToFree:
				err = vr.freeClaim(vol, claim)
				if err != nil {
					result = multierror.Append(result, err)
					break
				}
			case CSIVolumeClaimStateFreed:
				vr.logger.Debug("volume claim freed",
					"volID", vol.ID, "allocID", claim.AllocID, "nodeID", claim.NodeID)
				break
			default:
				result = multierror.Append(result, fmt.Errorf("invalid state"))
				break
			}
		}

	}

	return result.ErrorOrNil()

}

func (vr *VolumeReaper) nodeDetach(vol *structs.CSIVolume, claim *structs.CSIVolumeClaim, nodeClaims *map[string]int) error {
	// (1) NodePublish / NodeUnstage must be completed before controller
	// operations or releasing the claim.
	nReq := &cstructs.ClientCSINodeDetachVolumeRequest{
		PluginID:       vol.PluginID,
		VolumeID:       vol.ID,
		ExternalID:     vol.RemoteID(),
		AllocID:        claim.AllocID,
		NodeID:         claim.NodeID,
		AttachmentMode: vol.AttachmentMode, // should this be claim-specific?
		AccessMode:     vol.AccessMode,     // should this be claim-specific?
		ReadOnly:       claim.ReadOnly,
	}

	err := vr.srv.RPC("ClientCSI.NodeDetachVolume", nReq,
		&cstructs.ClientCSINodeDetachVolumeResponse{})
	if err != nil {
		return err
	}
	claim.State = CSIVolumeClaimStateNodeDetached
	nodeClaims[claim.NodeID]--
	return vr.syncClaim(vol, claim)
}

func (vr *VolumeReaper) controllerDetach(vol *structs.CSIVolume, claim *structs.CSIVolumeClaim, nodeClaims *map[string]int) error {
	// (2) we only emit the controller unpublish if no other allocs
	// on the node need it, but we also only want to make this
	// call at most once per node

	if !vol.ControllerRequired || nodeClaims[claim.NodeID] > 1 {
		claim.State = CSIVolumeClaimStateReadyToFree
		return vr.syncClaim(vol, claim)
	}

	// we need to get the CSI Node ID, which is not the same as
	// the Nomad Node ID
	ws := memdb.NewWatchSet()
	targetNode, err := srv.State().NodeByID(ws, nodeID)
	if err != nil {
		return err
	}
	if targetNode == nil {
		return fmt.Errorf("%s: %s", structs.ErrUnknownNodePrefix, nodeID)
	}
	targetCSIInfo, ok := targetNode.CSINodePlugins[vol.PluginID]
	if !ok {
		return fmt.Errorf("failed to find NodeInfo for node: %s", targetNode.ID)
	}

	plug, err := s.CSIPluginByID(ws, vol.PluginID)
	if err != nil {
		return fmt.Errorf("plugin lookup error: %s %v", vol.PluginID, err)
	}
	if plug == nil {
		return fmt.Errorf("plugin lookup error: %s missing plugin", vol.PluginID)
	}

	controllerNodeID, err := nodeForControllerPlugin(srv.State(), plug)
	if err != nil || controllerNodeID == "" {
		return err
	}
	cReq := &cstructs.ClientCSIControllerDetachVolumeRequest{
		VolumeID:        vol.RemoteID(),
		ClientCSINodeID: targetCSIInfo.NodeInfo.ID,
	}
	cReq.PluginID = args.plug.ID
	cReq.ControllerNodeID = controllerNodeID
	err = srv.RPC("ClientCSI.ControllerDetachVolume", cReq,
		&cstructs.ClientCSIControllerDetachVolumeResponse{})
	if err != nil {
		return err
	}

	claim.State = CSIVolumeClaimStateReadyToFree
	return vr.syncClaim(vol, claim)
}

func (vr *VolumeReaper) freeClaim(vol *structs.CSIVolume, claim *structs.CSIVolumeClaim, nodeClaims map[string]int) error {
	// (3) release the claim from the state store, allowing it to be rescheduled
	claim.State = CSIVolumeClaimStateFreed
	return vr.syncClaim(vol, claim)
}

func (vr *VolumeReaper) syncClaim(vol *structs.CSIVolume, claim *structs.CSIVolumeClaim) error {
	req := &structs.CSIVolumeClaimRequest{
		VolumeID:     vol.ID,
		AllocationID: claim.AllocID,
		NodeID:       claim.NodeID,
		Claim:        claim.Release,
		State:        claim.State,
		WriteRequest: structs.WriteRequest{
			// Region:    vol.Region, // TODO shouldn't volumes have regions?
			Namespace: vol.Namespace,
			AuthToken: vr.srv.getLeaderAcl(), // TODO push this up to constructor
		},
	}
	resp, index, err := vr.srv.raftApply(structs.CSIVolumeClaimRequestType, req)
	if err != nil {
		vr.logger.Error("csi raft apply failed", "error", err, "method", "claim")
		return err
	}
	if respErr, ok := resp.(error); ok {
		return respErr
	}
	return nil

}
