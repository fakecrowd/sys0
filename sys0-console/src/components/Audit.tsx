import { useEffect, useMemo, useState } from "react";
import { api } from "../api";

// derive a compact detail string from an audit row's actor/select.
function detail(r: any): string {
  if (r.actorKind === "node") {
    try { const s = JSON.parse(r.select); return `${s.label || ""} ${s.addr || ""}`.trim(); } catch { return ""; }
  }
  try {
    const s = JSON.parse(r.select);
    const where = s.all ? "all" : s.nodes ? s.nodes.join(",") : s.tags ? "#" + s.tags.join(",") : "";
    return `${r.targetCount} 节点 · ${where}`;
  } catch { return String(r.targetCount); }
}

type Kind = "all" | "command" | "node" | "online" | "offline" | "dangerous" | "dry";
const KINDS: [Kind, string][] = [
  ["all", "全部"], ["command", "指令"], ["node", "节点事件"],
  ["online", "上线"], ["offline", "下线"], ["dangerous", "危险方法"], ["dry", "dryRun"],
];

const DANGEROUS = new Set(["shell.run", "shell.open", "shell.input", "proc.signal", "fs.put", "fs.rm", "node.shutdown"]);

export function Audit() {
  const [rows, setRows] = useState<any[]>([]);
  const [kind, setKind] = useState<Kind>("all");
  const [q, setQ] = useState("");

  const reload = () => api.audit(200).then((r) => r.ok && setRows(r.audit)).catch(() => {});
  useEffect(() => { reload(); const t = setInterval(reload, 4000); return () => clearInterval(t); }, []);

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return rows.filter((r) => {
      switch (kind) {
        case "command": if (r.actorKind === "node") return false; break;
        case "node": if (r.actorKind !== "node") return false; break;
        case "online": if (r.method !== "node.online") return false; break;
        case "offline": if (r.method !== "node.offline") return false; break;
        case "dangerous": if (!DANGEROUS.has(r.method)) return false; break;
        case "dry": if (!r.dryRun) return false; break;
      }
      if (needle) {
        const hay = `${r.method} ${r.actorKind} ${r.actorId} ${r.select} ${r.outcome}`.toLowerCase();
        if (!hay.includes(needle)) return false;
      }
      return true;
    });
  }, [rows, kind, q]);

  return (
    <div className="flex flex-col gap-2 h-full min-h-0">
      <div className="flex gap-2 items-center flex-wrap">
        <span className="mono-sm">筛选</span>
        {KINDS.map(([k, label]) => (
          <button key={k} className="btn" style={{ padding: "3px 9px", ...(kind === k ? { borderColor: "var(--accent)", color: "var(--accent)" } : {}) }}
            onClick={() => setKind(k)}>{label}</button>
        ))}
        <input className="input" style={{ flex: 1, minWidth: 160 }} placeholder="关键词：方法 / 节点 / 操作者"
          value={q} onChange={(e) => setQ(e.target.value)} />
        <span className="mono-sm">{filtered.length}/{rows.length}</span>
      </div>
      <div className="panel overflow-auto flex-1">
        <table className="w-full" style={{ borderCollapse: "collapse" }}>
          <thead>
            <tr className="mono-sm" style={{ textAlign: "left" }}>
              {["time", "actor", "method", "target / detail", "outcome"].map((h) => (
                <th key={h} className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)", position: "sticky", top: 0, background: "var(--panel)" }}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {filtered.map((r) => {
              const isNode = r.actorKind === "node";
              return (
                <tr key={r.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td className="px-3 py-1.5 mono-sm">{new Date(r.startedAt * 1000).toLocaleTimeString()}</td>
                  <td className="px-3 py-1.5">{r.actorKind}:{r.actorId}</td>
                  <td className="px-3 py-1.5" style={{ color: isNode ? "var(--warn)" : "var(--accent-2)" }}>
                    {r.method}{r.dryRun ? " (dry)" : ""}
                  </td>
                  <td className="px-3 py-1.5 mono-sm">{detail(r)}</td>
                  <td className="px-3 py-1.5" style={{ color: r.outcome === "offline" ? "var(--danger)" : undefined }}>{r.outcome}</td>
                </tr>
              );
            })}
            {filtered.length === 0 && <tr><td colSpan={5} className="px-3 py-4 mono-sm">无匹配记录</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
