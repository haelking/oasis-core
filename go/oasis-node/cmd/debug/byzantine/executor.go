package byzantine

import (
	"context"
	"fmt"

	"github.com/oasislabs/oasis-core/go/common"
	"github.com/oasislabs/oasis-core/go/common/cbor"
	"github.com/oasislabs/oasis-core/go/common/crypto/hash"
	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
	"github.com/oasislabs/oasis-core/go/common/identity"
	"github.com/oasislabs/oasis-core/go/roothash/api/block"
	"github.com/oasislabs/oasis-core/go/roothash/api/commitment"
	"github.com/oasislabs/oasis-core/go/runtime/transaction"
	scheduler "github.com/oasislabs/oasis-core/go/scheduler/api"
	storage "github.com/oasislabs/oasis-core/go/storage/api"
	"github.com/oasislabs/oasis-core/go/storage/mkvs"
	"github.com/oasislabs/oasis-core/go/storage/mkvs/syncer"
	"github.com/oasislabs/oasis-core/go/storage/mkvs/writelog"
	"github.com/oasislabs/oasis-core/go/worker/common/p2p"
)

type computeBatchContext struct {
	bd    commitment.TxnSchedulerBatchDispatch
	bdSig signature.Signature

	ioTree    *transaction.Tree
	txs       []*transaction.Transaction
	stateTree mkvs.Tree

	stateWriteLog writelog.WriteLog
	newStateRoot  hash.Hash
	ioWriteLog    writelog.WriteLog
	newIORoot     hash.Hash

	storageReceipts []*storage.Receipt
	commit          *commitment.ExecutorCommitment
}

func newComputeBatchContext() *computeBatchContext {
	return &computeBatchContext{}
}

func (cbc *computeBatchContext) receiveBatch(ph *p2pHandle) error {
	req := <-ph.requests
	req.responseCh <- nil

	if req.msg.SignedTxnSchedulerBatchDispatch == nil {
		return fmt.Errorf("expecting signed transaction scheduler batch dispatch message, got %+v", req.msg)
	}

	if err := req.msg.SignedTxnSchedulerBatchDispatch.Open(&cbc.bd); err != nil {
		return fmt.Errorf("request message SignedTxnSchedulerBatchDispatch Open: %w", err)
	}

	cbc.bdSig = req.msg.SignedTxnSchedulerBatchDispatch.Signature
	return nil
}

func (cbc *computeBatchContext) openTrees(ctx context.Context, rs syncer.ReadSyncer) error {
	var err error
	cbc.ioTree = transaction.NewTree(rs, storage.Root{
		Namespace: cbc.bd.Header.Namespace,
		Version:   cbc.bd.Header.Round + 1,
		Hash:      cbc.bd.IORoot,
	})

	cbc.txs, err = cbc.ioTree.GetTransactions(ctx)
	if err != nil {
		return fmt.Errorf("IO tree GetTransactions: %w", err)
	}

	cbc.stateTree = mkvs.NewWithRoot(rs, nil, storage.Root{
		Namespace: cbc.bd.Header.Namespace,
		Version:   cbc.bd.Header.Round,
		Hash:      cbc.bd.Header.StateRoot,
	})

	return nil
}

func (cbc *computeBatchContext) closeTrees() {
	cbc.ioTree.Close()
	cbc.stateTree.Close()
}

func (cbc *computeBatchContext) addResult(ctx context.Context, tx *transaction.Transaction, output []byte, tags transaction.Tags) error {
	txCopy := *tx
	txCopy.Output = output

	// This rewrites the input artifact, but it shouldn't affect the root hash.
	if err := cbc.ioTree.AddTransaction(ctx, txCopy, tags); err != nil {
		return fmt.Errorf("IO tree AddTransaction: %w", err)
	}

	return nil
}

func (cbc *computeBatchContext) addResultSuccess(ctx context.Context, tx *transaction.Transaction, res interface{}, tags transaction.Tags) error {
	// Hack: The actual TxnOutput struct doesn't serialize right.
	return cbc.addResult(ctx, tx, cbor.Marshal(struct {
		Success interface{}
	}{
		Success: res,
	}), tags)
}

