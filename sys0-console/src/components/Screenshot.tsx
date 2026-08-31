import { useEffect, useState } from "react";
import { api } from "../api";

// Screenshot captures the FOCUSED node's screen on demand. Node is fixed by the
// workspace. Controls expose the agent's host.screenshot knobs:
//   - 分辨率 (maxWidth): scale the capture down to a max width (0 = native).
//   - 格式 (format): jpeg (smaller) or png (lossless).
//   - 色彩压缩 (quality): JPEG quality 1..100 (lower = smaller, only for jpeg).
// The encoded image comes back base64-inline; we render it as a data: URL and
// offer download + open-in-new-tab.
//
// HISTORY: every capture is kept in a per-node history list (right column).
// History persists in localStorage so it survives reload / node refocus (the
// WindowManager remounts this component on focus change). Entries auto-expire
// after 24h; we also enforce a byte budget so we never blow the localStorage
// quota (oldest evicted first).
type Shot = { format: string; width: number; height: number; size: number; data: string; tool: string };
type HistShot = Shot & { id: string; ts: number };

const WIDTHS = [
  { label: "原始", v: 0 },
  { label: "1920", v: 1920 },
  { label: "1280", v: 1280 },
  { label: "960", v: 960 },
  { label: "640", v: 640 },
];

const TTL_MS = 24 * 60 * 60 * 1000; // keep history for 24 hours
const MAX_BYTES = 4 * 1024 * 1024; // ~4MB per-node budget (localStorage is ~5MB total)

const keyFor = (node: string) => `sys0_shots_v1:${node}`;

function loadHist(node: string): HistShot[] {
  try {
    const raw = localStorage.getItem(keyFor(node));
    const arr = raw ? (JSON.parse(raw) as HistShot[]) : [];
    return Array.isArray(arr) ? arr : [];
  } catch {
    return [];
  }
}

// prune drops entries older than 24h, then enforces the byte budget (oldest out).
function prune(list: HistShot[]): HistShot[] {
  const cutoff = Date.now() - TTL_MS;
  const fresh = list.filter((s) => s.ts >= cutoff).sort((a, b) => b.ts - a.ts);
  const out: HistShot[] = [];
  let total = 0;
  for (const s of fresh) {
    total += s.data.length;
    if (out.length > 0 && total > MAX_BYTES) break;
    out.push(s);
  }
  return out;
}

// save prunes then writes; on QuotaExceeded it drops the oldest and retries.
function save(node: string, list: HistShot[]): HistShot[] {
  let l = prune(list);
  while (l.length > 0) {
    try {
      localStorage.setItem(keyFor(node), JSON.stringify(l));
      return l;
    } catch {
      l = l.slice(0, -1); // evict oldest, retry
    }
  }
  try {
    localStorage.removeItem(keyFor(node));
  } catch {
    /* ignore */
  }
  return l;
}

const fmtTime = (ts: number) => new Date(ts).toLocaleTimeString();

