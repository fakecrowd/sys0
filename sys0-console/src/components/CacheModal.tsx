import { useEffect, useState } from "react";
import { api, getRole, type CacheStatus } from "../api";
import { confirmDialog, alertDialog } from "./dialogs";

// CacheModal shows the agent/rescue release the hub currently has cached, with
// per-asset freshness, and (admin) a force-refresh that re-pulls the latest
// release from GitHub immediately — useful right after a build to push the new
// agent out to nodes without waiting for the cache TTL.
export function CacheModal({ onClose }: { onClose: () => void }) {
  const [st, setSt] = useState<CacheStatus | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const isAdmin = getRole() === "admin";

  const load = async () => {
    setErr("");
    try { setSt(await api.cacheStatus()); }
    catch (e: any) { setErr(String(e?.message || e)); }
  };
  useEffect(() => { load(); }, []);

  const refresh = async () => {
    if (!(await confirmDialog(
      "强制刷新：重新从 GitHub 拉取最新 release 的全部 agent/rescue 二进制到 hub 缓存。\n节点下次下载即取最新版本。继续？",
      { title: "强制更新缓存" }))) return;
    setBusy(true);
    try {
      const r = await api.cacheRefresh();
      setSt(r.status);
      if (!r.ok) await alertDialog(`部分资产刷新失败（${r.failed?.length || 0}）`, { title: "刷新未完全成功" });
    } catch (e: any) {
      await alertDialog(String(e?.message || e), { title: "刷新失败" });
    } finally {
      setBusy(false);
    }
  };

  const fmtKB = (n?: number) => (n ? (n / 1024 / 1024).toFixed(1) + " MB" : "—");

  return (
    <div onClick={onClose} style={{
      position: "fixed", inset: 0, background: "rgba(0,0,0,0.6)",
      display: "flex", alignItems: "center", justifyContent: "center", zIndex: 2147483600,
    }}>
      <div onClick={(e) => e.stopPropagation()} className="panel" style={{
        width: "min(760px, 94vw)", maxHeight: "84vh", display: "flex", flexDirection: "column", padding: 0,
      }}>
        <div className="flex items-center gap-2 px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
          <span style={{ flex: 1 }}>镜像缓存 · agent / rescue release</span>
          <button className="btn" style={{ padding: "1px 9px" }} onClick={load} disabled={busy}>刷新状态</button>
          {isAdmin && (
            <button className="btn btn-accent" style={{ padding: "1px 9px" }} onClick={refresh} disabled={busy}>
              {busy ? "更新中…" : "强制更新"}
            </button>
          )}
          <button className="btn" style={{ padding: "1px 9px" }} onClick={onClose}>关闭</button>
        </div>

        <div style={{ overflowY: "auto", padding: "10px 12px" }}>
          {err && <div className="mono-sm" style={{ color: "var(--danger)" }}>{err}</div>}
          {!st && !err && <div className="mono-sm" style={{ color: "var(--muted)" }}>加载中…</div>}
          {st && (
            <>
              <div className="mono-sm flex flex-col gap-1" style={{ marginBottom: 10 }}>
                <div className="flex gap-2">
                  <span style={{ color: "var(--muted)", minWidth: 88 }}>当前 release</span>
                  <span style={{ color: "var(--accent)" }}>{st.tag || "—"}</span>
                  {st.releaseUrl && <a href={st.releaseUrl} target="_blank" rel="noreferrer" style={{ color: "var(--muted)" }}>↗</a>}
                </div>
                <div className="flex gap-2">
                  <span style={{ color: "var(--muted)", minWidth: 88 }}>hub 版本</span>
                  <span>{st.hubVersion || "—"}</span>
                </div>
                <div className="flex gap-2">
                  <span style={{ color: "var(--muted)", minWidth: 88 }}>已缓存</span>
                  <span style={{ color: st.cachedCount === st.totalCount ? "var(--accent)" : "var(--warn)" }}>
                    {st.cachedCount} / {st.totalCount} 个资产
                  </span>
                  {st.releaseAgeSec != null && <span style={{ color: "var(--muted)" }}>· 列表 {st.releaseAgeSec}s 前刷新</span>}
                </div>
              </div>

              <div style={{ overflowX: "auto" }}>
                <table className="mono-sm" style={{ width: "100%", borderCollapse: "collapse" }}>
                  <thead>
                    <tr style={{ color: "var(--muted)", textAlign: "left" }}>
                      <th style={{ padding: "3px 6px" }}>类型</th>
                      <th style={{ padding: "3px 6px" }}>平台</th>
                      <th style={{ padding: "3px 6px" }}>缓存</th>
                      <th style={{ padding: "3px 6px" }}>大小</th>
                      <th style={{ padding: "3px 6px" }}>缓存时长</th>
                    </tr>
                  </thead>
                  <tbody>
                    {st.assets.map((a) => (
                      <tr key={a.name} style={{ borderTop: "1px solid var(--border)" }}>
                        <td style={{ padding: "3px 6px" }}>{a.kind}</td>
                        <td style={{ padding: "3px 6px" }}>{a.os}/{a.arch}</td>
                        <td style={{ padding: "3px 6px", color: a.cached ? "var(--accent)" : "var(--muted)" }}>
                          {a.cached ? "✓" : "—"}
                        </td>
                        <td style={{ padding: "3px 6px" }}>{a.cached ? fmtKB(a.size) : "—"}</td>
                        <td style={{ padding: "3px 6px", color: "var(--muted)" }}>{a.cached && a.ageSec != null ? `${a.ageSec}s` : "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {!isAdmin && (
                <div className="mono-sm" style={{ color: "var(--muted)", marginTop: 8 }}>
                  仅管理员可强制更新缓存。
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
