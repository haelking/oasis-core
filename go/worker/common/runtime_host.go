package common

import (
	"context"
	"fmt"
	"sync"

	"github.com/oasislabs/oasis-core/go/runtime/host"
	"github.com/oasislabs/oasis-core/go/runtime/host/protocol"
	runtimeRegistry "github.com/oasislabs/oasis-core/go/runtime/registry"
)

// RuntimeHostNode provides methods for nodes that need to host runtimes.
type RuntimeHostNode struct {
	sync.Mutex

	cfg     *RuntimeHostConfig
	factory RuntimeHostHandlerFactory

	runtime host.Runtime
}

// ProvisionHostedRuntime provisions the configured runtime.
//
// This method may return before the runtime is fully provisioned. The returned runtime will not be
// started automatically, you must call Start explicitly.
func (n *RuntimeHostNode) ProvisionHostedRuntime(ctx context.Context) (host.Runtime, error) {
	rt, err := n.factory.GetRuntime().RegistryDescriptor(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime registry descriptor: %w", err)
	}

	provisioner, ok := n.cfg.Provisioners[rt.TEEHardware]
	if !ok {
		return nil, fmt.Errorf("no provisioner suitable for TEE hardware '%s'", rt.TEEHardware)
	}

	// Get a copy of the configuration template for the given runtime and apply updates.
	cfg, ok := n.cfg.Runtimes[rt.ID]
	if !ok {
		return nil, fmt.Errorf("missing runtime host configuration for runtime '%s'", rt.ID)
	}

	// Provision the runtime.
	cfg.MessageHandler = n.factory.NewRuntimeHostHandler()
	prt, err := provisioner.NewRuntime(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to provision runtime: %w", err)
	}
	n.factory.StartWorker(prt)

	n.Lock()
	n.runtime = prt
	n.Unlock()

	return prt, nil
}

// GetHostedRuntime returns the provisioned hosted runtime (if any).
func (n *RuntimeHostNode) GetHostedRuntime() host.Runtime {
	n.Lock()
	rt := n.runtime
	n.Unlock()
	return rt
}

// RuntimeHostHandlerFactory is an interface that can be used to create new runtime handlers when
// provisioning hosted runtimes.
type RuntimeHostHandlerFactory interface {
	// GetRuntime returns the registered runtime for which a runtime host handler is to be created.
	GetRuntime() runtimeRegistry.Runtime

	// NewRuntimeHostHandler creates a new runtime host handler.
	NewRuntimeHostHandler() protocol.Handler

	// StartWorker starts the worker
	StartWorker(host host.Runtime)
}

// NewRuntimeHostNode creates a new runtime host node.
func NewRuntimeHostNode(cfg *RuntimeHostConfig, factory RuntimeHostHandlerFactory) (*RuntimeHostNode, error) {
	if cfg == nil {
		return nil, fmt.Errorf("runtime host not configured")
	}

	return &RuntimeHostNode{
		cfg:     cfg,
		factory: factory,
	}, nil
}
