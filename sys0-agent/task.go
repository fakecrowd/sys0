package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
	"github.com/shirou/gopsutil/v4/process"
)

// managedTask is a long-running supervised child process.
type managedTask struct {
	id      string
	name    string
	cmdline string
	started int64

	mu     sync.Mutex
	state  string // running | exited
	pid    int
	exit   int
	stdin  io.WriteCloser
	cancel context.CancelFunc
}

func (t *managedTask) info() wire.TaskInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return wire.TaskInfo{
		ID: t.id, Name: t.name, Cmd: t.cmdline, State: t.state,
		PID: t.pid, Exit: t.exit, Started: t.started,
	}
}

type taskManager struct {
	mu    sync.Mutex
	tasks map[string]*managedTask
}

func newTaskManager() *taskManager { return &taskManager{tasks: map[string]*managedTask{}} }

// shellArgs returns the OS shell invocation for a command line.
func shellArgs(cmdline string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", cmdline}
	}
	return "sh", []string{"-c", cmdline}
}

func (a *Agent) doTaskStart(params json.RawMessage) (any, *rpc.Error) {
	var p wire.TaskStartParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	if p.Cmd == "" {
		return nil, rpc.Errorf(rpc.CodeBadParams, "cmd required")
	}
	name := p.Name
	if name == "" {
		name = p.Cmd
	}
	ctx, cancel := context.WithCancel(context.Background())
	bin, args := shellArgs(p.Cmd)
	cmd := exec.CommandContext(ctx, bin, args...)
	if p.Cwd != "" {
		cmd.Dir = p.Cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, rpc.Errorf(rpc.CodeInternal, "stdin: %v", err)
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, rpc.Errorf(rpc.CodeInternal, "start: %v", err)
	}

	t := &managedTask{
		id: "t" + randID(), name: name, cmdline: p.Cmd, started: time.Now().Unix(),
		state: "running", pid: cmd.Process.Pid, stdin: stdin, cancel: cancel,
	}
	a.tasks.mu.Lock()
	a.tasks.tasks[t.id] = t
	a.tasks.mu.Unlock()

	a.mu.Lock()
	peer := a.peer
	a.mu.Unlock()
	emit := func(m map[string]any) {
		if peer == nil {
			return
		}
		m["task"] = t.id
		data, _ := json.Marshal(m)
		peer.Notify(wire.MethodEmit, wire.EmitParams{Chan: "task", Data: data})
	}

	// stream combined output
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				emit(map[string]any{"chunk": base64.StdEncoding.EncodeToString(buf[:n])})
			}
			if err != nil {
				return
			}
		}
	}()
	// wait for exit
	go func() {
		werr := cmd.Wait()
		pw.Close()
		code := 0
		if ee, ok := werr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if werr != nil {
			code = -1
		}
		t.mu.Lock()
		t.state = "exited"
		t.exit = code
		t.mu.Unlock()
		emit(map[string]any{"exited": true, "code": code})
	}()

	return wire.TaskStartResult{Task: t.id}, nil
}

func (a *Agent) doTaskInput(params json.RawMessage) (any, *rpc.Error) {
	var p wire.TaskInputParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	t := a.getTask(p.Task)
	if t == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such task")
	}
	raw, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "bad base64")
	}
	if _, err := t.stdin.Write(raw); err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doTaskSignal(params json.RawMessage) (any, *rpc.Error) {
	var p wire.TaskSignalParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	t := a.getTask(p.Task)
	if t == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such task")
	}
	a.stopTask(t, p.Sig)
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doTaskList(params json.RawMessage) (any, *rpc.Error) {
	a.tasks.mu.Lock()
	defer a.tasks.mu.Unlock()
	out := make([]wire.TaskInfo, 0, len(a.tasks.tasks))
	for _, t := range a.tasks.tasks {
		out = append(out, t.info())
	}
	return wire.TaskListResult{Tasks: out}, nil
}

func (a *Agent) doTaskRemove(params json.RawMessage) (any, *rpc.Error) {
	var p wire.TaskRemoveParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	t := a.getTask(p.Task)
	if t != nil {
		a.stopTask(t, "KILL")
		a.tasks.mu.Lock()
		delete(a.tasks.tasks, t.id)
		a.tasks.mu.Unlock()
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) getTask(id string) *managedTask {
	a.tasks.mu.Lock()
	defer a.tasks.mu.Unlock()
	return a.tasks.tasks[id]
}

func (a *Agent) stopTask(t *managedTask, sig string) {
	t.mu.Lock()
	pid, state := t.pid, t.state
	t.mu.Unlock()
	if state != "running" {
		return
	}
	if proc, err := process.NewProcess(int32(pid)); err == nil {
		if sig == "KILL" {
			proc.Kill()
		} else {
			proc.Terminate()
		}
	}
	if t.cancel != nil {
		t.cancel()
	}
}
