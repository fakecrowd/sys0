import { useState } from "react";
import { api, setSession } from "../api";

// First-run setup: create the first administrator. Shown only when the hub
// reports needsSetup=true (no users in the DB yet).
export function Setup({ onDone }: { onDone: () => void }) {
  const [u, setU] = useState("");
  const [p, setP] = useState("");
  const [p2, setP2] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr("");
    if (u.trim() === "") { setErr("请输入用户名"); return; }
    if (p.length < 6) { setErr("密码至少 6 位"); return; }
    if (p !== p2) { setErr("两次输入的密码不一致"); return; }
    setBusy(true);
    try {
      const r = await api.setup(u, p);
      if (r.ok && r.token) { setSession(r.token, r.role || "admin", r.username || u); onDone(); }
      else setErr(r.error || "setup failed");
    } catch { setErr("network error"); } finally { setBusy(false); }
  };

  return (
    <div className="h-full flex items-center justify-center px-4">
      <form onSubmit={submit} className="panel p-7 w-full max-w-[360px]">
        <div className="flex items-center gap-2 mb-1">
          <span className="dot" style={{ background: "var(--accent)" }} />
          <h1 className="text-lg" style={{ color: "var(--accent)" }}>sys0 · 初始化</h1>
        </div>
        <p className="mono-sm mb-5">首次启动 · 创建管理员账户 / create the first admin</p>
        <label className="mono-sm">管理员用户名 / ADMIN USER</label>
        <input className="input mt-1 mb-3" value={u} autoComplete="off" placeholder="用户名" onChange={(e) => setU(e.target.value)} />
        <label className="mono-sm">密码 / PASSWORD</label>
        <input className="input mt-1 mb-3" type="password" value={p} autoComplete="new-password" placeholder="至少 6 位" onChange={(e) => setP(e.target.value)} />
        <label className="mono-sm">确认密码 / CONFIRM</label>
        <input className="input mt-1 mb-4" type="password" value={p2} autoComplete="new-password" placeholder="再次输入密码" onChange={(e) => setP2(e.target.value)} />
        {err && <div className="mb-3" style={{ color: "var(--danger)" }}>{err}</div>}
        <button className="btn btn-accent w-full justify-center" disabled={busy}>
          {busy ? "..." : "创建管理员 / CREATE"}
        </button>
      </form>
    </div>
  );
}
