import { useEffect, useRef, useState } from "react";
import { api, getUser, } from "../api";
import { confirmDialog, alertDialog } from "./dialogs";
import { loadRecent, saveRecent } from "../nodeWorkspace";

// Process list for the FOCUSED node. Node is fixed by the workspace.
// Optional auto-refresh (default OFF) re-lists every few seconds.
export function Processes({ node, online }: { node: string; online: boolean }) {
  const account = useRef(getUser()).current;
  const onlineRef = useRef(online);
  onlineRef.current = online;
  useEffect(() => {
    return () => { onlineRef.current = false; };
  }, []);
  const recent = loadRecent<{ procs: any[] }>(localStorage, account, node, "processes");
  const [filter, setFilter] = useState("");
  const [procs, setProcs] = useState<any[]>(recent?.data.procs || []);
  const [savedAt, setSavedAt] = useState(recent?.savedAt || 0);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [auto, setAuto] = useState(false); // auto-refresh, default off
  const filterRef = useRef(filter);
  useEffect(() => { filterRef.current = filter; }, [filter]);

  const load = async () => {
    if (!node || !onlineRef.current) return;
    setBusy(true); setErr("");
    try {
      const v = await api.one(node, "proc.list", { filter: filterRef.current });
      if (!onlineRef.current) return;
      const next = (v.procs || []).sort((a: any, b: any) => (b.self ? 1 : 0) - (a.self ? 1 : 0) || b.rss - a.rss);
      const now = Date.now();
      setProcs(next); setSavedAt(now);
      saveRecent(localStorage, account, node, "processes", { procs: next }, now);
    } catch (e) { setErr(String(e)); } finally { setBusy(false); }
  };

  // auto-refresh loop
  useEffect(() => {
    if (!auto || !online) return;
    load(); // immediate refresh when toggled on
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [auto, node, online]);

  const kill = async (pid: number, name: string, sig: string) => {
    if (!(await confirmDialog(`${sig} ${name}（pid ${pid}）@ ${node}?`, { title: "结束进程", danger: sig === "KILL" })) || !onlineRef.current) return;
    try { await api.one(node, "proc.signal", { pid, sig }); if (onlineRef.current) setTimeout(load, 300); }
    catch (e) { alertDialog(String(e), { title: "操作失败" }); }
  };

  return (
    <div className="flex flex-col gap-3 h-full">
      <div className="flex gap-2 items-center flex-wrap">
        <input className="input" style={{ flex: 1, minWidth: 140 }} placeholder="filter by name" value={filter} disabled={!online}
          onChange={(e) => setFilter(e.target.value)} onKeyDown={(e) => e.key === "Enter" && load()} />
        <button className="btn btn-accent" disabled={busy || !node || !online} onClick={load}>列出</button>
        <label className="flex items-center gap-1 cursor-pointer mono-sm" title="每 3 秒自动刷新">
          <input type="checkbox" checked={auto} disabled={!online} onChange={(e) => setAuto(e.target.checked)}
            style={{ accentColor: "var(--accent)" }} />
          自动刷新
        </label>
        {auto && online && <span className="dot" style={{ background: "var(--accent)", boxShadow: "0 0 6px var(--accent)" }} title="自动刷新中" />}
      </div>
      {!online && <div className="mono-sm" style={{ color: "var(--muted)" }}>{savedAt ? `记录于 ${new Date(savedAt).toLocaleString()}` : "暂无历史数据"}</div>}
      {err && online && <div style={{ color: "var(--danger)" }}>{err}</div>}
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
                  {p.self && <span className="tag ml-1" style={{ color: "var(--accent)", borderColor: "var(--accent)" }} title="当前 sys0 agent 进程">agent</span>}
                </td>
                <td className="px-3 py-1">
                  <button className="btn" style={{ padding: "1px 6px" }} disabled={!online} onClick={() => kill(p.pid, p.name, "TERM")}>TERM</button>{" "}
                  <button className="btn" style={{ padding: "1px 6px", color: "var(--danger)" }} disabled={!online} onClick={() => kill(p.pid, p.name, "KILL")}>KILL</button>
                </td>
              </tr>
            ))}
            {procs.length === 0 && <tr><td colSpan={6} className="px-3 py-4 mono-sm">{online ? "点「列出」加载进程" : "暂无历史数据"}</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
