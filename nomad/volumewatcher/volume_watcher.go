package volumewatcher

import (
	"context"
	"fmt"
	"sync"

	log "github.com/hashicorp/go-hclog"
	memdb "github.com/hashicorp/go-memdb"
	multierror "github.com/hashicorp/go-multierror"
	cstructs "github.com/hashicorp/nomad/client/structs"
	"github.com/hashicorp/nomad/nomad/state"
	"github.com/hashicorp/nomad/nomad/structs"
	"golang.org/x/time/rate"
)

// volumeWatcher is used to watch a single volume and trigger the
// scheduler when allocation health transitions.
type volumeWatcher struct {
	// queryLimiter is used to limit the rate of blocking queries
	queryLimiter *rate.Limiter

	// claimUpdater holds the methods required to update claims
	claimUpdater

	nodeForControllerPlugin nodeForControllerPluginFn

	// state is the state that is watched for state changes.
	state *state.StateStore

	// volumeID is the volume's ID being watched
	volumeID string

	// updateCh is triggered when there is an updated volume
	updateCh chan struct{}

	// v is the volume being watched
	v *structs.CSIVolume

	rpc ClientRPC

	logger log.Logger
	ctx    context.Context
	exitFn context.CancelFunc
	wLock  sync.RWMutex
}

// newVolumeWatcher returns a volume watcher that is used to watch
// volumes
func newVolumeWatcher(parent *Watcher, vol *structs.CSIVolume) *volumeWatcher {
	ctx, exitFn := context.WithCancel(parent.ctx)
	w := &volumeWatcher{
		queryLimiter:            parent.queryLimiter,
		volumeID:                vol.ID,
		updateCh:                make(chan struct{}, 1),
		claimUpdater:            parent,
		v:                       vol,
		nodeForControllerPlugin: parent.nodeForControllerPlugin,
		state:                   parent.state,
		rpc:                     parent.rpc,
		logger:                  parent.logger.With("volume_id", vol.ID, "namespace", vol.Namespace),
		ctx:                     ctx,
		exitFn:                  exitFn,
	}

	// Start the long lived watcher that scans for allocation updates
	go w.watch()

	return w
}

// updateVolume is used to update the tracked volume.
func (w *volumeWatcher) Notify(v *structs.CSIVolume) {
	w.wLock.Lock()
	defer w.wLock.Unlock()

	// Update and trigger
	w.v = v
	// TODO: why is this a select?
	select {
	case w.updateCh <- struct{}{}:
	default:
	}
}

// StopWatch stops watching the volume. This should be called whenever a
// volume's claims are fully reaped or the watcher is no longer needed.
func (w *volumeWatcher) Stop() {
	w.exitFn()
}

// getVolume returns the tracked volume.
func (w *volumeWatcher) getVolume() *structs.CSIVolume {
	w.wLock.RLock()
	defer w.wLock.RUnlock()
	return w.v
}

// watch is the long-running function that watches for changes to a volume.
// Each pass steps the volume's claims through the various states of reaping
// until the volume has no more claims eligible to be reaped.
func (vw *volumeWatcher) watch() {
	// TODO: add a deadline
	// TODO: probably still need to wire this all up for step-down, etc.
	for {
		select {
		case <-vw.ctx.Done():
			return
		case <-vw.updateCh:
			vol := vw.v.Copy() // TODO copy here or elsewhere?
			vw.volumeReap(vol)
		}
	}

}
func (vw *volumeWatcher) volumeReap(vol *structs.CSIVolume) {
	vw.volumeReapImpl(vol) // TODO: swallows errors!?
}

func (vw *volumeWatcher) volumeReapImpl(vol *structs.CSIVolume) error {

	// TODO: assumption here is we've denormalized first, is that likely?
	// TODO: no!

	var result *multierror.Error
	nodeClaims := map[string]int{} // node IDs -> count

	collect := func(allocs map[string]*structs.Allocation, claims map[string]*structs.CSIVolumeClaim) {
		for allocID, alloc := range allocs {
			claim, ok := claims[allocID]
			if !ok {
				err := fmt.Errorf(
					"alloc read claims corrupt: %s missing from read claims", allocID)
				result = multierror.Append(result, err)
				continue // TODO: we should never see this, but what can we do with it?
			}
			nodeClaims[claim.NodeID]++
			if alloc == nil || alloc.Terminated() {
				// only overwrite the PastClaim if this is new,
				// so that we can track state between subsequent
				// calls of VolumeReap
				if _, exists := vol.PastClaims[claim.AllocationID]; !exists {
					claim.State = structs.CSIVolumeClaimStateTaken
					vol.PastClaims[claim.AllocationID] = claim
				}
			}
		}
	}

	collect(vol.ReadAllocs, vol.ReadClaims)
	collect(vol.WriteAllocs, vol.WriteClaims)

	for _, claim := range vol.PastClaims {
		for {
			switch claim.State {
			case structs.CSIVolumeClaimStateTaken:
				err := vw.nodeDetach(vol, claim)
				if err != nil {
					result = multierror.Append(result, err)
					break
				} else {
					nodeClaims[claim.NodeID]-- // TODO this is ugly still
				}
			case structs.CSIVolumeClaimStateNodeDetached:
				err := vw.controllerDetach(vol, claim, nodeClaims)
				if err != nil {
					result = multierror.Append(result, err)
					break
				}
			case structs.CSIVolumeClaimStateReadyToFree:
				err := vw.freeClaim(vol, claim)
				if err != nil {
					result = multierror.Append(result, err)
					break
				}
			case structs.CSIVolumeClaimStateFreed:
				vw.logger.Debug("volume claim freed",
					"volID", vol.ID, "allocID", claim.AllocationID, "nodeID", claim.NodeID)
				break
			default:
				result = multierror.Append(result, fmt.Errorf("invalid state"))
				break
			}
		}

	}

	return result.ErrorOrNil()

}

