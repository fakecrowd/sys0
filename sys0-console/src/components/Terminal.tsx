import { useState } from "react";
import { api, type DispatchItem } from "../api";

export function Terminal({ targets, allCount }: { targets: string[]; allCount: number }) {
  const [cmd, setCmd] = useState("uname -a && uptime");
  const [cwd, setCwd] = useState("");
  const [timeout, setTimeoutS] = useState(30);
  const [dry, setDry] = useState(false);
  const [items, setItems] = useState<DispatchItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const run = async () => {
    setBusy(true); setErr("");
    const select = targets.length > 0 ? { nodes: targets } : { all: true };
    try {
      const r = await api.dispatch(select, "shell.run", { cmd, cwd: cwd || undefined, timeout }, dry);
      if (r.ok) setItems(r.items || []);
      else setErr(r.error || "dispatch failed");
    } catch { setErr("network error"); } finally { setBusy(false); }
  };

  const count = targets.length || allCount;
  return (
    <div className="flex flex-col gap-3 h-full">
      <div className="flex gap-2 items-center">
        <span className="mono-sm" style={{ color: "var(--accent)" }}>$</span>
        <input className="input" value={cmd} onChange={(e) => setCmd(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && !busy && run()} placeholder="shell command (批量, 非交互)" />
        <button className="btn btn-accent whitespace-nowrap" disabled={busy || !cmd} onClick={run}>
          {busy ? "运行中" : `运行 · ${count}`}
        </button>
      </div>
      <div className="flex gap-2 items-center mono-sm">
        <span>cwd</span>
        <input className="input" style={{ width: 200 }} value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder="(默认)" />
        <span>timeout</span>
        <input className="input" style={{ width: 70 }} type="number" value={timeout} onChange={(e) => setTimeoutS(+e.target.value)} />
        <label className="flex items-center gap-1 cursor-pointer">
          <input type="checkbox" checked={dry} onChange={(e) => setDry(e.target.checked)} /> dryRun
        </label>
      </div>
      {err && <div style={{ color: "var(--danger)" }}>{err}</div>}
      <div className="flex-1 space-y-2 overflow-auto">
        {items.map((it) => (
          <div key={it.node} className="panel p-3">
            <div className="flex items-center gap-2 mb-1.5">
              <span className="dot" style={{ background: it.ok ? "var(--accent)" : "var(--danger)" }} />
              <span className="mono-sm">{it.node}</span>
              {it.ok ? (
                <span className="tag ml-auto" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                  {it.value?.dryRun ? "dry-run" : `exit ${it.value?.exit ?? 0}`}
                </span>
              ) : (
                <span className="tag ml-auto" style={{ color: "var(--danger)", borderColor: "var(--danger)" }}>{it.error?.message}</span>
              )}
            </div>
            {it.ok && !it.value?.dryRun && (
              <pre className="whitespace-pre-wrap" style={{ margin: 0 }}>
                {it.value?.stdout}
                {it.value?.stderr ? <span style={{ color: "var(--warn)" }}>{it.value.stderr}</span> : null}
              </pre>
            )}
          </div>
        ))}
        {items.length === 0 && !busy && <div className="mono-sm">选中节点（或留空=全部）后回车执行</div>}
      </div>
    </div>
  );
}
