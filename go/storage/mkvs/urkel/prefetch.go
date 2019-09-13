package urkel

import (
	"bytes"
	"context"

	"github.com/oasislabs/ekiden/go/storage/mkvs/urkel/node"
	"github.com/oasislabs/ekiden/go/storage/mkvs/urkel/syncer"
)

// PrefetchPrefixes populates the in-memory tree with nodes for keys
// starting with given prefixes.
func (t *Tree) PrefetchPrefixes(ctx context.Context, prefixes [][]byte, limit uint16) error {
	t.cache.Lock()
	defer t.cache.Unlock()

	if t.cache.isClosed() {
		return ErrClosed
	}

	// TODO: Can we avoid fetching items that we already have?

	return t.cache.remoteSync(
		ctx,
		t.cache.pendingRoot,
		func(ctx context.Context, ptr *node.Pointer, rs syncer.ReadSyncer) (*syncer.Proof, error) {
			rsp, err := rs.SyncGetPrefixes(ctx, &syncer.GetPrefixesRequest{
				Tree: syncer.TreeID{
					Root:     t.cache.syncRoot,
					Position: t.cache.syncRoot.Hash,
				},
				Prefixes: prefixes,
				Limit:    limit,
			})
			if err != nil {
				return nil, err
			}
			return &rsp.Proof, nil
		},
	)
}

// SyncGetPrefixes fetches all keys under the given prefixes and returns
// the corresponding proofs.
func (t *Tree) SyncGetPrefixes(ctx context.Context, request *syncer.GetPrefixesRequest) (*syncer.ProofResponse, error) {
	t.cache.Lock()
	defer t.cache.Unlock()

	if t.cache.isClosed() {
		return nil, ErrClosed
	}
	if !request.Tree.Root.Equal(&t.cache.syncRoot) {
		return nil, syncer.ErrInvalidRoot
	}
	if !t.cache.pendingRoot.IsClean() {
		return nil, syncer.ErrDirtyRoot
	}

	// First, trigger same prefetching locally if a remote read syncer
	// is available. This is needed to ensure that the same optimization
	// carries on to the next layer.
	if t.cache.rs != syncer.NopReadSyncer {
		err := t.PrefetchPrefixes(ctx, request.Prefixes, request.Limit)
		if err != nil {
			return nil, err
		}
	}

	it := t.NewIterator(ctx)
	defer it.Close()

	pb := syncer.NewProofBuilder(request.Tree.Root.Hash)
	it.(*treeIterator).proofBuilder = pb

	var total int
prefixLoop:
	for _, prefix := range request.Prefixes {
		it.Seek(prefix)
		if it.Err() != nil {
			return nil, it.Err()
		}
		for ; it.Valid(); total++ {
			if total >= int(request.Limit) {
				break prefixLoop
			}
			if !bytes.HasPrefix(it.Key(), prefix) {
				break
			}
			it.Next()
		}
		if it.Err() != nil {
			return nil, it.Err()
		}
	}

	proof, err := pb.Build(ctx)
	if err != nil {
		return nil, err
	}

	return &syncer.ProofResponse{
		Proof: *proof,
	}, nil
}