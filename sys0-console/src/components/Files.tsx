import { useEffect, useState } from "react";
import { api, b64encode, b64decode, type Node } from "../api";
import { confirmDialog, alertDialog } from "./dialogs";

export function Files({ nodes, primary }: { nodes: Node[]; primary: string }) {
  const [node, setNode] = useState(primary);
  const [path, setPath] = useState("/");
  const [entries, setEntries] = useState<any[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => setNode(primary || nodes[0]?.id || ""), [primary, nodes.length]);

  const ls = async (p: string) => {
    if (!node) return;
    setBusy(true); setErr("");
    try {
      const v = await api.one(node, "fs.ls", { path: p });
      setEntries((v.entries || []).sort((a: any, b: any) => (b.isDir ? 1 : 0) - (a.isDir ? 1 : 0)));
      setPath(p);
    } catch (e) { setErr(String(e)); } finally { setBusy(false); }
  };

  const join = (p: string, name: string) => (p.endsWith("/") ? p : p + "/") + name;
  const parent = (p: string) => { const i = p.replace(/\/+$/, "").lastIndexOf("/"); return i <= 0 ? "/" : p.slice(0, i); };

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

  const upload = async (file: File) => {
    const buf = new Uint8Array(await file.arrayBuffer());
    try { await api.one(node, "fs.put", { path: join(path, file.name), data: b64encode(buf) }); ls(path); }
    catch (e) { alertDialog(String(e), { title: "上传失败" }); }
  };

  useEffect(() => { if (node) ls("/"); }, [node]);

  return (
    <div className="flex flex-col gap-3 h-full">
      <div className="flex gap-2 items-center flex-wrap">
        <select className="input" style={{ width: 180 }} value={node} onChange={(e) => setNode(e.target.value)}>
          {nodes.map((n) => <option key={n.id} value={n.id}>{n.label}</option>)}
        </select>
        <input className="input" style={{ flex: 1, minWidth: 200 }} value={path}
          onChange={(e) => setPath(e.target.value)} onKeyDown={(e) => e.key === "Enter" && ls(path)} />
        <button className="btn" onClick={() => ls(path)} disabled={busy}>转到</button>
        <button className="btn" onClick={() => ls(parent(path))} disabled={busy}>↑ 上级</button>
        <label className="btn btn-accent" style={{ cursor: "pointer" }}>
          上传
          <input type="file" style={{ display: "none" }} onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
        </label>
      </div>
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
