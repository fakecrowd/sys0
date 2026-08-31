// Package rpc implements a minimal JSON-RPC 2.0 codec and a peer that
// multiplexes requests, responses and notifications over a transport.Conn.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/fakecrowd/sys0/internal/transport"
)

// Message is a JSON-RPC 2.0 envelope. A request carries ID+Method, a response
// carries ID+Result/Error, a notification carries Method only.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Errorf builds an *Error.
func Errorf(code int, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Common error codes (JSON-RPC reserves -32768..-32000; app codes use 4xxx).
const (
	CodeParse     = -32700
	CodeInvalid   = -32600
	CodeNoMethod  = -32601
	CodeBadParams = -32602
	CodeInternal  = -32603

	CodeOffline   = 4010
	CodeForbidden = 4030
	CodeTimeout   = 4080
)

// Handler answers an inbound request. Returning an *Error produces an error
// response; otherwise result is marshalled into the response.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, *Error)

// NotifyHandler receives inbound notifications (no response expected).
type NotifyHandler func(method string, params json.RawMessage)

// Peer runs the JSON-RPC state machine over a single connection.
type Peer struct {
	conn    transport.Conn
	handler Handler
	notify  NotifyHandler

	// async, when non-nil, decides which inbound request methods are served on
	// their own goroutine. Requests for which it returns false are served
	// INLINE in the read loop, which preserves their arrival order — essential
	// for interactive terminal input (task.input/shell.input/resize), where
	// concurrent goroutines would otherwise race to write the PTY and scramble
	// the echoed characters. Long-blocking requests (e.g. shell.run) should
	// return true so they don't stall the connection (heartbeats, later input).
	// It receives the raw params too, so a wrapper method (e.g. the hub's
	// "dispatch") can peek at the inner call to classify it. When nil, every
	// request is served on its own goroutine (legacy behavior).
	async func(method string, params json.RawMessage) bool

	mu      sync.Mutex
	pending map[string]chan *Message
	seq     uint64
	closed  bool
}

// NewPeer creates a peer. handler/notify may be nil if this side never
// receives requests/notifications.
func NewPeer(conn transport.Conn, handler Handler, notify NotifyHandler) *Peer {
	return &Peer{conn: conn, handler: handler, notify: notify, pending: map[string]chan *Message{}}
}

// SetAsyncFunc installs a predicate deciding which inbound requests are
// dispatched on their own goroutine vs. served inline (in arrival order).
// Call once, before Run. See the Peer.async field for rationale.
func (p *Peer) SetAsyncFunc(f func(method string, params json.RawMessage) bool) { p.async = f }

// Run reads and dispatches messages until the connection fails or ctx is done.
func (p *Peer) Run(ctx context.Context) error {
	go func() { <-ctx.Done(); p.conn.Close() }()
	for {
		raw, err := p.conn.Read()
		if err != nil {
			p.failAll(err)
			return err
		}
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			continue // skip malformed frame
		}
		switch {
		case m.Method != "" && m.ID != "": // request
			// Serve inline (preserving arrival order) unless flagged async.
			// Inline dispatch keeps fast interactive input (e.g. task.input ->
			// PTY write) from being reordered by the goroutine scheduler.
			if p.async != nil && !p.async(m.Method, m.Params) {
				p.serve(ctx, &m)
			} else {
				go p.serve(ctx, &m)
			}
		case m.Method != "": // notification
			if p.notify != nil {
				p.notify(m.Method, m.Params)
			}
		case m.ID != "": // response
			p.deliver(&m)
		}
	}
}

func (p *Peer) serve(ctx context.Context, req *Message) {
	if p.handler == nil {
		p.write(&Message{JSONRPC: "2.0", ID: req.ID, Error: Errorf(CodeNoMethod, "no handler")})
		return
	}
	res, rerr := p.handler(ctx, req.Method, req.Params)
	out := &Message{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		out.Error = rerr
	} else {
		b, err := json.Marshal(res)
		if err != nil {
			out.Error = Errorf(CodeInternal, "marshal result: %v", err)
		} else {
			out.Result = b
		}
	}
	p.write(out)
}

func (p *Peer) deliver(m *Message) {
	p.mu.Lock()
	ch := p.pending[m.ID]
	delete(p.pending, m.ID)
	p.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (p *Peer) failAll(err error) {
	p.mu.Lock()
	p.closed = true
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.mu.Unlock()
}

// Call issues a request and waits for the matching response.
func (p *Peer) Call(ctx context.Context, method string, params any) (json.RawMessage, *Error) {
	pb, err := marshalParams(params)
	if err != nil {
		return nil, Errorf(CodeBadParams, "%v", err)
	}
	id := fmt.Sprintf("c%d", atomic.AddUint64(&p.seq, 1))
	ch := make(chan *Message, 1)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, Errorf(CodeInternal, "peer closed")
	}
	p.pending[id] = ch
	p.mu.Unlock()

	if err := p.write(&Message{JSONRPC: "2.0", ID: id, Method: method, Params: pb}); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, Errorf(CodeInternal, "write: %v", err)
	}
	select {
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, Errorf(CodeTimeout, "call timeout")
	case m, ok := <-ch:
		if !ok {
			return nil, Errorf(CodeInternal, "connection lost")
		}
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	}
}

// Notify sends a notification (no response).
func (p *Peer) Notify(method string, params any) error {
	pb, err := marshalParams(params)
	if err != nil {
		return err
	}
	return p.write(&Message{JSONRPC: "2.0", Method: method, Params: pb})
}

func (p *Peer) write(m *Message) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return p.conn.Write(b)
}

// Close closes the underlying connection.
func (p *Peer) Close() error { return p.conn.Close() }

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(params)
}
