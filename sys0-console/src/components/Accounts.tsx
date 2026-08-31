import { useEffect, useState } from "react";
import { api, type Node, type User_ } from "../api";
import { confirmDialog, promptDialog, alertDialog } from "./dialogs";

// Account management (admin): multi-user, per-user host access, roles, the
// new-node default-access policy, password resets.
export function Accounts({ nodes, meName }: { nodes: Node[]; meName: string }) {
  const [users, setUsers] = useState<User_[]>([]);
  const [defAccess, setDefAccess] = useState<string[]>([]);
  // new-user form
  const [nu, setNu] = useState("");
  const [np, setNp] = useState("");
  const [nrole, setNrole] = useState("member");
  const [nscope, setNscope] = useState<Set<string>>(new Set());

  const load = () => {
    api.usersList().then((r) => r.ok && setUsers(r.users || [])).catch(() => {});
    api.getDefaultAccess().then((r) => r.ok && setDefAccess(r.users || [])).catch(() => {});
  };
  useEffect(() => { load(); }, []);

  const nodeLabel = (id: string) => {
    const n = nodes.find((x) => x.id === id);
    return n ? (n.label || id) + (n.host?.name ? ` (${n.host.name})` : "") : id;
  };

  const create = async () => {
    if (nu.trim() === "" || np.length < 6) {
      alertDialog("用户名必填，密码至少 6 位", { title: "创建失败" }); return;
    }
    const r = await api.userCreate({ username: nu, password: np, role: nrole, nodeScope: [...nscope] });
    if (r.ok) { setNu(""); setNp(""); setNrole("member"); setNscope(new Set()); load(); }
    else alertDialog(r.error || "failed", { title: "创建失败" });
  };

  const toggleScope = async (u: User_, nodeId: string) => {
    const has = u.nodeScope.includes(nodeId);
    const next = has ? u.nodeScope.filter((x) => x !== nodeId) : [...u.nodeScope, nodeId];
    const r = await api.userSetScope(u.id, next);
    if (r.ok) load(); else alertDialog(r.error || "failed");
  };

  const setRole = async (u: User_) => {
    const role = u.role === "admin" ? "member" : "admin";
    if (!(await confirmDialog(`将 ${u.username} 设为 ${role}?`, { title: "修改角色" }))) return;
    const r = await api.userSetRole(u.id, role);
    if (r.ok) load(); else alertDialog(r.error || "failed", { title: "失败" });
  };

  const resetPw = async (u: User_) => {
    const pw = await promptDialog(`为 ${u.username} 设置新密码`, "", "至少 6 位");
    if (pw === null) return;
    if (pw.length < 6) { alertDialog("密码至少 6 位"); return; }
    const r = await api.userSetPassword(u.id, pw);
    if (r.ok) alertDialog("已更新", { title: "成功" }); else alertDialog(r.error || "failed", { title: "失败" });
  };

  const del = async (u: User_) => {
    if (!(await confirmDialog(`删除账户 ${u.username}?`, { title: "删除账户", danger: true }))) return;
    const r = await api.userDelete(u.id);
    if (r.ok) load(); else alertDialog(r.error || "failed", { title: "删除失败" });
  };

  const toggleDefault = async (username: string) => {
    const has = defAccess.includes(username);
    const next = has ? defAccess.filter((x) => x !== username) : [...defAccess, username];
    const r = await api.setDefaultAccess(next);
    if (r.ok) setDefAccess(next); else alertDialog(r.error || "failed");
  };

  return (
    <div className="space-y-3">
      {/* new-node default access policy */}
      <div className="panel p-3 space-y-2">
        <div className="mono-sm" style={{ color: "var(--accent)" }}>新节点默认访问权限 / new-node default access</div>
        <div className="mono-sm" style={{ opacity: 0.7 }}>
          新被控端首次上线时，自动授予以下成员访问权限（管理员始终可见全部）：
        </div>
        <div className="flex gap-3 flex-wrap">
          {users.filter((u) => u.role !== "admin").map((u) => (
            <label key={u.id} className="flex items-center gap-1 cursor-pointer mono-sm">
              <input type="checkbox" checked={defAccess.includes(u.username)} onChange={() => toggleDefault(u.username)} />
              {u.username}
            </label>
          ))}
          {users.filter((u) => u.role !== "admin").length === 0 && (
            <span className="mono-sm" style={{ opacity: 0.6 }}>暂无成员账户</span>
          )}
        </div>
      </div>

      {/* create user */}
      <div className="panel p-3 space-y-2">
        <div className="mono-sm" style={{ color: "var(--accent)" }}>创建账户 / create account</div>
        <div className="flex gap-2 items-center flex-wrap">
          <input className="input" style={{ width: 150 }} value={nu} autoComplete="off" placeholder="用户名" onChange={(e) => setNu(e.target.value)} />
          <input className="input" style={{ width: 150 }} type="password" value={np} autoComplete="new-password" placeholder="密码(≥6)" onChange={(e) => setNp(e.target.value)} />
          <select className="input" value={nrole} onChange={(e) => setNrole(e.target.value)}>
            <option value="member">member</option>
            <option value="admin">admin</option>
          </select>
          <button className="btn btn-accent" onClick={create}>创建</button>
        </div>
        {nrole === "member" && (
          <div>
            <div className="mono-sm mb-1" style={{ opacity: 0.7 }}>可访问的节点 / host access（member）：</div>
            <div className="flex gap-3 flex-wrap">
              {nodes.map((n) => (
                <label key={n.id} className="flex items-center gap-1 cursor-pointer mono-sm">
                  <input type="checkbox" checked={nscope.has(n.id)}
                    onChange={(e) => { const s = new Set(nscope); e.target.checked ? s.add(n.id) : s.delete(n.id); setNscope(s); }} />
                  {nodeLabel(n.id)}
                </label>
              ))}
              {nodes.length === 0 && <span className="mono-sm" style={{ opacity: 0.6 }}>暂无节点</span>}
            </div>
          </div>
        )}
      </div>

      {/* user list */}
      <div className="panel overflow-auto">
        <table className="w-full" style={{ borderCollapse: "collapse" }}>
          <thead>
            <tr className="mono-sm" style={{ textAlign: "left" }}>
              {["用户", "角色", "可访问节点 / host access", "操作"].map((h) => (
                <th key={h} className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id} style={{ borderBottom: "1px solid var(--border)", verticalAlign: "top" }}>
                <td className="px-3 py-2">{u.username}{u.username === meName && <span className="mono-sm" style={{ opacity: 0.5 }}> (你)</span>}</td>
                <td className="px-3 py-2 mono-sm">{u.role}</td>
                <td className="px-3 py-2">
                  {u.role === "admin" ? (
                    <span className="mono-sm" style={{ opacity: 0.6 }}>全部节点（管理员）</span>
                  ) : (
                    <div className="flex gap-3 flex-wrap">
                      {nodes.map((n) => (
                        <label key={n.id} className="flex items-center gap-1 cursor-pointer mono-sm">
                          <input type="checkbox" checked={u.nodeScope.includes(n.id)} onChange={() => toggleScope(u, n.id)} />
                          {nodeLabel(n.id)}
                        </label>
                      ))}
                      {nodes.length === 0 && <span className="mono-sm" style={{ opacity: 0.6 }}>暂无节点</span>}
                    </div>
                  )}
                </td>
                <td className="px-3 py-2 whitespace-nowrap">
                  <button className="btn" style={{ padding: "1px 7px" }} onClick={() => setRole(u)}>切换角色</button>{" "}
                  <button className="btn" style={{ padding: "1px 7px" }} onClick={() => resetPw(u)}>改密</button>{" "}
                  <button className="btn" style={{ padding: "1px 7px", color: "var(--danger)" }} onClick={() => del(u)}>删除</button>
                </td>
              </tr>
            ))}
            {users.length === 0 && <tr><td colSpan={4} className="px-3 py-4 mono-sm">暂无账户</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
