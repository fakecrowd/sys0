import { useState } from "react";
import { api } from "../api";

// Screenshot captures the FOCUSED node's screen on demand. Node is fixed by the
// workspace. Controls expose the agent's host.screenshot knobs:
//   - 分辨率 (maxWidth): scale the capture down to a max width (0 = native).
//   - 格式 (format): jpeg (smaller) or png (lossless).
//   - 色彩压缩 (quality): JPEG quality 1..100 (lower = smaller, only for jpeg).
// The encoded image comes back base64-inline; we render it as a data: URL and
// offer download + open-in-new-tab.
type Shot = { format: string; width: number; height: number; size: number; data: string; tool: string };

const WIDTHS = [
  { label: "原始", v: 0 },
  { label: "1920", v: 1920 },
  { label: "1280", v: 1280 },
  { label: "960", v: 960 },
  { label: "640", v: 640 },
];

export function Screenshot({ node }: { node: string }) {
  const [maxWidth, setMaxWidth] = useState(1280);
  const [format, setFormat] = useState<"jpeg" | "png">("jpeg");
  const [quality, setQuality] = useState(80);
  const [display, setDisplay] = useState(0);
  const [shot, setShot] = useState<Shot | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [ts, setTs] = useState(0);

  const capture = async () => {
    setBusy(true);
    setErr("");
    try {
      const v = (await api.one(node, "host.screenshot", {
        display, maxWidth, format, quality,
      })) as Shot;
      setShot(v);
      setTs(Date.now());
    } catch (e: any) {
      setErr(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  const url = shot ? `data:image/${shot.format};base64,${shot.data}` : "";
  const kb = shot ? (shot.size / 1024).toFixed(0) : "0";

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

      {shot && (
        <div className="mono-sm flex flex-wrap items-center gap-2" style={{ color: "var(--muted)" }}>
          <span>{shot.width}×{shot.height} · {shot.format.toUpperCase()} · {kb} KB · 后端 {shot.tool}</span>
          <a className="btn" style={{ padding: "2px 8px" }}
            href={url} download={`sys0-${node}-${ts}.${shot.format === "jpeg" ? "jpg" : "png"}`}>下载</a>
          <a className="btn" style={{ padding: "2px 8px" }} href={url} target="_blank" rel="noreferrer">新窗口打开</a>
        </div>
      )}

      <div style={{ flex: 1, minHeight: 0, overflow: "auto", background: "#0b1013",
        border: "1px solid var(--border)", borderRadius: 6, display: "flex",
        alignItems: "center", justifyContent: "center" }}>
        {shot
          ? <img src={url} alt="screenshot" style={{ maxWidth: "100%", height: "auto", display: "block" }} />
          : <span className="mono-sm" style={{ color: "var(--muted)", padding: 16 }}>
              点击「截屏」抓取该节点的屏幕
            </span>}
      </div>
    </div>
  );
}
