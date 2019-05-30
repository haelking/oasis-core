// Package api implements the root hash backend API and common datastructures.
package api

import (
	"context"
	"encoding"
	"encoding/hex"
	"errors"

	"github.com/oasislabs/ekiden/go/common/cbor"
	"github.com/oasislabs/ekiden/go/common/crypto/hash"
	"github.com/oasislabs/ekiden/go/common/crypto/signature"
	"github.com/oasislabs/ekiden/go/common/pubsub"
	"github.com/oasislabs/ekiden/go/roothash/api/block"
)

const (
	// HashSize is the size of the various hashes in bytes.
	HashSize = 32
)

var (
	// ErrMalformedHash is the error returned when a hash is malformed.
	ErrMalformedHash = errors.New("roothash: malformed hash")

	// ErrInvalidArgument is the error returned on malformed argument(s).
	ErrInvalidArgument = errors.New("roothash: invalid argument")

	// ErrNotFound is the error returned when a block is not found.
	ErrNotFound = errors.New("roothash: block not found")

	_ encoding.BinaryMarshaler   = (*OpaqueCommitment)(nil)
	_ encoding.BinaryUnmarshaler = (*OpaqueCommitment)(nil)
	_ cbor.Marshaler             = (*DiscrepancyDetectedEvent)(nil)
	_ cbor.Unmarshaler           = (*DiscrepancyDetectedEvent)(nil)

	// RakSigContext is the context string of a batch's RAK signature.
	RakSigContext = []byte("EkBatch-")
)

// OpaqueCommitment is an opaque commitment from a compute node.
type OpaqueCommitment struct {
	// Data is the opaque commitment.
	Data []byte
}

// MarshalBinary encodes an opaque commitment into binary form.
func (c *OpaqueCommitment) MarshalBinary() (data []byte, err error) {
	data = append([]byte{}, c.Data...)
	return
}

// UnmarshalBinary decodes a binary marshaled opaque commitment.
func (c *OpaqueCommitment) UnmarshalBinary(data []byte) error {
	c.Data = append([]byte{}, data...)

	return nil
}

// String returns a string representation of the opaque commitment.
func (c *OpaqueCommitment) String() string {
	return hex.EncodeToString(c.Data)
}

// Backend is a root hash consensus implementation.
type Backend interface {
	// GetLatestBlock returns the latest block.
	//
	// The metadata contained in this block can be further used to get
	// the latest state from the storage backend.
	// TODO: ctx should be removed since we use tendermintBackend.ctx -Matevz
	GetLatestBlock(context.Context, signature.PublicKey) (*block.Block, error)

	// GetBlock returns the block at a specific height.
	GetBlock(context.Context, signature.PublicKey, uint64) (*block.Block, error)

	// WatchBlocks returns a channel that produces a stream of blocks.
	//
	// The latest block if any will get pushed to the stream immediately.
	// Subsequent blocks will be pushed into the stream as they are
	// confirmed.
	WatchBlocks(signature.PublicKey) (<-chan *block.Block, *pubsub.Subscription, error)

	// WatchEvents returns a stream of protocol events.
	WatchEvents(signature.PublicKey) (<-chan *Event, *pubsub.Subscription, error)

	// WatchPrunedBlocks returns a channel that produces a stream of pruned
	// blocks.
	WatchPrunedBlocks() (<-chan *PrunedBlock, *pubsub.Subscription, error)

	// Commit commits to a result of processing a batch of runtime invocations.
	// TODO: ctx should be removed since we use tendermintBackend.ctx -Matevz
	Commit(context.Context, signature.PublicKey, *OpaqueCommitment) error

	// Cleanup cleans up the roothash backend.
	Cleanup()
}

// BlockBackend is a root hash backend that is backed by a blockchain.
type BlockBackend interface {
	Backend

	// WatchAnnotatedBlocks returns a channel that produces a stream of
	// annotated blocks.
	WatchAnnotatedBlocks(signature.PublicKey) (<-chan *AnnotatedBlock, *pubsub.Subscription, error)
}

// AnnotatedBlock is an annotated roothash block.
type AnnotatedBlock struct {
	// Height is the underlying roothash backend's block height that
	// generated this block.
	Height int64

	// Block is the roothash block.
	Block *block.Block
}

// MapAnnotatedBlockToBlock maps a channel of annotated blocks to a channel of
// plain blocks.
func MapAnnotatedBlockToBlock(annCh <-chan *AnnotatedBlock) <-chan *block.Block {
	ch := make(chan *block.Block)
	go func() {
		for {
			ann, ok := <-annCh
			if !ok {
				close(ch)
				return
			}

			ch <- ann.Block
		}
	}()

	return ch
}

// DiscrepancyDetectedEvent is a discrepancy detected event.
type DiscrepancyDetectedEvent struct {
	// IORoot is the I/O merkle root that is set when a discrepancy
	// is detected to signal to the backup workers that a computation
	// should be re-executed.
	//
	// The backup workers should use this root to fetch the batch.
	IORoot hash.Hash `codec:"io_root"`

	// BlockHeader is the block header of the block on which the backup
	// computation should be based.
	BlockHeader block.Header `codec:"header"`
}

// MarshalCBOR serializes the type into a CBOR byte vector.
func (e *DiscrepancyDetectedEvent) MarshalCBOR() []byte {
	return cbor.Marshal(e)
}

// UnmarshalCBOR decodes a CBOR marshaled block.
func (e *DiscrepancyDetectedEvent) UnmarshalCBOR(data []byte) error {
	return cbor.Unmarshal(data, e)
}

// Event is a protocol event.
type Event struct {
	// DiscrepancyDetected is the I/O merkle root that is set when a
	// discrepancy is detected to signal to the backup workers that a
	// computation should be re-executed.
	DiscrepancyDetected *DiscrepancyDetectedEvent
}

// MetricsMonitorable is the interface exposed by backends capable of
// providing metrics data.
type MetricsMonitorable interface {
	// WatchAllBlocks returns a channel that produces a stream of blocks.
	//
	// All blocks from all runtimes will be pushed into the stream
	// immediately as they are finalized.
	WatchAllBlocks() (<-chan *block.Block, *pubsub.Subscription)
}

// PrunedBlock describes a block that was pruned.
type PrunedBlock struct {
	// RuntimeID is the runtime identifier of the block that was pruned.
	RuntimeID signature.PublicKey
	// Round is the block round.
	Round uint64
}

// Genesis is the roothash genesis state.
type Genesis struct {
	// Blocks is the per-runtime map of genesis blocks.
	Blocks map[signature.MapKey]*block.Block `codec:"blocks,omit_empty"`
}
