// Package transport provides a pluggable, message-oriented connection
// abstraction. Implementations frame a byte stream (or use native message
// boundaries) so callers exchange whole messages, not bytes.
package transport

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// MaxMessage bounds a single message to guard against malformed length headers.
const MaxMessage = 32 << 20 // 32 MiB

// Conn is one established bidirectional message connection.
type Conn interface {
	Read() ([]byte, error)
	Write(b []byte) error
	RemoteAddr() string
	Close() error
}

// Dialer establishes an outbound Conn (agent side).
type Dialer interface {
	Dial() (Conn, error)
}

// Listener accepts inbound Conns (hub side).
type Listener interface {
	Accept() (Conn, error)
}

// ---- TCP transport: 4-byte big-endian length prefix + body ----

type tcpConn struct {
	c   net.Conn
	r   *bufio.Reader
	wmu sync.Mutex
}

func newTCPConn(c net.Conn) *tcpConn { return &tcpConn{c: c, r: bufio.NewReader(c)} }

func (t *tcpConn) Read() ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(t.r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return []byte{}, nil
	}
	if n > MaxMessage {
		return nil, errors.New("transport: message too large")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(t.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (t *tcpConn) Write(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	frame := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(b)))
	copy(frame[4:], b)
	_, err := t.c.Write(frame)
	return err
}

func (t *tcpConn) RemoteAddr() string { return t.c.RemoteAddr().String() }
func (t *tcpConn) Close() error       { return t.c.Close() }

// TCPDialer dials a TCP endpoint.
type TCPDialer struct{ Addr string }

func (d TCPDialer) Dial() (Conn, error) {
	c, err := net.Dial("tcp", d.Addr)
	if err != nil {
		return nil, err
	}
	return newTCPConn(c), nil
}

// Pipe returns a connected pair of in-memory Conns (framed like TCP). Useful for
// loopback and tests.
func Pipe() (Conn, Conn) {
	a, b := net.Pipe()
	return newTCPConn(a), newTCPConn(b)
}

// TCPListener accepts TCP connections.
type TCPListener struct{ ln net.Listener }

// ListenTCP starts a TCP listener.
func ListenTCP(addr string) (*TCPListener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &TCPListener{ln: ln}, nil
}

func (l *TCPListener) Accept() (Conn, error) {
	c, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	return newTCPConn(c), nil
}

func (l *TCPListener) Addr() net.Addr { return l.ln.Addr() }
func (l *TCPListener) Close() error   { return l.ln.Close() }

// ---- WebSocket transport ----

type wsConn struct {
	c   *websocket.Conn
	wmu sync.Mutex
}

func newWSConn(c *websocket.Conn) *wsConn { return &wsConn{c: c} }

func (w *wsConn) Read() ([]byte, error) {
	_, data, err := w.c.ReadMessage()
	return data, err
}

func (w *wsConn) Write(b []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	return w.c.WriteMessage(websocket.TextMessage, b)
}

func (w *wsConn) RemoteAddr() string { return w.c.RemoteAddr().String() }
func (w *wsConn) Close() error       { return w.c.Close() }

// WSDialer dials a WebSocket endpoint, e.g. ws://host:port/agent.
type WSDialer struct{ URL string }

func (d WSDialer) Dial() (Conn, error) {
	c, _, err := websocket.DefaultDialer.Dial(d.URL, nil)
	if err != nil {
		return nil, err
	}
	return newWSConn(c), nil
}

// WSListener bridges an http.Handler upgrade into the Listener interface.
type WSListener struct {
	up websocket.Upgrader
	ch chan Conn
}

// NewWSListener creates a WebSocket listener whose Handler should be mounted on
// an http server. Each upgraded connection is delivered via Accept.
func NewWSListener() *WSListener {
	return &WSListener{
		up: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		ch: make(chan Conn),
	}
}

// Handler upgrades inbound HTTP requests and hands them to Accept.
func (l *WSListener) Handler(w http.ResponseWriter, r *http.Request) {
	c, err := l.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := newWSConn(c)
	select {
	case l.ch <- conn:
	default:
		// no acceptor ready; still deliver (blocking) so we don't drop it
		l.ch <- conn
	}
}

func (l *WSListener) Accept() (Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, errors.New("transport: ws listener closed")
	}
	return c, nil
}
