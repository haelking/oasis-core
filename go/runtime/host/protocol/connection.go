// Package protocol implements the Runtime Host Protocol.
package protocol

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/opentracing/opentracing-go"
	opentracingExt "github.com/opentracing/opentracing-go/ext"

	"github.com/oasislabs/oasis-core/go/common"
	"github.com/oasislabs/oasis-core/go/common/cbor"
	"github.com/oasislabs/oasis-core/go/common/errors"
	"github.com/oasislabs/oasis-core/go/common/logging"
	"github.com/oasislabs/oasis-core/go/common/tracing"
	"github.com/oasislabs/oasis-core/go/common/version"
)

const moduleName = "rhp/internal"

// ErrNotReady is the error reported when the Runtime Host Protocol is not initialized.
var ErrNotReady = errors.New(moduleName, 1, "rhp: not ready")

// Handler is a protocol message handler interface.
type Handler interface {
	// Handle given request and return a response.
	Handle(ctx context.Context, body *Body) (*Body, error)
}

// Connection is a Runtime Host Protocol connection interface.
type Connection interface {
	// Close closes the connection.
	Close()

	// Call sends a request to the other side and returns the response or error.
	Call(ctx context.Context, body *Body) (*Body, error)

	// InitHost performs initialization in host mode and transitions the connection to Ready state.
	//
	// This method must be called before the host will answer requests.
	//
	// Only one of InitHost/InitGuest can be called otherwise the method may panic.
	//
	// Returns the self-reported runtime version.
	InitHost(ctx context.Context, conn net.Conn) (*version.Version, error)

	// InitGuest performs initialization in guest mode and transitions the connection to Ready
	// state.
	//
	// Only one of InitHost/InitGuest can be called otherwise the method may panic.
	InitGuest(ctx context.Context, conn net.Conn) error
}

// state is the connection state.
type state uint8

const (
	stateUninitialized state = iota
	stateInitializing
	stateReady
	stateClosed
)

func (s state) String() string {
	switch s {
	case stateUninitialized:
		return "uninitialized"
	case stateInitializing:
		return "initializing"
	case stateReady:
		return "ready"
	case stateClosed:
		return "closed"
	default:
		return fmt.Sprintf("[malformed: %d]", s)
	}
}

// validStateTransitions are allowed connection state transitions.
var validStateTransitions = map[state][]state{
	stateUninitialized: {
		stateInitializing,
	},
	stateInitializing: {
		stateReady,
		stateClosed,
	},
	stateReady: {
		stateClosed,
	},
	// No transitions from Closed state.
	stateClosed: {},
}

type connection struct { // nolint: maligned
	sync.RWMutex

	conn  net.Conn
	codec *cbor.MessageCodec

	runtimeID common.Namespace
	handler   Handler

	state           state
	pendingRequests map[uint64]chan *Body
	nextRequestID   uint64

	outCh   chan *Message
	closeCh chan struct{}
	quitWg  sync.WaitGroup

	logger *logging.Logger
}

func (c *connection) getState() state {
	c.RLock()
	s := c.state
	c.RUnlock()
	return s
}

func (c *connection) setStateLocked(s state) {
	// Validate state transition.
	dests := validStateTransitions[c.state]

	var valid bool
	for _, dest := range dests {
		if dest == s {
			valid = true
			break
		}
	}

	if !valid {
		panic(fmt.Sprintf("invalid state transition: %s -> %s", c.state, s))
	}

	c.state = s
}

// Implements Connection.
func (c *connection) Close() {
	c.Lock()
	if c.state != stateReady && c.state != stateInitializing {
		c.Unlock()
		return
	}

	c.setStateLocked(stateClosed)
	c.Unlock()

	if err := c.conn.Close(); err != nil {
		c.logger.Error("error while closing connection",
			"err", err,
		)
	}

	// Wait for all the connection-handling goroutines to terminate.
	c.quitWg.Wait()
}

