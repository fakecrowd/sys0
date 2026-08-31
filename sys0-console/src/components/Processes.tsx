import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { confirmDialog, alertDialog } from "./dialogs";

// Process list for the FOCUSED node. Node is fixed by the workspace.
// Optional auto-refresh (default OFF) re-lists every few seconds.
export function Processes({ node }: { node: string }) {
  const [filter, setFilter] = useState("");
  const [procs, setProcs] = useState<any[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [auto, setAuto] = useState(false); // auto-refresh, default off
  const filterRef = useRef(filter);
  useEffect(() => { filterRef.current = filter; }, [filter]);

  const load = async () => {
    if (!node) return;
    setBusy(true); setErr("");
    try {
      const v = await api.one(node, "proc.list", { filter: filterRef.current });
      setProcs((v.procs || []).sort((a: any, b: any) => (b.self ? 1 : 0) - (a.self ? 1 : 0) || b.rss - a.rss));
    } catch (e) { setErr(String(e)); } finally { setBusy(false); }
  };

  // auto-refresh loop
  useEffect(() => {
    if (!auto) return;
    load(); // immediate refresh when toggled on
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [auto, node]);

  const kill = async (pid: number, name: string, sig: string) => {
    if (!(await confirmDialog(`${sig} ${name}（pid ${pid}）@ ${node}?`, { title: "结束进程", danger: sig === "KILL" }))) return;
    try { await api.one(node, "proc.signal", { pid, sig }); setTimeout(load, 300); }
    catch (e) { alertDialog(String(e), { title: "操作失败" }); }
  };

  return (
    <div className="flex flex-col gap-3 h-full">
      <div className="flex gap-2 items-center flex-wrap">
        <input className="input" style={{ flex: 1, minWidth: 140 }} placeholder="filter by name" value={filter}
          onChange={(e) => setFilter(e.target.value)} onKeyDown={(e) => e.key === "Enter" && load()} />
        <button className="btn btn-accent" disabled={busy || !node} onClick={load}>列出</button>
        <label className="flex items-center gap-1 cursor-pointer mono-sm" title="每 3 秒自动刷新">
          <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)}
            style={{ accentColor: "var(--accent)" }} />
          自动刷新
        </label>
        {auto && <span className="dot" style={{ background: "var(--accent)", boxShadow: "0 0 6px var(--accent)" }} title="自动刷新中" />}
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
              <tr key={p.pid} style={{ borderBottom: "1px solid var(--border)", ...(p.self ? { background: "rgba(80,200,120,0.10)" } : {}) }}>
                <td className="px-3 py-1 mono-sm">{p.pid}</td>
                <td className="px-3 py-1 mono-sm">{p.ppid}</td>
                <td className="px-3 py-1">{p.user}</td>
                <td className="px-3 py-1 mono-sm">{(p.rss / 1e6).toFixed(1)}M</td>
                <td className="px-3 py-1" style={{ color: p.self ? "var(--accent)" : "var(--accent-2)" }}>
                  {p.name}
                  {p.self && <span className="tag ml-1" style={{ color: "var(--accent)", borderColor: "var(--accent)" }} title="这是 sys0-agent 本体（已伪装进程名）">agent</span>}
                </td>
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
