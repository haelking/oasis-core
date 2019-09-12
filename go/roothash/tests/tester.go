// Package tests si a collection of roothash implementation test cases.
package tests

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/oasislabs/ekiden/go/common"
	"github.com/oasislabs/ekiden/go/common/crypto/hash"
	"github.com/oasislabs/ekiden/go/common/crypto/signature"
	"github.com/oasislabs/ekiden/go/common/pubsub"
	epochtime "github.com/oasislabs/ekiden/go/epochtime/api"
	epochtimeTests "github.com/oasislabs/ekiden/go/epochtime/tests"
	registry "github.com/oasislabs/ekiden/go/registry/api"
	registryTests "github.com/oasislabs/ekiden/go/registry/tests"
	"github.com/oasislabs/ekiden/go/roothash/api"
	"github.com/oasislabs/ekiden/go/roothash/api/block"
	"github.com/oasislabs/ekiden/go/roothash/api/commitment"
	"github.com/oasislabs/ekiden/go/runtime/transaction"
	scheduler "github.com/oasislabs/ekiden/go/scheduler/api"
	storage "github.com/oasislabs/ekiden/go/storage/api"
)

const (
	recvTimeout = 5 * time.Second
	nrRuntimes  = 3
)

type runtimeState struct {
	id           string
	rt           *registryTests.TestRuntime
	genesisBlock *block.Block

	computeCommittee  *testCommittee
	mergeCommittee    *testCommittee
	storageCommittee  *testCommittee
	txnSchedCommittee *testCommittee
}

// RootHashImplementationTests exercises the basic functionality of a
// roothash backend.
func RootHashImplementationTests(t *testing.T, backend api.Backend, epochtime epochtime.SetableBackend, scheduler scheduler.Backend, storage storage.Backend, registry registry.Backend) {
	seedBase := []byte("RootHashImplementationTests")

	require := require.New(t)

	// Ensure that we leave the registry empty when we are done.
	rtStates := make([]*runtimeState, 0, nrRuntimes)
	defer func() {
		if len(rtStates) > 0 {
			// This is entity deregistration based, and all of the
			// runtimes used in this test share the entity.
			rtStates[0].rt.Cleanup(t, registry)
		}

		registryTests.EnsureRegistryEmpty(t, registry)
	}()

	// Populate the registry.
	runtimes := make([]*registryTests.TestRuntime, 0, nrRuntimes)
	for i := 0; i < nrRuntimes; i++ {
		t.Logf("Generating runtime: %d", i)
		seed := append([]byte{}, seedBase...)
		seed = append(seed, byte(i))

		rt, err := registryTests.NewTestRuntime(seed, nil)
		require.NoError(err, "NewTestRuntime")

		rtStates = append(rtStates, &runtimeState{
			id: strconv.Itoa(i),
			rt: rt,
		})
		runtimes = append(runtimes, rt)
	}
	registryTests.BulkPopulate(t, registry, runtimes, seedBase)
	for _, rt := range runtimes {
		rt.MustRegister(t, registry)
	}

	// Run the various tests. (Ordering matters)
	for _, v := range rtStates {
		t.Run("GenesisBlock/"+v.id, func(t *testing.T) {
			testGenesisBlock(t, backend, v)
		})
	}
	success := t.Run("EpochTransitionBlock", func(t *testing.T) {
		testEpochTransitionBlock(t, backend, epochtime, scheduler, rtStates)
	})
	if success {
		// It only makes sense to run the SuccessfulRound test in case the
		// EpochTransitionBlock was successful. Otherwise this may leave the
		// committees set to nil and cause a crash.
		t.Run("SucessfulRound", func(t *testing.T) {
			testSucessfulRound(t, backend, storage, rtStates)
		})
	}

	// TODO: Test the various failures.
}

func testGenesisBlock(t *testing.T, backend api.Backend, state *runtimeState) {
	require := require.New(t)

	id := state.rt.Runtime.ID
	ch, sub, err := backend.WatchBlocks(id)
	require.NoError(err, "WatchBlocks")
	defer sub.Close()

	var genesisBlock *block.Block
	select {
	case blk := <-ch:
		header := blk.Block.Header

		require.EqualValues(header.Version, 0, "block version")
		require.EqualValues(0, header.Round, "block round")
		require.Equal(block.Normal, header.HeaderType, "block header type")
		require.True(header.IORoot.IsEmpty(), "block I/O root empty")
		require.True(header.StateRoot.IsEmpty(), "block root hash empty")
		genesisBlock = blk.Block
	case <-time.After(recvTimeout):
		t.Fatalf("failed to receive block")
	}

	blk, err := backend.GetLatestBlock(context.Background(), id)
	require.NoError(err, "GetLatestBlock")
	require.EqualValues(genesisBlock, blk, "retreived block is genesis block")

	// We need to wait for the indexer to index the block. We could have a channel
	// to subscribe to these updates and this would not be needed.
	time.Sleep(1 * time.Second)

	blk, err = backend.GetBlock(context.Background(), id, 0)
	require.NoError(err, "GetBlock")
	require.EqualValues(genesisBlock, blk, "retreived block is genesis block")

	blk, err = backend.GetGenesisBlock(context.Background(), id)
	require.NoError(err, "GetGenesisBlock")
	require.EqualValues(genesisBlock, blk, "retrieved block is genesis block")
}