// Implements Connection.
func (c *connection) Call(ctx context.Context, body *Body) (*Body, error) {
	if c.getState() != stateReady {
		return nil, ErrNotReady
	}

	return c.call(ctx, body)
}

func (c *connection) call(ctx context.Context, body *Body) (*Body, error) {
	respCh, err := c.makeRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	select {
	case resp, ok := <-respCh:
		if !ok {
			return nil, fmt.Errorf("channel closed")
		}

		if resp.Error != nil {
			// Decode error.
			err = errors.FromCode(resp.Error.Module, resp.Error.Code)
			if err == nil {
				err = fmt.Errorf("%s", resp.Error.Message)
			}
			return nil, err
		}

		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *connection) makeRequest(ctx context.Context, body *Body) (<-chan *Body, error) {
	// Create channel for sending the response and grab next request identifier.
	ch := make(chan *Body, 1)

	c.Lock()
	id := c.nextRequestID
	c.nextRequestID++
	c.pendingRequests[id] = ch
	c.Unlock()

	span := opentracing.SpanFromContext(ctx)
	scBinary := []byte{}
	var err error
	if span != nil {
		scBinary, err = tracing.SpanContextToBinary(span.Context())
		if err != nil {
			c.logger.Error("error while marshalling span context",
				"err", err,
			)
		}
	}

	msg := Message{
		ID:          id,
		MessageType: MessageRequest,
		Body:        *body,
		SpanContext: scBinary,
	}

	// Queue the message.
	if err := c.sendMessage(ctx, &msg); err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	return ch, nil
}

func (c *connection) sendMessage(ctx context.Context, msg *Message) error {
	select {
	case c.outCh <- msg:
		return nil
	case <-c.closeCh:
		return fmt.Errorf("connection closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *connection) workerOutgoing() {
	defer c.quitWg.Done()

	for {
		select {
		case msg := <-c.outCh:
			// Outgoing message, send it.
			if err := c.codec.Write(msg); err != nil {
				c.logger.Error("error while sending message",
					"err", err,
				)
			}
		case <-c.closeCh:
			// Connection has terminated.
			return
		}
	}
}

func errorToBody(err error) *Body {
	module, code := errors.Code(err)
	return &Body{
		Error: &Error{
			Module:  module,
			Code:    code,
			Message: err.Error(),
		},
	}
}

func newResponseMessage(req *Message, body *Body) *Message {
	return &Message{
		ID:          req.ID,
		MessageType: MessageResponse,
		Body:        *body,
		SpanContext: cbor.FixSliceForSerde(nil),
	}
}

func (c *connection) handleMessage(ctx context.Context, message *Message) {
	switch message.MessageType {
	case MessageRequest:
		// Incoming request.
		var allowed bool
		state := c.getState()
		switch {
		case state == stateReady:
			// All requests allowed.
			allowed = true
		default:
			// No requests allowed.
			allowed = false
		}
		if !allowed {
			// Reject incoming requests if not in correct state.
			c.logger.Warn("rejecting incoming request before being ready",
				"state", state,
				"request", fmt.Sprintf("%+v", message.Body),
			)
			_ = c.sendMessage(ctx, newResponseMessage(message, errorToBody(ErrNotReady)))
			return
		}

		// Import runtime-provided span.
		var span = opentracing.SpanFromContext(ctx)
		if len(message.SpanContext) != 0 {
			sc, err := tracing.SpanContextFromBinary(message.SpanContext)
			if err != nil {
				c.logger.Error("error while unmarshalling span context",
					"err", err,
				)
			} else {
				span = opentracing.StartSpan("RHP", opentracingExt.RPCServerOption(sc))
				defer span.Finish()

				ctx = opentracing.ContextWithSpan(ctx, span)
			}
		}

		// Call actual handler.
		body, err := c.handler.Handle(ctx, &message.Body)
		if err != nil {
			body = errorToBody(err)
		}

		// Prepare and send response.
		if err := c.sendMessage(ctx, newResponseMessage(message, body)); err != nil {
			c.logger.Warn("failed to send response message",
				"err", err,
			)
		}
	case MessageResponse:
		// Response to our request.
		c.Lock()
		respCh, ok := c.pendingRequests[message.ID]
		delete(c.pendingRequests, message.ID)
		c.Unlock()

		if !ok {
			c.logger.Warn("received a response but no request with id is outstanding",
				"id", message.ID,
			)
			break
		}

		respCh <- &message.Body
		close(respCh)
	default:
		c.logger.Warn("received a malformed message from worker, ignoring",
			"message", fmt.Sprintf("%+v", message),
		)
	}
}

func (c *connection) workerIncoming() {
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		// Close connection and signal that connection is closed.
		_ = c.conn.Close()
		close(c.closeCh)

		// Cancel all request handlers.
		cancel()

		// Close all pending request channels.
		c.Lock()
		for id, ch := range c.pendingRequests {
			close(ch)
			delete(c.pendingRequests, id)
		}
		c.Unlock()

		c.quitWg.Done()
	}()

	for {
		// Decode incoming messages.
		var message Message
		err := c.codec.Read(&message)
		if err != nil {
			c.logger.Error("error while receiving message from worker",
				"err", err,
			)
			break
		}

		// Handle message in a separate goroutine.
		go c.handleMessage(ctx, &message)
	}
}

func (c *connection) initConn(conn net.Conn) {
	c.Lock()
	defer c.Unlock()

	if c.state != stateUninitialized {
		panic("rhp: connection already initialized")
	}

	c.conn = conn
	c.codec = cbor.NewMessageCodec(conn)

	c.quitWg.Add(2)
	go c.workerIncoming()
	go c.workerOutgoing()

	// Change protocol state to Initializing so that some of the requests are allowed.
	c.setStateLocked(stateInitializing)
}

// Implements Connection.
func (c *connection) InitGuest(ctx context.Context, conn net.Conn) error {
	c.initConn(conn)

	// Transition the protocol state to Ready.
	c.Lock()
	c.setStateLocked(stateReady)
	c.Unlock()

	return nil
}

// Implements Connection.
func (c *connection) InitHost(ctx context.Context, conn net.Conn) (*version.Version, error) {
	c.initConn(conn)

	// Check Runtime Host Protocol version.
	rsp, err := c.call(ctx, &Body{RuntimeInfoRequest: &RuntimeInfoRequest{
		RuntimeID: c.runtimeID,
	}})
	switch {
	default:
	case err != nil:
		return nil, fmt.Errorf("rhp: error while requesting runtime info: %w", err)
	case rsp.RuntimeInfoResponse == nil:
		c.logger.Error("unexpected response to RuntimeInfoRequest",
			"response", rsp,
		)
		return nil, fmt.Errorf("rhp: unexpected response to RuntimeInfoRequest")
	}

	info := rsp.RuntimeInfoResponse
	if ver := version.FromU64(info.ProtocolVersion); ver.MajorMinor() != version.RuntimeProtocol.MajorMinor() {
		c.logger.Error("runtime has incompatible protocol version",
			"version", ver,
			"expected_version", version.RuntimeProtocol,
		)
		return nil, fmt.Errorf("rhp: incompatible protocol version (expected: %s got: %s)",
			version.RuntimeProtocol,
			ver,
		)
	}

	rtVersion := version.FromU64(info.RuntimeVersion)
	c.logger.Info("runtime host protocol initialized", "runtime_version", rtVersion)

	// Transition the protocol state to Ready.
	c.Lock()
	c.setStateLocked(stateReady)
	c.Unlock()

	return &rtVersion, nil
}

// NewConnection creates a new uninitialized RHP connection.
func NewConnection(logger *logging.Logger, runtimeID common.Namespace, handler Handler) (Connection, error) {
	c := &connection{
		runtimeID:       runtimeID,
		handler:         handler,
		state:           stateUninitialized,
		pendingRequests: make(map[uint64]chan *Body),
		outCh:           make(chan *Message),
		closeCh:         make(chan struct{}),
		logger:          logger,
	}

	return c, nil
}
