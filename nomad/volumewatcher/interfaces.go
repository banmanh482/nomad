package volumewatcher

import (
	cstructs "github.com/hashicorp/nomad/client/structs"
	"github.com/hashicorp/nomad/nomad/state"
	"github.com/hashicorp/nomad/nomad/structs"
)

// VolumeRaftEndpoints exposes the volume watcher to a set of functions
// to apply data transforms via Raft.
type VolumeRaftEndpoints interface {

	// UpsertVolumeClaims applys a batch of claims to raft
	UpsertVolumeClaims(*structs.CSIVolumeClaimBatchRequest) (uint64, error)
}

// TODO: note why we do this to avoid circular references
// ClientRPC is a minimal interface of the Server, intended as
// an aid for testing logic surrounding server-to-server or
// server-to-client RPC calls
type ClientRPC interface {
	ControllerDetachVolume(args *cstructs.ClientCSIControllerDetachVolumeRequest, reply *cstructs.ClientCSIControllerDetachVolumeResponse) error
	NodeDetachVolume(args *cstructs.ClientCSINodeDetachVolumeRequest, reply *cstructs.ClientCSINodeDetachVolumeResponse) error
}

// nodeForControllerPlugin returns the node ID for a random controller
// to load-balance long-blocking RPCs across client nodes.
type nodeForControllerPluginFn func(state *state.StateStore, plugin *structs.CSIPlugin) (string, error)

// claimUpdater is the set of functions required to update claims on
// behalf of a volume (used to wrap batch updates so that we can test
// volumeWatcher methods synchronously without batching)
// TODO: explain this better
// TODO: note it can't be the same as either of the other two above
type claimUpdater interface {
	updateClaims(claims []structs.CSIVolumeClaimRequest) (uint64, error)
}
