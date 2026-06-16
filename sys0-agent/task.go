package main

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
	"github.com/shirou/gopsutil/v4/process"
)

const taskBufferCap = 256 << 10 // keep last 256 KiB of output per task

// managedTask is a long-running supervised child process backed by a PTY, so
// output is a real terminal (ANSI colors) and stdin is interactive.
type managedTask struct {
	id      string
	name    string
	cmdline string
	cwd     string
	cols    int
	rows    int

	mu       sync.Mutex
	state    string // running | exited
	pid      int
	exit     int
	started  int64
	finished int64
	pty      pty.Pty
	buf      []byte // ring buffer of recent output
}

func (t *managedTask) info() wire.TaskInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return wire.TaskInfo{
		ID: t.id, Name: t.name, Cmd: t.cmdline, State: t.state,
		PID: t.pid, Exit: t.exit, Started: t.started, Finished: t.finished,
	}
}

func (t *managedTask) append(b []byte) {
	t.mu.Lock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > taskBufferCap {
		t.buf = t.buf[len(t.buf)-taskBufferCap:]
	}
	t.mu.Unlock()
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
	t := &managedTask{
		id: "t" + randID(), name: name, cmdline: p.Cmd, cwd: p.Cwd,
		cols: orInt(p.Cols, 100), rows: orInt(p.Rows, 30),
	}
	if e := a.launchTask(t); e != nil {
		return nil, e
	}
	a.tasks.mu.Lock()
	a.tasks.tasks[t.id] = t
	a.tasks.mu.Unlock()
	return wire.TaskStartResult{Task: t.id}, nil
}

// launchTask starts (or restarts) the process for a task.
func (a *Agent) launchTask(t *managedTask) *rpc.Error {
	ptmx, err := pty.New()
	if err != nil {
		return rpc.Errorf(rpc.CodeInternal, "open pty: %v", err)
	}
	bin, args := shellArgs(t.cmdline)
	cmd := ptmx.Command(bin, args...)
	if t.cwd != "" {
		cmd.Dir = t.cwd
	}
	if err := cmd.Start(); err != nil {
		ptmx.Close()
		return rpc.Errorf(rpc.CodeInternal, "start: %v", err)
	}
	_ = ptmx.Resize(t.cols, t.rows)

	t.mu.Lock()
	t.state = "running"
	t.pid = cmd.Process.Pid
	t.started = time.Now().Unix()
	t.finished = 0
	t.exit = 0
	t.pty = ptmx
	t.mu.Unlock()

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

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				t.append(buf[:n])
				emit(map[string]any{"chunk": base64.StdEncoding.EncodeToString(buf[:n])})
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		cmd.Wait()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		ptmx.Close()
		t.mu.Lock()
		t.state = "exited"
		t.exit = code
		t.finished = time.Now().Unix()
		t.mu.Unlock()
		emit(map[string]any{"exited": true, "code": code})
	}()
	return nil
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
	t.mu.Lock()
	ptmx := t.pty
	t.mu.Unlock()
	if ptmx == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "task not running")
	}
	if _, err := ptmx.Write(raw); err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doTaskResize(params json.RawMessage) (any, *rpc.Error) {
	var p wire.TaskResizeParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	t := a.getTask(p.Task)
	if t == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such task")
	}
	t.mu.Lock()
	t.cols, t.rows = p.Cols, p.Rows
	ptmx := t.pty
	t.mu.Unlock()
	if ptmx != nil {
		ptmx.Resize(p.Cols, p.Rows)
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

func (a *Agent) doTaskOutput(params json.RawMessage) (any, *rpc.Error) {
	var p wire.TaskRefParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	t := a.getTask(p.Task)
	if t == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such task")
	}
	t.mu.Lock()
	data := base64.StdEncoding.EncodeToString(t.buf)
	state, exit := t.state, t.exit
	t.mu.Unlock()
	return wire.TaskOutputResult{Task: t.id, Data: data, State: state, Exit: exit}, nil
}

func (a *Agent) doTaskRestart(params json.RawMessage) (any, *rpc.Error) {
	var p wire.TaskRefParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	t := a.getTask(p.Task)
	if t == nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such task")
	}
	a.stopTask(t, "KILL")
	time.Sleep(150 * time.Millisecond)
	t.mu.Lock()
	t.buf = nil
	t.mu.Unlock()
	if e := a.launchTask(t); e != nil {
		return nil, e
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doTaskRemove(params json.RawMessage) (any, *rpc.Error) {
	var p wire.TaskRefParams
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
}

func orInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
