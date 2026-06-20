import { useEffect, useRef, useState } from "react";
import { api, b64encode, b64decode } from "../api";
import { confirmDialog, alertDialog } from "./dialogs";

// File browser for the FOCUSED node. Node is fixed by the workspace.
// OS-aware:
//  - Windows: a drive <select> (C:\ D:\ …) is separate from the path box; the
//    path box holds only the sub-path under the selected drive. Defaults to C:
//    (or the first available drive) on open.
//  - POSIX: a single path box rooted at "/".
export function Files({ node, os }: { node: string; os: string }) {
  const win = os === "windows";
  const sep = win ? "\\" : "/";

  const [drives, setDrives] = useState<string[]>([]); // windows: ["C:\\","D:\\"]
  const [path, setPath] = useState(win ? "" : "/");    // current listing path (full)
  const [edit, setEdit] = useState("");                // path box text (sub-path on win)
  const [entries, setEntries] = useState<any[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  // upload progress: null = idle; otherwise live transfer state + a small log.
  const [up, setUp] = useState<null | {
    name: string; sent: number; total: number; done: boolean; failed: boolean;
    canceled?: boolean;
    bps: number; // bytes/sec, live
  }>(null);
  // Set true by the 取消 button; the chunk loop checks it between chunks and
  // aborts (the in-flight chunk finishes first, then we stop). A ref so the
  // running async loop sees the latest value without a re-render dependency.
  const cancelRef = useRef(false);
  const [uplog, setUplog] = useState<string[]>([]);

  // --- windows path helpers ---
  const driveOf = (p: string) => { const m = /^([A-Za-z]:)/.exec(p); return m ? m[1] + "\\" : ""; };
  const subOf = (p: string) => p.replace(/^[A-Za-z]:\\?/, ""); // strip "C:\" -> rest
  const isDriveRoot = (p: string) => /^[A-Za-z]:\\?$/.test(p);

  const join = (p: string, name: string) => {
    if (p === "") return name;
    return p.endsWith(sep) ? p + name : p + sep + name;
  };
  const parent = (p: string) => {
    if (win) {
      if (isDriveRoot(p)) return p; // already at drive root — stay
      const trimmed = p.replace(/\\+$/, "");
      const i = trimmed.lastIndexOf("\\");
      const up = trimmed.slice(0, i);
      return /^[A-Za-z]:$/.test(up) ? up + "\\" : up;
    }
    const t = p.replace(/\/+$/, "");
    const i = t.lastIndexOf("/");
    return i <= 0 ? "/" : t.slice(0, i);
  };

  const ls = async (p: string) => {
    if (!node) return;
    setBusy(true); setErr("");
    try {
      const v = await api.one(node, "fs.ls", { path: p });
      setEntries((v.entries || []).sort((a: any, b: any) => (b.isDir ? 1 : 0) - (a.isDir ? 1 : 0)));
      const np = v.path ?? p;
      setPath(np);
      setEdit(win ? subOf(np) : np);
    } catch (e) { setErr(String(e)); } finally { setBusy(false); }
  };

  // On node change: windows → fetch drive list, default to C: (or first);
  // posix → list "/".
  useEffect(() => {
    if (!node) return;
    if (win) {
      (async () => {
        // New agents list drive letters for an empty path; default to C: (or
        // the first available drive) and list it.
        const v = await api.one(node, "fs.ls", { path: "" });
        const ds: string[] = (v.entries || []).filter((e: any) => e.mode === "drive").map((e: any) => e.name);
        setDrives(ds);
        const def = ds.find((d) => /^c:/i.test(d)) || ds[0] || "C:\\";
        ls(def);
      })().catch((e) => setErr(String(e)));
    } else {
      ls("/");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node]);

  // Compose the full path from the drive select (windows) + the edit box, then list.
  const go = () => {
    if (win) {
      const drive = driveOf(path) || drives[0] || "C:\\";
      const sub = edit.replace(/^[\\/]+/, ""); // no leading slash
      ls(sub ? drive + sub : drive);
    } else {
      ls(edit || "/");
    }
  };

  const download = async (name: string) => {
    try {
      const v = await api.one(node, "fs.get", { path: join(path, name) });
      const blob = new Blob([b64decode(v.data) as unknown as BlobPart]);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a"); a.href = url; a.download = name; a.click();
      URL.revokeObjectURL(url);
    } catch (e) { alertDialog(String(e), { title: "下载失败" }); }
  };

  const remove = async (name: string, isDir: boolean) => {
    if (!(await confirmDialog(`删除 ${join(path, name)} @ ${node}?`, { title: "删除文件", danger: true }))) return;
    try { await api.one(node, "fs.rm", { path: join(path, name), recursive: isDir }); ls(path); }
    catch (e) { alertDialog(String(e), { title: "删除失败" }); }
  };

  // Chunked upload with live progress + a small trace log. Each chunk is one
  // fs.put dispatch carrying its byte offset; the first chunk (offset 0)
  // truncates, later chunks WriteAt their offset on the agent.
  const CHUNK = 512 * 1024; // 512 KiB per chunk (~683 KiB base64 on the wire)
  const upload = async (file: File) => {
    const dest = join(path, file.name);
    const total = file.size;
    const t0 = Date.now();
    const log = (m: string) => setUplog((l) => [...l.slice(-60), `${new Date().toLocaleTimeString()} ${m}`]);
    setUplog([]);
    cancelRef.current = false;
    setUp({ name: file.name, sent: 0, total, done: false, failed: false, bps: 0 });
    log(`开始上传 ${file.name}（${fmtSize(total)}）→ ${dest}`);
    try {
      const buf = new Uint8Array(await file.arrayBuffer());
      if (total === 0) {
        await api.one(node, "fs.put", { path: dest, data: "", offset: 0 });
      }
      for (let off = 0; off < total; off += CHUNK) {
        if (cancelRef.current) {
          const sent = off;
          setUp({ name: file.name, sent, total, done: false, failed: true, canceled: true, bps: 0 });
          log(`已取消 ✗ 已传 ${fmtSize(sent)} / ${fmtSize(total)}（节点上为不完整文件）`);
          ls(path);
          return;
        }
        const slice = buf.subarray(off, Math.min(off + CHUNK, total));
        const res: any = await api.one(node, "fs.put", { path: dest, data: b64encode(slice), offset: off });
        const sent = Math.min(off + slice.length, total);
        const elapsed = Math.max(0.001, (Date.now() - t0) / 1000);
        const bps = sent / elapsed;
        setUp({ name: file.name, sent, total, done: false, failed: false, bps });
        const pct = total ? Math.floor((sent / total) * 100) : 100;
        log(`分片 @${fmtSize(off)} 写入 ${fmtSize(slice.length)}（${pct}%${res?.size != null ? `，节点 ${fmtSize(res.size)}` : ""}）`);
      }
      const elapsed = Math.max(0.001, (Date.now() - t0) / 1000);
      setUp({ name: file.name, sent: total, total, done: true, failed: false, bps: total / elapsed });
      log(`完成 ✓ 共 ${fmtSize(total)} · 用时 ${elapsed.toFixed(1)}s · 均速 ${fmtSize(total / elapsed)}/s`);
      ls(path);
      // NOTE: do NOT auto-dismiss. Small files transfer in a blink; if the bar
      // vanished on its own the user would never see that anything happened.
      // The panel persists (showing the completed state + trace) until the user
      // closes it or starts another upload.
    } catch (e) {
      setUp((u) => (u ? { ...u, failed: true } : u));
      log(`失败 ✗ ${String(e)}`);
    }
  };

  const atRoot = win ? isDriveRoot(path) : path === "/";

  return (
    <div className="flex flex-col gap-3 h-full">
      <div className="flex gap-2 items-center flex-wrap">
        {win && (
          <select className="input" style={{ width: 92, flexShrink: 0 }} value={driveOf(path)}
            disabled={busy} onChange={(e) => ls(e.target.value)} title="盘符">
            {drives.length === 0 && <option value={driveOf(path)}>{driveOf(path) || "C:\\"}</option>}
            {drives.map((d) => <option key={d} value={d}>{d}</option>)}
          </select>
        )}
        <input className="input" style={{ flex: 1, minWidth: 160 }} value={edit}
          placeholder={win ? "盘内路径，如 Users\\Public" : "/"}
          onChange={(e) => setEdit(e.target.value)} onKeyDown={(e) => e.key === "Enter" && go()} />
        <button className="btn" onClick={go} disabled={busy}>转到</button>
        <button className="btn" onClick={() => ls(parent(path))} disabled={busy || atRoot}>↑ 上级</button>
        <label className="btn btn-accent" style={{ cursor: up && !up.done && !up.failed ? "not-allowed" : "pointer", opacity: up && !up.done && !up.failed ? 0.6 : 1 }}>
          {up && !up.done && !up.failed ? `上传中 ${up.total ? Math.floor((up.sent / up.total) * 100) : 0}%` : "上传"}
          <input type="file" style={{ display: "none" }} disabled={!!up && !up.done && !up.failed}
            onChange={(e) => { if (e.target.files?.[0]) upload(e.target.files[0]); e.target.value = ""; }} />
        </label>
      </div>

      {up && (
        <div className="panel p-2 flex flex-col gap-1">
          <div className="flex items-center gap-2 mono-sm">
            <span style={{ flex: 1, color: up.failed ? "var(--danger)" : up.done ? "var(--accent)" : "var(--fg)" }}>
              {up.canceled ? "已取消" : up.failed ? "上传失败" : up.done ? "上传完成" : "上传中"} · {up.name}
            </span>
            <span style={{ color: "var(--muted)" }}>
              {fmtSize(up.sent)} / {fmtSize(up.total)} · {up.total ? Math.floor((up.sent / up.total) * 100) : 100}%{up.bps > 0 ? ` · ${fmtSize(up.bps)}/s` : ""}
            </span>
            {!up.done && !up.failed && (
              <button className="btn" style={{ padding: "0 9px", color: "var(--danger)" }}
                onClick={() => { cancelRef.current = true; }}>取消</button>
            )}
            {(up.done || up.failed) && (
              <button className="btn" style={{ padding: "0 7px" }} onClick={() => setUp(null)}>×</button>
            )}
          </div>
          <div style={{ height: 6, background: "var(--border)", borderRadius: 3, overflow: "hidden" }}>
            <div style={{
              height: "100%",
              width: `${up.total ? Math.floor((up.sent / up.total) * 100) : 100}%`,
              background: up.failed ? "var(--danger)" : "var(--accent)",
              transition: "width 0.15s ease",
            }} />
          </div>
          {uplog.length > 0 && (
            <div className="mono-sm" style={{ maxHeight: 96, overflowY: "auto", color: "var(--muted)", marginTop: 2 }}>
              {uplog.map((line, i) => <div key={i} style={{ lineHeight: 1.5 }}>{line}</div>)}
            </div>
          )}
        </div>
      )}
      <div className="mono-sm" style={{ color: "var(--muted)" }}>{path || (win ? "盘符" : "/")}</div>
      {err && <div style={{ color: "var(--danger)" }}>{err}</div>}
      <div className="panel flex-1 overflow-auto">
        {entries.map((e) => (
          <div key={e.name} className="flex items-center gap-2 px-3 py-1.5" style={{ borderBottom: "1px solid var(--border)" }}>
            <span style={{ width: 16 }}>{e.isDir ? "📁" : "📄"}</span>
            <span className={e.isDir ? "cursor-pointer" : ""} style={{ color: e.isDir ? "var(--accent)" : "var(--fg)", flex: 1 }}
              onClick={() => e.isDir && ls(join(path, e.name))}>{e.name}</span>
            <span className="mono-sm" style={{ width: 90, textAlign: "right" }}>{e.isDir ? "" : fmtSize(e.size)}</span>
            <span className="mono-sm" style={{ width: 110 }}>{e.mode}</span>
            {!e.isDir && <button className="btn" style={{ padding: "1px 7px" }} onClick={() => download(e.name)}>下载</button>}
            <button className="btn" style={{ padding: "1px 7px", color: "var(--danger)" }} onClick={() => remove(e.name, e.isDir)}>删</button>
          </div>
        ))}
        {entries.length === 0 && <div className="mono-sm px-3 py-4">空目录或未加载</div>}
      </div>
    </div>
  );
}

function fmtSize(n: number) {
  if (n < 1024) return n + "B";
  if (n < 1e6) return (n / 1024).toFixed(1) + "K";
  if (n < 1e9) return (n / 1e6).toFixed(1) + "M";
  return (n / 1e9).toFixed(1) + "G";
}
