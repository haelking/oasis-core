package genesis

import (
	"encoding/hex"
	"math"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/oasislabs/oasis-core/go/common"
	"github.com/oasislabs/oasis-core/go/common/crypto/hash"
	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
	memorySigner "github.com/oasislabs/oasis-core/go/common/crypto/signature/signers/memory"
	"github.com/oasislabs/oasis-core/go/common/entity"
	"github.com/oasislabs/oasis-core/go/common/node"
	consensus "github.com/oasislabs/oasis-core/go/consensus/genesis"
	tendermint "github.com/oasislabs/oasis-core/go/consensus/tendermint/api"
	epochtime "github.com/oasislabs/oasis-core/go/epochtime/api"
	genesis "github.com/oasislabs/oasis-core/go/genesis/api"
	genesisTestHelpers "github.com/oasislabs/oasis-core/go/genesis/tests"
	keymanager "github.com/oasislabs/oasis-core/go/keymanager/api"
	cmdFlags "github.com/oasislabs/oasis-core/go/oasis-node/cmd/common/flags"
	registry "github.com/oasislabs/oasis-core/go/registry/api"
	roothashAPI "github.com/oasislabs/oasis-core/go/roothash/api"
	scheduler "github.com/oasislabs/oasis-core/go/scheduler/api"
	staking "github.com/oasislabs/oasis-core/go/staking/api"
	stakingTests "github.com/oasislabs/oasis-core/go/staking/tests/debug"
	storage "github.com/oasislabs/oasis-core/go/storage/api"
)

// Note: If you are here wanting to alter the genesis document used for
// the node that is spun up as part of the tests, you really want
// consensus/tendermint/tests/genesis/genesis.go.
var testDoc = &genesis.Document{
	ChainID:   genesisTestHelpers.TestChainID,
	Time:      time.Unix(1574858284, 0),
	HaltEpoch: epochtime.EpochTime(math.MaxUint64),
	EpochTime: epochtime.Genesis{
		Parameters: epochtime.ConsensusParameters{
			DebugMockBackend: true,
		},
	},
	Registry: registry.Genesis{
		Parameters: registry.ConsensusParameters{
			DebugAllowUnroutableAddresses:          true,
			DebugBypassStake:                       true,
			DebugAllowEntitySignedNodeRegistration: true,
		},
	},
	Scheduler: scheduler.Genesis{
		Parameters: scheduler.ConsensusParameters{
			MinValidators:          1,
			MaxValidators:          100,
			MaxValidatorsPerEntity: 100,
			DebugBypassStake:       true,
			DebugStaticValidators:  true,
			// Zero RewardFactorEpochElectionAny is normal.
		},
	},
	Consensus: consensus.Genesis{
		Backend: tendermint.BackendName,
		Parameters: consensus.Parameters{
			TimeoutCommit:     1 * time.Millisecond,
			SkipTimeoutCommit: true,
		},
	},
	Staking: stakingTests.DebugGenesisState,
}

func signEntityOrDie(signer signature.Signer, e *entity.Entity) *entity.SignedEntity {
	signedEntity, err := entity.SignEntity(signer, registry.RegisterGenesisEntitySignatureContext, e)
	if err != nil {
		panic(err)
	}
	return signedEntity
}

func signRuntimeOrDie(signer signature.Signer, rt *registry.Runtime) *registry.SignedRuntime {
	signedRuntime, err := registry.SignRuntime(signer, registry.RegisterGenesisRuntimeSignatureContext, rt)
	if err != nil {
		panic(err)
	}
	return signedRuntime
}

func signNodeOrDie(signers []signature.Signer, n *node.Node) *node.MultiSignedNode {
	signedNode, err := node.MultiSignNode(
		signers,
		registry.RegisterGenesisNodeSignatureContext,
		n,
	)
	if err != nil {
		panic(err)
	}
	return signedNode
}