func (cbc *computeBatchContext) addResultError(ctx context.Context, tx *transaction.Transaction, err string, tags transaction.Tags) error { // nolint: unused
	// Hack: The actual TxnOutput struct doesn't serialize right.
	return cbc.addResult(ctx, tx, cbor.Marshal(struct {
		Error *string
	}{
		Error: &err,
	}), tags)
}

func (cbc *computeBatchContext) commitTrees(ctx context.Context) error {
	var err error
	cbc.stateWriteLog, cbc.newStateRoot, err = cbc.stateTree.Commit(ctx, cbc.bd.Header.Namespace, cbc.bd.Header.Round+1)
	if err != nil {
		return fmt.Errorf("state tree Commit: %w", err)
	}

	cbc.ioWriteLog, cbc.newIORoot, err = cbc.ioTree.Commit(ctx)
	if err != nil {
		return fmt.Errorf("state tree Commit: %w", err)
	}

	return nil
}

func (cbc *computeBatchContext) uploadBatch(ctx context.Context, hnss []*honestNodeStorage) error {
	var err error
	cbc.storageReceipts, err = storageBroadcastApplyBatch(ctx, hnss, cbc.bd.Header.Namespace, cbc.bd.Header.Round+1, []storage.ApplyOp{
		storage.ApplyOp{
			SrcRound: cbc.bd.Header.Round + 1,
			SrcRoot:  cbc.bd.IORoot,
			DstRoot:  cbc.newIORoot,
			WriteLog: cbc.ioWriteLog,
		},
		storage.ApplyOp{
			SrcRound: cbc.bd.Header.Round,
			SrcRoot:  cbc.bd.Header.StateRoot,
			DstRoot:  cbc.newStateRoot,
			WriteLog: cbc.stateWriteLog,
		},
	})
	if err != nil {
		return fmt.Errorf("storage broadcast apply batch: %w", err)
	}

	return nil
}

func (cbc *computeBatchContext) createCommitment(id *identity.Identity, rak signature.Signer, committeeID hash.Hash) error {
	var storageSigs []signature.Signature
	for _, receipt := range cbc.storageReceipts {
		storageSigs = append(storageSigs, receipt.Signature)
	}
	header := commitment.ComputeResultsHeader{
		PreviousHash: cbc.bd.Header.EncodedHash(),
		IORoot:       cbc.newIORoot,
		StateRoot:    cbc.newStateRoot,
		// TODO: allow script to set roothash messages?
		Messages: []*block.Message{},
	}
	computeBody := &commitment.ComputeBody{
		CommitteeID:       committeeID,
		Header:            header,
		StorageSignatures: storageSigs,
		TxnSchedSig:       cbc.bdSig,
		InputRoot:         cbc.bd.IORoot,
		InputStorageSigs:  cbc.bd.StorageSignatures,
	}
	if rak != nil {
		rakSig, err := signature.Sign(rak, commitment.ComputeResultsHeaderSignatureContext, cbor.Marshal(header))
		if err != nil {
			return fmt.Errorf("signature Sign RAK: %w", err)
		}

		computeBody.RakSig = rakSig.Signature
	}
	var err error
	cbc.commit, err = commitment.SignExecutorCommitment(id.NodeSigner, computeBody)
	if err != nil {
		return fmt.Errorf("commitment sign executor commitment: %w", err)
	}

	return nil
}

func (cbc *computeBatchContext) publishToCommittee(ht *honestTendermint, height int64, committee *scheduler.Committee, role scheduler.Role, ph *p2pHandle, runtimeID common.Namespace, groupVersion int64) error {
	if err := schedulerPublishToCommittee(ht, height, committee, role, ph, &p2p.Message{
		RuntimeID:    runtimeID,
		GroupVersion: groupVersion,
		SpanContext:  nil,
		ExecutorWorkerFinished: &p2p.ExecutorWorkerFinished{
			Commitment: *cbc.commit,
		},
	}); err != nil {
		return fmt.Errorf("scheduler publish to committee: %w", err)
	}

	return nil
}
