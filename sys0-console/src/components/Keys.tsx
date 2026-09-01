import { useEffect, useState } from "react";
import { api, type AccountKey } from "../api";
import { confirmDialog, alertDialog } from "./dialogs";

// Account-owned HTTP/MCP credentials. Self mode lets every logged-in user manage
// only their own keys; admin mode is audit/revocation across all accounts.
export function Keys({ mode }: { mode: "self" | "admin" }) {
  const [keys, setKeys] = useState<AccountKey[]>([]);
  const [name, setName] = useState("mcp-http");
  const [methods, setMethods] = useState("");
  const [created, setCreated] = useState("");

  const load = () => (mode === "self" ? api.myKeysList() : api.keysList())
    .then((r) => r.ok && setKeys(r.keys || [])).catch(() => {});
  useEffect(() => { load(); }, [mode]);

  const create = async () => {
    const scope = methods.split(",").map((s) => s.trim()).filter(Boolean);
    const r = await api.myKeyCreate({ name: name.trim(), methodScope: scope });
    if (r.ok && r.key) { setCreated(r.key); load(); }
    else alertDialog(r.error || "failed", { title: "创建失败" });
  };
  const revoke = async (id: string) => {
    if (!(await confirmDialog("吊销 " + id + "? 吊销后使用该 key 的 MCP/HTTP 客户端会立即失效。", { title: "吊销密钥", danger: true }))) return;
    const r = mode === "self" ? await api.myKeyRevoke(id) : await api.keyRevoke(id);
    if (!r.ok) await alertDialog(r.error || "failed", { title: "吊销失败" });
    load();
  };
  const copy = async () => {
    try { await navigator.clipboard.writeText(created); await alertDialog("已复制", { title: "密钥" }); }
    catch { await alertDialog("复制失败，请手动选择密钥", { title: "密钥" }); }
  };

  return (
    <div className="space-y-3">
      {mode === "self" && (
        <div className="panel p-3 space-y-2">
          <div className="mono-sm" style={{ color: "var(--accent)" }}>我的密钥 · HTTP API / MCP</div>
          <div className="mono-sm" style={{ color: "var(--muted)", lineHeight: 1.6 }}>
            密钥归属于当前账户，实时继承账户角色和可访问节点；账户降权、删除或密钥吊销后立即失效。方法范围留空表示不额外收窄。
          </div>
          <div className="flex gap-2 items-center flex-wrap">
            <input className="input" style={{ width: 180 }} value={name} onChange={(e) => setName(e.target.value)} placeholder="名称" />
            <input className="input" style={{ minWidth: 260, flex: 1 }} value={methods} onChange={(e) => setMethods(e.target.value)} placeholder="方法范围（逗号分隔，留空=账户允许的全部）" />
            <button className="btn btn-accent" disabled={!name.trim()} onClick={create}>创建</button>
          </div>
          <div className="mono-sm" style={{ color: "var(--muted)" }}>
            MCP endpoint · <span style={{ userSelect: "all" }}>{location.origin}/mcp</span>　HTTP · Authorization: Bearer &lt;key&gt;
          </div>
          {created && (
            <div className="p-2" style={{ border: "1px solid var(--accent)", borderRadius: 5 }}>
              <div className="mono-sm" style={{ color: "var(--warn)" }}>新密钥仅显示一次，请立即保存</div>
              <div className="flex gap-2 items-center mt-1">
                <code style={{ userSelect: "all", wordBreak: "break-all", flex: 1 }}>{created}</code>
                <button className="btn" onClick={copy}>复制</button>
                <button className="btn" onClick={() => setCreated("")}>隐藏</button>
              </div>
            </div>
          )}
        </div>
      )}
      {mode === "admin" && (
        <div className="mono-sm" style={{ color: "var(--muted)" }}>
          管理员审计视图：密钥由各账户自行创建；管理员只能查看归属和范围，或在泄露时吊销，不能读取密钥正文。
        </div>
      )}
      <div className="panel overflow-auto">
        <table className="w-full" style={{ borderCollapse: "collapse", minWidth: 620 }}>
          <thead><tr className="mono-sm" style={{ textAlign: "left" }}>
            {(mode === "admin" ? ["ID", "账户", "名称", "方法范围", "创建时间", ""] : ["ID", "名称", "方法范围", "创建时间", ""]).map((h) =>
              <th key={h} className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>{h}</th>)}
          </tr></thead>
          <tbody>
            {keys.map((k) => <tr key={k.id} style={{ borderBottom: "1px solid var(--border)" }}>
              <td className="px-3 py-1.5 mono-sm">{k.id}</td>
              {mode === "admin" && <td className="px-3 py-1.5">{k.owner}</td>}
              <td className="px-3 py-1.5">{k.name}</td>
              <td className="px-3 py-1.5 mono-sm">{k.methodScope || "账户允许的全部"}</td>
              <td className="px-3 py-1.5 mono-sm">{new Date(k.createdAt * 1000).toLocaleString()}</td>
              <td className="px-3 py-1.5"><button className="btn" style={{ padding: "1px 7px", color: "var(--danger)" }} onClick={() => revoke(k.id)}>吊销</button></td>
            </tr>)}
            {keys.length === 0 && <tr><td colSpan={mode === "admin" ? 6 : 5} className="px-3 py-4 mono-sm">暂无密钥</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
