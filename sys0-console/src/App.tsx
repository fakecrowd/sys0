import { useEffect, useState, useCallback } from "react";
import { api, getToken, getRole, setSession, clearSession, eventStream, type Node } from "./api";
import { Terminal } from "./components/Terminal";
import { Shell } from "./components/Shell";
import { Processes } from "./components/Processes";
import { Files } from "./components/Files";
import { Monitor } from "./components/Monitor";
import { Actions } from "./components/Actions";
import { Audit } from "./components/Audit";
import { Keys } from "./components/Keys";
import { Dialogs, confirmDialog, promptDialog, alertDialog } from "./components/dialogs";

export function App() {
  const [authed, setAuthed] = useState(!!getToken());
  return (
    <>
      {!authed ? <Login onAuthed={() => setAuthed(true)} />
        : <Console onLogout={() => { clearSession(); setAuthed(false); }} />}
      <Dialogs />
    </>
  );
}

function Login({ onAuthed }: { onAuthed: () => void }) {
  const [u, setU] = useState("admin");
  const [p, setP] = useState("admin");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr("");
    try {
      const r = await api.login(u, p);
      if (r.ok && r.token) { setSession(r.token, r.role || "operator"); onAuthed(); }
      else setErr(r.error || "login failed");
    } catch { setErr("network error"); } finally { setBusy(false); }
  };
  return (
    <div className="h-full flex items-center justify-center">
      <form onSubmit={submit} className="panel p-7 w-[340px]">
        <div className="flex items-center gap-2 mb-1">
          <span className="dot" style={{ background: "var(--accent)" }} />
          <h1 className="text-lg" style={{ color: "var(--accent)" }}>sys0</h1>
        </div>
        <p className="mono-sm mb-5">远程指令控制 · 中心控制台</p>
        <label className="mono-sm">USER</label>
        <input className="input mt-1 mb-3" value={u} onChange={(e) => setU(e.target.value)} />
        <label className="mono-sm">PASSWORD</label>
        <input className="input mt-1 mb-4" type="password" value={p} onChange={(e) => setP(e.target.value)} />
        {err && <div className="mb-3" style={{ color: "var(--danger)" }}>{err}</div>}
        <button className="btn btn-accent w-full justify-center" disabled={busy}>
          {busy ? "..." : "登录 / LOGIN"}
        </button>
      </form>
    </div>
  );
}

const TABS = [
  ["terminal", "终端"], ["shell", "Shell"], ["proc", "进程"], ["files", "文件"],
  ["monitor", "监控"], ["actions", "动作"], ["audit", "审计"], ["keys", "密钥"],
] as const;
type Tab = (typeof TABS)[number][0];

