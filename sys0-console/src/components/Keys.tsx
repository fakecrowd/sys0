import { useEffect, useState } from "react";
import { api } from "../api";

// API Key management (admin) — create machine credentials for HTTP API / MCP.
export function Keys() {
  const [keys, setKeys] = useState<any[]>([]);
  const [name, setName] = useState("agent-bot");
  const [dangerous, setDangerous] = useState(false);
  const [created, setCreated] = useState("");

  const load = () => api.keysList().then((r) => r.ok && setKeys(r.keys || [])).catch(() => {});
  useEffect(() => { load(); }, []);

  const create = async () => {
    const r = await api.keyCreate({ name, role: "operator", allowDangerous: dangerous });
    if (r.ok && r.key) { setCreated(r.key); load(); }
    else alert(r.error || "failed");
  };
  const revoke = async (id: string) => {
    if (!confirm("吊销 " + id + "?")) return;
    await api.keyRevoke(id); load();
  };

  return (
    <div className="space-y-3">
      <div className="panel p-3 space-y-2">
        <div className="mono-sm">创建 API Key（用于 HTTP API / MCP 机器接入）</div>
        <div className="flex gap-2 items-center flex-wrap">
          <input className="input" style={{ width: 200 }} value={name} onChange={(e) => setName(e.target.value)} placeholder="name" />
          <label className="flex items-center gap-1 cursor-pointer mono-sm">
            <input type="checkbox" checked={dangerous} onChange={(e) => setDangerous(e.target.checked)} /> 允许危险方法
          </label>
          <button className="btn btn-accent" onClick={create}>创建</button>
        </div>
        {created && (
          <div className="mono-sm" style={{ color: "var(--accent)" }}>
            新密钥（仅显示一次）：<span style={{ userSelect: "all" }}>{created}</span>
          </div>
        )}
      </div>
      <div className="panel overflow-auto">
        <table className="w-full" style={{ borderCollapse: "collapse" }}>
          <thead>
            <tr className="mono-sm" style={{ textAlign: "left" }}>
              {["id", "name", "role", "dangerous", "scopes", ""].map((h) => (
                <th key={h} className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.ID} style={{ borderBottom: "1px solid var(--border)" }}>
                <td className="px-3 py-1.5 mono-sm">{k.ID}</td>
                <td className="px-3 py-1.5">{k.Name}</td>
                <td className="px-3 py-1.5">{k.Role}</td>
                <td className="px-3 py-1.5">{k.AllowDangerous ? "✓" : "—"}</td>
                <td className="px-3 py-1.5 mono-sm">{k.NodeScope || "*"} / {k.MethodScope || "*"}</td>
                <td className="px-3 py-1.5">
                  <button className="btn" style={{ padding: "1px 7px", color: "var(--danger)" }} onClick={() => revoke(k.ID)}>吊销</button>
                </td>
              </tr>
            ))}
            {keys.length === 0 && <tr><td colSpan={6} className="px-3 py-4 mono-sm">暂无 API Key</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
