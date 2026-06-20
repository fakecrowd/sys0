//go:build !modular || mod_shell

package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

const shellBufferCap = 256 << 10 // keep last 256 KiB of scrollback per shell

// shellSession is one persistent interactive PTY-backed shell on the agent
// (cross-platform via go-pty: Unix pty / Windows ConPTY). Unlike a transient
// stream, it lives on the agent across console (re)connections: output is
// buffered in a ring so a reattaching console can replay recent scrollback, and
// the read loop keeps running (and buffering) even when no console is attached.
type shellSession struct {
	id    string
	name  string
	shell string

	mu      sync.Mutex
	state   string // running | exited
	exit    int
	cols    int
	rows    int
	started int64
	pty     pty.Pty
	cmd     *pty.Cmd
	buf     []byte // ring buffer of recent output
}

func (s *shellSession) info() wire.ShellInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return wire.ShellInfo{
		Session: s.id, Name: s.name, Shell: s.shell, State: s.state,
		Exit: s.exit, Cols: s.cols, Rows: s.rows, Started: s.started,
	}
}

func (s *shellSession) append(b []byte) {
	s.mu.Lock()
	s.buf = append(s.buf, b...)
	if len(s.buf) > shellBufferCap {
		s.buf = s.buf[len(s.buf)-shellBufferCap:]
	}
	s.mu.Unlock()
}

type shellManager struct {
	mu       sync.Mutex
	sessions map[string]*shellSession
}

func newShellManager() *shellManager {
	return &shellManager{sessions: map[string]*shellSession{}}
}

// shellMgr is the package-global shell session manager (shell module only).
var shellMgr = newShellManager()

// doShellOpen spawns a persistent PTY shell. Its output is buffered on the agent
// and streamed to any subscribed console via emit; the session survives console
// disconnects and can be reattached later (see shell.list / shell.output).
func (a *Agent) doShellOpen(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellOpenParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	shell := p.Shell
	if shell == "" {
		shell = pickShell()
	}
	cols, rows := p.Cols, p.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	ptmx, err := pty.New()
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "open pty: %v", err)
	}
	cmd := ptmx.Command(shell)
	if err := cmd.Start(); err != nil {
		ptmx.Close()
		return nil, rpc.Errorf(rpc.CodeInternal, "start shell: %v", err)
	}
	_ = ptmx.Resize(cols, rows)

	id := "s" + randID()
	name := p.Name
	if name == "" {
		name = shellBase(shell)
	}
	sess := &shellSession{
		id: id, name: name, shell: shell,
		state: "running", cols: cols, rows: rows,
		started: time.Now().Unix(), pty: ptmx, cmd: cmd,
	}
	shellMgr.mu.Lock()
	shellMgr.sessions[id] = sess
	shellMgr.mu.Unlock()

	// Reader: buffer everything, and best-effort stream to the current console.
	// Crucially it re-fetches the live peer each emit and NEVER tears the shell
	// down on a failed emit — the shell outlives any single console session.
	// This goroutine is the SOLE owner of cmd.Wait() and the final ptmx.Close():
	// closeShell must never call Wait() itself, or the concurrent Wait races and
	// panics (see closeShell). The recover keeps any fault from killing the agent.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.log.Error("shell reader panic", "session", id, "err", r)
			}
		}()
		buf := make([]byte, 8192)
		seq := 0
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				sess.append(buf[:n])
				seq++
				data, _ := json.Marshal(map[string]string{
					"session": id,
					"chunk":   base64.StdEncoding.EncodeToString(buf[:n]),
				})
				if peer := a.currentPeer(); peer != nil {
					peer.Notify(wire.MethodEmit, wire.EmitParams{Chan: "shell", Seq: seq, Data: data})
				}
			}
			if rerr != nil {
				break
			}
		}
		// Process ended: mark exited, keep the session (and its buffer) around
		// so the console can still see the final output and explicitly close it.
		code := 0
		sess.mu.Lock()
		if sess.cmd != nil {
			sess.cmd.Wait()
			if sess.cmd.ProcessState != nil {
				code = sess.cmd.ProcessState.ExitCode()
			}
		}
		sess.state = "exited"
		sess.exit = code
		ptmx.Close()
		sess.mu.Unlock()
		if peer := a.currentPeer(); peer != nil {
			data, _ := json.Marshal(map[string]any{"session": id, "exited": true, "code": code})
			peer.Notify(wire.MethodEmit, wire.EmitParams{Chan: "shell", Data: data})
		}
	}()

	return wire.ShellOpenResult{Session: id}, nil
}

