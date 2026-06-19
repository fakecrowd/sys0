import { useEffect, useRef, useState } from "react";
import { api } from "../api";

// Live host metrics for the FOCUSED node. Node is fixed by the workspace.
// Auto-starts host.watch on mount (no manual button). Keeps a rolling history
// per metric and renders lightweight inline SVG sparkline charts — no chart
// library, to keep the bundle lean.
const HISTORY = 60; // samples kept (~2min at 2s interval)

type Sample = {
  ts: number; cpuPct: number; memPct: number; swapPct: number;
  load1: number; diskPct: number; rxRate: number; txRate: number; procs: number;
};

export function Monitor({ node, live }: { node: string; live: Record<string, any> }) {
  const [hist, setHist] = useState<Sample[]>([]);
  const prevNet = useRef<{ ts: number; rx: number; tx: number } | null>(null);
  const m = live[node];

  // Auto-start watching on mount; stop on unmount. Re-runs if node changes
  // (but the window remounts per workspace, so node is effectively constant).
  useEffect(() => {
    if (!node) return;
    api.dispatch({ nodes: [node] }, "host.watch", { enable: true, interval: 2 }).catch(() => {});
    return () => { api.dispatch({ nodes: [node] }, "host.watch", { enable: false }).catch(() => {}); };
  }, [node]);

  // Fold each incoming metrics sample into rolling history (derive net rates).
  useEffect(() => {
    if (!m || typeof m.ts !== "number") return;
    let rxRate = 0, txRate = 0;
    const p = prevNet.current;
    if (p && m.ts > p.ts) {
      const dt = m.ts - p.ts;
      rxRate = Math.max(0, (m.netRx - p.rx) / dt);
      txRate = Math.max(0, (m.netTx - p.tx) / dt);
    }
    prevNet.current = { ts: m.ts, rx: m.netRx, tx: m.netTx };
    const s: Sample = {
      ts: m.ts,
      cpuPct: m.cpuPct ?? 0,
      memPct: m.memTotal ? (m.memUsed / m.memTotal) * 100 : 0,
      swapPct: m.swapTotal ? (m.swapUsed / m.swapTotal) * 100 : 0,
      load1: m.load1 ?? 0,
      diskPct: m.diskTotal ? (m.diskUsed / m.diskTotal) * 100 : 0,
      rxRate, txRate, procs: m.procs ?? 0,
    };
    setHist((h) => {
      // de-dup same-ts (SSE can repeat) and cap length
      if (h.length && h[h.length - 1].ts === s.ts) return h;
      const next = [...h, s];
      return next.length > HISTORY ? next.slice(next.length - HISTORY) : next;
    });
  }, [m]);

  if (!node) return <div className="mono-sm">无聚焦节点</div>;

  const cores: number[] = m?.cpuCores || [];
  const memUsedG = m ? (m.memUsed / 1e9).toFixed(2) : "0";
  const memTotG = m ? (m.memTotal / 1e9).toFixed(2) : "0";
  const swapUsedG = m ? (m.swapUsed / 1e9).toFixed(2) : "0";
  const swapTotG = m ? (m.swapTotal / 1e9).toFixed(2) : "0";
  const diskUsedG = m ? (m.diskUsed / 1e9).toFixed(1) : "0";
  const diskTotG = m ? (m.diskTotal / 1e9).toFixed(1) : "0";

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="dot" style={{ background: m ? "var(--accent)" : "var(--muted)" }} />
        <span className="mono-sm">{node} · 实时监控（自动开启）</span>
        {m && <span className="mono-sm" style={{ color: "var(--muted)" }}>
          {fmtUptime(m.uptimeSec)} · {m.procs ?? 0} 进程
        </span>}
        {!m && <span className="mono-sm">等待数据…</span>}
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Metric title="CPU" value={`${(m?.cpuPct ?? 0).toFixed(1)}%`} pct={m?.cpuPct ?? 0}
          color="var(--accent)" data={hist.map((s) => s.cpuPct)} />
        <Metric title="内存 MEM" value={`${memUsedG}G / ${memTotG}G`}
          pct={hist.length ? hist[hist.length - 1].memPct : 0}
          color="var(--accent-2)" data={hist.map((s) => s.memPct)} />
        <Metric title="磁盘 DISK" value={`${diskUsedG}G / ${diskTotG}G`}
          pct={hist.length ? hist[hist.length - 1].diskPct : 0}
          color="#a78bfa" data={hist.map((s) => s.diskPct)} />
        <Metric title="交换 SWAP" value={Number(swapTotG) > 0 ? `${swapUsedG}G / ${swapTotG}G` : "无"}
          pct={hist.length ? hist[hist.length - 1].swapPct : 0}
          color="var(--warn)" data={hist.map((s) => s.swapPct)} />
      </div>

      {/* network throughput (rate, auto-scaled) */}
      <div className="panel p-3">
        <div className="flex justify-between mono-sm mb-2">
          <span>网络吞吐 NET</span>
          <span>↓ {fmtRate(hist.length ? hist[hist.length - 1].rxRate : 0)} · ↑ {fmtRate(hist.length ? hist[hist.length - 1].txRate : 0)}</span>
        </div>
        <DualSpark rx={hist.map((s) => s.rxRate)} tx={hist.map((s) => s.txRate)} />
      </div>

      {/* load averages */}
      <div className="panel p-3">
        <div className="flex justify-between mono-sm mb-2">
          <span>负载 LOAD</span>
          <span>1m {fmt2(m?.load1)} · 5m {fmt2(m?.load5)} · 15m {fmt2(m?.load15)}</span>
        </div>
        <Spark data={hist.map((s) => s.load1)} color="#f59e0b" fill autoscale />
      </div>

      {/* per-core CPU */}
      {cores.length > 0 && (
        <div className="panel p-3">
          <div className="mono-sm mb-2">每核 CPU · {cores.length} 核</div>
          <div className="grid gap-1.5" style={{ gridTemplateColumns: `repeat(${Math.min(cores.length, 8)}, 1fr)` }}>
            {cores.map((c, i) => (
              <div key={i} title={`core ${i}: ${c.toFixed(0)}%`}>
                <div className="mono-sm" style={{ fontSize: 9, textAlign: "center" }}>{c.toFixed(0)}</div>
                <div style={{ height: 40, background: "#0b1013", borderRadius: 3, overflow: "hidden", display: "flex", flexDirection: "column", justifyContent: "flex-end" }}>
                  <div style={{ height: `${Math.max(0, Math.min(100, c))}%`, background: coreColor(c), transition: "height .3s" }} />
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// A metric card: big value, % bar, and a filled sparkline of recent history.
function Metric({ title, value, pct, color, data }: { title: string; value: string; pct: number; color: string; data: number[] }) {
  const p = Math.max(0, Math.min(100, pct || 0));
  return (
    <div className="panel p-3">
      <div className="flex justify-between mono-sm mb-1">
        <span>{title}</span>
        <span style={{ color: "var(--fg)" }}>{value}</span>
      </div>
      <div className="mb-2" style={{ height: 5, background: "#0b1013", borderRadius: 4, overflow: "hidden" }}>
        <div style={{ width: `${p}%`, height: "100%", background: p > 85 ? "var(--danger)" : color, transition: "width .3s" }} />
      </div>
      <Spark data={data} color={color} fill max={100} />
    </div>
  );
}

// Single-series sparkline. max fixes the y-scale (e.g. 100 for %); autoscale
// derives it from the data; fill paints the area under the line.
function Spark({ data, color, max, fill, autoscale }: { data: number[]; color: string; max?: number; fill?: boolean; autoscale?: boolean }) {
  const W = 300, H = 40, n = data.length;
  if (n < 2) return <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="none" />;
  const top = max ?? (autoscale ? Math.max(1, ...data) * 1.15 : Math.max(1, ...data));
  const x = (i: number) => (i / (n - 1)) * W;
  const y = (v: number) => H - (Math.max(0, Math.min(top, v)) / top) * (H - 2) - 1;
  const pts = data.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const area = `0,${H} ${pts} ${W},${H}`;
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="none">
      {fill && <polygon points={area} fill={color} opacity={0.12} />}
      <polyline points={pts} fill="none" stroke={color} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

// Dual-series sparkline for rx (down) / tx (up), shared auto-scale.
function DualSpark({ rx, tx }: { rx: number[]; tx: number[] }) {
  const W = 300, H = 48, n = Math.max(rx.length, tx.length);
  if (n < 2) return <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="none" />;
  const top = Math.max(1, ...rx, ...tx) * 1.15;
  const x = (i: number, len: number) => (i / (len - 1)) * W;
  const y = (v: number) => H - (Math.max(0, Math.min(top, v)) / top) * (H - 2) - 1;
  const line = (d: number[]) => d.map((v, i) => `${x(i, d.length).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="none">
      <polygon points={`0,${H} ${line(rx)} ${W},${H}`} fill="var(--accent)" opacity={0.1} />
      <polyline points={line(rx)} fill="none" stroke="var(--accent)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
      <polyline points={line(tx)} fill="none" stroke="var(--accent-2)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

function coreColor(c: number) { return c > 85 ? "var(--danger)" : c > 50 ? "var(--warn)" : "var(--accent)"; }
function fmt2(v?: number) { return (v ?? 0).toFixed(2); }
function fmtRate(bps: number) {
  if (bps < 1024) return `${bps.toFixed(0)} B/s`;
  if (bps < 1e6) return `${(bps / 1024).toFixed(1)} KB/s`;
  if (bps < 1e9) return `${(bps / 1e6).toFixed(1)} MB/s`;
  return `${(bps / 1e9).toFixed(2)} GB/s`;
}
function fmtUptime(sec?: number) {
  if (!sec) return "—";
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), mi = Math.floor((sec % 3600) / 60);
  if (d > 0) return `up ${d}d${h}h`;
  if (h > 0) return `up ${h}h${mi}m`;
  return `up ${mi}m`;
}