func hex2pk(str string) signature.PublicKey {
	var pk signature.PublicKey
	if err := pk.UnmarshalHex(str); err != nil {
		panic(err)
	}
	return pk
}

func hex2ns(str string, force bool) common.Namespace {
	var ns common.Namespace
	if force {
		b, err := hex.DecodeString(str)
		if err != nil {
			panic(err)
		}
		copy(ns[:], b)
		return ns
	}
	if err := ns.UnmarshalHex(str); err != nil {
		panic(err)
	}
	return ns
}

func TestGenesisChainContext(t *testing.T) {
	// Ensure that the chain context is stable.
	stableDoc := *testDoc
	// NOTE: Staking part is not stable as it generates a new public key
	//       on each run.
	stableDoc.Staking = staking.Genesis{}

	require.Equal(t, "1024b5ca04a34e17cab59fdae43c32c05e1a51875841b99ea49321a4ec83adb3", stableDoc.ChainContext())
}

func TestGenesisSanityCheck(t *testing.T) {
	viper.Set(cmdFlags.CfgDebugDontBlameOasis, true)
	require := require.New(t)

	// First, set up a few things we'll need in the tests below.
	signer := memorySigner.NewTestSigner("genesis sanity checks signer")
	signer2 := memorySigner.NewTestSigner("another genesis sanity checks signer")
	nodeSigner := memorySigner.NewTestSigner("node genesis sanity checks signer")
	nodeConsensusSigner := memorySigner.NewTestSigner("node consensus genesis sanity checks signer")
	nodeP2PSigner := memorySigner.NewTestSigner("node P2P genesis sanity checks signer")
	nodeTLSSigner := memorySigner.NewTestSigner("node TLS genesis sanity checks signer")
	validPK := signer.Public()
	var validNS common.Namespace
	_ = validNS.UnmarshalBinary(validPK[:])

	invalidPK := hex2pk("c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a")
	unknownPK := memorySigner.NewTestSigner("unknown genesis sanity checks signer").Public()

	signature.BuildPublicKeyBlacklist(true)

	var emptyHash hash.Hash
	emptyHash.Empty()
	var nonEmptyHash hash.Hash
	_ = nonEmptyHash.UnmarshalHex("1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	// Note that this test entity has no nodes by design, those will be added
	// later by various tests.
	testEntity := &entity.Entity{
		DescriptorVersion:      entity.LatestEntityDescriptorVersion,
		ID:                     validPK,
		AllowEntitySignedNodes: true,
	}
	signedTestEntity := signEntityOrDie(signer, testEntity)

	kmRuntimeID := hex2ns("4000000000000000ffffffffffffffffffffffffffffffffffffffffffffffff", false)
	testKMRuntime := &registry.Runtime{
		DescriptorVersion: registry.LatestRuntimeDescriptorVersion,
		ID:                kmRuntimeID,
		EntityID:          testEntity.ID,
		Kind:              registry.KindKeyManager,
		AdmissionPolicy: registry.RuntimeAdmissionPolicy{
			EntityWhitelist: &registry.EntityWhitelistRuntimeAdmissionPolicy{
				Entities: map[signature.PublicKey]bool{
					validPK: true,
				},
			},
		},
	}
	signedTestKMRuntime := signRuntimeOrDie(signer, testKMRuntime)

	testRuntimeID := hex2ns("0000000000000000000000000000000000000000000000000000000000000001", false)
	testRuntime := &registry.Runtime{
		DescriptorVersion: registry.LatestRuntimeDescriptorVersion,
		ID:                testRuntimeID,
		EntityID:          testEntity.ID,
		Kind:              registry.KindCompute,
		KeyManager:        &testKMRuntime.ID,
		Executor: registry.ExecutorParameters{
			GroupSize:    1,
			RoundTimeout: 1 * time.Second,
		},
		Merge: registry.MergeParameters{
			GroupSize:    1,
			RoundTimeout: 1 * time.Second,
		},
		TxnScheduler: registry.TxnSchedulerParameters{
			GroupSize:         1,
			Algorithm:         "batching",
			BatchFlushTimeout: 1 * time.Second,
			MaxBatchSize:      1,
			MaxBatchSizeBytes: 1,
		},
		Storage: registry.StorageParameters{
			GroupSize:               1,
			MaxApplyWriteLogEntries: 100_000,
			MaxApplyOps:             2,
			MaxMergeRoots:           8,
			MaxMergeOps:             2,
		},
		AdmissionPolicy: registry.RuntimeAdmissionPolicy{
			AnyNode: &registry.AnyNodeRuntimeAdmissionPolicy{},
		},
	}
	signedTestRuntime := signRuntimeOrDie(signer, testRuntime)

	var testConsensusAddress node.ConsensusAddress
	_ = testConsensusAddress.UnmarshalText([]byte("AAAAAAAAAAAAAAAAAAAABBBBBBBBBBBBBBBBBBBBBBA=@127.0.0.1:1234"))
	var testAddress node.Address
	_ = testAddress.UnmarshalText([]byte("127.0.0.1:1234"))
	testNode := &node.Node{
		DescriptorVersion: node.LatestNodeDescriptorVersion,
		ID:                nodeSigner.Public(),
		EntityID:          testEntity.ID,
		Expiration:        10,
		Roles:             node.RoleValidator,
		TLS: node.TLSInfo{
			PubKey: nodeTLSSigner.Public(),
			Addresses: []node.TLSAddress{
				{PubKey: nodeTLSSigner.Public(), Address: testAddress},
			},
		},
		P2P: node.P2PInfo{
			ID:        nodeP2PSigner.Public(),
			Addresses: []node.Address{testAddress},
		},
		Consensus: node.ConsensusInfo{
			ID:        nodeConsensusSigner.Public(),
			Addresses: []node.ConsensusAddress{testConsensusAddress},
		},
	}
	nodeSigners := []signature.Signer{
		nodeSigner,
		nodeP2PSigner,
		nodeTLSSigner,
		nodeConsensusSigner,
	}
	signedTestNode := signNodeOrDie(nodeSigners, testNode)
	entitySignedTestNode := signNodeOrDie(append([]signature.Signer{signer}, nodeSigners...), testNode)

	// Test genesis document should pass sanity check.
	require.NoError(testDoc.SanityCheck(), "test genesis document should be valid")

	// Test top-level genesis checks.
	d := *testDoc
	d.Height = -123
	require.Error(d.SanityCheck(), "height < 0 should be invalid")

	d = *testDoc
	d.Time = time.Now().Add(time.Minute * 2)
	require.Error(d.SanityCheck(), "future time of genesis doc should be invalid")

	d = *testDoc
	d.ChainID = "   \t"
	require.Error(d.SanityCheck(), "empty chain ID should be invalid")

	d = *testDoc
	d.EpochTime.Base = 10
	d.HaltEpoch = 5
	require.Error(d.SanityCheck(), "halt epoch in the past should be invalid")

	// Test consensus genesis checks.
	d = *testDoc
	d.Consensus.Parameters.TimeoutCommit = 0
	d.Consensus.Parameters.SkipTimeoutCommit = false
	require.Error(d.SanityCheck(), "too small timeout commit should be invalid")

	d = *testDoc
	d.Consensus.Parameters.TimeoutCommit = 0
	d.Consensus.Parameters.SkipTimeoutCommit = true
	require.NoError(d.SanityCheck(), "too small timeout commit should be allowed if it's skipped")

	// Test epochtime genesis checks.
	d = *testDoc
	d.EpochTime.Base = epochtime.EpochInvalid
	require.Error(d.SanityCheck(), "invalid base epoch should be rejected")

	d = *testDoc
	d.EpochTime.Parameters.Interval = 0
	d.EpochTime.Parameters.DebugMockBackend = false
	require.Error(d.SanityCheck(), "invalid epoch interval should be rejected")

	// Test keymanager genesis checks.
	d = *testDoc
	d.KeyManager = keymanager.Genesis{
		Statuses: []*keymanager.Status{
			{
				ID: testRuntimeID,
			},
		},
	}
	require.Error(d.SanityCheck(), "invalid keymanager runtime should be rejected")

	d = *testDoc
	d.KeyManager = keymanager.Genesis{
		Statuses: []*keymanager.Status{
			{
				ID:    validNS,
				Nodes: []signature.PublicKey{invalidPK},
			},
		},
	}
	require.Error(d.SanityCheck(), "invalid keymanager node should be rejected")

	// Test roothash genesis checks.
	// First we define a helper function for calling the SanityCheck() on RuntimeStates.
	rtsSanityCheck := func(g roothashAPI.Genesis, isGenesis bool) error {
		for _, rts := range g.RuntimeStates {
			if err := rts.SanityCheck(isGenesis); err != nil {
				return err
			}
		}
		return nil
	}

	d = *testDoc
	d.RootHash.RuntimeStates = make(map[common.Namespace]*registry.RuntimeGenesis)
	d.RootHash.RuntimeStates[validNS] = &registry.RuntimeGenesis{
		StateRoot: nonEmptyHash,
		// Empty list of storage receipts.
		StorageReceipts: []signature.Signature{},
	}
	require.Error(rtsSanityCheck(d.RootHash, false), "empty StorageReceipts for StateRoot should be rejected")
	require.NoError(rtsSanityCheck(d.RootHash, true), "empty StorageReceipts for StateRoot should be ignored, if isGenesis=true")

	d = *testDoc
	d.RootHash.RuntimeStates = make(map[common.Namespace]*registry.RuntimeGenesis)
	d.RootHash.RuntimeStates[validNS] = &registry.RuntimeGenesis{
		StateRoot: nonEmptyHash,
		// List with one empty (invalid) storage receipt.
		StorageReceipts: []signature.Signature{signature.Signature{}},
	}
	require.Error(rtsSanityCheck(d.RootHash, false), "empty StorageReceipt for StateRoot should be rejected")
	require.NoError(rtsSanityCheck(d.RootHash, true), "empty StorageReceipt for StateRoot should be ignored, if isGenesis=true")

	d = *testDoc
	signature.SetChainContext("test: oasis-core tests")
	stateRootSig, _ := signature.Sign(signer, storage.ReceiptSignatureContext, nonEmptyHash[:])
	stateRootSig2, _ := signature.Sign(signer2, storage.ReceiptSignatureContext, nonEmptyHash[:])
	wrongSig, _ := signature.Sign(signer, storage.ReceiptSignatureContext, []byte{1, 2, 3})
	d.RootHash.RuntimeStates = make(map[common.Namespace]*registry.RuntimeGenesis)
	d.RootHash.RuntimeStates[validNS] = &registry.RuntimeGenesis{
		StateRoot: nonEmptyHash,
		// Some non-empty signature, but not related to StateRoot.
		StorageReceipts: []signature.Signature{*wrongSig, *stateRootSig, *stateRootSig2},
	}
	require.Error(rtsSanityCheck(d.RootHash, false), "some incorrect StorageReceipt for StateRoot should be rejected")
	require.NoError(rtsSanityCheck(d.RootHash, true), "some incorrect StorageReceipt for StateRoot should be ignored, if isGenesis=true")

	d = *testDoc
	d.RootHash.RuntimeStates = make(map[common.Namespace]*registry.RuntimeGenesis)
	d.RootHash.RuntimeStates[validNS] = &registry.RuntimeGenesis{
		StateRoot:       nonEmptyHash,
		StorageReceipts: []signature.Signature{*stateRootSig, *stateRootSig2},
	}
	require.NoError(rtsSanityCheck(d.RootHash, false), "non-empty StateRoot with all correct StorageReceipts should pass")
	require.NoError(rtsSanityCheck(d.RootHash, true), "non-empty StateRoot with all correct StorageReceipts should pass, if isGenesis=true")

	d = *testDoc
	nonEmptyState := storage.WriteLog{storage.LogEntry{
		Key:   []byte{1, 2, 3},
		Value: []byte{1, 2, 3},
	}}
	d.RootHash.RuntimeStates = make(map[common.Namespace]*registry.RuntimeGenesis)
	d.RootHash.RuntimeStates[validNS] = &registry.RuntimeGenesis{
		State:           nonEmptyState,
		StateRoot:       nonEmptyHash,
		StorageReceipts: []signature.Signature{*wrongSig, *stateRootSig, *stateRootSig2},
	}
	require.NoError(rtsSanityCheck(d.RootHash, false), "non-empty StateRoot with non-empty State and some invalid StorageReceipt should pass")
	require.NoError(rtsSanityCheck(d.RootHash, true), "non-empty StateRoot with non-empty State and some invalid StorageReceipt should pass, if isGenesis=true")

	d.RootHash.RuntimeStates = make(map[common.Namespace]*registry.RuntimeGenesis)
	d.RootHash.RuntimeStates[validNS] = &registry.RuntimeGenesis{
		State:           nonEmptyState,
		StateRoot:       nonEmptyHash,
		StorageReceipts: []signature.Signature{*stateRootSig, *stateRootSig2},
	}
	require.NoError(rtsSanityCheck(d.RootHash, false), "non-empty StateRoot with non-empty State and all valid StorageReceipts should pass")
	require.NoError(rtsSanityCheck(d.RootHash, true), "non-empty StateRoot with non-empty State and all valid StorageReceipts should pass, if isGenesis=true")

	// Test registry genesis checks.
	d = *testDoc
	d.Registry.Entities = []*entity.SignedEntity{signedTestEntity}
	require.NoError(d.SanityCheck(), "test entity should pass")

	d = *testDoc
	te := *testEntity
	te.ID = invalidPK
	signedBrokenEntity := signEntityOrDie(signer, &te)
	d.Registry.Entities = []*entity.SignedEntity{signedBrokenEntity}
	require.Error(d.SanityCheck(), "invalid test entity ID should be rejected")

	d = *testDoc
	te = *testEntity
	te.Nodes = []signature.PublicKey{invalidPK}
	signedBrokenEntity = signEntityOrDie(signer, &te)
	d.Registry.Entities = []*entity.SignedEntity{signedBrokenEntity}
	require.Error(d.SanityCheck(), "test entity's invalid node public key should be rejected")

	d = *testDoc
	te = *testEntity
	signedBrokenEntity, err := entity.SignEntity(signer, signature.NewContext("genesis sanity check invalid ctx"), &te)
	if err != nil {
		panic(err)
	}
	d.Registry.Entities = []*entity.SignedEntity{signedBrokenEntity}
	require.Error(d.SanityCheck(), "test entity with invalid signing context should be rejected")

	d = *testDoc
	d.Registry.Entities = []*entity.SignedEntity{signedTestEntity}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	require.NoError(d.SanityCheck(), "test keymanager runtime should pass")

	d = *testDoc
	d.Registry.Entities = []*entity.SignedEntity{signedTestEntity}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime, signedTestRuntime}
	require.NoError(d.SanityCheck(), "test runtimes should pass")

	d = *testDoc
	d.Registry.Entities = []*entity.SignedEntity{signedTestEntity}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestRuntime, signedTestKMRuntime}
	require.NoError(d.SanityCheck(), "test runtimes in reverse order should pass")

	d = *testDoc
	d.Registry.Entities = []*entity.SignedEntity{signedTestEntity}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestRuntime}
	require.Error(d.SanityCheck(), "test runtime with missing keymanager runtime should be rejected")

	d = *testDoc
	d.Registry.Entities = []*entity.SignedEntity{signedTestEntity}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime, signedTestRuntime, signedTestRuntime}
	require.Error(d.SanityCheck(), "duplicate runtime IDs should be rejected")

	// TODO: fiddle with executor/merge/txnsched parameters.

	d = *testDoc
	te = *testEntity
	te.Nodes = []signature.PublicKey{testNode.ID}
	signedEntityWithTestNode := signEntityOrDie(signer, &te)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{}
	d.Registry.Nodes = []*node.MultiSignedNode{signedTestNode}
	require.NoError(d.SanityCheck(), "entity with node should pass")

	d = *testDoc
	te = *testEntity
	te.Nodes = []signature.PublicKey{unknownPK}
	te.AllowEntitySignedNodes = false
	signedEntityWithBrokenNode := signEntityOrDie(signer, &te)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithBrokenNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{}
	d.Registry.Nodes = []*node.MultiSignedNode{signedTestNode}
	require.Error(d.SanityCheck(), "node not listed among controlling entity's nodes should be rejected if the entity doesn't allow entity-signed nodes")

	d = *testDoc
	te = *testEntity
	te.Nodes = []signature.PublicKey{unknownPK}
	te.AllowEntitySignedNodes = true
	signedEntityWithBrokenNode = signEntityOrDie(signer, &te)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithBrokenNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{}
	d.Registry.Nodes = []*node.MultiSignedNode{entitySignedTestNode}
	require.NoError(d.SanityCheck(), "node not listed among controlling entity's nodes should still be accepted if the entity allows entity-signed nodes")

	d = *testDoc
	tn := *testNode
	tn.EntityID = unknownPK
	signedBrokenTestNode := signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "node with unknown entity ID should be rejected")

	d = *testDoc
	tn = *testNode
	signedBrokenTestNode, err = node.MultiSignNode(
		[]signature.Signer{
			signer,
		},
		signature.NewContext("genesis sanity check test invalid node ctx"),
		&tn,
	)
	if err != nil {
		panic(err)
	}
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "node with wrong signing context should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Roles = 1<<16 | 1<<17
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "node with any reserved role bits set should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Roles = 0
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "node without any role bits set should be rejected")

	d = *testDoc
	tn = *testNode
	tn.TLS.PubKey = signature.PublicKey{}
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "node with invalid TLS public key should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Consensus.ID = invalidPK
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "node with invalid consensus ID should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Roles = node.RoleComputeWorker
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "compute node without runtimes should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Roles = node.RoleKeyManager
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "keymanager node without runtimes should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Roles = node.RoleKeyManager
	tn.Runtimes = []*node.Runtime{
		&node.Runtime{
			ID: testKMRuntime.ID,
		},
	}
	signedKMTestNode := signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedKMTestNode}
	require.NoError(d.SanityCheck(), "keymanager node with valid runtime should pass")

	d = *testDoc
	tn = *testNode
	tn.Roles = node.RoleKeyManager
	tn.Runtimes = []*node.Runtime{
		&node.Runtime{
			ID: testRuntime.ID,
		},
	}
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "keymanager node with invalid runtime should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Roles = node.RoleKeyManager
	tn.Runtimes = []*node.Runtime{
		&node.Runtime{
			ID: testRuntime.ID,
		},
	}
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime, signedTestRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "keymanager node with non-KM runtime should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Roles = node.RoleComputeWorker
	tn.Runtimes = []*node.Runtime{
		&node.Runtime{
			ID: testKMRuntime.ID,
		},
	}
	signedBrokenTestNode = signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime, signedTestRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedBrokenTestNode}
	require.Error(d.SanityCheck(), "compute node with non-compute runtime should be rejected")

	d = *testDoc
	tn = *testNode
	tn.Roles = node.RoleComputeWorker
	tn.Runtimes = []*node.Runtime{
		&node.Runtime{
			ID: testRuntime.ID,
		},
	}
	signedComputeTestNode := signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime, signedTestRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedComputeTestNode}
	require.NoError(d.SanityCheck(), "compute node with compute runtime should pass")

	d = *testDoc
	tn = *testNode
	tn.Roles = node.RoleStorageWorker
	tn.Runtimes = []*node.Runtime{
		&node.Runtime{
			ID: testRuntime.ID,
		},
	}
	signedStorageTestNode := signNodeOrDie(nodeSigners, &tn)
	d.Registry.Entities = []*entity.SignedEntity{signedEntityWithTestNode}
	d.Registry.Runtimes = []*registry.SignedRuntime{signedTestKMRuntime, signedTestRuntime}
	d.Registry.Nodes = []*node.MultiSignedNode{signedStorageTestNode}
	require.NoError(d.SanityCheck(), "storage node with compute runtime should pass")

	// Test staking genesis checks.
	// NOTE: There doesn't seem to be a way to generate invalid Quantities, so
	// we're just going to test the code that checks if things add up.
	d = *testDoc
	d.Staking.TotalSupply = stakingTests.QtyFromInt(100)
	require.Error(d.SanityCheck(), "invalid total supply should be rejected")

	d = *testDoc
	d.Staking.CommonPool = stakingTests.QtyFromInt(100)
	require.Error(d.SanityCheck(), "invalid common pool should be rejected")

	d = *testDoc
	d.Staking.LastBlockFees = stakingTests.QtyFromInt(100)
	require.Error(d.SanityCheck(), "invalid last block fees should be rejected")

	d = *testDoc
	d.Staking.Ledger[stakingTests.DebugStateSrcID].General.Balance = stakingTests.QtyFromInt(100)
	require.Error(d.SanityCheck(), "invalid general balance should be rejected")

	d = *testDoc
	d.Staking.Ledger[stakingTests.DebugStateSrcID].Escrow.Active.Balance = stakingTests.QtyFromInt(100)
	require.Error(d.SanityCheck(), "invalid escrow active balance should be rejected")

	d = *testDoc
	d.Staking.Ledger[stakingTests.DebugStateSrcID].Escrow.Debonding.Balance = stakingTests.QtyFromInt(100)
	require.Error(d.SanityCheck(), "invalid escrow debonding balance should be rejected")

	d = *testDoc
	d.Staking.Ledger[stakingTests.DebugStateSrcID].Escrow.Active.TotalShares = stakingTests.QtyFromInt(1)
	require.Error(d.SanityCheck(), "invalid escrow active total shares should be rejected")

	d = *testDoc
	d.Staking.Ledger[stakingTests.DebugStateSrcID].Escrow.Debonding.TotalShares = stakingTests.QtyFromInt(1)
	require.Error(d.SanityCheck(), "invalid escrow debonding total shares should be rejected")

	d = *testDoc
	d.Staking.Delegations = map[signature.PublicKey]map[signature.PublicKey]*staking.Delegation{
		stakingTests.DebugStateSrcID: map[signature.PublicKey]*staking.Delegation{
			stakingTests.DebugStateDestID: &staking.Delegation{
				Shares: stakingTests.QtyFromInt(1),
			},
		},
	}
	require.Error(d.SanityCheck(), "invalid delegation should be rejected")

	d = *testDoc
	d.Staking.DebondingDelegations = map[signature.PublicKey]map[signature.PublicKey][]*staking.DebondingDelegation{
		stakingTests.DebugStateSrcID: map[signature.PublicKey][]*staking.DebondingDelegation{
			stakingTests.DebugStateDestID: []*staking.DebondingDelegation{
				&staking.DebondingDelegation{
					Shares:        stakingTests.QtyFromInt(1),
					DebondEndTime: 10,
				},
			},
		},
	}
	require.Error(d.SanityCheck(), "invalid debonding delegation should be rejected")
}