func testEpochTransitionBlock(t *testing.T, backend api.Backend, epochtime epochtime.SetableBackend, scheduler scheduler.Backend, states []*runtimeState) {
	require := require.New(t)

	// Before an epoch transition there should just be a genesis block.
	for _, v := range states {
		genesisBlock, err := backend.GetLatestBlock(context.Background(), v.rt.Runtime.ID)
		require.NoError(err, "GetLatestBlock")
		require.EqualValues(0, genesisBlock.Header.Round, "genesis block round")

		v.genesisBlock = genesisBlock
	}

	// Advance the epoch, get the committee.
	epoch, err := epochtime.GetEpoch(context.Background(), 0)
	require.NoError(err, "GetEpoch")

	// Subscribe to blocks for all of the runtimes.
	var blkChannels []<-chan *api.AnnotatedBlock
	for i := range states {
		v := states[i]
		var ch <-chan *api.AnnotatedBlock
		var sub *pubsub.Subscription
		ch, sub, err = backend.WatchBlocks(v.rt.Runtime.ID)
		require.NoError(err, "WatchBlocks")
		defer sub.Close()

		blkChannels = append(blkChannels, ch)
	}

	// Advance the epoch.
	epochtimeTests.MustAdvanceEpoch(t, epochtime, 1)

	// Check for the expected post-epoch transition events.
	for i, state := range states {
		blkCh := blkChannels[i]
		state.testEpochTransitionBlock(t, scheduler, epoch, blkCh)
	}

	// Check if GetGenesisBlock still returns the correct genesis block.
	for i := range states {
		var blk *block.Block
		blk, err = backend.GetGenesisBlock(context.Background(), states[i].rt.Runtime.ID)
		require.NoError(err, "GetGenesisBlock")
		require.EqualValues(0, blk.Header.Round, "retrieved block is genesis block")
	}
}

func (s *runtimeState) testEpochTransitionBlock(t *testing.T, scheduler scheduler.Backend, epoch epochtime.EpochTime, ch <-chan *api.AnnotatedBlock) {
	require := require.New(t)

	nodes := make(map[signature.MapKey]*registryTests.TestNode)
	for _, node := range s.rt.TestNodes() {
		nodes[node.Node.ID.ToMapKey()] = node
	}

	s.computeCommittee, s.mergeCommittee, s.storageCommittee, s.txnSchedCommittee = mustGetCommittee(t, s.rt, epoch+1, scheduler, nodes)

	// Wait to receive an epoch transition block.
	for {
		select {
		case blk := <-ch:
			header := blk.Block.Header

			if header.HeaderType != block.EpochTransition {
				continue
			}

			require.True(header.IsParentOf(&s.genesisBlock.Header), "parent is parent of genesis block")
			require.True(header.IORoot.IsEmpty(), "block I/O root empty")
			require.EqualValues(s.genesisBlock.Header.StateRoot, header.StateRoot, "state root preserved")

			// Nothing more to do after the epoch transition block was received.
			return
		case <-time.After(recvTimeout):
			t.Fatalf("failed to receive block")
		}
	}
}

func testSucessfulRound(t *testing.T, backend api.Backend, storage storage.Backend, states []*runtimeState) {
	for _, state := range states {
		state.testSuccessfulRound(t, backend, storage)
	}
}

