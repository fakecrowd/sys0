import { useEffect, useState, useCallback } from "react";
import { createPortal } from "react-dom";
import { api, getToken, getRole, getUser, setSession, clearSession, eventStream, rememberCreds, forgetCreds, hasRemembered, getRememberedUser, type Node, type RescueInfo, type RescueCommand } from "./api";
import { Shell } from "./components/Shell";
import { Tasks } from "./components/Tasks";
import { Processes } from "./components/Processes";
import { Files } from "./components/Files";
import { Monitor } from "./components/Monitor";
import { Screenshot } from "./components/Screenshot";
import { Actions } from "./components/Actions";
import { Audit } from "./components/Audit";
import { Keys } from "./components/Keys";
import { AccountModal } from "./components/AccountModal";
import { CacheModal } from "./components/CacheModal";
import { Setup } from "./components/Setup";
import { Download } from "./components/Download";
import { Dialogs, confirmDialog, promptDialog, alertDialog } from "./components/dialogs";
import { WindowManager, type WinApp } from "./WindowManager";

export function App() {
  // Public agent-download page (works logged-out). No hooks above this gate
  // run conditionally because AppRoot's hooks live in a separate component.
  if (location.pathname === "/dl" || location.pathname === "/dl/") {
    return (<><Download /><Dialogs /></>);
  }
  return <AppRoot />;
}

function AppRoot() {
  const [authed, setAuthed] = useState(!!getToken());
  const [needsSetup, setNeedsSetup] = useState<boolean | null>(null);

  useEffect(() => {
    if (authed) return;
    api.setupStatus().then((r) => setNeedsSetup(r.ok ? r.needsSetup : false)).catch(() => setNeedsSetup(false));
  }, [authed]);

  let body: React.ReactNode;
  if (authed) {
    body = <Console onLogout={() => { clearSession(); setAuthed(false); }} />;
  } else if (needsSetup === null) {
    body = <div className="h-full flex items-center justify-center mono-sm">…</div>;
  } else if (needsSetup) {
    body = <Setup onDone={() => { setNeedsSetup(false); setAuthed(true); }} />;
  } else {
    body = <Login onAuthed={() => setAuthed(true)} />;
  }
  return (
    <>
      {body}
      <Dialogs />
    </>
  );
}

function Login({ onAuthed }: { onAuthed: () => void }) {
  const [u, setU] = useState(getRememberedUser());
  const [p, setP] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [remember, setRemember] = useState(hasRemembered());
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr("");
    try {
      const r = await api.login(u, p);
      if (r.ok && r.token) {
        setSession(r.token, r.role || "member", r.username || u);
        if (remember) rememberCreds(u, p); else forgetCreds();
        onAuthed();
      }
      else setErr(r.error || "login failed");
    } catch { setErr("network error"); } finally { setBusy(false); }
  };
  return (
    <div className="h-full flex items-center justify-center px-4">
      <form onSubmit={submit} className="panel p-7 w-full max-w-[340px]">
        <div className="flex items-center gap-2 mb-1">
          <span className="dot" style={{ background: "var(--accent)" }} />
          <h1 className="text-lg" style={{ color: "var(--accent)" }}>sys0</h1>
        </div>
        <p className="mono-sm mb-5">远程指令控制 · 中心控制台</p>
        <label className="mono-sm">USER</label>
        <input className="input mt-1 mb-3" value={u} autoComplete="off" placeholder="用户名 / username" onChange={(e) => setU(e.target.value)} />
        <label className="mono-sm">PASSWORD</label>
        <input className="input mt-1 mb-3" type="password" value={p} autoComplete="current-password" placeholder="密码 / password" onChange={(e) => setP(e.target.value)} />
        <label className="flex items-center gap-2 mb-4 mono-sm" style={{ cursor: "pointer", userSelect: "none" }}>
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} style={{ accentColor: "var(--accent)", width: 16, height: 16 }} />
          记住密码 / remember me
        </label>
        {err && <div className="mb-3" style={{ color: "var(--danger)" }}>{err}</div>}
        <button className="btn btn-accent w-full justify-center" disabled={busy}>
          {busy ? "..." : "登录 / LOGIN"}
        </button>
      </form>
    </div>
  );
}