function Console({ onLogout }: { onLogout: () => void }) {
  const [nodes, setNodes] = useState<Node[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [tab, setTab] = useState<Tab>("terminal");
  const [live, setLive] = useState<Record<string, any>>({});
  const isAdmin = getRole() === "admin";

  const refresh = useCallback(async () => {
    try { const r = await api.nodes(); if (r.ok) setNodes(r.nodes); } catch {}
  }, []);

  useEffect(() => {
    refresh();
    const es = eventStream((type, data) => {
      if (type === "event.node") refresh();
      if (type === "event.metrics") setLive((m) => ({ ...m, [data.node]: data.metrics }));
    });
    const t = setInterval(refresh, 5000);
    return () => { es.close(); clearInterval(t); };
  }, [refresh]);

  const toggle = (id: string) =>
    setSelected((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });

  const targets = [...selected].filter((id) => nodes.some((n) => n.id === id));
  const primary = targets[0] || nodes[0]?.id || "";

  return (
    <div className="h-full flex flex-col">
      <header className="flex items-center justify-between px-4 py-2.5" style={{ borderBottom: "1px solid var(--border)" }}>
        <div className="flex items-center gap-2">
          <span className="dot" style={{ background: "var(--accent)", boxShadow: "0 0 8px var(--accent)" }} />
          <span style={{ color: "var(--accent)" }}>sys0</span>
          <span className="mono-sm">/ console</span>
        </div>
        <div className="flex items-center gap-3">
          <span className="mono-sm">{nodes.length} online · {targets.length} selected · {getRole()}</span>
          <button className="btn" onClick={onLogout}>退出</button>
        </div>
      </header>

      <div className="flex-1 flex min-h-0">
        <NodeList nodes={nodes} selected={selected} toggle={toggle} live={live} onRefresh={refresh} />
        <main className="flex-1 flex flex-col min-w-0">
          <nav className="flex flex-wrap gap-1 px-3 pt-3">
            {TABS.filter(([t]) => t !== "keys" || isAdmin).map(([t, label]) => (
              <button key={t} onClick={() => setTab(t)} className="btn"
                style={tab === t ? { borderColor: "var(--accent)", color: "var(--accent)" } : {}}>
                {label}
              </button>
            ))}
          </nav>
          <div className="flex-1 p-3 min-h-0 overflow-auto">
            {tab === "terminal" && <Terminal targets={targets} allCount={nodes.length} />}
            {tab === "shell" && <Shell nodes={nodes} primary={primary} />}
            {tab === "proc" && <Processes nodes={nodes} primary={primary} />}
            {tab === "files" && <Files nodes={nodes} primary={primary} />}
            {tab === "monitor" && <Monitor targets={targets} live={live} />}
            {tab === "actions" && <Actions targets={targets} allCount={nodes.length} />}
            {tab === "audit" && <Audit />}
            {tab === "keys" && isAdmin && <Keys />}
          </div>
        </main>
      </div>
    </div>
  );
}

const SORT_KEY = "sys0_nodesort";
type SortField = "label" | "id" | "cpu" | "mem" | "lastSeen";
const SORT_FIELDS: [SortField, string][] = [
  ["label", "名称"], ["id", "ID"], ["cpu", "CPU"], ["mem", "内存"], ["lastSeen", "上线时间"],
];

function loadSort(): { field: SortField; dir: 1 | -1 } {
  try { return JSON.parse(localStorage.getItem(SORT_KEY) || "") || { field: "label", dir: 1 }; }
  catch { return { field: "label", dir: 1 }; }
}

function sortNodes(nodes: Node[], live: Record<string, any>, field: SortField, dir: 1 | -1): Node[] {
  const val = (n: Node): string | number => {
    switch (field) {
      case "label": return n.label.toLowerCase();
      case "id": return n.id;
      case "cpu": return live[n.id]?.cpuPct ?? -1;
      case "mem": { const m = live[n.id]; return m ? m.memUsed / m.memTotal : -1; }
      case "lastSeen": return n.lastSeen;
    }
  };
  return [...nodes].sort((a, b) => {
    // online nodes always group before offline
    const oa = a.state === "online" ? 0 : 1, ob = b.state === "online" ? 0 : 1;
    if (oa !== ob) return oa - ob;
    const va = val(a), vb = val(b);
    if (va < vb) return -1 * dir;
    if (va > vb) return 1 * dir;
    return a.id < b.id ? -1 : 1; // stable tiebreak by id
  });
}

function NodeList({
  nodes, selected, toggle, live, onRefresh,
}: {
  nodes: Node[]; selected: Set<string>; toggle: (id: string) => void;
  live: Record<string, any>; onRefresh: () => void;
}) {
  const [sort, setSort] = useState(loadSort);
  const update = (s: { field: SortField; dir: 1 | -1 }) => {
    setSort(s);
    localStorage.setItem(SORT_KEY, JSON.stringify(s));
  };
  const ordered = sortNodes(nodes, live, sort.field, sort.dir);

  return (
    <aside className="w-[300px] flex flex-col" style={{ borderRight: "1px solid var(--border)" }}>
      <div className="flex items-center justify-between px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
        <span className="mono-sm">NODES · 工作集</span>
        <button className="btn" onClick={onRefresh}>↻</button>
      </div>
      <div className="flex items-center gap-2 px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
        <span className="mono-sm">排序</span>
        <select className="input" style={{ flex: 1, padding: "3px 6px" }} value={sort.field}
          onChange={(e) => update({ ...sort, field: e.target.value as SortField })}>
          {SORT_FIELDS.map(([f, label]) => <option key={f} value={f}>{label}</option>)}
        </select>
        <button className="btn" style={{ padding: "3px 9px" }} title="切换升降序"
          onClick={() => update({ ...sort, dir: (sort.dir * -1) as 1 | -1 })}>
          {sort.dir === 1 ? "↑" : "↓"}
        </button>
      </div>
      <div className="flex-1 overflow-auto p-2 space-y-2">
        {ordered.length === 0 && <div className="mono-sm px-2 py-4">无在线节点</div>}
        {ordered.map((n) => (
          <NodeCard key={n.id} n={n} on={selected.has(n.id)} toggle={toggle} m={live[n.id]} onChanged={onRefresh} />
        ))}
      </div>
    </aside>
  );
}

function NodeCard({
  n, on, toggle, m, onChanged,
}: { n: Node; on: boolean; toggle: (id: string) => void; m: any; onChanged: () => void }) {
  const [open, setOpen] = useState(false);
  const [info, setInfo] = useState<any>(null);

  const act = async (label: string, danger: boolean, fn: () => Promise<any>) => {
    if (!(await confirmDialog(`${label} @ ${n.label}（${n.id}）?`, { title: label, danger }))) return;
    try { await fn(); onChanged(); } catch (e) { alertDialog(String(e), { title: "操作失败" }); }
  };
  const rename = async () => {
    const v = await promptDialog("重命名节点", n.label, "新别名");
    if (v) { await api.setLabel(n.id, v, n.tags || []); onChanged(); }
  };
  const showInfo = async () => {
    setOpen((o) => !o);
    if (!info) { try { setInfo(await api.one(n.id, "host.info")); } catch (e) { setInfo({ err: String(e) }); } }
  };
  const forget = async () => {
    if (!(await confirmDialog(`从记录中删除离线节点 ${n.label}（${n.id}）?`, { title: "忘记节点", danger: true }))) return;
    const r = await api.deleteNode(n.id);
    if (!r.ok) await alertDialog(r.error || "失败", { title: "删除失败" });
    onChanged();
  };

  const offline = n.state === "offline";

  return (
    <div className="panel p-2.5" style={{ ...(on ? { borderColor: "var(--accent)" } : {}), ...(offline ? { opacity: 0.55 } : {}) }}>
      <div className={offline ? "flex items-center gap-2" : "flex items-center gap-2 cursor-pointer"}
        onClick={() => !offline && toggle(n.id)}>
        <span className="dot" style={{ background: offline ? "var(--muted)" : "var(--accent)" }} />
        <span style={{ color: on ? "var(--accent)" : "var(--fg)" }}>{n.label}</span>
        {offline && <span className="tag" style={{ color: "var(--muted)" }}>offline</span>}
        <span className="mono-sm ml-auto">{n.id}</span>
      </div>
      <div className="mono-sm mt-1.5">{n.host.os}/{n.host.arch} · {n.host.ip || "—"}</div>
      {m && !offline && (
        <div className="mono-sm mt-1">
          cpu {m.cpuPct?.toFixed?.(1)}% · mem {((m.memUsed / m.memTotal) * 100).toFixed(0)}% · load {m.load1}
        </div>
      )}
      {offline && n.lastSeen > 0 && (
        <div className="mono-sm mt-1">上次在线 {new Date(n.lastSeen * 1000).toLocaleString()}</div>
      )}
      <div className="flex flex-wrap gap-1 mt-2">
        {!offline ? (
          <>
            <button className="btn" style={{ padding: "2px 7px" }} onClick={showInfo}>ⓘ</button>
            <button className="btn" style={{ padding: "2px 7px" }} onClick={rename}>✎</button>
            <button className="btn" style={{ padding: "2px 7px" }}
              onClick={() => act("重连", false, () => api.one(n.id, "node.reconnect"))}>⟳</button>
            <button className="btn" style={{ padding: "2px 7px", color: "var(--warn)" }}
              onClick={() => act("关闭被控端", true, () => api.one(n.id, "node.shutdown"))}>⏻</button>
            <button className="btn" style={{ padding: "2px 7px", color: "var(--danger)" }}
              onClick={() => act("断开", true, () => api.detach(n.id))}>✕</button>
          </>
        ) : (
          <button className="btn" style={{ padding: "2px 7px", color: "var(--danger)" }} onClick={forget}>忘记</button>
        )}
      </div>
      {open && info && (
        <pre className="mono-sm mt-2" style={{ margin: 0, whiteSpace: "pre-wrap", color: "var(--muted)" }}>
          {info.err ? info.err :
            `host ${info.hostname}\nkernel ${info.kernel}\ncpu ${info.cpuModel} x${info.cpuCount}\nmem ${(info.memTotal / 1e9).toFixed(1)}G\nup ${(info.uptimeSec / 3600).toFixed(1)}h`}
        </pre>
      )}
    </div>
  );
}
