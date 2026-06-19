import { useEffect, useRef, useState, useCallback } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { WSClient } from "../ws";
import { b64encode, b64decode } from "../api";

type ShellInfo = {
  session: string; name: string; shell: string;
  state: string; exit: number; cols: number; rows: number; started: number;
};

// Interactive PTY shells on the FOCUSED node. Shells are persistent: they live
// on the node across console (re)connects, so reconnecting reattaches to the
// existing sessions (with scrollback replay) instead of forcing a new one.
// Multiple shells per node are supported via the tab bar. The node is fixed by
// the workspace — there is no in-window node picker.
//
// Each session owns its OWN persistent container div (created once, term.open
// called once). Switching just toggles which container is visible — we never
// wipe the host or re-open a terminal (re-opening detaches xterm's renderer and
// renders blank). A ResizeObserver refits the active terminal whenever the
// window frame changes size.
export function Shell({ node }: { node: string }) {
  const [connected, setConnected] = useState(false);
  const [shells, setShells] = useState<ShellInfo[]>([]);
  const [active, setActive] = useState<string>("");
  const areaRef = useRef<HTMLDivElement>(null); // holds all per-session containers

  const st = useRef<{
    ws?: WSClient;
    terms: Map<string, { term: XTerm; fit: FitAddon; el: HTMLDivElement; loaded: boolean }>;
    node: string;
    active: string;
  }>({ terms: new Map(), node: "", active: "" });

  const dispatch = useCallback((method: string, params: any) => {
    const s = st.current;
    if (!s.ws) return Promise.reject("no ws");
    return s.ws.call("dispatch", { select: { nodes: [s.node] }, call: { method, params } });
  }, []);

  // Fit the active terminal to its container and tell the agent the new size.
  const fitActive = useCallback(() => {
    const s = st.current;
    const e = s.terms.get(s.active);
    if (!e || e.el.style.display === "none") return;
    try {
      e.fit.fit();
      if (e.term.cols > 0 && e.term.rows > 0)
        dispatch("shell.resize", { session: s.active, cols: e.term.cols, rows: e.term.rows });
    } catch { /* container not measurable yet */ }
  }, [dispatch]);

  const connect = async () => {
    if (!node) return;
    const ws = new WSClient();
    ws.connect();
    await ws.call("hub.subscribe", { topics: ["shell"] });
    st.current = { ws, terms: new Map(), node, active: "" };

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

  // Attach/switch to a session: ensure its container exists (created once),
  // hide every other container, show this one, replay scrollback on first open.
  const attach = (session: string) => {
    setActive(session);
    st.current.active = session;
    setTimeout(async () => {
      const s = st.current;
      const host = areaRef.current;
      if (!host) return;
      let entry = s.terms.get(session);
      if (!entry) {
        const el = document.createElement("div");
        el.style.height = "100%";
        el.style.width = "100%";
        host.appendChild(el);
        const term = new XTerm({
          fontFamily: '"JetBrains Mono", monospace', fontSize: 13,
          theme: { background: "#0a0e0f", foreground: "#c8d3d6", cursor: "#38e07b" },
          cursorBlink: true, scrollback: 5000,
        });
        const fit = new FitAddon();
        term.loadAddon(fit);
        term.open(el); // open ONCE on this dedicated container
        entry = { term, fit, el, loaded: false };
        s.terms.set(session, entry);
        term.onData((d) => {
          dispatch("shell.input", { session, data: b64encode(new TextEncoder().encode(d)) });
        });
      }
      // Show only this session's container.
      s.terms.forEach((e, sid) => { e.el.style.display = sid === session ? "block" : "none"; });
      try { entry.fit.fit(); } catch { /* not measurable yet */ }
      // Replay scrollback once.
      if (!entry.loaded) {
        try {
          const r = await dispatch("shell.output", { session });
          const item = r.items?.[0];
          if (item?.ok && item.value.data) entry.term.write(b64decode(item.value.data));
          entry.loaded = true;
        } catch { /* ignore */ }
      }
      if (entry.term.cols > 0)
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

  const closeShell = async (session: string) => {
    await dispatch("shell.close", { session }).catch(() => {});
    const entry = st.current.terms.get(session);
    if (entry) { entry.term.dispose(); entry.el.remove(); }
    st.current.terms.delete(session);
    const list = await refreshList();
    if (st.current.active === session) {
      if (list.length > 0) attach(list[list.length - 1].session);
      else { setActive(""); st.current.active = ""; }
    }
  };

  const disconnect = () => {
    st.current.terms.forEach((e) => { e.term.dispose(); e.el.remove(); });
    st.current.ws?.close();
    st.current = { terms: new Map(), node: "", active: "" };
    setShells([]); setActive(""); setConnected(false);
  };

  // Refit the active terminal whenever the window frame (host) resizes.
  useEffect(() => {
    const host = areaRef.current;
    if (!host) return;
    const ro = new ResizeObserver(() => fitActive());
    ro.observe(host);
    window.addEventListener("resize", fitActive);
    return () => { ro.disconnect(); window.removeEventListener("resize", fitActive); };
  }, [fitActive]);

  useEffect(() => () => disconnect(), []); // cleanup on unmount

  return (
    <div className="flex flex-col gap-2 h-full">
      <div className="flex gap-2 items-center flex-wrap">
        <span className="mono-sm">交互 Shell（agent 侧常驻 · 可复用/多开）·</span>
        {!connected
          ? <button className="btn btn-accent" disabled={!node} onClick={connect}>连接</button>
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
        <div ref={areaRef} style={{ height: "100%", width: "100%", position: "relative" }} />
      </div>
    </div>
  );
}
