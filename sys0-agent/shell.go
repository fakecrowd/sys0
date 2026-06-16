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

	"github.com/aymanbagabas/go-pty"
	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

// shellSession is one interactive PTY-backed shell on the agent (cross-platform
// via go-pty: Unix pty / Windows ConPTY).
type shellSession struct {
	id  string
	pty pty.Pty
	cmd *pty.Cmd
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
	sess := &shellSession{id: id, pty: ptmx, cmd: cmd}
	a.shells.mu.Lock()
	a.shells.sessions[id] = sess
	a.shells.mu.Unlock()

	a.mu.Lock()
	peer := a.peer
	a.mu.Unlock()

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
	if _, err := sess.pty.Write(raw); err != nil {
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
	_ = sess.pty.Resize(p.Cols, p.Rows)
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
	sess.pty.Close()
	if sess.cmd != nil {
		sess.cmd.Wait()
	}
	if peer != nil {
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

func randID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