function Console({ onLogout }: { onLogout: () => void }) {
  const [nodes, setNodes] = useState<Node[]>([]);
  // Single-focus model (macOS-workspace style): exactly one node is focused at
  // a time; the right-hand content area is its workspace. "" = nothing focused.
  const [focused, setFocused] = useState<string>("");
  const [live, setLive] = useState<Record<string, any>>({});
  const [navOpen, setNavOpen] = useState(false); // mobile node drawer
  const [acctOpen, setAcctOpen] = useState(false); // account modal (node-independent)
  const [cacheOpen, setCacheOpen] = useState(false); // hub release-binary cache modal
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

  // If the focused node disappears (deleted / forgotten), drop focus so the
  // workspace collapses back to the empty state rather than dangling.
  useEffect(() => {
    if (focused && !nodes.some((n) => n.id === focused)) setFocused("");
  }, [nodes, focused]);

  const select = (id: string) => {
    setFocused(id);
    setNavOpen(false); // reveal the workspace (closes the mobile drawer)
  };

  const focusedNode = nodes.find((n) => n.id === focused);

  // Every console surface is a window bound to the FOCUSED node — no in-window
  // node picker, no batch targeting. Admin-only surfaces append conditionally.
  const apps: WinApp[] = focused ? [
    { key: "shell", title: "Shell", render: () => <Shell node={focused} /> },
    { key: "tasks", title: "任务", render: () => <Tasks node={focused} /> },
    { key: "proc", title: "进程", render: () => <Processes node={focused} /> },
    { key: "files", title: "文件", render: () => <Files node={focused} os={focusedNode?.host.os || ""} /> },
    { key: "monitor", title: "监控", render: () => <Monitor node={focused} live={live} /> },
    { key: "screenshot", title: "截屏", render: () => <Screenshot node={focused} /> },
    { key: "actions", title: "动作", render: () => <Actions node={focused} /> },
    { key: "audit", title: "审计", render: () => <Audit /> },
    ...(isAdmin ? [
      { key: "keys", title: "密钥", render: () => <Keys /> },
    ] as WinApp[] : []),
  ] : [];

  return (
    <div className="h-full flex flex-col">
      <header className="flex items-center justify-between px-4 py-2.5 gap-2" style={{ borderBottom: "1px solid var(--border)" }}>
        <div className="flex items-center gap-2 min-w-0">
          <button className="btn nav-toggle" style={{ padding: "2px 9px" }} aria-label="节点列表"
            onClick={() => setNavOpen((o) => !o)}>☰</button>
          <span className="dot" style={{ background: "var(--accent)", boxShadow: "0 0 8px var(--accent)" }} />
          <span style={{ color: "var(--accent)" }}>sys0</span>
          <span className="mono-sm hide-sm">/ console</span>
        </div>
        <div className="flex items-center gap-3 min-w-0">
          <span className="mono-sm truncate">
            {nodes.filter((n) => n.state === "online").length} online · {focusedNode ? `▸ ${focusedNode.label}` : "未聚焦"}
          </span>
          <button className="btn" title="hub 缓存的 agent/rescue release 版本与强制更新" onClick={() => setCacheOpen(true)}>镜像</button>
          <button className="btn" title="账户" onClick={() => setAcctOpen(true)}>
            <span className="dot" style={{ background: "var(--accent)" }} /> {getUser() || getRole()}
          </button>
          <button className="btn" onClick={onLogout}>退出</button>
        </div>
      </header>

      <div className="flex-1 flex min-h-0 relative">
        {navOpen && <div className="nav-backdrop" onClick={() => setNavOpen(false)} />}
        <div className={navOpen ? "node-drawer open" : "node-drawer"}>
          <NodeList nodes={nodes} focused={focused} onSelect={select} live={live} onRefresh={refresh} />
        </div>
        <main className="flex-1 flex flex-col min-w-0">
          {focused
            ? <WindowManager key={focused} workspaceKey={focused} apps={apps} />
            : <div className="flex-1 flex items-center justify-center px-6" style={{ color: "var(--muted)" }}>
                <div style={{ textAlign: "center", lineHeight: 1.7 }}>
                  <div className="text-lg" style={{ color: "var(--fg)", opacity: 0.8 }}>未聚焦任何节点</div>
                  <div className="mono-sm mt-1">在左侧选择一个节点，打开它的专属工作区</div>
                  <div className="mono-sm mt-1" style={{ opacity: 0.6 }}>窗口布局、位置、大小与内容均与该节点绑定</div>
                </div>
              </div>}
        </main>
      </div>
      {acctOpen && <AccountModal nodes={nodes} onClose={() => setAcctOpen(false)} />}
      {cacheOpen && <CacheModal onClose={() => setCacheOpen(false)} />}
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
  nodes, focused, onSelect, live, onRefresh,
}: {
  nodes: Node[]; focused: string; onSelect: (id: string) => void;
  live: Record<string, any>; onRefresh: () => void;
}) {
  const [sort, setSort] = useState(loadSort);
  const update = (s: { field: SortField; dir: 1 | -1 }) => {
    setSort(s);
    localStorage.setItem(SORT_KEY, JSON.stringify(s));
  };
  const ordered = sortNodes(nodes, live, sort.field, sort.dir);

  return (
    <aside className="w-full h-full flex flex-col" style={{ borderRight: "1px solid var(--border)" }}>
      <div className="flex items-center justify-between px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
        <span className="mono-sm">NODES · 聚焦工作区</span>
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
          <NodeCard key={n.id} n={n} on={focused === n.id} onSelect={onSelect} m={live[n.id]} onChanged={onRefresh} />
        ))}
      </div>
    </aside>
  );
}