export function Screenshot({ node }: { node: string }) {
  const [maxWidth, setMaxWidth] = useState(1280);
  const [format, setFormat] = useState<"jpeg" | "png">("jpeg");
  const [quality, setQuality] = useState(80);
  const [display, setDisplay] = useState(0);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [hist, setHist] = useState<HistShot[]>([]);
  const [selId, setSelId] = useState("");

  // On mount / node change: load history, prune expired immediately, and keep a
  // 1-minute timer pruning so 24h-old shots vanish on their own without a reload.
  useEffect(() => {
    const initial = save(node, loadHist(node));
    setHist(initial);
    setSelId(initial[0]?.id || "");
    const t = setInterval(() => {
      setHist((cur) => {
        const next = prune(cur);
        if (next.length !== cur.length) {
          save(node, next);
          return next;
        }
        return cur;
      });
    }, 60 * 1000);
    return () => clearInterval(t);
  }, [node]);

  const capture = async () => {
    setBusy(true);
    setErr("");
    try {
      const v = (await api.one(node, "host.screenshot", {
        display, maxWidth, format, quality,
      })) as Shot;
      const entry: HistShot = {
        ...v,
        id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        ts: Date.now(),
      };
      const next = save(node, [entry, ...hist]);
      setHist(next);
      setSelId(entry.id);
    } catch (e: any) {
      setErr(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  const remove = (id: string) => {
    const next = save(node, hist.filter((s) => s.id !== id));
    setHist(next);
    if (selId === id) setSelId(next[0]?.id || "");
  };

  const clearAll = () => {
    save(node, []);
    setHist([]);
    setSelId("");
  };

  const sel = hist.find((s) => s.id === selId) || null;
  const url = sel ? `data:image/${sel.format};base64,${sel.data}` : "";
  const kb = sel ? (sel.size / 1024).toFixed(0) : "0";

  return (
    <div className="flex flex-col gap-2" style={{ height: "100%" }}>
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn" onClick={capture} disabled={busy || !node}>
          {busy ? "截取中…" : "截屏"}
        </button>

        <label className="mono-sm flex items-center gap-1">
          分辨率
          <select className="input" style={{ padding: "2px 6px", width: "auto" }}
            value={maxWidth} onChange={(e) => setMaxWidth(Number(e.target.value))}>
            {WIDTHS.map((w) => <option key={w.v} value={w.v}>{w.label}</option>)}
          </select>
        </label>

        <label className="mono-sm flex items-center gap-1">
          格式
          <select className="input" style={{ padding: "2px 6px", width: "auto" }}
            value={format} onChange={(e) => setFormat(e.target.value as any)}>
            <option value="jpeg">JPEG</option>
            <option value="png">PNG</option>
          </select>
        </label>

        {format === "jpeg" && (
          <label className="mono-sm flex items-center gap-1" title="JPEG 色彩压缩质量：越低越小">
            色彩压缩 {quality}
            <input type="range" min={10} max={100} step={5}
              value={quality} onChange={(e) => setQuality(Number(e.target.value))} />
          </label>
        )}

        <label className="mono-sm flex items-center gap-1" title="多显示器时选择屏幕（0=主屏）">
          屏幕
          <input className="input" type="number" min={0} style={{ padding: "2px 6px", width: 56 }}
            value={display} onChange={(e) => setDisplay(Math.max(0, Number(e.target.value)))} />
        </label>
      </div>

      {err && <div className="mono-sm" style={{ color: "var(--danger)" }}>截屏失败：{err}</div>}

      {sel && (
        <div className="mono-sm flex flex-wrap items-center gap-2" style={{ color: "var(--muted)" }}>
          <span>{new Date(sel.ts).toLocaleString()} · {sel.width}×{sel.height} · {sel.format.toUpperCase()} · {kb} KB · 后端 {sel.tool}</span>
          <a className="btn" style={{ padding: "2px 8px" }}
            href={url} download={`sys0-${node}-${sel.ts}.${sel.format === "jpeg" ? "jpg" : "png"}`}>下载</a>
          <a className="btn" style={{ padding: "2px 8px" }} href={url} target="_blank" rel="noreferrer">新窗口打开</a>
        </div>
      )}

      <div style={{ flex: 1, minHeight: 0, display: "flex", gap: 8 }}>
        {/* preview of the selected shot */}
        <div style={{ flex: 1, minWidth: 0, overflow: "auto", background: "#0b1013",
          border: "1px solid var(--border)", borderRadius: 6, display: "flex",
          alignItems: "center", justifyContent: "center" }}>
          {sel
            ? <img src={url} alt="screenshot" style={{ maxWidth: "100%", height: "auto", display: "block" }} />
            : <span className="mono-sm" style={{ color: "var(--muted)", padding: 16 }}>
                点击「截屏」抓取该节点的屏幕
              </span>}
        </div>

        {/* history list (24h retention, auto-cleaned) */}
        <div style={{ width: 172, flexShrink: 0, display: "flex", flexDirection: "column", gap: 6,
          borderLeft: "1px solid var(--border)", paddingLeft: 8 }}>
          <div className="mono-sm flex items-center justify-between" style={{ color: "var(--muted)" }}>
            <span title="历史截图保留 24 小时，过期自动清理">历史 ({hist.length})</span>
            {hist.length > 0 && (
              <button className="btn" style={{ padding: "1px 6px" }} onClick={clearAll}>清空</button>
            )}
          </div>
          <div style={{ flex: 1, minHeight: 0, overflowY: "auto", display: "flex", flexDirection: "column", gap: 6 }}>
            {hist.length === 0
              ? <span className="mono-sm" style={{ color: "var(--muted)" }}>暂无历史<br />（保留 24 小时）</span>
              : hist.map((s) => (
                  <div key={s.id} onClick={() => setSelId(s.id)}
                    style={{ cursor: "pointer", position: "relative", borderRadius: 4, overflow: "hidden",
                      border: `1px solid ${s.id === selId ? "var(--accent)" : "var(--border)"}` }}>
                    <img src={`data:image/${s.format};base64,${s.data}`} alt=""
                      style={{ width: "100%", height: 70, objectFit: "cover", display: "block", background: "#0b1013" }} />
                    <div className="mono-sm" style={{ display: "flex", justifyContent: "space-between",
                      padding: "2px 4px", color: "var(--muted)", fontSize: 11 }}>
                      <span>{fmtTime(s.ts)}</span>
                      <span>{(s.size / 1024).toFixed(0)}K</span>
                    </div>
                    <button className="btn" title="删除"
                      onClick={(e) => { e.stopPropagation(); remove(s.id); }}
                      style={{ position: "absolute", top: 2, right: 2, padding: "0 5px",
                        lineHeight: "16px", fontSize: 12 }}>×</button>
                  </div>
                ))}
          </div>
        </div>
      </div>
    </div>
  );
}