func (s *runtimeState) testSuccessfulRound(t *testing.T, backend api.Backend, storageBackend storage.Backend) {
	require := require.New(t)

	rt, computeCommittee, mergeCommittee := s.rt, s.computeCommittee, s.mergeCommittee

	child, err := backend.GetLatestBlock(context.Background(), rt.Runtime.ID)
	require.NoError(err, "GetLatestBlock")

	ch, sub, err := backend.WatchBlocks(rt.Runtime.ID)
	require.NoError(err, "WatchBlocks")
	defer sub.Close()

	// Generate a dummy I/O root.
	ioRoot := storage.Root{
		Namespace: child.Header.Namespace,
		Round:     child.Header.Round + 1,
	}
	ioRoot.Hash.Empty()

	ctx := context.Background()
	tree := transaction.NewTree(nil, ioRoot)
	defer tree.Close()
	err = tree.AddTransaction(ctx, transaction.Transaction{Input: []byte("testInput"), Output: []byte("testOutput")}, nil)
	require.NoError(err, "tree.AddTransaction")
	ioWriteLog, ioRootHash, err := tree.Commit(ctx)
	require.NoError(err, "tree.Commit")

	var emptyRoot hash.Hash
	emptyRoot.Empty()

	// Create the new block header that the leader and nodes will commit to.
	parent := &block.Block{
		Header: block.Header{
			Version:      0,
			Namespace:    child.Header.Namespace,
			Round:        child.Header.Round + 1,
			Timestamp:    uint64(time.Now().Unix()),
			HeaderType:   block.Normal,
			PreviousHash: child.Header.EncodedHash(),
			IORoot:       ioRootHash,
			StateRoot:    ioRootHash,
		},
	}
	require.True(parent.Header.IsParentOf(&child.Header), "parent is parent of child")
	parent.Header.StorageSignatures = mustStore(
		t,
		storageBackend,
		s.storageCommittee,
		child.Header.Namespace,
		child.Header.Round+1,
		[]storage.ApplyOp{
			storage.ApplyOp{SrcRound: child.Header.Round + 1, SrcRoot: emptyRoot, DstRoot: ioRootHash, WriteLog: ioWriteLog},
			// NOTE: Twice to get a receipt over both roots which we set to the same value.
			storage.ApplyOp{SrcRound: child.Header.Round, SrcRoot: emptyRoot, DstRoot: ioRootHash, WriteLog: ioWriteLog},
		},
	)

	// Generate all the compute commitments.
	var toCommit []*registryTests.TestNode
	var computeCommits []commitment.ComputeCommitment
	toCommit = append(toCommit, computeCommittee.workers...)
	for _, node := range toCommit {
		commitBody := commitment.ComputeBody{
			CommitteeID: computeCommittee.committee.EncodedMembersHash(),
			Header: commitment.ComputeResultsHeader{
				PreviousHash: parent.Header.PreviousHash,
				IORoot:       parent.Header.IORoot,
				StateRoot:    parent.Header.StateRoot,
			},
			StorageSignatures: parent.Header.StorageSignatures,
			InputRoot:         hash.Hash{},
			InputStorageSigs:  []signature.Signature{},
		}

		// Fake txn scheduler signature.
		dispatch := &commitment.TxnSchedulerBatchDispatch{
			CommitteeID:       commitBody.CommitteeID,
			IORoot:            commitBody.InputRoot,
			StorageSignatures: commitBody.InputStorageSigs,
			Header:            child.Header,
		}
		signer := s.txnSchedCommittee.leader.Signer
		var signedDispatch *signature.Signed
		signedDispatch, err = signature.SignSigned(signer, commitment.TxnSchedulerBatchDispatchSigCtx, dispatch)
		require.NoError(err, "SignSigned")
		commitBody.TxnSchedSig = signedDispatch.Signature

		// `err` shadows outside.
		commit, err := commitment.SignComputeCommitment(node.Signer, &commitBody) // nolint: vetshadow
		require.NoError(err, "SignSigned")

		computeCommits = append(computeCommits, *commit)
	}

	// Generate all the merge commitments.
	var mergeCommits []commitment.MergeCommitment
	toCommit = []*registryTests.TestNode{}
	toCommit = append(toCommit, mergeCommittee.workers...)
	for _, node := range toCommit {
		commitBody := commitment.MergeBody{
			ComputeCommits: computeCommits,
			Header:         parent.Header,
		}
		// `err` shadows outside.
		commit, err := commitment.SignMergeCommitment(node.Signer, &commitBody) // nolint: vetshadow
		require.NoError(err, "SignSigned")

		mergeCommits = append(mergeCommits, *commit)
	}

	err = backend.MergeCommit(context.Background(), rt.Runtime.ID, mergeCommits)
	require.NoError(err, "MergeCommit")

	// Ensure that the round was finalized.
	for {
		select {
		case blk := <-ch:
			header := blk.Block.Header

			// Ensure that WatchBlocks uses the correct latest block.
			require.True(header.Round >= child.Header.Round, "WatchBlocks must start at child block")

			if header.Round == child.Header.Round {
				require.EqualValues(child.Header, header, "old block is equal")
				continue
			}

			// Can't direcly compare headers, some backends rewrite the timestamp.
			require.EqualValues(parent.Header.Version, header.Version, "block version")
			require.EqualValues(parent.Header.Namespace, header.Namespace, "block namespace")
			require.EqualValues(parent.Header.Round, header.Round, "block round")
			// Timestamp
			require.EqualValues(parent.Header.HeaderType, header.HeaderType, "block header type")
			require.EqualValues(parent.Header.PreviousHash, header.PreviousHash, "block previous hash")
			require.EqualValues(parent.Header.IORoot, header.IORoot, "block I/O root")
			require.EqualValues(parent.Header.StateRoot, header.StateRoot, "block root hash")

			// We need to wait for the indexer to index the block. We could have a channel
			// to subscribe to these updates and this would not be needed.
			time.Sleep(1 * time.Second)

			// Check if we can fetch the block via GetBlock.
			gblk, err := backend.GetBlock(context.Background(), rt.Runtime.ID, header.Round)
			require.NoError(err, "GetBlock")
			require.EqualValues(blk.Block, gblk, "GetBlock")

			// Nothing more to do after the block was received.
			return
		case <-time.After(recvTimeout):
			t.Fatalf("failed to receive block")
		}
	}
}

