import { useEffect, useState } from "react";
import { api } from "../api";

export function Audit() {
  const [rows, setRows] = useState<any[]>([]);
  const reload = () => api.audit(100).then((r) => r.ok && setRows(r.audit)).catch(() => {});
  useEffect(() => { reload(); const t = setInterval(reload, 4000); return () => clearInterval(t); }, []);
  return (
    <div className="panel overflow-auto">
      <table className="w-full" style={{ borderCollapse: "collapse" }}>
        <thead>
          <tr className="mono-sm" style={{ textAlign: "left" }}>
            {["time", "actor", "method", "targets", "outcome"].map((h) => (
              <th key={h} className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id} style={{ borderBottom: "1px solid var(--border)" }}>
              <td className="px-3 py-1.5 mono-sm">{new Date(r.startedAt * 1000).toLocaleTimeString()}</td>
              <td className="px-3 py-1.5">{r.actorKind}:{r.actorId}</td>
              <td className="px-3 py-1.5" style={{ color: "var(--accent-2)" }}>{r.method}{r.dryRun ? " (dry)" : ""}</td>
              <td className="px-3 py-1.5">{r.targetCount}</td>
              <td className="px-3 py-1.5">{r.outcome}</td>
            </tr>
          ))}
          {rows.length === 0 && <tr><td colSpan={5} className="px-3 py-4 mono-sm">暂无审计记录</td></tr>}
        </tbody>
      </table>
    </div>
  );
}