func (a *Agent) doShellInput(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellInputParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	shellMgr.mu.Lock()
	sess := shellMgr.sessions[p.Session]
	shellMgr.mu.Unlock()
	if sess == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such session")
	}
	raw, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "bad base64")
	}
	sess.mu.Lock()
	ptmx := sess.pty
	running := sess.state == "running"
	sess.mu.Unlock()
	if ptmx == nil || !running {
		return nil, rpc.Errorf(rpc.CodeBadParams, "shell not running")
	}
	if _, err := ptmx.Write(raw); err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doShellResize(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellResizeParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	shellMgr.mu.Lock()
	sess := shellMgr.sessions[p.Session]
	shellMgr.mu.Unlock()
	if sess == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such session")
	}
	sess.mu.Lock()
	sess.cols, sess.rows = p.Cols, p.Rows
	ptmx := sess.pty
	sess.mu.Unlock()
	if ptmx != nil {
		_ = ptmx.Resize(p.Cols, p.Rows)
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doShellClose(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellCloseParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	a.closeShell(p.Session)
	return wire.OKResult{OK: true}, nil
}

// doShellList returns every persistent shell on the node so a (re)connecting
// console can reuse an existing session instead of always spawning a new one.
func (a *Agent) doShellList(params json.RawMessage) (any, *rpc.Error) {
	shellMgr.mu.Lock()
	defer shellMgr.mu.Unlock()
	out := make([]wire.ShellInfo, 0, len(shellMgr.sessions))
	for _, s := range shellMgr.sessions {
		out = append(out, s.info())
	}
	return wire.ShellListResult{Sessions: out}, nil
}

// doShellOutput returns the buffered scrollback for a shell, used to repaint the
// terminal when a console reattaches to an existing session.
func (a *Agent) doShellOutput(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellRefParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	shellMgr.mu.Lock()
	sess := shellMgr.sessions[p.Session]
	shellMgr.mu.Unlock()
	if sess == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such session")
	}
	sess.mu.Lock()
	data := base64.StdEncoding.EncodeToString(sess.buf)
	state, exit := sess.state, sess.exit
	sess.mu.Unlock()
	return wire.ShellOutputResult{Session: sess.id, Data: data, State: state, Exit: exit}, nil
}

func (a *Agent) closeShell(id string) {
	shellMgr.mu.Lock()
	sess := shellMgr.sessions[id]
	delete(shellMgr.sessions, id)
	shellMgr.mu.Unlock()
	if sess == nil {
		return
	}
	// Signal the shell to exit by killing its process. That makes the reader
	// goroutine's ptmx.Read return an error, after which the reader -- the SOLE
	// owner of cmd.Wait() and ptmx.Close() -- tears the PTY down. We must NOT
	// call cmd.Wait() or ptmx.Close() here: neither go-pty's Cmd.wait nor
	// unixPty.Close is concurrency-safe, so doing either alongside the reader
	// races (double Wait / double Close) and the unrecovered panic would take
	// down the whole agent.
	sess.mu.Lock()
	cmd := sess.cmd
	sess.state = "exited"
	sess.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if peer := a.currentPeer(); peer != nil {
		data, _ := json.Marshal(map[string]any{"session": id, "closed": true})
		peer.Notify(wire.MethodEmit, wire.EmitParams{Chan: "shell", Data: data})
	}
}

func pickShell() string {
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("powershell.exe"); err == nil {
			return p
		}
		return "cmd.exe"
	}
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return "sh"
}

// shellBase returns a short friendly name for a shell path (e.g. /bin/bash -> bash).
func shellBase(shell string) string {
	b := shell
	for i := len(shell) - 1; i >= 0; i-- {
		if shell[i] == '/' || shell[i] == '\\' {
			b = shell[i+1:]
			break
		}
	}
	return b
}

func randID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
