import { useEffect, useRef, useState, useCallback } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { WSClient } from "../ws";
import { b64encode, b64decode, type Node } from "../api";

type ShellInfo = {
  session: string; name: string; shell: string;
  state: string; exit: number; cols: number; rows: number; started: number;
};

// Interactive PTY shells managed on the agent. Shells are persistent: they live
// on the node across console (re)connects, so reconnecting reattaches to the
// existing sessions (with scrollback replay) instead of forcing a new one.
// Multiple shells per node are supported via the tab bar.
export function Shell({ nodes, primary }: { nodes: Node[]; primary: string }) {
  const [node, setNode] = useState(primary);
  const [connected, setConnected] = useState(false);
  const [shells, setShells] = useState<ShellInfo[]>([]);
  const [active, setActive] = useState<string>("");
  const termRef = useRef<HTMLDivElement>(null);

  // ws + per-session terminals persist across renders.
  const st = useRef<{
    ws?: WSClient;
    terms: Map<string, { term: XTerm; fit: FitAddon; loaded: boolean }>;
    node: string;
  }>({ terms: new Map(), node: "" });

  useEffect(() => setNode(primary || nodes[0]?.id || ""), [primary, nodes.length]);

  const dispatch = useCallback((method: string, params: any) => {
    const s = st.current;
    if (!s.ws) return Promise.reject("no ws");
    return s.ws.call("dispatch", { select: { nodes: [s.node] }, call: { method, params } });
  }, []);

  // Connect: open ws, subscribe, list existing shells, attach to the first
  // (or spawn one if the node has none).
  const connect = async (target?: string) => {
    const nid = target || node;
    if (!nid) return;
    const ws = new WSClient();
    ws.connect();
    await ws.call("hub.subscribe", { topics: ["shell"] });
    st.current = { ws, terms: new Map(), node: nid };

    // Route all live shell output to the matching terminal.
    ws.on("event.shell", (p: any) => {
      if (p.node !== st.current.node) return;
      const entry = st.current.terms.get(p.session);
      if (p.closed || p.exited) {
        if (entry) entry.term.writeln(`\r\n\x1b[33m[${p.closed ? "session closed" : "process exited" + (p.code !== undefined ? " (" + p.code + ")" : "")}]\x1b[0m`);
        if (p.closed) refreshList();
        return;
      }
      if (p.chunk && entry) entry.term.write(b64decode(p.chunk));
    });

    setConnected(true);
    const list = await refreshList();
    if (list.length > 0) attach(list[0].session);
    else newShell();
  };

  const refreshList = async (): Promise<ShellInfo[]> => {
    try {
      const r = await dispatch("shell.list", {});
      const item = r.items?.[0];
      const list: ShellInfo[] = item?.ok ? (item.value.sessions || []) : [];
      list.sort((a, b) => a.started - b.started);
      setShells(list);
      return list;
    } catch { return []; }
  };

  // Attach a terminal to an existing session: create the xterm lazily, replay
  // the buffered scrollback once, then live chunks flow via the emit handler.
  const attach = async (session: string) => {
    setActive(session);
    // Defer to let the container mount, then bind/replay.
    setTimeout(async () => {
      const s = st.current;
      let entry = s.terms.get(session);
      if (!entry) {
        const term = new XTerm({
          fontFamily: '"JetBrains Mono", monospace', fontSize: 13,
          theme: { background: "#0a0e0f", foreground: "#c8d3d6", cursor: "#38e07b" },
          cursorBlink: true, scrollback: 5000,
        });
        const fit = new FitAddon();
        term.loadAddon(fit);
        entry = { term, fit, loaded: false };
        s.terms.set(session, entry);
        term.onData((d) => {
          dispatch("shell.input", { session, data: b64encode(new TextEncoder().encode(d)) });
        });
      }
      const host = termRef.current;
      if (host) {
        host.innerHTML = "";
        entry.term.open(host);
        entry.fit.fit();
      }
      // Replay scrollback once.
      if (!entry.loaded) {
        try {
          const r = await dispatch("shell.output", { session });
          const item = r.items?.[0];
          if (item?.ok && item.value.data) entry.term.write(b64decode(item.value.data));
          entry.loaded = true;
        } catch { /* ignore */ }
      }
      // Sync size to the agent.
      dispatch("shell.resize", { session, cols: entry.term.cols, rows: entry.term.rows });
      entry.term.focus();
    }, 0);
  };

  const newShell = async () => {
    const cols = 100, rows = 30;
    const r = await dispatch("shell.open", { cols, rows });
    const item = r.items?.[0];
    if (!item?.ok) return;
    await refreshList();
    attach(item.value.session);
  };

  // Close (kill) a shell on the agent.
  const closeShell = async (session: string) => {
    await dispatch("shell.close", { session }).catch(() => {});
    const entry = st.current.terms.get(session);
    entry?.term.dispose();
    st.current.terms.delete(session);
    const list = await refreshList();
    if (active === session) {
      if (list.length > 0) attach(list[list.length - 1].session);
      else { setActive(""); if (termRef.current) termRef.current.innerHTML = ""; }
    }
  };

  // Disconnect the console only — shells keep running on the agent.
  const disconnect = () => {
    st.current.terms.forEach((e) => e.term.dispose());
    st.current.ws?.close();
    st.current = { terms: new Map(), node: "" };
    setShells([]); setActive(""); setConnected(false);
    if (termRef.current) termRef.current.innerHTML = "";
  };

  // Switch to another node while connected: tear down the current console
  // connection (shells stay alive on the previous agent) and reconnect to the
  // newly selected node, reattaching to its existing sessions.
  const switchNode = async (next: string) => {
    st.current.terms.forEach((e) => e.term.dispose());
    st.current.ws?.close();
    st.current = { terms: new Map(), node: next };
    setShells([]); setActive("");
    if (termRef.current) termRef.current.innerHTML = "";
    await connect(next);
  };

  useEffect(() => {
    const onResize = () => {
      const e = st.current.terms.get(active);
      if (e) { e.fit.fit(); dispatch("shell.resize", { session: active, cols: e.term.cols, rows: e.term.rows }); }
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [active, dispatch]);

  useEffect(() => () => disconnect(), []); // cleanup on unmount

  return (
    <div className="flex flex-col gap-2 h-full">
      <div className="flex gap-2 items-center">
        <span className="mono-sm">交互 Shell（agent 侧常驻 · 可复用/多开）·</span>
        <select className="input" style={{ width: 200 }} value={node}
          onChange={(e) => { const next = e.target.value; setNode(next); if (connected && next !== st.current.node) switchNode(next); }}>
          {nodes.map((n) => <option key={n.id} value={n.id}>{n.label} · {n.id}</option>)}
        </select>
        {!connected
          ? <button className="btn btn-accent" disabled={!node} onClick={() => connect()}>连接</button>
          : <button className="btn" style={{ color: "var(--danger)" }} onClick={disconnect}>断开（保留会话）</button>}
        <span className="mono-sm">{connected ? "● 已连接" : "○ 未连接"}</span>
      </div>

      {connected && (
        <div className="flex gap-1 items-center" style={{ flexWrap: "wrap" }}>
          {shells.map((s) => (
            <span key={s.session}
              className="mono-sm"
              style={{
                display: "inline-flex", alignItems: "center", gap: 6,
                padding: "3px 8px", borderRadius: 6, cursor: "pointer",
                border: "1px solid var(--border)",
                background: active === s.session ? "var(--accent)" : "transparent",
                color: active === s.session ? "#0a0e0f" : "var(--fg)",
                opacity: s.state === "running" ? 1 : 0.55,
              }}
              onClick={() => attach(s.session)}>
              {s.name || s.shell}{s.state !== "running" ? " (exited)" : ""}
              <span style={{ opacity: 0.7 }} title="关闭会话"
                onClick={(e) => { e.stopPropagation(); closeShell(s.session); }}>✕</span>
            </span>
          ))}
          <button className="btn" style={{ padding: "2px 10px" }} onClick={newShell} title="新建 shell">＋</button>
        </div>
      )}

      <div className="panel" style={{ flex: 1, padding: 8, minHeight: 0 }}>
        <div ref={termRef} style={{ height: "100%", width: "100%" }} />
      </div>
    </div>
  );
}