type testCommittee struct {
	committee     *scheduler.Committee
	leader        *registryTests.TestNode
	workers       []*registryTests.TestNode
	backupWorkers []*registryTests.TestNode
}

func mustGetCommittee(
	t *testing.T,
	rt *registryTests.TestRuntime,
	epoch epochtime.EpochTime,
	sched scheduler.Backend,
	nodes map[signature.MapKey]*registryTests.TestNode,
) (
	computeCommittee *testCommittee,
	mergeCommittee *testCommittee,
	storageCommittee *testCommittee,
	txnSchedCommittee *testCommittee,
) {
	require := require.New(t)

	ch, sub := sched.WatchCommittees()
	defer sub.Close()

	for {
		select {
		case committee := <-ch:
			if committee.ValidFor < epoch {
				continue
			}
			if !rt.Runtime.ID.Equal(committee.RuntimeID) {
				continue
			}

			var ret testCommittee
			ret.committee = committee
			for _, member := range committee.Members {
				node := nodes[member.PublicKey.ToMapKey()]
				require.NotNil(node, "member is one of the nodes")
				switch member.Role {
				case scheduler.Worker:
					ret.workers = append(ret.workers, node)
				case scheduler.BackupWorker:
					ret.backupWorkers = append(ret.backupWorkers, node)
				case scheduler.Leader:
					ret.leader = node
				}
			}

			var groupSize, groupBackupSize int
			switch committee.Kind {
			case scheduler.KindTransactionScheduler:
				groupSize = int(rt.Runtime.ReplicaGroupSize)
				groupBackupSize = 0
			case scheduler.KindCompute:
				fallthrough
			case scheduler.KindMerge:
				groupSize = int(rt.Runtime.ReplicaGroupSize)
				groupBackupSize = int(rt.Runtime.ReplicaGroupBackupSize)
			case scheduler.KindStorage:
				groupSize = int(rt.Runtime.StorageGroupSize)
			}

			if committee.Kind.NeedsLeader() {
				require.Len(ret.workers, groupSize-1, "workers exist")
				require.NotNil(ret.leader, "leader exist")
			} else {
				require.Len(ret.workers, groupSize, "workers exist")
				require.Nil(ret.leader, "no leader")
			}
			require.Len(ret.backupWorkers, groupBackupSize, "backup workers exist")

			switch committee.Kind {
			case scheduler.KindTransactionScheduler:
				txnSchedCommittee = &ret
			case scheduler.KindCompute:
				computeCommittee = &ret
			case scheduler.KindMerge:
				mergeCommittee = &ret
			case scheduler.KindStorage:
				storageCommittee = &ret
			}

			if computeCommittee == nil || mergeCommittee == nil || storageCommittee == nil || txnSchedCommittee == nil {
				continue
			}

			return
		case <-time.After(recvTimeout):
			t.Fatalf("failed to receive committee event")
		}
	}
}

func mustStore(
	t *testing.T,
	store storage.Backend,
	committee *testCommittee,
	ns common.Namespace,
	round uint64,
	ops []storage.ApplyOp,
) []signature.Signature {
	require := require.New(t)

	receipts, err := store.ApplyBatch(context.Background(), ns, round, ops)
	require.NoError(err, "ApplyBatch")
	require.NotEmpty(receipts, "ApplyBatch must return some storage receipts")

	// We need to fake the storage signatures as the storage committee under test
	// does not contain the key of the actual storage backend.

	var body storage.ReceiptBody
	err = receipts[0].Open(&body)
	require.NoError(err, "Open")

	var signatures []signature.Signature
	for _, node := range committee.workers {
		var receipt *storage.Receipt
		receipt, err = storage.SignReceipt(node.Signer, ns, round, body.Roots)
		require.NoError(err, "SignReceipt")

		signatures = append(signatures, receipt.Signed.Signature)
	}
	return signatures
}
