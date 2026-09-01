import { useEffect, useState } from "react";
import { api, getRole, getUser, type Node } from "../api";
import { Accounts } from "./Accounts";
import { Keys } from "./Keys";
import { alertDialog } from "./dialogs";

// Account operations live OUTSIDE the node workspace. Everyone manages their
// password and account-owned HTTP/MCP keys here; admins also manage users and
// audit/revoke keys across accounts.
export function AccountModal({ nodes, onClose }: { nodes: Node[]; onClose: () => void }) {
  const isAdmin = getRole() === "admin";
  const [tab, setTab] = useState<"me" | "users" | "keys">("me");

  return (
    <div
      className="fixed inset-0 flex items-center justify-center"
      style={{ background: "rgba(0,0,0,.6)", backdropFilter: "blur(2px)", zIndex: 2147483500, padding: 16 }}
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="panel" style={{ width: "min(920px, 96vw)", maxHeight: "90vh", display: "flex", flexDirection: "column", boxShadow: "0 12px 40px rgba(0,0,0,.5)" }}>
        <div className="flex items-center justify-between px-4 py-3" style={{ borderBottom: "1px solid var(--border)" }}>
          <div className="flex items-center gap-2">
            <span className="dot" style={{ background: "var(--accent)" }} />
            <span style={{ color: "var(--accent)" }}>账户 / account</span>
            <span className="mono-sm">· {getUser()}（{getRole()}）</span>
          </div>
          <button className="wm-btn wm-close" title="关闭" onClick={onClose} style={{ width: 26, height: 24 }}>✕</button>
        </div>

        <div className="flex gap-2 px-4 py-2 flex-wrap" style={{ borderBottom: "1px solid var(--border)" }}>
          <button className="btn" style={{ padding: "3px 10px", ...(tab === "me" ? { borderColor: "var(--accent)", color: "var(--accent)" } : {}) }}
            onClick={() => setTab("me")}>我的账户</button>
          {isAdmin && (
            <>
              <button className="btn" style={{ padding: "3px 10px", ...(tab === "users" ? { borderColor: "var(--accent)", color: "var(--accent)" } : {}) }}
                onClick={() => setTab("users")}>用户管理</button>
              <button className="btn" style={{ padding: "3px 10px", ...(tab === "keys" ? { borderColor: "var(--accent)", color: "var(--accent)" } : {}) }}
                onClick={() => setTab("keys")}>密钥审计</button>
            </>
          )}
        </div>

        <div className="overflow-auto p-4" style={{ minHeight: 0 }}>
          {tab === "me" && <SelfAccount />}
          {tab === "users" && isAdmin && <Accounts nodes={nodes} meName={getUser()} />}
          {tab === "keys" && isAdmin && <Keys mode="admin" />}
        </div>
      </div>
    </div>
  );
}

function SelfAccount() {
  const [me, setMe] = useState<any>(null);
  const [oldp, setOldp] = useState("");
  const [newp, setNewp] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => { api.me().then((r) => r.ok && setMe(r.user)).catch(() => {}); }, []);

  const change = async () => {
    if (newp.length < 6) { alertDialog("新密码至少 6 位", { title: "失败" }); return; }
    setBusy(true);
    try {
      const r = await api.changeOwnPassword(oldp, newp);
      if (r.ok) { alertDialog("密码已更新", { title: "成功" }); setOldp(""); setNewp(""); }
      else alertDialog(r.error || "失败", { title: "失败" });
    } finally { setBusy(false); }
  };

  return (
    <div className="space-y-4">
      <div className="space-y-3" style={{ maxWidth: 380 }}>
        {me && (
          <div className="panel p-3 mono-sm" style={{ lineHeight: 1.7 }}>
            <div>用户名 · {me.username}</div>
            <div>角色 · {me.role}</div>
            {me.role !== "admin" && <div>可访问节点 · {me.nodeScope?.length || 0} 个</div>}
          </div>
        )}
        <div className="panel p-3 space-y-2">
          <div className="mono-sm" style={{ color: "var(--accent)" }}>修改密码 / change password</div>
          <input className="input" type="password" autoComplete="current-password" placeholder="当前密码" value={oldp} onChange={(e) => setOldp(e.target.value)} />
          <input className="input" type="password" autoComplete="new-password" placeholder="新密码（≥6）" value={newp} onChange={(e) => setNewp(e.target.value)} />
          <button className="btn btn-accent w-full justify-center" disabled={busy || !oldp || !newp} onClick={change}>更新密码</button>
        </div>
      </div>
      <section aria-label="我的密钥">
        <Keys mode="self" />
      </section>
    </div>
  );
}
