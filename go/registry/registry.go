// Package registry implements the runtime and entity registries.
package registry

import (
	"errors"

	"github.com/oasislabs/ekiden/go/common/contract"
	"github.com/oasislabs/ekiden/go/common/crypto/signature"
	"github.com/oasislabs/ekiden/go/common/entity"
	"github.com/oasislabs/ekiden/go/common/node"
	"github.com/oasislabs/ekiden/go/common/pubsub"
	"github.com/oasislabs/ekiden/go/epochtime"
)

var (
	// RegisterEntitySignatureContext is the context used for entity
	// registration.
	RegisterEntitySignatureContext = []byte("EkEntReg")

	// DeregisterEntitySignatureContext is the context used for entity
	// deregistration.
	DeregisterEntitySignatureContext = []byte("EkEDeReg")

	// RegisterNodeSignatureContext is the context used for node
	// registration.
	RegisterNodeSignatureContext = []byte("EkNodReg")

	// RegisterContractSignatureContext is the context used for contract
	// registration.
	RegisterContractSignatureContext = []byte("EkConReg")

	// ErrInvalidArgument is the error returned on malformed argument(s).
	ErrInvalidArgument = errors.New("registry: invalid argument")

	// ErrInvalidSignature is the error returned on an invalid signature.
	ErrInvalidSignature = errors.New("registry: invalid signature")

	// ErrBadEntityForNode is the error returned when a node registration
	// with an unknown entity is attempted.
	ErrBadEntityForNode = errors.New("registry: unknown entity in node registration")
)

// EntityRegistry is a entity registry implementation.
type EntityRegistry interface {
	// RegisterEntity registers and or updates an entity with the registry.
	//
	// The signature should be made using RegisterEntitySignatureContext.
	RegisterEntity(*entity.Entity, *signature.Signature) error

	// DeregisterEntity deregisters an entity.
	//
	// The signature should be made using DeregisterEntitySignatureContext.
	DeregisterEntity(signature.PublicKey, *signature.Signature) error

	// GetEntity gets an entity by ID.
	GetEntity(signature.PublicKey) *entity.Entity

	// GetEntities gets a list of all registered entities.
	GetEntities() []*entity.Entity

	// WatchEntities returns a channel that produces a stream of
	// EntityEvent on entity registration changes.
	WatchEntities() (<-chan *EntityEvent, *pubsub.Subscription)

	// RegisterNode registers and or updates a node with the registry.
	//
	// The signature should be made using RegisterNodeSignatureContext.
	RegisterNode(*node.Node, *signature.Signature) error

	// GetNode gets a node by ID.
	GetNode(signature.PublicKey) *node.Node

	// GetNodes gets a list of all registered nodes.
	GetNodes() []*node.Node

	// GetNodesForEntity gets a list of nodes registered to an entity ID.
	GetNodesForEntity(signature.PublicKey) []*node.Node

	// WatchNodes returns a channel that produces a stream of
	// NodeEvent on node registration changes.
	WatchNodes() (<-chan *NodeEvent, *pubsub.Subscription)

	// WatchNodeList returns a channel that produces a stream of NodeList.
	// Upon subscription, the node list for the current epoch will be sent
	// immediately if available.
	//
	// Each node list will be sorted by node ID in lexographically ascending
	// order.
	WatchNodeList() (<-chan *NodeList, *pubsub.Subscription)
}

// EntityEvent is the event that is returned via WatchEntities to signify
// entity registration changes and updates.
type EntityEvent struct {
	Entity         *entity.Entity
	IsRegistration bool
}

// NodeEvent is the event that is returned via WatchNodes to signify node
// registration changes and updates.
type NodeEvent struct {
	Node           *node.Node
	IsRegistration bool
}

// NodeList is a per-epoch immutable node list.
type NodeList struct {
	Epoch epochtime.EpochTime
	Nodes []*node.Node
}

// ContractRegistry is a contract (runtime) registry implementation.
type ContractRegistry interface {
	// RegisterContract registers a contract.
	RegisterContract(*contract.Contract, *signature.Signature) error

	// GetContract gets a contract by ID.
	GetContract(signature.PublicKey) *contract.Contract

	// WatchContracts returns a stream of Contract.  Upon subscription,
	// all contracts will be sent immediately.
	WatchContracts() (<-chan *contract.Contract, *pubsub.Subscription)
}

func subscribeTypedEntityEvent(notifier *pubsub.Broker) (<-chan *EntityEvent, *pubsub.Subscription) {
	typedCh := make(chan *EntityEvent)
	sub := notifier.Subscribe()
	sub.Unwrap(typedCh)

	return typedCh, sub
}

func subscribeTypedNodeEvent(notifier *pubsub.Broker) (<-chan *NodeEvent, *pubsub.Subscription) {
	typedCh := make(chan *NodeEvent)
	sub := notifier.Subscribe()
	sub.Unwrap(typedCh)

	return typedCh, sub
}

func subscribeTypedNodeList(notifier *pubsub.Broker) (<-chan *NodeList, *pubsub.Subscription) {
	typedCh := make(chan *NodeList)
	sub := notifier.Subscribe()
	sub.Unwrap(typedCh)

	return typedCh, sub
}

func subscribeTypedContract(notifier *pubsub.Broker) (<-chan *contract.Contract, *pubsub.Subscription) {
	typedCh := make(chan *contract.Contract)
	sub := notifier.Subscribe()
	sub.Unwrap(typedCh)

	return typedCh, sub
}