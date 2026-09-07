import { useEffect, useRef, useState } from "react";
import { api, type Node, type User_ } from "../api";
import { roleLabel } from "../nodeAccess";
import { confirmDialog, promptDialog } from "./dialogs";

// Instance-owner account administration. Node grants live on each node.
export function Accounts({ meName }: { nodes: Node[]; meName: string }) {
  const [users, setUsers] = useState<User_[]>([]);
  const [nu, setNu] = useState("");
  const [np, setNp] = useState("");
  const [nrole, setNrole] = useState("member");
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const load = async () => {
    const result = await api.usersList();
    if (!result.ok) throw new Error("无法读取账户列表");
    setUsers(result.users || []);
  };
  useEffect(() => { load().catch(() => setError("无法读取账户列表，请重试")); }, []);
  const run = async (action: () => Promise<void>) => {
    if (pending.current) return;
    pending.current = true; setBusy(true); setError(""); setMessage("");
    try { await action(); } catch (e) { setError(e instanceof Error ? e.message : "操作失败，请重试"); }
    finally { pending.current = false; setBusy(false); }
  };
  const create = (event: React.FormEvent) => {
    event.preventDefault();
    return run(async () => {
      if (!nu.trim() || np.length < 6) throw new Error("用户名必填，密码至少 6 位");
      const result = await api.userCreate({ username: nu.trim(), password: np, role: nrole, nodeScope: [] });
      if (!result.ok) throw new Error(result.error || "创建失败");
      setNu(""); setNp(""); setNrole("member"); await load(); setMessage("账户已创建");
    });
  };
  const setRole = (user: User_) => run(async () => {
    if (user.username === meName) return;
    const role = user.role === "admin" ? "member" : "admin";
    if (!(await confirmDialog(`将 ${user.username} 设为${roleLabel(role)}？${role === "admin" ? "此角色可管理全部节点和账户。" : ""}`, { title: "修改角色" }))) return;
    const result = await api.userSetRole(user.id, role);
    if (!result.ok) throw new Error(result.error || "修改失败");
    await load(); setMessage("角色已更新");
  });
  const resetPw = (user: User_) => run(async () => {
    const password = await promptDialog(`为 ${user.username} 设置新密码`, "", "至少 6 位");
    if (password === null) return;
    if (password.length < 6) throw new Error("密码至少 6 位");
    const result = await api.userSetPassword(user.id, password);
    if (!result.ok) throw new Error(result.error || "修改失败");
    setMessage("密码已更新");
  });
  const del = (user: User_) => run(async () => {
    if (user.username === meName) return;
    if (!(await confirmDialog(`删除账户 ${user.username}？`, { title: "删除账户", danger: true }))) return;
    const result = await api.userDelete(user.id);
    if (!result.ok) throw new Error(result.error || "删除失败");
    await load(); setMessage("账户已删除");
  });
  return <div className="space-y-3">
    <div className="mono-sm">账户由实例 owner 创建。节点访问权限在节点的「访问管理」中设置。</div>
    <form className="panel p-3 space-y-2" onSubmit={create}>
      <div className="mono-sm" style={{ color: "var(--accent)" }}>创建账户</div>
      <div className="grid gap-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(140px, 1fr))" }}>
        <input className="input" aria-label="用户名" value={nu} disabled={busy} autoComplete="off" placeholder="用户名" onChange={(event) => setNu(event.target.value)} />
        <input className="input" aria-label="初始密码" type="password" value={np} disabled={busy} autoComplete="new-password" placeholder="密码（≥6）" onChange={(event) => setNp(event.target.value)} />
        <select className="input" aria-label="账户角色" value={nrole} disabled={busy} onChange={(event) => setNrole(event.target.value)}>
          <option value="member">普通用户</option><option value="admin">实例 owner</option>
        </select>
        <button className="btn btn-accent justify-center" disabled={busy || !nu.trim() || np.length < 6}>创建</button>
      </div>
    </form>
    {error && <div role="alert" className="mono-sm" style={{ color: "var(--danger)" }}>{error} <button className="btn" disabled={busy} onClick={() => run(load)}>刷新</button></div>}
    {message && <div role="status" className="mono-sm" style={{ color: "var(--accent)" }}>{message}</div>}
    <div className="panel overflow-auto">
      <table className="w-full" style={{ borderCollapse: "collapse" }}>
        <thead><tr className="mono-sm" style={{ textAlign: "left" }}>{["用户", "角色", "操作"].map((label) => <th key={label} className="px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>{label}</th>)}</tr></thead>
        <tbody>{users.map((user) => <tr key={user.id} style={{ borderBottom: "1px solid var(--border)" }}>
          <td className="px-3 py-2">{user.username}{user.username === meName && <span className="mono-sm">（你）</span>}</td>
          <td className="px-3 py-2 mono-sm whitespace-nowrap">{roleLabel(user.role)}</td>
          <td className="px-3 py-2"><div className="flex gap-1 flex-wrap">
            <button className="btn" disabled={busy || user.username === meName} onClick={() => setRole(user)}>切换角色</button>
            <button className="btn" disabled={busy} onClick={() => resetPw(user)}>改密</button>
            <button className="btn" disabled={busy || user.username === meName} style={{ color: "var(--danger)" }} onClick={() => del(user)}>删除</button>
          </div></td>
        </tr>)}</tbody>
      </table>
      {users.length === 0 && !error && <div className="px-3 py-4 mono-sm">暂无账户</div>}
    </div>
  </div>;
}
