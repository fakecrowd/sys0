import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, type Node, type NodeAccess } from "../api";
import { roleLabel } from "../nodeAccess";

export function NodeOwnership({ node, onChanged }: { node: Node; onChanged: () => void | Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const [message, setMessage] = useState("");
  const [open, setOpen] = useState(false);
  useEffect(() => { if (!node.canManageAccess) setOpen(false); }, [node.canManageAccess]);
  const claim = async () => {
    if (pending.current || !node.canClaim) return;
    pending.current = true; setBusy(true); setMessage("");
    try {
      const result = await api.claimNode(node.id);
      await onChanged();
      if (!result.ok) setMessage(result.httpStatus === 409 ? "已被其他用户认领" : result.error || "认领失败");
    } catch { setMessage("无法认领，请重试"); }
    finally { pending.current = false; setBusy(false); }
  };
  const dialog = open && node.canManageAccess
    ? <NodeAccessDialog key={node.id} node={node} onChanged={onChanged} onClose={() => setOpen(false)} /> : null;
  return <div className="mt-2" onClick={(event) => event.stopPropagation()}>
    <div className="flex items-center gap-2 flex-wrap">
      <span className="tag" style={{ color: node.owner ? "var(--accent)" : "var(--muted)" }}>
        {node.owner ? `节点 owner · ${node.owner}` : "未认领"}
      </span>
      {node.canClaim && <button className="btn btn-accent" style={{ padding: "2px 7px" }} disabled={busy} onClick={claim}>{busy ? "认领中…" : "认领"}</button>}
      {node.canManageAccess && <button className="btn" style={{ padding: "2px 7px" }} onClick={() => setOpen(true)}>访问管理</button>}
    </div>
    {message && <div role="status" className="mono-sm mt-1" style={{ color: "var(--warn)" }}>{message}</div>}
    {dialog && (typeof document === "undefined" ? dialog : createPortal(dialog, document.body))}
  </div>;
}

function NodeAccessDialog({ node, onChanged, onClose }: { node: Node; onChanged: () => void | Promise<void>; onClose: () => void }) {
  const [access, setAccess] = useState<NodeAccess | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const pending = useRef(false);
  const alive = useRef(true);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const apply = (result: NodeAccess) => {
    if (!alive.current) return;
    setAccess(result);
    setSelected(new Set(result.users.filter((user) => user.allowed).map((user) => user.username)));
  };
  useEffect(() => {
    alive.current = true;
    api.nodeAccess(node.id).then((result) => {
      if (!alive.current) return;
      if (!result.ok) { setError(result.error || "无法读取访问权限"); return; }
      apply(result);
    }).catch(() => { if (alive.current) setError("无法读取访问权限，请关闭后重试"); });
    return () => { alive.current = false; };
  }, [node.id]);
  const save = async () => {
    if (pending.current || !access || !node.canManageAccess) return;
    pending.current = true; setBusy(true); setError(""); setSaved(false);
    try {
      const users = access.users.filter((user) => user.username !== access.owner && user.role !== "admin" && selected.has(user.username)).map((user) => user.username);
      const result = await api.setNodeAccess(node.id, users);
      if (!result.ok) {
        if (alive.current) setError(result.error || "保存失败");
        await onChanged(); return;
      }
      const verified = await api.nodeAccess(node.id);
      await onChanged();
      if (!alive.current) return;
      if (!verified.ok) { setAccess(null); setError("无法确认访问权限，请关闭后重试"); return; }
      apply(verified); setSaved(true);
    } catch { if (alive.current) setError("保存失败，请重试"); }
    finally { pending.current = false; if (alive.current) setBusy(false); }
  };
  return <div className="fixed inset-0 flex items-center justify-center" style={{ background: "rgba(0,0,0,.6)", backdropFilter: "blur(2px)", zIndex: 2147483500, padding: 16 }}
    onMouseDown={(event) => event.target === event.currentTarget && onClose()} onKeyDown={(event) => event.key === "Escape" && onClose()}>
    <section role="dialog" aria-modal="true" aria-labelledby="node-access-title" className="panel" style={{ width: "min(520px, 96vw)", maxHeight: "85vh", display: "flex", flexDirection: "column" }}>
      <header className="flex items-center justify-between px-4 py-3" style={{ borderBottom: "1px solid var(--border)" }}>
        <div><div id="node-access-title" style={{ color: "var(--accent)" }}>节点访问 · {node.label}</div><div className="mono-sm mt-1">节点 owner · {access?.owner || node.owner || "未认领"}</div></div>
        <button className="wm-btn wm-close" aria-label="关闭访问管理" autoFocus onClick={onClose}>✕</button>
      </header>
      <div className="p-4 overflow-auto space-y-3">
        <p className="mono-sm">允许访问的用户可执行全部节点操作。节点 owner 和实例 owner 始终可访问，且仅他们可管理访问权限。</p>
        {!access && !error && <div className="mono-sm">加载中…</div>}
        {access && <div className="grid gap-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))" }}>
          {access.users.map((user) => {
            const implicit = user.username === access.owner || user.role === "admin";
            return <label key={user.id} className="panel flex items-center gap-2 px-3 py-2">
              <input type="checkbox" checked={implicit || selected.has(user.username)} disabled={implicit || busy}
                onChange={(event) => { const next = new Set(selected); event.target.checked ? next.add(user.username) : next.delete(user.username); setSelected(next); setSaved(false); }} />
              <span className="min-w-0"><span className="block truncate">{user.username}</span><span className="mono-sm">{user.username === access.owner ? "节点 owner" : user.role ? roleLabel(user.role) : "允许访问"}</span></span>
            </label>;
          })}
          {access.users.length === 0 && <div className="mono-sm">暂无其他账户</div>}
        </div>}
        {error && <div role="alert" className="mono-sm" style={{ color: "var(--danger)" }}>{error}</div>}
        {saved && <div role="status" className="mono-sm" style={{ color: "var(--accent)" }}>已保存</div>}
      </div>
      <footer className="flex justify-end gap-2 px-4 py-3" style={{ borderTop: "1px solid var(--border)" }}>
        <button className="btn" onClick={onClose}>关闭</button><button className="btn btn-accent" disabled={!access || busy} onClick={save}>{busy ? "保存中…" : "保存"}</button>
      </footer>
    </section>
  </div>;
}
