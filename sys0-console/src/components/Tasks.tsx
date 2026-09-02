import { useEffect, useRef, useState, useCallback } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { api, getUser, b64encode, b64decode } from "../api";
import { WSClient } from "../ws";
import { confirmDialog } from "./dialogs";
import { createThrottledSaver, loadRecent, saveRecent } from "../nodeWorkspace";
import { GenerationFence, SnapshotEventBuffer, trimChunkHistory } from "../workspaceLifecycle";

type TaskCache = { tasks: any[]; outputs: Record<string, string[]> };

export function Tasks({ node, online }: { node: string; online: boolean }) {
  const account = useRef(getUser()).current;
  const recent = loadRecent<TaskCache>(localStorage, account, node, "tasks");
  const [tasks, setTasks] = useState<any[]>(recent?.data.tasks || []);
  const [savedAt, setSavedAt] = useState(recent?.savedAt || 0);
  const [reconnect, setReconnect] = useState(0);
  const [sel, setSel] = useState("");
  const [name, setName] = useState("");
  const [cmd, setCmd] = useState("");
  const termHost = useRef<HTMLDivElement>(null);
  const term = useRef<XTerm>();
  const fit = useRef<FitAddon>();
  const ws = useRef<WSClient>();
  const onlineRef = useRef(online);
  onlineRef.current = online;
  const selRef = useRef(sel);
  const tasksRef = useRef<any[]>(recent?.data.tasks || []);
  const outputsRef = useRef<Record<string, string[]>>(recent?.data.outputs || {});
  const fenceRef = useRef(new GenerationFence());
  const connectionGenerationRef = useRef(0);
  const snapshotsRef = useRef(new SnapshotEventBuffer());
  const hydratedRef = useRef(new Set<string>());
  const saverRef = useRef<ReturnType<typeof createThrottledSaver<TaskCache>>>();

  if (!saverRef.current) {
    saverRef.current = createThrottledSaver((value: TaskCache) => {
      const now = Date.now();
      if (saveRecent(localStorage, account, node, "tasks", value, now)) setSavedAt(now);
    });
  }
  const cacheValue = (): TaskCache => ({ tasks: tasksRef.current, outputs: outputsRef.current });
  const persistSoon = () => saverRef.current!.schedule(cacheValue());
  const persistNow = () => {
    const now = Date.now();
    if (saveRecent(localStorage, account, node, "tasks", cacheValue(), now)) setSavedAt(now);
  };

  useEffect(() => { selRef.current = sel; }, [sel]);

  const fitTerm = useCallback(() => {
    const currentFit = fit.current, currentTerm = term.current;
    if (!currentFit || !currentTerm) return;
    try {
      currentFit.fit();
      if (onlineRef.current && selRef.current && ws.current && currentTerm.cols > 0 && currentTerm.rows > 0)
        ws.current.call("dispatch", { select: { nodes: [node] }, call: { method: "task.resize", params: { task: selRef.current, cols: currentTerm.cols, rows: currentTerm.rows } } }).catch(() => {});
    } catch {}
  }, [node]);

  useEffect(() => {
    const currentTerm = new XTerm({
      fontFamily: '"JetBrains Mono", monospace', fontSize: 13,
      theme: { background: "#0a0e0f", foreground: "#c8d3d6", cursor: "#38e07b" },
      cursorBlink: true, convertEol: false,
    });
    const currentFit = new FitAddon();
    currentTerm.loadAddon(currentFit);
    const openTimer = setTimeout(() => {
      if (!termHost.current) return;
      currentTerm.open(termHost.current);
      currentFit.fit();
      currentTerm.focus();
    }, 0);
    currentTerm.onData((data) => {
      const id = selRef.current, socket = ws.current;
      if (!onlineRef.current || !id || !socket) return;
      socket.call("dispatch", {
        select: { nodes: [node] },
        call: { method: "task.input", params: { task: id, data: b64encode(new TextEncoder().encode(data)) } },
      }).catch(() => {});
    });
    term.current = currentTerm;
    fit.current = currentFit;
    const observer = new ResizeObserver(fitTerm);
    if (termHost.current) observer.observe(termHost.current);
    window.addEventListener("resize", fitTerm);
    return () => { clearTimeout(openTimer); observer.disconnect(); window.removeEventListener("resize", fitTerm); currentTerm.dispose(); };
  }, [fitTerm, node]);

  const hydrateTask = useCallback(async (id: string, generation: number): Promise<void> => {
    if (hydratedRef.current.has(id) || !fenceRef.current.current(generation, onlineRef.current)) return;
    const snapshotGeneration = snapshotsRef.current.begin(id);
    try {
      const value = await api.one(node, "task.output", { task: id });
      if (!fenceRef.current.current(generation, onlineRef.current)) return;
      const merged = snapshotsRef.current.complete(id, snapshotGeneration, { data: value.data || "", seq: value.seq || 0 });
      if (merged === null) { await hydrateTask(id, generation); return; }
      const withoutSnapshotStream = { ...outputsRef.current };
      delete withoutSnapshotStream[id];
      outputsRef.current = trimChunkHistory(withoutSnapshotStream, id, merged);
      hydratedRef.current.add(id);
      persistSoon();
      if (selRef.current === id) {
        term.current?.clear(); term.current?.reset();
        for (const chunk of outputsRef.current[id] || []) term.current?.write(b64decode(chunk));
        if (value.state === "exited") term.current?.write(`\r\n\x1b[33m[exited code ${value.exit}]\x1b[0m\r\n`);
      }
    } catch {}
  }, [node]);

  const refresh = useCallback(async () => {
    const generation = connectionGenerationRef.current;
    if (!node || !fenceRef.current.current(generation, onlineRef.current)) return;
    try {
      const value = await api.one(node, "task.list");
      if (!fenceRef.current.current(generation, onlineRef.current)) return;
      const next = (value.tasks || []).sort((a: any, b: any) => b.started - a.started);
      tasksRef.current = next;
      setTasks(next);
      persistSoon();
      await Promise.allSettled(next.map((task: any) => hydrateTask(task.id, generation)));
    } catch {}
  }, [hydrateTask, node]);

  useEffect(() => {
    if (!online) {
      saverRef.current!.flush();
      return;
    }
    const generation = fenceRef.current.begin();
    connectionGenerationRef.current = generation;
    const socket = new WSClient();
    ws.current?.close();
    ws.current = socket;
    let disposed = false;
    socket.connect();
    socket.onClose(() => {
      if (ws.current !== socket) return;
      ws.current = undefined;
      snapshotsRef.current.cancelAll();
      hydratedRef.current.clear();
      setTimeout(() => { if (!disposed && onlineRef.current) setReconnect((value) => value + 1); }, 1_000);
    });
    socket.on("event.task", (event: any) => {
      if (!fenceRef.current.current(generation, onlineRef.current) || event.node !== node) return;
      if (event.chunk) {
        const status = snapshotsRef.current.event(event.task, event.chunk, event.seq);
        if (status === "gap") {
          hydratedRef.current.delete(event.task);
          if (event.task === selRef.current) { term.current?.clear(); term.current?.reset(); }
          void hydrateTask(event.task, generation);
        } else if (status === "accept") {
          outputsRef.current = trimChunkHistory(outputsRef.current, event.task, event.chunk);
          persistSoon();
          if (event.task === selRef.current && hydratedRef.current.has(event.task)) term.current?.write(b64decode(event.chunk));
        } else if (status === "duplicate") {
          // Already represented by the accepted snapshot/event high-water mark.
        }
      }
      if (event.exited) {
        if (event.task === selRef.current) term.current?.write(`\r\n\x1b[33m[exited code ${event.code}]\x1b[0m\r\n`);
        refresh();
      }
    });
    let subscribed = false;
    socket.call("hub.subscribe", { topics: ["task"] }).then(() => {
      if (fenceRef.current.current(generation, onlineRef.current) && ws.current === socket) {
        subscribed = true;
        refresh();
      } else socket.close();
    }).catch(() => socket.close());
    const timer = setInterval(() => { if (subscribed) refresh(); }, 2_000);
    return () => {
      disposed = true;
      clearInterval(timer);
      fenceRef.current.invalidate();
      snapshotsRef.current.cancelAll();
      if (ws.current === socket) ws.current = undefined;
      socket.close();
      hydratedRef.current.clear();
    };
  }, [online, node, refresh, reconnect]);

  useEffect(() => () => saverRef.current?.dispose(), []);

  const selectTask = async (id: string) => {
    setSel(id);
    selRef.current = id;
    term.current?.clear(); term.current?.reset();
    fitTerm();
    if (!onlineRef.current || hydratedRef.current.has(id)) {
      for (const chunk of outputsRef.current[id] || []) term.current?.write(b64decode(chunk));
    } else {
      await hydrateTask(id, connectionGenerationRef.current);
    }
    term.current?.focus();
  };

  const start = async () => {
    const socket = ws.current;
    if (!cmd.trim() || !node || !onlineRef.current || !socket) return;
    const cols = term.current?.cols || 100, rows = term.current?.rows || 30;
    const result = await socket.call("dispatch", {
      select: { nodes: [node] }, call: { method: "task.start", params: { name: name || cmd, cmd, cols, rows } },
    });
    if (!onlineRef.current || ws.current !== socket) return;
    const id = result.items?.[0]?.value?.task;
    if (id) { setCmd(""); setName(""); await refresh(); if (onlineRef.current) selectTask(id); }
  };

  const manage = async (method: string, params: any = {}) => {
    if (!onlineRef.current) return;
    await api.one(node, method, params).catch(() => {});
    if (onlineRef.current) refresh();
  };
  const remove = async (id: string) => {
    if (!onlineRef.current) return;
    if (!(await confirmDialog("移除任务 " + id + "?", { title: "移除任务", danger: true })) || !onlineRef.current) return;
    await api.one(node, "task.remove", { task: id }).catch(() => {});
    if (!onlineRef.current) return;
    delete outputsRef.current[id]; hydratedRef.current.delete(id); persistNow();
    if (selRef.current === id) { setSel(""); selRef.current = ""; term.current?.reset(); }
    refresh();
  };

  const current = tasks.find((task) => task.id === sel);

  return (
    <div className="task-split flex gap-3 h-full min-h-0">
      <div className="task-aside w-[280px] flex flex-col gap-2 min-h-0">
        <div className="panel p-2 space-y-2">
          <input className="input" placeholder="任务名 (可选)" value={name} disabled={!online} onChange={(e) => setName(e.target.value)} />
          <input className="input" placeholder="命令，如 top / ping 1.1.1.1" value={cmd} disabled={!online}
            onChange={(e) => setCmd(e.target.value)} onKeyDown={(e) => e.key === "Enter" && start()} />
          <button className="btn btn-accent w-full justify-center" disabled={!cmd.trim() || !online} onClick={start}>拉起进程</button>
        </div>
        <div className="panel flex-1 overflow-auto">
          <div className="mono-sm px-3 py-1.5" style={{ borderBottom: "1px solid var(--border)" }}>
            托管进程 · {tasks.filter((task) => task.state === "running").length} 运行 / {tasks.length} 总
          </div>
          {!online && <div className="mono-sm px-3 py-1.5" style={{ color: "var(--muted)" }}>{savedAt ? `记录于 ${new Date(savedAt).toLocaleString()}` : "暂无历史数据"}</div>}
          {tasks.length === 0 && <div className="mono-sm px-3 py-3">无托管进程</div>}
          {tasks.map((task) => (
            <div key={task.id} onClick={() => selectTask(task.id)} className="px-3 py-2 cursor-pointer"
              style={{ borderBottom: "1px solid var(--border)", ...(sel === task.id ? { background: "var(--panel-2)" } : {}) }}>
              <div className="flex items-center gap-2">
                <span className="dot" style={{ background: task.state === "running" ? "var(--accent)" : "var(--muted)" }} />
                <span style={{ color: sel === task.id ? "var(--accent)" : "var(--fg)" }}>{task.name}</span>
                <span className="mono-sm ml-auto">{task.state === "running" ? "pid " + task.pid : "exit " + task.exit}</span>
              </div>
              <div className="mono-sm mt-0.5" style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{task.cmd}</div>
            </div>
          ))}
        </div>
      </div>
      <div className="flex-1 flex flex-col gap-2 min-h-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="mono-sm">{current ? `${current.name} · ${current.state}${current.state === "running" ? " · pid " + current.pid : " · exit " + current.exit}` : "选择或拉起一个进程"}</span>
          {current && current.state === "running" && <>
            <button className="btn" style={{ color: "var(--warn)" }} disabled={!online} onClick={() => manage("task.signal", { task: current.id, sig: "TERM" })}>停止</button>
            <button className="btn" style={{ color: "var(--danger)" }} disabled={!online} onClick={() => manage("task.signal", { task: current.id, sig: "KILL" })}>强杀</button>
          </>}
          {current && <button className="btn" disabled={!online} onClick={() => manage("task.restart", { task: current.id }).then(() => { if (onlineRef.current) selectTask(current.id); })}>重启</button>}
          {current && <button className="btn" style={{ color: "var(--danger)" }} disabled={!online} onClick={() => remove(current.id)}>移除</button>}
        </div>
        <div className="panel" style={{ flex: 1, padding: 8, minHeight: 0 }} onMouseDown={() => term.current?.focus()}>
          <div ref={termHost} style={{ height: "100%", width: "100%" }} />
        </div>
        <div className="mono-sm">{online ? "点击终端即可交互，支持 ANSI / TUI" : "显示最近保存的输出"}</div>
      </div>
    </div>
  );
}
