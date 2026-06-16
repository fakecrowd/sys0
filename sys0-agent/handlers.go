package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

func decode(params json.RawMessage, v any) *rpc.Error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return rpc.Errorf(rpc.CodeBadParams, "%v", err)
	}
	return nil
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
	cmd := exec.CommandContext(cctx, "sh", "-c", p.Cmd)
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

func (a *Agent) doProcSignal(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ProcSignalParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	sig := map[string]syscall.Signal{
		"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL,
		"INT": syscall.SIGINT, "HUP": syscall.SIGHUP, "": syscall.SIGTERM,
	}[p.Sig]
	if sig == 0 {
		return nil, rpc.Errorf(rpc.CodeBadParams, "unknown signal %q", p.Sig)
	}
	if err := syscall.Kill(p.PID, sig); err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doFsLs(params json.RawMessage) (any, *rpc.Error) {
	var p wire.FsLsParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	entries, err := os.ReadDir(p.Path)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	res := wire.FsLsResult{Path: p.Path, Entries: []wire.FsEntry{}}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		res.Entries = append(res.Entries, wire.FsEntry{
			Name: e.Name(), Size: info.Size(), Mode: info.Mode().String(),
			IsDir: e.IsDir(), MTime: info.ModTime().Unix(),
		})
	}
	return res, nil
}

func (a *Agent) doFsStat(params json.RawMessage) (any, *rpc.Error) {
	var p wire.FsLsParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	info, err := os.Stat(p.Path)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.FsStatResult{
		Path: p.Path, Size: info.Size(), Mode: info.Mode().String(),
		IsDir: info.IsDir(), MTime: info.ModTime().Unix(),
	}, nil
}

func (a *Agent) doFsGet(params json.RawMessage) (any, *rpc.Error) {
	var p wire.FsGetParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	b, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.FsGetResult{Path: p.Path, Size: int64(len(b)), Data: base64.StdEncoding.EncodeToString(b)}, nil
}

func (a *Agent) doFsPut(params json.RawMessage) (any, *rpc.Error) {
	var p wire.FsPutParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	b, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "bad base64: %v", err)
	}
	mode := os.FileMode(0o644)
	if p.Mode != 0 {
		mode = os.FileMode(p.Mode)
	}
	if dir := filepath.Dir(p.Path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(p.Path, b, mode); err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) doFsRm(params json.RawMessage) (any, *rpc.Error) {
	var p wire.FsRmParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	var err error
	if p.Recursive {
		err = os.RemoveAll(p.Path)
	} else {
		err = os.Remove(p.Path)
	}
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.OKResult{OK: true}, nil
}

func atoiSafe(s string) int { n, _ := strconv.Atoi(s); return n }
