import { useEffect, useState, useCallback } from "react";
import {
  api,
  getToken,
  setToken,
  clearToken,
  eventStream,
  type Node,
  type DispatchItem,
} from "./api";

export function App() {
  const [authed, setAuthed] = useState(!!getToken());
  if (!authed) return <Login onAuthed={() => setAuthed(true)} />;
  return <Console onLogout={() => { clearToken(); setAuthed(false); }} />;
}

function Login({ onAuthed }: { onAuthed: () => void }) {
  const [u, setU] = useState("admin");
  const [p, setP] = useState("admin");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const r = await api.login(u, p);
      if (r.ok && r.token) {
        setToken(r.token);
        onAuthed();
      } else setErr(r.error || "login failed");
    } catch {
      setErr("network error");
    } finally {
      setBusy(false);
    }
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

type Tab = "terminal" | "metrics" | "audit";

function Console({ onLogout }: { onLogout: () => void }) {
  const [nodes, setNodes] = useState<Node[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [tab, setTab] = useState<Tab>("terminal");
  const [live, setLive] = useState<Record<string, any>>({});

  const refresh = useCallback(async () => {
    try {
      const r = await api.nodes();
      if (r.ok) setNodes(r.nodes);
    } catch {
      /* ignore */
    }
  }, []);

  useEffect(() => {
    refresh();
    const es = eventStream(["node", "metrics"], (type, data) => {
      if (type === "event.node") refresh();
      if (type === "event.metrics") setLive((m) => ({ ...m, [data.node]: data.metrics }));
    });
    const t = setInterval(refresh, 5000);
    return () => { es.close(); clearInterval(t); };
  }, [refresh]);

  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s);
      n.has(id) ? n.delete(id) : n.add(id);
      return n;
    });

  const targets = [...selected].filter((id) => nodes.some((n) => n.id === id));

  return (
    <div className="h-full flex flex-col">
      <header className="flex items-center justify-between px-4 py-2.5" style={{ borderBottom: "1px solid var(--border)" }}>
        <div className="flex items-center gap-2">
          <span className="dot" style={{ background: "var(--accent)", boxShadow: "0 0 8px var(--accent)" }} />
          <span style={{ color: "var(--accent)" }}>sys0</span>
          <span className="mono-sm">/ console</span>
        </div>
        <div className="flex items-center gap-3">
          <span className="mono-sm">{nodes.length} online · {targets.length} selected</span>
          <button className="btn" onClick={onLogout}>退出</button>
        </div>
      </header>

      <div className="flex-1 flex min-h-0">
        <NodeList nodes={nodes} selected={selected} toggle={toggle} live={live} onRefresh={refresh} />

        <main className="flex-1 flex flex-col min-w-0">
          <nav className="flex gap-1 px-3 pt-3">
            {(["terminal", "metrics", "audit"] as Tab[]).map((t) => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className="btn"
                style={tab === t ? { borderColor: "var(--accent)", color: "var(--accent)" } : {}}
              >
                {t === "terminal" ? "终端" : t === "metrics" ? "监控" : "审计"}
              </button>
            ))}
          </nav>
          <div className="flex-1 p-3 min-h-0 overflow-auto">
            {tab === "terminal" && <Terminal targets={targets} allCount={nodes.length} />}
            {tab === "metrics" && <Metrics targets={targets} live={live} />}
            {tab === "audit" && <Audit />}
          </div>
        </main>
      </div>
    </div>
  );
}

function NodeList({
  nodes, selected, toggle, live, onRefresh,
}: {
  nodes: Node[]; selected: Set<string>; toggle: (id: string) => void;
  live: Record<string, any>; onRefresh: () => void;
}) {
  return (
    <aside className="w-[290px] flex flex-col" style={{ borderRight: "1px solid var(--border)" }}>
      <div className="flex items-center justify-between px-3 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
        <span className="mono-sm">NODES · 工作集</span>
        <button className="btn" onClick={onRefresh}>↻</button>
      </div>
      <div className="flex-1 overflow-auto p-2 space-y-2">
        {nodes.length === 0 && <div className="mono-sm px-2 py-4">无在线节点</div>}
        {nodes.map((n) => {
          const on = selected.has(n.id);
          const m = live[n.id];
          return (
            <div
              key={n.id}
              onClick={() => toggle(n.id)}
              className="panel p-2.5 cursor-pointer"
              style={on ? { borderColor: "var(--accent)" } : {}}
            >
              <div className="flex items-center gap-2">
                <span className="dot" style={{ background: "var(--accent)" }} />
                <span style={{ color: on ? "var(--accent)" : "var(--fg)" }}>{n.label}</span>
                <span className="mono-sm ml-auto">{n.id}</span>
              </div>
              <div className="mono-sm mt-1.5">{n.host.os}/{n.host.arch} · {n.host.ip}</div>
              {m && (
                <div className="mono-sm mt-1">
                  cpu {m.cpuPct?.toFixed?.(1)}% · mem{" "}
                  {((m.memUsed / m.memTotal) * 100).toFixed(0)}% · load {m.load1}
                </div>
              )}
              {n.tags?.length > 0 && (
                <div className="flex gap-1 mt-1.5">{n.tags.map((t) => <span key={t} className="tag">{t}</span>)}</div>
              )}
            </div>
          );
        })}
      </div>
    </aside>
  );
}

