import { useState } from "react";
import { api } from "../api";

// Live host metrics for the FOCUSED node. Node is fixed by the workspace —
// no batch monitoring of multiple selected nodes.
export function Monitor({ node, live }: { node: string; live: Record<string, any> }) {
  const [busy, setBusy] = useState(false);
  const watch = async (enable: boolean) => {
    if (!node) return;
    setBusy(true);
    try { await api.dispatch({ nodes: [node] }, "host.watch", { enable, interval: 2 }); }
    finally { setBusy(false); }
  };
  const m = live[node];
  return (
    <div className="space-y-3">
      <div className="flex gap-2 items-center">
        <span className="mono-sm">监控 {node}（host.watch 实时推送）</span>
        <button className="btn btn-accent" disabled={busy || !node} onClick={() => watch(true)}>开启</button>
        <button className="btn" disabled={busy || !node} onClick={() => watch(false)}>停止</button>
      </div>
      <div className="panel p-3">
        {!m && <div className="mono-sm">等待数据…（点开启）</div>}
        {m && (
          <>
            <Bar label="CPU" pct={m.cpuPct} text={`${m.cpuPct?.toFixed(1)}%`} />
            <Bar label="MEM" pct={(m.memUsed / m.memTotal) * 100}
              text={`${(m.memUsed / 1e9).toFixed(2)}G / ${(m.memTotal / 1e9).toFixed(2)}G`} />
            <div className="mono-sm mt-2">load1 {m.load1} · rx {(m.netRx / 1e6).toFixed(0)}MB · tx {(m.netTx / 1e6).toFixed(0)}MB</div>
          </>
        )}
      </div>
    </div>
  );
}

function Bar({ label, pct, text }: { label: string; pct: number; text: string }) {
  const p = Math.max(0, Math.min(100, pct || 0));
  return (
    <div className="mb-2">
      <div className="flex justify-between mono-sm mb-1"><span>{label}</span><span>{text}</span></div>
      <div style={{ height: 6, background: "#0b1013", borderRadius: 4, overflow: "hidden" }}>
        <div style={{ width: `${p}%`, height: "100%", background: p > 85 ? "var(--danger)" : "var(--accent)" }} />
      </div>
    </div>
  );
}
