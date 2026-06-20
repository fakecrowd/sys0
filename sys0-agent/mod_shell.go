//go:build !modular || mod_shell

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

// SHELL MODULE — command execution: one-shot shell.run plus the interactive PTY
// shell.* and task.* sessions (implemented in shell.go / task.go, also tagged
// mod_shell). High AV signal (spawns cmd/powershell/sh); isolated so quarantine
// here doesn't kill core telemetry.

func init() {
	registerMethod(wire.MethodShellRun, func(a *Agent, ctx context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doShellRun(ctx, params)
	})
	registerMethod(wire.MethodShellOpen, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doShellOpen(params)
	})
	registerMethod(wire.MethodShellInput, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doShellInput(params)
	})
	registerMethod(wire.MethodShellResize, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doShellResize(params)
	})
	registerMethod(wire.MethodShellClose, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doShellClose(params)
	})
	registerMethod(wire.MethodShellList, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doShellList(params)
	})
	registerMethod(wire.MethodShellOutput, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doShellOutput(params)
	})
	registerMethod(wire.MethodTaskStart, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doTaskStart(params)
	})
	registerMethod(wire.MethodTaskInput, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doTaskInput(params)
	})
	registerMethod(wire.MethodTaskResize, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doTaskResize(params)
	})
	registerMethod(wire.MethodTaskSignal, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doTaskSignal(params)
	})
	registerMethod(wire.MethodTaskList, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doTaskList(params)
	})
	registerMethod(wire.MethodTaskOutput, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doTaskOutput(params)
	})
	registerMethod(wire.MethodTaskRestart, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doTaskRestart(params)
	})
	registerMethod(wire.MethodTaskRemove, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doTaskRemove(params)
	})
}

func (a *Agent) doShellRun(ctx context.Context, params json.RawMessage) (any, *rpc.Error) {
	var p wire.ShellRunParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	if p.Cmd == "" {
		return nil, rpc.Errorf(rpc.CodeBadParams, "cmd required")
	}
	timeout := time.Duration(p.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := shellCommand(cctx, p.Cmd)
	if p.Cwd != "" {
		cmd.Dir = p.Cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := wire.ShellRunResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if cctx.Err() == context.DeadlineExceeded {
		return nil, rpc.Errorf(rpc.CodeTimeout, "command timed out")
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.Exit = ee.ExitCode()
		} else {
			return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
		}
	}
	return res, nil
}

// shellCommand builds a non-interactive shell invocation for the current OS.
func shellCommand(ctx context.Context, cmdline string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		c := exec.CommandContext(ctx, "cmd", "/c", cmdline)
		hideWindow(c) // no black console window flash for background shell.run
		return c
	}
	return exec.CommandContext(ctx, "sh", "-c", cmdline)
}
