package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

// shellSession is one interactive PTY-backed shell on the agent.
type shellSession struct {
	id   string
	cmd  *exec.Cmd
	ptmx *os.File
}

type shellManager struct {
	mu       sync.Mutex
	sessions map[string]*shellSession
}

func newShellManager() *shellManager {
	return &shellManager{sessions: map[string]*shellSession{}}
}

// doShellOpen spawns a PTY shell and streams its output to the hub via emit.
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

	cmd := exec.Command(shell, "-i")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "open pty: %v", err)
	}

	id := "s" + randID()
	sess := &shellSession{id: id, cmd: cmd, ptmx: ptmx}
	a.shells.mu.Lock()
	a.shells.sessions[id] = sess
	a.shells.mu.Unlock()

	a.mu.Lock()
	peer := a.peer
	a.mu.Unlock()

	// stream output -> emit (chan "shell")
	go func() {
		buf := make([]byte, 8192)
		seq := 0
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				seq++
				data, _ := json.Marshal(map[string]string{
					"session": id,
					"chunk":   base64.StdEncoding.EncodeToString(buf[:n]),
				})
				if peer == nil || peer.Notify(wire.MethodEmit, wire.EmitParams{Chan: "shell", Seq: seq, Data: data}) != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		a.closeShell(id, peer)
	}()

	return wire.ShellOpenResult{Session: id}, nil
}

func (a *Agent) doShellInput(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellInputParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	a.shells.mu.Lock()
	sess := a.shells.sessions[p.Session]
	a.shells.mu.Unlock()
	if sess == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such session")
	}
	raw, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "bad base64")
	}
	if _, err := sess.ptmx.Write(raw); err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doShellResize(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellResizeParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	a.shells.mu.Lock()
	sess := a.shells.sessions[p.Session]
	a.shells.mu.Unlock()
	if sess == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such session")
	}
	pty.Setsize(sess.ptmx, &pty.Winsize{Cols: uint16(p.Cols), Rows: uint16(p.Rows)})
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doShellClose(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellCloseParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	a.mu.Lock()
	peer := a.peer
	a.mu.Unlock()
	a.closeShell(p.Session, peer)
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) closeShell(id string, peer *rpc.Peer) {
	a.shells.mu.Lock()
	sess := a.shells.sessions[id]
	delete(a.shells.sessions, id)
	a.shells.mu.Unlock()
	if sess == nil {
		return
	}
	sess.ptmx.Close()
	if sess.cmd.Process != nil {
		sess.cmd.Process.Kill()
	}
	sess.cmd.Wait()
	// notify the hub that the session ended
	if peer != nil {
		data, _ := json.Marshal(map[string]any{"session": id, "closed": true})
		peer.Notify(wire.MethodEmit, wire.EmitParams{Chan: "shell", Data: data})
	}
}

func pickShell() string {
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return "sh"
}

func randID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