function Terminal({ targets, allCount }: { targets: string[]; allCount: number }) {
  const [cmd, setCmd] = useState("uname -a && uptime");
  const [items, setItems] = useState<DispatchItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const run = async () => {
    setBusy(true);
    setErr("");
    const select = targets.length > 0 ? { nodes: targets } : { all: true };
    try {
      const r = await api.dispatch({ select, call: { method: "shell.run", params: { cmd } } });
      if (r.ok) setItems(r.items || []);
      else setErr(r.error || "dispatch failed");
    } catch {
      setErr("network error");
    } finally {
      setBusy(false);
    }
  };

  const count = targets.length || allCount;
  return (
    <div className="flex flex-col gap-3 h-full">
      <div className="flex gap-2">
        <span className="mono-sm" style={{ color: "var(--accent)", paddingTop: 8 }}>$</span>
        <input
          className="input"
          value={cmd}
          onChange={(e) => setCmd(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && !busy && run()}
          placeholder="shell command"
        />
        <button className="btn btn-accent whitespace-nowrap" disabled={busy || !cmd} onClick={run}>
          {busy ? "运行中" : `运行 · ${count} node${count === 1 ? "" : "s"}`}
        </button>
      </div>
      {err && <div style={{ color: "var(--danger)" }}>{err}</div>}
      <div className="flex-1 space-y-2 overflow-auto">
        {items.map((it) => (
          <div key={it.node} className="panel p-3">
            <div className="flex items-center gap-2 mb-1.5">
              <span className="dot" style={{ background: it.ok ? "var(--accent)" : "var(--danger)" }} />
              <span className="mono-sm">{it.node}</span>
              {it.ok ? (
                <span className="tag ml-auto" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                  exit {it.value?.exit ?? 0}
                </span>
              ) : (
                <span className="tag ml-auto" style={{ color: "var(--danger)", borderColor: "var(--danger)" }}>
                  {it.error?.message}
                </span>
              )}
            </div>
            {it.ok && (
              <pre className="whitespace-pre-wrap" style={{ margin: 0, color: "var(--fg)" }}>
                {it.value?.stdout}
                {it.value?.stderr ? <span style={{ color: "var(--warn)" }}>{it.value.stderr}</span> : null}
              </pre>
            )}
          </div>
        ))}
        {items.length === 0 && !busy && <div className="mono-sm">选中节点（或留空=全部）后回车执行</div>}
      </div>
    </div>
  );
}

function Metrics({ targets, live }: { targets: string[]; live: Record<string, any> }) {
  const [busy, setBusy] = useState(false);
  const watch = async (enable: boolean) => {
    if (targets.length === 0) return;
    setBusy(true);
    try {
      await api.dispatch({
        select: { nodes: targets },
        call: { method: "host.watch", params: { enable, interval: 2 } },
      });
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="space-y-3">
      <div className="flex gap-2 items-center">
        <span className="mono-sm">监控 {targets.length} 个选中节点（host.watch 实时推送）</span>
        <button className="btn btn-accent" disabled={busy || !targets.length} onClick={() => watch(true)}>开启</button>
        <button className="btn" disabled={busy || !targets.length} onClick={() => watch(false)}>停止</button>
      </div>
      <div className="grid grid-cols-2 gap-3">
        {targets.map((id) => {
          const m = live[id];
          return (
            <div key={id} className="panel p-3">
              <div className="mono-sm mb-2">{id}</div>
              {!m && <div className="mono-sm">等待数据…（点开启）</div>}
              {m && (
                <>
                  <Bar label="CPU" pct={m.cpuPct} text={`${m.cpuPct?.toFixed(1)}%`} />
                  <Bar label="MEM" pct={(m.memUsed / m.memTotal) * 100}
                    text={`${(m.memUsed / 1e9).toFixed(2)}G / ${(m.memTotal / 1e9).toFixed(2)}G`} />
                  <div className="mono-sm mt-2">load1 {m.load1} · rx {(m.netRx / 1e6).toFixed(0)}MB · tx {(m.netTx / 1e6).toFixed(0)}MB</div>
                </>
              )}
            </div>
          );
        })}
        {targets.length === 0 && <div className="mono-sm">先在左侧选中节点</div>}
      </div>
    </div>
  );
}

function Bar({ label, pct, text }: { label: string; pct: number; text: string }) {
  const p = Math.max(0, Math.min(100, pct || 0));
  return (
    <div className="mb-2">
      <div className="flex justify-between mono-sm mb-1">
        <span>{label}</span>
        <span>{text}</span>
      </div>
      <div style={{ height: 6, background: "#0b1013", borderRadius: 4, overflow: "hidden" }}>
        <div style={{ width: `${p}%`, height: "100%", background: p > 85 ? "var(--danger)" : "var(--accent)" }} />
      </div>
    </div>
  );
}

function Audit() {
  const [rows, setRows] = useState<any[]>([]);
  useEffect(() => {
    api.audit(80).then((r) => r.ok && setRows(r.audit)).catch(() => {});
  }, []);
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
          {rows.length === 0 && (
            <tr><td colSpan={5} className="px-3 py-4 mono-sm">暂无审计记录</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
