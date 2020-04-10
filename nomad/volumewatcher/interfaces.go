package volumewatcher

import (
	"github.com/hashicorp/nomad/nomad/structs"
)

// VolumeRaftEndpoints exposes the volume watcher to a set of functions
// to apply data transforms via Raft.
type VolumeRaftEndpoints interface {

	// UpsertVolumeClaims applys a batch of claims to raft
	UpsertVolumeClaims(*structs.CSIVolumeClaimBatchRequest) (uint64, error)
}

// TODO: note why we do this to avoid circular references
// VolumeRPCServer is a minimal interface of the Server, intended as
// an aid for testing logic surrounding server-to-server or
// server-to-client RPC calls
type VolumeRPCServer interface {
	RPC(method string, args interface{}, reply interface{}) error

	// TODO: maybe don't need this?
	//	State() *state.StateStore
}

// claimUpdater is the set of functions required to update claims on
// behalf of a volume (used to wrap batch updates so that we can test
// volumeWatcher methods synchronously without batching)
// TODO: explain this better
// TODO: note it can't be the same as either of the other two above
type claimUpdater interface {
	updateClaims(claims []structs.CSIVolumeClaimRequest) (uint64, error)
}
