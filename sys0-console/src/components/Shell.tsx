import { useEffect, useRef, useState, useCallback } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { WSClient } from "../ws";
import { b64encode, b64decode, getUser } from "../api";
import { createThrottledSaver, loadRecent, saveRecent } from "../nodeWorkspace";
import { GenerationFence, SnapshotEventBuffer, trimChunkHistory } from "../workspaceLifecycle";

type ShellInfo = {
  session: string; name: string; shell: string;
  state: string; exit: number; cols: number; rows: number; started: number;
};
type ShellCache = { shells: ShellInfo[]; active: string; outputs: Record<string, string[]> };
type TermEntry = { term: XTerm; fit: FitAddon; el: HTMLDivElement; loaded: boolean };

export function Shell({ node, online }: { node: string; online: boolean }) {
  const account = useRef(getUser()).current;
  const recent = loadRecent<ShellCache>(localStorage, account, node, "shell");
  const [connected, setConnected] = useState(false);
  const [reconnect, setReconnect] = useState(0);
  const [shells, setShells] = useState<ShellInfo[]>(recent?.data.shells || []);
  const [active, setActive] = useState(recent?.data.active || "");
  const [savedAt, setSavedAt] = useState(recent?.savedAt || 0);
  const areaRef = useRef<HTMLDivElement>(null);
  const onlineRef = useRef(online);
  onlineRef.current = online;
  const shellsRef = useRef<ShellInfo[]>(recent?.data.shells || []);
  const outputsRef = useRef<Record<string, string[]>>(recent?.data.outputs || {});
  const termsRef = useRef(new Map<string, TermEntry>());
  const activeRef = useRef(recent?.data.active || "");
  const socketRef = useRef<WSClient>();
  const connectingRef = useRef<WSClient>();
  const fenceRef = useRef(new GenerationFence());
  const connectionGenerationRef = useRef(0);
  const snapshotsRef = useRef(new SnapshotEventBuffer());
  const hydratedRef = useRef(new Set<string>());
  const saverRef = useRef<ReturnType<typeof createThrottledSaver<ShellCache>>>();
  const reconnectTimerRef = useRef<number>();

  const cacheValue = (nextShells = shellsRef.current, nextActive = activeRef.current): ShellCache => ({
    shells: nextShells, active: nextActive, outputs: outputsRef.current,
  });
  if (!saverRef.current) {
    saverRef.current = createThrottledSaver((value: ShellCache) => {
      const now = Date.now();
      if (saveRecent(localStorage, account, node, "shell", value, now)) setSavedAt(now);
    });
  }
  const persistSoon = () => saverRef.current!.schedule(cacheValue());
  const persistNow = () => {
    const now = Date.now();
    if (saveRecent(localStorage, account, node, "shell", cacheValue(), now)) setSavedAt(now);
  };

  const currentSocket = () => socketRef.current;
  const dispatch = useCallback((method: string, params: any) => {
    const socket = currentSocket();
    if (!onlineRef.current || !socket) return Promise.reject(new Error("offline"));
    return socket.call("dispatch", { select: { nodes: [node] }, call: { method, params } });
  }, [node]);

  const fitActive = useCallback(() => {
    const entry = termsRef.current.get(activeRef.current);
    if (!entry || entry.el.style.display === "none") return;
    try {
      entry.fit.fit();
      if (onlineRef.current && entry.term.cols > 0 && entry.term.rows > 0)
        dispatch("shell.resize", { session: activeRef.current, cols: entry.term.cols, rows: entry.term.rows }).catch(() => {});
    } catch {}
  }, [dispatch]);

  const hydrateOutput = async (session: string, generation: number) => {
    if (hydratedRef.current.has(session) || !fenceRef.current.current(generation, onlineRef.current)) return;
    const snapshotGeneration = snapshotsRef.current.begin(session);
    try {
      const result = await dispatch("shell.output", { session });
      if (!fenceRef.current.current(generation, onlineRef.current)) return;
      const item = result.items?.[0];
      if (!item?.ok) return;
      const merged = snapshotsRef.current.complete(session, snapshotGeneration, { data: item.value.data || "", seq: item.value.seq || 0 });
      if (merged === null) { await hydrateOutput(session, generation); return; }
      const withoutSnapshotStream = { ...outputsRef.current };
      delete withoutSnapshotStream[session];
      outputsRef.current = trimChunkHistory(withoutSnapshotStream, session, merged);
      hydratedRef.current.add(session);
      persistSoon();
      const entry = termsRef.current.get(session);
      if (entry) {
        entry.term.clear();
        entry.term.reset();
        for (const chunk of outputsRef.current[session] || []) entry.term.write(b64decode(chunk));
        entry.loaded = true;
      }
    } catch {}
  };

  const refreshList = async (generation: number): Promise<ShellInfo[]> => {
    try {
      const result = await dispatch("shell.list", {});
      if (!fenceRef.current.current(generation, onlineRef.current)) return shellsRef.current;
      const item = result.items?.[0];
      if (!item?.ok) throw new Error("shell.list failed");
      const list: ShellInfo[] = (item.value.sessions || []).sort((a: ShellInfo, b: ShellInfo) => a.started - b.started);
      shellsRef.current = list;
      setShells(list);
      persistSoon();
      await Promise.allSettled(list.map((shell) => hydrateOutput(shell.session, generation)));
      return list;
    } catch {
      return shellsRef.current;
    }
  };

  const attach = (session: string) => {
    setActive(session);
    activeRef.current = session;
    persistNow();
    setTimeout(() => {
      const host = areaRef.current;
      if (!host) return;
      let entry = termsRef.current.get(session);
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
        term.open(el);
        entry = { term, fit, el, loaded: false };
        termsRef.current.set(session, entry);
        term.onData((data) => {
          if (onlineRef.current) dispatch("shell.input", { session, data: b64encode(new TextEncoder().encode(data)) }).catch(() => {});
        });
      }
      termsRef.current.forEach((other, id) => { other.el.style.display = id === session ? "block" : "none"; });
      if (!entry.loaded && hydratedRef.current.has(session)) {
        for (const chunk of outputsRef.current[session] || []) entry.term.write(b64decode(chunk));
        entry.loaded = true;
      } else if (!entry.loaded && !onlineRef.current) {
        for (const chunk of outputsRef.current[session] || []) entry.term.write(b64decode(chunk));
        entry.loaded = true;
      }
      try { entry.fit.fit(); } catch {}
      if (onlineRef.current && entry.term.cols > 0)
        dispatch("shell.resize", { session, cols: entry.term.cols, rows: entry.term.rows }).catch(() => {});
      entry.term.focus();
    }, 0);
  };

  const connect = async () => {
    if (!node || !onlineRef.current) return;
    const generation = fenceRef.current.begin();
    connectionGenerationRef.current = generation;
    connectingRef.current?.close();
    socketRef.current?.close();
    const socket = new WSClient();
    connectingRef.current = socket;
    socket.connect();
    socket.onClose(() => {
      if (connectingRef.current !== socket && socketRef.current !== socket) return;
      if (connectingRef.current === socket) connectingRef.current = undefined;
      if (socketRef.current === socket) socketRef.current = undefined;
      snapshotsRef.current.cancelAll();
      hydratedRef.current.clear();
      setConnected(false);
      reconnectTimerRef.current = window.setTimeout(() => {
        if (onlineRef.current) setReconnect((value) => value + 1);
      }, 1_000);
    });
    socket.on("event.shell", (event: any) => {
      if (!fenceRef.current.current(generation, onlineRef.current) || event.node !== node) return;
      if (event.chunk) {
        const status = snapshotsRef.current.event(event.session, event.chunk, event.seq);
        if (status === "gap") {
          hydratedRef.current.delete(event.session);
          const entry = termsRef.current.get(event.session);
          if (entry?.loaded) { entry.term.clear(); entry.term.reset(); entry.loaded = false; }
          void hydrateOutput(event.session, generation);
        } else if (status === "accept") {
          outputsRef.current = trimChunkHistory(outputsRef.current, event.session, event.chunk);
          persistSoon();
          const entry = termsRef.current.get(event.session);
          if (entry?.loaded) entry.term.write(b64decode(event.chunk));
        } else if (status === "duplicate") {
          // Already represented by the accepted snapshot/event high-water mark.
        }
      }
      if (event.closed || event.exited) {
        const entry = termsRef.current.get(event.session);
        if (entry) entry.term.writeln(`\r\n\x1b[33m[${event.closed ? "session closed" : "process exited"}]\x1b[0m`);
        if (event.closed) refreshList(generation);
      }
    });
    try {
      await socket.call("hub.subscribe", { topics: ["shell"] });
      if (!fenceRef.current.current(generation, onlineRef.current) || connectingRef.current !== socket) {
        socket.close();
        return;
      }
      connectingRef.current = undefined;
      socketRef.current = socket;
      setConnected(true);
      const list = await refreshList(generation);
      if (!fenceRef.current.current(generation, onlineRef.current)) return;
      const preferred = list.find((shell) => shell.session === activeRef.current)?.session || list[0]?.session;
      if (preferred) attach(preferred);
      else await newShell(generation);
    } catch {
      socket.close();
      if (connectingRef.current === socket) connectingRef.current = undefined;
    }
  };

  const newShell = async (generation?: number) => {
    if (!onlineRef.current) return;
    const result = await dispatch("shell.open", { cols: 100, rows: 30 });
    if (!onlineRef.current || (generation !== undefined && !fenceRef.current.current(generation, true))) return;
    const item = result.items?.[0];
    if (!item?.ok) return;
    const currentGeneration = generation ?? connectionGenerationRef.current;
    const list = await refreshList(currentGeneration);
    if (list.some((shell) => shell.session === item.value.session)) attach(item.value.session);
  };

  const closeShell = async (session: string) => {
    if (!onlineRef.current) return;
    await dispatch("shell.close", { session }).catch(() => {});
    if (!onlineRef.current) return;
    const entry = termsRef.current.get(session);
    if (entry) { entry.term.dispose(); entry.el.remove(); }
    termsRef.current.delete(session);
    delete outputsRef.current[session];
    hydratedRef.current.delete(session);
    const list = await refreshList(connectionGenerationRef.current);
    if (activeRef.current === session) {
      const next = list[list.length - 1]?.session || "";
      if (next) attach(next); else { setActive(""); activeRef.current = ""; persistNow(); }
    }
  };

  const disconnect = (clearState = true) => {
    fenceRef.current.invalidate();
    if (reconnectTimerRef.current !== undefined) window.clearTimeout(reconnectTimerRef.current);
    reconnectTimerRef.current = undefined;
    snapshotsRef.current.cancelAll();
    const connecting = connectingRef.current;
    const socket = socketRef.current;
    connectingRef.current = undefined;
    socketRef.current = undefined;
    connecting?.close();
    if (socket && socket !== connecting) socket.close();
    termsRef.current.forEach((entry) => { entry.term.dispose(); entry.el.remove(); });
    termsRef.current.clear();
    hydratedRef.current.clear();
    if (clearState) { setShells([]); setActive(""); activeRef.current = ""; }
    setConnected(false);
  };

  useEffect(() => {
    const host = areaRef.current;
    if (!host) return;
    const observer = new ResizeObserver(fitActive);
    observer.observe(host);
    window.addEventListener("resize", fitActive);
    return () => { observer.disconnect(); window.removeEventListener("resize", fitActive); };
  }, [fitActive]);

  useEffect(() => {
    if (!online) {
      saverRef.current!.flush();
      const id = activeRef.current || shellsRef.current[0]?.session;
      if (id) attach(id);
      return;
    }
    connect();
    return () => disconnect(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online, node, reconnect]);

  useEffect(() => () => saverRef.current?.dispose(), []);

  return (
    <div className="flex flex-col gap-2 h-full">
      <div className="flex gap-2 items-center flex-wrap">
        <span className="mono-sm">交互 Shell ·</span>
        {!connected
          ? <button className="btn btn-accent" disabled={!node || !online} onClick={connect}>连接</button>
          : <button className="btn" style={{ color: "var(--danger)" }} onClick={() => disconnect()}>断开（保留会话）</button>}
        <span className="mono-sm">{connected ? "● 已连接" : online ? "○ 未连接" : "○ 节点离线"}</span>
      </div>
      {shells.length > 0 && (
        <div className="flex gap-1 items-center" style={{ flexWrap: "wrap" }}>
          {shells.map((shell) => (
            <span key={shell.session} className="mono-sm" style={{
              display: "inline-flex", alignItems: "center", gap: 6, padding: "3px 8px", borderRadius: 6,
              cursor: "pointer", border: "1px solid var(--border)",
              background: active === shell.session ? "var(--accent)" : "transparent",
              color: active === shell.session ? "#0a0e0f" : "var(--fg)", opacity: shell.state === "running" ? 1 : 0.55,
            }} onClick={() => attach(shell.session)}>
              {shell.name || shell.shell}{shell.state !== "running" ? " (exited)" : ""}
              {online && <span style={{ opacity: 0.7 }} title="关闭会话" onClick={(event) => { event.stopPropagation(); closeShell(shell.session); }}>✕</span>}
            </span>
          ))}
          <button className="btn" disabled={!online} style={{ padding: "2px 10px" }} onClick={() => newShell()} title="新建 shell">＋</button>
        </div>
      )}
      {!online && <div className="mono-sm" style={{ color: "var(--muted)" }}>{savedAt ? `记录于 ${new Date(savedAt).toLocaleString()}` : "暂无历史输出"}</div>}
      <div className="panel" style={{ flex: 1, padding: 8, minHeight: 0 }}>
        <div ref={areaRef} style={{ height: "100%", width: "100%", position: "relative" }} />
      </div>
    </div>
  );
}
