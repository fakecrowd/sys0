import { useEffect, useState } from "react";
import { api, type ReleaseList, type ReleaseAsset } from "../api";

function human(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}

function AssetTable({ title, empty, assets }: { title: string; empty: string; assets: ReleaseAsset[] }) {
  return (
    <div>
      <div className="mono-sm mb-2" style={{ color: "var(--accent)" }}>{title}</div>
      {assets.length === 0 ? (
        <div className="panel p-4 mono-sm">{empty}</div>
      ) : (
        <table className="w-full" style={{ borderCollapse: "collapse" }}>
          <thead>
            <tr className="mono-sm" style={{ textAlign: "left", opacity: 0.7 }}>
              <th style={{ padding: "6px 8px" }}>平台 OS</th>
              <th style={{ padding: "6px 8px" }}>架构 ARCH</th>
              <th style={{ padding: "6px 8px" }}>文件 FILE</th>
              <th style={{ padding: "6px 8px" }}>大小</th>
              <th style={{ padding: "6px 8px" }}></th>
            </tr>
          </thead>
          <tbody>
            {assets.map((a) => (
              <tr key={a.name} style={{ borderTop: "1px solid var(--border)" }}>
                <td className="mono-sm" style={{ padding: "8px" }}>{a.os || "\u2014"}</td>
                <td className="mono-sm" style={{ padding: "8px" }}>{a.arch || "\u2014"}</td>
                <td className="mono-sm" style={{ padding: "8px", wordBreak: "break-all" }}>{a.name}</td>
                <td className="mono-sm" style={{ padding: "8px" }}>{human(a.size)}</td>
                <td style={{ padding: "8px" }}>
                  <a className="btn btn-accent" href={a.url}>下载</a>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// Public agent-download page, served at /dl. Lists the sys0-agent binaries
// from the latest GitHub release; updates automatically on each new release.
export function Download() {
  const [data, setData] = useState<ReleaseList | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    api.releases()
      .then((r) => { if (r.ok) setData(r); else setErr(r.error || "failed to load releases"); })
      .catch(() => setErr("network error"));
  }, []);

  return (
    <div className="h-full overflow-auto" style={{ padding: "32px 0" }}>
      <div style={{ maxWidth: 760, margin: "0 auto" }}>
        <div className="flex items-center gap-2 mb-1">
          <span className="dot" style={{ background: "var(--accent)" }} />
          <h1 className="text-lg" style={{ color: "var(--accent)" }}>sys0-agent</h1>
        </div>
        <p className="mono-sm mb-5">被控端可执行文件下载 · agent downloads</p>

        {err && <div className="panel p-4" style={{ color: "var(--danger)" }}>{err}</div>}
        {!data && !err && <div className="mono-sm">加载中…</div>}

        {data && (
          <>
            <div className="mono-sm mb-4">
              最新版本 / latest:{" "}
              <a href={data.releaseUrl} target="_blank" rel="noreferrer" style={{ color: "var(--accent)" }}>
                {data.tag || data.name || "—"}
              </a>
              {data.publishedAt && <> · {new Date(data.publishedAt).toLocaleString()}</>}
            </div>

            <AssetTable
              title="被控端 · sys0-agent"
              empty="该 release 暂无 agent 可执行文件。"
              assets={data.assets.filter((a) => a.kind !== "rescue")}
            />

            <div style={{ marginTop: 24 }}>
              <AssetTable
                title="守护/救援端 · sys0-rescue"
                empty="该 release 暂无 rescue 可执行文件。"
                assets={data.assets.filter((a) => a.kind === "rescue")}
              />
            </div>

            <div className="panel p-4 mt-6 mono-sm" style={{ lineHeight: 1.7 }}>
              <div style={{ color: "var(--accent)", marginBottom: 6 }}>开箱即用 · zero-config</div>
              <div style={{ opacity: 0.85 }}>
                下载后直接<strong>双击运行</strong>即可——已内置本环境地址，自动以 wss 安全连接到{" "}
                <code>{location.host}</code>。无需任何参数。
              </div>
              <div style={{ opacity: 0.85, marginTop: 6 }}>
                Just download &amp; run — the hosted hub address is baked in; it connects to{" "}
                <code>{location.host}</code> over wss automatically. No flags needed.
                <br />
                <span style={{ opacity: 0.65 }}>
                  (macOS/Linux 命令行需先 <code>chmod +x</code>；如遇 Gatekeeper 拦截可右键打开。)
                </span>
              </div>
              <div style={{ opacity: 0.6, marginTop: 10 }}>
                自建 hub 可手动覆盖:{" "}
                <code>./sys0-agent -hub &lt;host&gt; -transport wss -key &lt;ACCESS_KEY&gt; -label &lt;name&gt;</code>
              </div>
              <div style={{ opacity: 0.6, marginTop: 10 }}>
                守护/开机自启:下载 <code>sys0-rescue</code> 后运行{" "}
                <code>sys0-rescue install</code>{" "}
                即自动下载并保活 agent、注册开机自启(有管理员=系统级,普通用户=登录级,无需 root)。
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