function NodeCard({
  n, on, onSelect, m, onChanged,
}: { n: Node; on: boolean; onSelect: (id: string) => void; m: any; onChanged: () => void }) {
  const [open, setOpen] = useState(false);
  const [info, setInfo] = useState<any>(null);
  const [rescueOpen, setRescueOpen] = useState(n.state === "bootstrapping");

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
  const dismiss = async () => {
    if (!(await confirmDialog(`清除引导中节点 ${n.id}?\n（某处的 rescue 仍在上报会让它出现;清除后将忽略其上报 30 分钟。若是失控进程,记得在该机器上停掉它。）`, { title: "清除僵尸节点", danger: true }))) return;
    const r = await api.dismissRescue(n.id);
    if (!r.ok) await alertDialog(r.error || "失败", { title: "清除失败" });
    onChanged();
  };

  const offline = n.state === "offline";
  const bootstrapping = n.state === "bootstrapping";
  const selectable = !offline && !bootstrapping;

  return (
    <div className={selectable ? "panel p-2.5 cursor-pointer" : "panel p-2.5"}
      onClick={() => selectable && onSelect(n.id)}
      style={{ ...(on ? { borderColor: "var(--accent)" } : {}), ...(offline ? { opacity: 0.55 } : {}) }}>
      <div className="flex items-center gap-2">
        <span className="dot" style={{ background: offline ? "var(--muted)" : bootstrapping ? "var(--warn)" : "var(--accent)" }} />
        <span style={{ color: on ? "var(--accent)" : "var(--fg)" }}>{n.label}</span>
        {on && <span className="tag" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>聚焦</span>}
        {offline && <span className="tag" style={{ color: "var(--muted)" }}>offline</span>}
        {bootstrapping && <span className="tag" style={{ color: "var(--warn)", borderColor: "var(--warn)" }}>引导中</span>}
        {n.rescue && (
          <span className="tag cursor-pointer"
            title="点击查看 rescue 守护详情"
            onClick={(e) => { e.stopPropagation(); setRescueOpen((o) => !o); }}
            style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
            rescue {rescueOpen ? "▾" : "▸"}
          </span>
        )}
        <span className="mono-sm ml-auto">{n.id}</span>
      </div>
      <div className="mono-sm mt-1.5">{n.host.os}/{n.host.arch} · {n.host.ip || "—"}</div>
      <div className="mono-sm mt-1" style={{ color: "var(--muted)" }}>
        agent {n.version || "—"}{n.rescue ? ` · rescue ${n.rescueVersion || "?"}` : ""}
      </div>
      {!offline && n.modules && n.modules.length > 0 && (
        <div className="flex items-center gap-1 mt-1 flex-wrap" title="agent 模块连接状态（绿=已连接, 灰=未连接/被拦截）">
          {n.modules.map((mod) => (
            <span key={mod.name} className="tag"
              style={mod.online
                ? { color: "var(--accent)", borderColor: "var(--accent)" }
                : { color: "var(--muted)", borderColor: "var(--border)", opacity: 0.6 }}>
              {mod.online ? "" : "⃠"}{mod.name}
            </span>
          ))}
        </div>
      )}
      {!offline && (n.agentPid || n.agentCwd) && (
        <div className="mono-sm mt-1" style={{ color: "var(--muted)" }}>
          {n.agentPid ? `pid ${n.agentPid}` : ""}{n.agentPid && n.agentCwd ? " · " : ""}{n.agentCwd ? `cwd ${n.agentCwd}` : ""}
        </div>
      )}
      {n.rescue && rescueOpen && <RescueDetail nodeId={n.id} r={n.rescueInfo} fallbackVer={n.rescueVersion} onChanged={onChanged} />}
      {m && !offline && (
        <div className="mono-sm mt-1">
          cpu {m.cpuPct?.toFixed?.(1)}% · mem {((m.memUsed / m.memTotal) * 100).toFixed(0)}% · load {m.load1}
        </div>
      )}
      {offline && n.lastSeen > 0 && (
        <div className="mono-sm mt-1">上次在线 {new Date(n.lastSeen * 1000).toLocaleString()}</div>
      )}
      <div className="flex flex-wrap gap-1 mt-2" onClick={(e) => e.stopPropagation()}>
        {bootstrapping ? (
          <button className="btn" style={{ padding: "2px 7px", color: "var(--danger)" }} onClick={dismiss}>清除</button>
        ) : !offline ? (
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

const RESCUE_PHASES: Record<string, { label: string; color: string }> = {
  starting: { label: "启动中", color: "var(--accent-2)" },
  downloading: { label: "下载 agent", color: "var(--warn)" },
  "starting-agent": { label: "拉起 agent", color: "var(--accent-2)" },
  supervising: { label: "守护中", color: "var(--accent)" },
  restarting: { label: "重启中", color: "var(--warn)" },
  error: { label: "异常", color: "var(--danger)" },
};

function fmtDur(sec: number): string {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m${sec % 60}s`;
  return `${Math.floor(sec / 3600)}h${Math.floor((sec % 3600) / 60)}m`;
}

// fmtClock renders a unix-seconds timestamp as local HH:MM:SS for the trace log.
function fmtClock(t: number): string {
  if (!t) return "—";
  const d = new Date(t * 1000);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

const CMD_LABEL: Record<string, string> = {
  "update-agent": "更新 agent",
  "restart-agent": "重启 agent",
};
const CMD_STATUS: Record<string, { label: string; color: string }> = {
  pending: { label: "待执行", color: "var(--warn)" },
  acked: { label: "已下发", color: "var(--warn)" },
  running: { label: "执行中", color: "var(--accent-2)" },
  done: { label: "完成", color: "var(--accent)" },
  error: { label: "失败", color: "var(--danger)" },
};

// TraceModal shows the rescue's full activity timeline (downloads, agent
// start/exit, operator command receipt + execution results) in a scrollable
// overlay. High z-index so it sits above the window manager (which uses an
// unbounded growing z-index).
function TraceModal({ nodeId, trace, onClose }: {
  nodeId: string; trace: { t: number; m: string }[]; onClose: () => void;
}) {
  return createPortal((
    <div onClick={onClose} style={{
      position: "fixed", inset: 0, background: "rgba(0,0,0,0.6)",
      display: "flex", alignItems: "center", justifyContent: "center", zIndex: 2147483600,
    }}>
      <div onClick={(e) => e.stopPropagation()} className="panel" style={{
        width: "min(680px, 92vw)", maxHeight: "82vh", display: "flex", flexDirection: "column",
        background: "var(--panel)", padding: 0,
      }}>
        <div className="flex items-center gap-2 px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
          <span style={{ flex: 1 }}>活动追踪 · {nodeId}</span>
          <span className="mono-sm" style={{ color: "var(--muted)" }}>{trace.length} 条</span>
          <button className="btn" style={{ padding: "1px 9px" }} onClick={onClose}>关闭</button>
        </div>
        <div className="mono-sm" style={{ overflowY: "auto", padding: "8px 12px" }}>
          {trace.length === 0 && <div style={{ color: "var(--muted)" }}>暂无记录</div>}
          {trace.map((ev, i) => (
            <div key={i} className="flex gap-2" style={{ lineHeight: 1.6, padding: "1px 0" }}>
              <span style={{ color: "var(--muted)", minWidth: 64, flexShrink: 0 }}>{fmtClock(ev.t)}</span>
              <span style={{ wordBreak: "break-all" }}>{ev.m}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  ), document.body);
}

function RescueDetail({ nodeId, r, fallbackVer, onChanged }: {
  nodeId: string; r?: RescueInfo | null; fallbackVer?: string; onChanged: () => void;
}) {
  // r may be absent if the node view predates the richer payload; show a
  // minimal card from whatever we have.
  const phase = r?.status || "supervising";
  const ph = RESCUE_PHASES[phase] || { label: phase, color: "var(--muted)" };
  const rows: [string, string][] = [];
  rows.push(["阶段", ph.label]);
  if (r?.detail) rows.push(["详情", r.detail]);
  rows.push(["版本", r?.version || fallbackVer || "—"]);
  if (r) {
    if (r.agentPid && r.agentPid > 0) rows.push(["agent pid", String(r.agentPid)]);
    if (r.cwd) rows.push(["工作目录", r.cwd]);
    rows.push(["重启次数", String(r.restarts)]);
    if (r.lastExit >= 0 || r.lastUptimeMs > 0)
      rows.push(["上次退出", `code=${r.lastExit}${r.lastUptimeMs ? ` · 存活 ${fmtDur(Math.round(r.lastUptimeMs / 1000))}` : ""}`]);
    rows.push(["守护时长", fmtDur(r.sinceSec)]);
    rows.push(["最近上报", `${r.ageSec}s 前`]);
  }

  const sendCmd = async (kind: string) => {
    const label = CMD_LABEL[kind] || kind;
    if (!(await confirmDialog(`向 ${nodeId} 的 rescue 下发「${label}」?
（rescue 每 ~30s 轮询一次,命令将在下次轮询时执行）`, { title: label }))) return;
    const res = await api.rescueCommand(nodeId, kind);
    if (!res.ok) await alertDialog(res.error || "失败", { title: "下发失败" });
    onChanged();
  };

  // Active (non-terminal) commands shown newest-first; terminal ones too but
  // de-emphasised. Only show controls when the rescue is actually live.
  const cmds: RescueCommand[] = (r?.commands || []).slice().reverse();
  // Full trace, chronological (oldest -> newest). Shown in a modal on demand.
  const trace = r?.trace || [];
  const [traceOpen, setTraceOpen] = useState(false);

  return (
    <div className="mono-sm mt-1.5 p-2" style={{ background: "#0b1013", border: "1px solid var(--border)", borderRadius: 6 }}>
      <div className="flex items-center gap-2 mb-1">
        <span className="dot" style={{ background: ph.color }} />
        <span style={{ color: ph.color }}>sys0-rescue · {ph.label}</span>
      </div>
      {rows.map(([k, v]) => (
        <div key={k} className="flex gap-2" style={{ lineHeight: 1.5 }}>
          <span style={{ color: "var(--muted)", minWidth: 56, flexShrink: 0 }}>{k}</span>
          <span style={{ wordBreak: "break-all" }}>{v}</span>
        </div>
      ))}

      {trace.length > 0 && (
        <div className="mt-2" style={{ borderTop: "1px solid var(--border)", paddingTop: 6 }} onClick={(e) => e.stopPropagation()}>
          <button className="btn" style={{ padding: "2px 8px" }}
            title="查看 rescue 的完整活动追踪（下载/启动/命令获取与执行）"
            onClick={() => setTraceOpen(true)}>
            活动追踪 ({trace.length})
          </button>
        </div>
      )}

      {traceOpen && (
        <TraceModal nodeId={nodeId} trace={trace} onClose={() => setTraceOpen(false)} />
      )}

      {r?.live && (
        <div className="flex flex-wrap gap-1 mt-2" onClick={(e) => e.stopPropagation()}>
          <button className="btn" style={{ padding: "2px 8px" }}
            title="让 rescue 重新从 hub 下载最新 agent 并重启" onClick={() => sendCmd("update-agent")}>更新 agent</button>
          <button className="btn" style={{ padding: "2px 8px" }}
            title="重启被守护的 agent（不重新下载）" onClick={() => sendCmd("restart-agent")}>重启 agent</button>
        </div>
      )}

      {cmds.length > 0 && (
        <div className="mt-2" style={{ borderTop: "1px solid var(--border)", paddingTop: 6 }}>
          <div style={{ color: "var(--muted)", marginBottom: 3 }}>操作记录</div>
          {cmds.slice(0, 5).map((c) => {
            const st = CMD_STATUS[c.status] || { label: c.status, color: "var(--muted)" };
            return (
              <div key={c.id} className="flex gap-2" style={{ lineHeight: 1.5 }}>
                <span style={{ minWidth: 64, flexShrink: 0 }}>{CMD_LABEL[c.kind] || c.kind}</span>
                <span style={{ color: st.color, minWidth: 44, flexShrink: 0 }}>{st.label}</span>
                <span style={{ color: "var(--muted)", wordBreak: "break-all" }}>{c.detail}</span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
