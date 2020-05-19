package committee

import (
	"context"
	"errors"

	"github.com/opentracing/opentracing-go"

	"github.com/oasislabs/oasis-core/go/common/cbor"
	"github.com/oasislabs/oasis-core/go/common/logging"
	keymanagerApi "github.com/oasislabs/oasis-core/go/keymanager/api"
	keymanagerClient "github.com/oasislabs/oasis-core/go/keymanager/client"
	"github.com/oasislabs/oasis-core/go/runtime/host"
	"github.com/oasislabs/oasis-core/go/runtime/host/protocol"
	"github.com/oasislabs/oasis-core/go/runtime/localstorage"
	runtimeRegistry "github.com/oasislabs/oasis-core/go/runtime/registry"
	storage "github.com/oasislabs/oasis-core/go/storage/api"
)

var (
	errMethodNotSupported   = errors.New("method not supported")
	errEndpointNotSupported = errors.New("RPC endpoint not supported")
)

// computeRuntimeHostHandler is a runtime host handler suitable for compute runtimes.
type computeRuntimeHostHandler struct {
	runtime runtimeRegistry.Runtime

	storage          storage.Backend
	keyManager       keymanagerApi.Backend
	keyManagerClient *keymanagerClient.Client
	localStorage     localstorage.LocalStorage
}

func (h *computeRuntimeHostHandler) Handle(ctx context.Context, body *protocol.Body) (*protocol.Body, error) {
	// RPC.
	if body.HostRPCCallRequest != nil {
		switch body.HostRPCCallRequest.Endpoint {
		case keymanagerApi.EnclaveRPCEndpoint:
			// Call into the remote key manager.
			if h.keyManagerClient == nil {
				return nil, errEndpointNotSupported
			}
			res, err := h.keyManagerClient.CallRemote(ctx, body.HostRPCCallRequest.Request)
			if err != nil {
				return nil, err
			}
			return &protocol.Body{HostRPCCallResponse: &protocol.HostRPCCallResponse{
				Response: cbor.FixSliceForSerde(res),
			}}, nil
		default:
			return nil, errEndpointNotSupported
		}
	}
	// Storage.
	if body.HostStorageSyncRequest != nil {
		rq := body.HostStorageSyncRequest
		span, sctx := opentracing.StartSpanFromContext(ctx, "storage.Sync")
		defer span.Finish()

		var rsp *storage.ProofResponse
		var err error
		switch {
		case rq.SyncGet != nil:
			rsp, err = h.storage.SyncGet(sctx, rq.SyncGet)
		case rq.SyncGetPrefixes != nil:
			rsp, err = h.storage.SyncGetPrefixes(sctx, rq.SyncGetPrefixes)
		case rq.SyncIterate != nil:
			rsp, err = h.storage.SyncIterate(sctx, rq.SyncIterate)
		default:
			return nil, errMethodNotSupported
		}
		if err != nil {
			return nil, err
		}

		return &protocol.Body{HostStorageSyncResponse: &protocol.HostStorageSyncResponse{ProofResponse: rsp}}, nil
	}
	// Local storage.
	if body.HostLocalStorageGetRequest != nil {
		value, err := h.localStorage.Get(body.HostLocalStorageGetRequest.Key)
		if err != nil {
			return nil, err
		}
		return &protocol.Body{HostLocalStorageGetResponse: &protocol.HostLocalStorageGetResponse{Value: value}}, nil
	}
	if body.HostLocalStorageSetRequest != nil {
		if err := h.localStorage.Set(body.HostLocalStorageSetRequest.Key, body.HostLocalStorageSetRequest.Value); err != nil {
			return nil, err
		}
		return &protocol.Body{HostLocalStorageSetResponse: &protocol.Empty{}}, nil
	}

	return nil, errMethodNotSupported
}

// Implements RuntimeHostHandlerFactory.
func (n *Node) GetRuntime() runtimeRegistry.Runtime {
	return n.Runtime
}

// computeRuntimeHostworker is a runtime host handler suitable for compute runtimes.
type computeRuntimeHostworker struct {
	ctx context.Context

	logger *logging.Logger

	runtime    runtimeRegistry.Runtime
	host       host.Runtime
	keyManager keymanagerApi.Backend
}

func (h *computeRuntimeHostworker) watchPolicyUpdates() {
	// Wait for the runtime.
	rt, err := h.runtime.RegistryDescriptor(h.ctx)
	if err != nil {
		h.logger.Error("failed to wait for registry descriptor",
			"err", err,
		)
		return
	}
	if rt.KeyManager == nil {
		h.logger.Info("no keymanager needed, not watching for policy updates")
		return
	}

	stCh, stSub := h.keyManager.WatchStatuses()
	defer stSub.Close()
	h.logger.Info("watching policy updates", "keymanager_runtime", rt.KeyManager)

	for {
		select {
		case <-h.ctx.Done():
			return
		case st := <-stCh:
			h.logger.Info("got policy update", "status", st)

			// Ignore status updates if key manager is not yet known (is nil) or if the status
			// update is for a different key manager.
			if !st.ID.Equal(rt.KeyManager) {
				continue
			}

			raw := cbor.Marshal(st.Policy)
			req := &protocol.Body{RuntimeKeyManagerPolicyUpdateRequest: &protocol.RuntimeKeyManagerPolicyUpdateRequest{
				SignedPolicyRaw: raw,
			}}

			response, err := h.host.Call(h.ctx, req)
			if err != nil {
				h.logger.Error("failed to dispatch RPC call to runtime",
					"err", err,
				)
				continue
			}

			if response.Error != nil {
				h.logger.Error("error from runtime",
					"err", response.Error.Message,
				)
				continue
			}
			h.logger.Info("finished")
		}
	}
}

// Implements RuntimeHostHandlerFactory.
func (n *Node) StartWorker(host host.Runtime) {
	w := &computeRuntimeHostworker{
		context.Background(),
		logging.GetLogger("committee/runtime-host"),
		n.Runtime,
		host,
		n.KeyManager,
	}
	go w.watchPolicyUpdates()
}

// Implements RuntimeHostHandlerFactory.
func (n *Node) NewRuntimeHostHandler() protocol.Handler {
	handler := &computeRuntimeHostHandler{
		n.Runtime,
		n.Runtime.Storage(),
		n.KeyManager,
		n.KeyManagerClient,
		n.Runtime.LocalStorage(),
	}

	return handler
}
