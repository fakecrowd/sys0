import { useState } from "react";
import { api } from "../api";

export function Monitor({ targets, live }: { targets: string[]; live: Record<string, any> }) {
  const [busy, setBusy] = useState(false);
  const watch = async (enable: boolean) => {
    if (targets.length === 0) return;
    setBusy(true);
    try { await api.dispatch({ nodes: targets }, "host.watch", { enable, interval: 2 }); }
    finally { setBusy(false); }
  };
  return (
    <div className="space-y-3">
      <div className="flex gap-2 items-center">
        <span className="mono-sm">监控 {targets.length} 个选中节点（host.watch 实时推送）</span>
        <button className="btn btn-accent" disabled={busy || !targets.length} onClick={() => watch(true)}>开启</button>
        <button className="btn" disabled={busy || !targets.length} onClick={() => watch(false)}>停止</button>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        {targets.map((id) => {
          const m = live[id];
          return (
            <div key={id} className="panel p-3">
              <div className="mono-sm mb-2">{id}</div>
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
          );
        })}
        {targets.length === 0 && <div className="mono-sm">先在左侧选中节点</div>}
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