// TODO: is this useful?
// // nodeClaimCount returns a map of node IDs to the number of
// // claims (current or past but unreaped) for the volume.
// func nodeClaimCount(v *structs.CSIVolume) map[string]int {
// 	nodeClaims := map[string]int{}
// 	for _, claim := range v.ReadClaims {
// 		nodeClaims[claim.NodeID]++
// 	}
// 	for _, claim := range v.WriteClaims {
// 		nodeClaims[claim.NodeID]++
// 	}
// 	for _, claim := range v.PastClaims {
// 		nodeClaims[claim.NodeID]++
// 	}
// 	return nodeClaims
// }

func (vw *volumeWatcher) nodeDetach(vol *structs.CSIVolume, claim *structs.CSIVolumeClaim) error {
	// (1) NodePublish / NodeUnstage must be completed before controller
	// operations or releasing the claim.
	nReq := &cstructs.ClientCSINodeDetachVolumeRequest{
		PluginID:       vol.PluginID,
		VolumeID:       vol.ID,
		ExternalID:     vol.RemoteID(),
		AllocID:        claim.AllocationID,
		NodeID:         claim.NodeID,
		AttachmentMode: vol.AttachmentMode, // TODO: should this be claim-specific?
		AccessMode:     vol.AccessMode,     // TODO: should this be claim-specific?
		ReadOnly:       claim.Mode == structs.CSIVolumeClaimRead,
	}

	err := vw.rpc.NodeDetachVolume(nReq,
		&cstructs.ClientCSINodeDetachVolumeResponse{})
	if err != nil {
		return err
	}
	claim.State = structs.CSIVolumeClaimStateNodeDetached
	return vw.syncClaim(vol, claim)
}

func (vw *volumeWatcher) controllerDetach(vol *structs.CSIVolume, claim *structs.CSIVolumeClaim, nodeClaims map[string]int) error {
	// (2) we only emit the controller unpublish if no other allocs
	// on the node need it, but we also only want to make this
	// call at most once per node

	if !vol.ControllerRequired || nodeClaims[claim.NodeID] > 1 {
		claim.State = structs.CSIVolumeClaimStateReadyToFree
		return vw.syncClaim(vol, claim)
	}

	// we need to get the CSI Node ID, which is not the same as
	// the Nomad Node ID
	ws := memdb.NewWatchSet()
	targetNode, err := vw.state.NodeByID(ws, claim.NodeID)
	if err != nil {
		return err
	}
	if targetNode == nil {
		return fmt.Errorf("%s: %s", structs.ErrUnknownNodePrefix, claim.NodeID)
	}
	targetCSIInfo, ok := targetNode.CSINodePlugins[vol.PluginID]
	if !ok {
		return fmt.Errorf("failed to find NodeInfo for node: %s", targetNode.ID)
	}

	plug, err := vw.state.CSIPluginByID(ws, vol.PluginID)
	if err != nil {
		return fmt.Errorf("plugin lookup error: %s %v", vol.PluginID, err)
	}
	if plug == nil {
		return fmt.Errorf("plugin lookup error: %s missing plugin", vol.PluginID)
	}

	controllerNodeID, err := vw.nodeForControllerPlugin(vw.state, plug)
	if err != nil || controllerNodeID == "" {
		return err
	}
	cReq := &cstructs.ClientCSIControllerDetachVolumeRequest{
		VolumeID:        vol.RemoteID(),
		ClientCSINodeID: targetCSIInfo.NodeInfo.ID,
	}
	cReq.PluginID = plug.ID
	cReq.ControllerNodeID = controllerNodeID
	err = vw.rpc.ControllerDetachVolume(cReq,
		&cstructs.ClientCSIControllerDetachVolumeResponse{})
	if err != nil {
		return err
	}

	claim.State = structs.CSIVolumeClaimStateReadyToFree
	return vw.syncClaim(vol, claim)
}

func (vw *volumeWatcher) freeClaim(vol *structs.CSIVolume, claim *structs.CSIVolumeClaim) error {
	// (3) release the claim from the state store, allowing it to be rescheduled
	claim.State = structs.CSIVolumeClaimStateFreed
	return vw.syncClaim(vol, claim)
}

func (vw *volumeWatcher) syncClaim(vol *structs.CSIVolume, claim *structs.CSIVolumeClaim) error {
	req := structs.CSIVolumeClaimRequest{
		VolumeID:     vol.ID,
		AllocationID: claim.AllocationID,
		NodeID:       claim.NodeID,
		Claim:        claim.Mode,
		State:        claim.State,
		WriteRequest: structs.WriteRequest{
			// Region:    vol.Region, // TODO shouldn't volumes have regions?
			Namespace: vol.Namespace,
			// AuthToken: vw.srv.getLeaderAcl(), // TODO this only runs on leader, not an RPC
		},
	}

	// TODO: probably need the index?
	_, err := vw.updateClaims([]structs.CSIVolumeClaimRequest{req})
	return err
}
