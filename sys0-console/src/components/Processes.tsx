import { useEffect, useState } from "react";
import { api, type Node } from "../api";
import { confirmDialog, alertDialog } from "./dialogs";

export function Processes({ nodes, primary }: { nodes: Node[]; primary: string }) {
  const [node, setNode] = useState(primary);
  const [filter, setFilter] = useState("");
  const [procs, setProcs] = useState<any[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => setNode(primary || nodes[0]?.id || ""), [primary, nodes.length]);

  const load = async () => {
    if (!node) return;
    setBusy(true); setErr("");
    try {
      const v = await api.one(node, "proc.list", { filter });
      setProcs((v.procs || []).sort((a: any, b: any) => b.rss - a.rss));
    } catch (e) { setErr(String(e)); } finally { setBusy(false); }
  };

  const kill = async (pid: number, name: string, sig: string) => {
    if (!(await confirmDialog(`${sig} ${name}（pid ${pid}）@ ${node}?`, { title: "结束进程", danger: sig === "KILL" }))) return;
    try { await api.one(node, "proc.signal", { pid, sig }); setTimeout(load, 300); }
    catch (e) { alertDialog(String(e), { title: "操作失败" }); }
  };

  return (
    <div className="flex flex-col gap-3 h-full">
      <div className="flex gap-2 items-center">
        <select className="input" style={{ width: 200 }} value={node} onChange={(e) => setNode(e.target.value)}>
          {nodes.map((n) => <option key={n.id} value={n.id}>{n.label} · {n.id}</option>)}
        </select>
        <input className="input" placeholder="filter by name" value={filter}
          onChange={(e) => setFilter(e.target.value)} onKeyDown={(e) => e.key === "Enter" && load()} />
        <button className="btn btn-accent" disabled={busy || !node} onClick={load}>列出</button>
      </div>
      {err && <div style={{ color: "var(--danger)" }}>{err}</div>}
      <div className="panel flex-1 overflow-auto">
        <table className="w-full" style={{ borderCollapse: "collapse" }}>
          <thead>
            <tr className="mono-sm" style={{ textAlign: "left" }}>
              {["pid", "ppid", "user", "rss", "name", ""].map((h, i) => (
                <th key={i} className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)", position: "sticky", top: 0, background: "var(--panel)" }}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {procs.map((p) => (
              <tr key={p.pid} style={{ borderBottom: "1px solid var(--border)" }}>
                <td className="px-3 py-1 mono-sm">{p.pid}</td>
                <td className="px-3 py-1 mono-sm">{p.ppid}</td>
                <td className="px-3 py-1">{p.user}</td>
                <td className="px-3 py-1 mono-sm">{(p.rss / 1e6).toFixed(1)}M</td>
                <td className="px-3 py-1" style={{ color: "var(--accent-2)" }}>{p.name}</td>
                <td className="px-3 py-1">
                  <button className="btn" style={{ padding: "1px 6px" }} onClick={() => kill(p.pid, p.name, "TERM")}>TERM</button>{" "}
                  <button className="btn" style={{ padding: "1px 6px", color: "var(--danger)" }} onClick={() => kill(p.pid, p.name, "KILL")}>KILL</button>
                </td>
              </tr>
            ))}
            {procs.length === 0 && <tr><td colSpan={6} className="px-3 py-4 mono-sm">点「列出」加载进程</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
