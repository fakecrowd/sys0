import { useEffect, useRef, useState, useCallback } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { api, b64encode, b64decode } from "../api";
import { WSClient } from "../ws";
import { confirmDialog } from "./dialogs";

// Managed processes: launch & supervise long-running child processes on the
// FOCUSED node. Output is a real PTY rendered with xterm (ANSI colors), fully
// interactive, with history for both running and past (exited) processes.
// The node is fixed by the workspace — no in-window node picker. A
// ResizeObserver refits the terminal whenever the window frame changes size.
export function Tasks({ node }: { node: string }) {
  const [tasks, setTasks] = useState<any[]>([]);
  const [sel, setSel] = useState<string>("");
  const [name, setName] = useState("");
  const [cmd, setCmd] = useState("");

  const termHost = useRef<HTMLDivElement>(null);
  const term = useRef<XTerm>();
  const fit = useRef<FitAddon>();
  const ws = useRef<WSClient>();
  const selRef = useRef(sel);
  useEffect(() => { selRef.current = sel; }, [sel]);

  const fitTerm = useCallback(() => {
    const f = fit.current, t = term.current;
    if (!f || !t) return;
    try {
      f.fit();
      if (selRef.current && ws.current && t.cols > 0 && t.rows > 0)
        ws.current.call("dispatch", { select: { nodes: [node] }, call: { method: "task.resize", params: { task: selRef.current, cols: t.cols, rows: t.rows } } });
    } catch { /* not measurable yet */ }
  }, [node]);

  // one xterm + one WS for the whole window
  useEffect(() => {
    const t = new XTerm({
      fontFamily: '"JetBrains Mono", monospace', fontSize: 13,
      theme: { background: "#0a0e0f", foreground: "#c8d3d6", cursor: "#38e07b" },
      cursorBlink: true, convertEol: false,
    });
    const f = new FitAddon();
    t.loadAddon(f);
    t.open(termHost.current!);
    f.fit();
    t.onData((d) => {
      const id = selRef.current;
      if (!id || !ws.current) return;
      ws.current.call("dispatch", {
        select: { nodes: [node] },
        call: { method: "task.input", params: { task: id, data: b64encode(new TextEncoder().encode(d)) } },
      });
    });
    term.current = t;
    fit.current = f;
    const ro = new ResizeObserver(() => fitTerm());
    if (termHost.current) ro.observe(termHost.current);
    window.addEventListener("resize", fitTerm);
    return () => { ro.disconnect(); window.removeEventListener("resize", fitTerm); t.dispose(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // connect WS once, subscribe task stream
  useEffect(() => {
    const c = new WSClient();
    c.connect();
    c.call("hub.subscribe", { topics: ["task"] });
    c.on("event.task", (p: any) => {
      if (p.node !== node || p.task !== selRef.current) return;
      if (p.chunk) term.current?.write(b64decode(p.chunk));
      if (p.exited) { term.current?.write(`\r\n\x1b[33m[exited code ${p.code}]\x1b[0m\r\n`); refresh(); }
    });
    ws.current = c;
    return () => c.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const refresh = useCallback(async () => {
    if (!node) return;
    try { const v = await api.one(node, "task.list"); setTasks((v.tasks || []).sort((a: any, b: any) => b.started - a.started)); }
    catch { setTasks([]); }
  }, [node]);
  useEffect(() => { refresh(); const t = setInterval(refresh, 2000); return () => clearInterval(t); }, [refresh]);

  const selectTask = async (id: string) => {
    setSel(id);
    selRef.current = id;
    term.current?.clear();
    term.current?.reset();
    fitTerm();
    try {
      const v = await api.one(node, "task.output", { task: id });
      if (v.data) term.current?.write(b64decode(v.data));
      if (v.state === "exited") term.current?.write(`\r\n\x1b[33m[exited code ${v.exit}]\x1b[0m\r\n`);
    } catch {}
    term.current?.focus();
  };

  const start = async () => {
    if (!cmd.trim() || !node || !ws.current) return;
    const cols = term.current?.cols || 100, rows = term.current?.rows || 30;
    const r = await ws.current.call("dispatch", {
      select: { nodes: [node] },
      call: { method: "task.start", params: { name: name || cmd, cmd, cols, rows } },
    });
    const id = r.items?.[0]?.value?.task;
    if (id) { setCmd(""); setName(""); await refresh(); selectTask(id); }
  };

  const manage = async (method: string, params: any = {}) => {
    await api.one(node, method, params).catch(() => {});
    refresh();
  };
  const remove = async (id: string) => {
    if (!(await confirmDialog("移除任务 " + id + "?", { title: "移除任务", danger: true }))) return;
    await api.one(node, "task.remove", { task: id }).catch(() => {});
    if (sel === id) { setSel(""); term.current?.reset(); }
    refresh();
  };

  const current = tasks.find((t) => t.id === sel);

  return (
    <div className="task-split flex gap-3 h-full min-h-0">
      <div className="task-aside w-[280px] flex flex-col gap-2 min-h-0">
        <div className="panel p-2 space-y-2">
          <input className="input" placeholder="任务名 (可选)" value={name} onChange={(e) => setName(e.target.value)} />
          <input className="input" placeholder="命令，如 top / ping 1.1.1.1" value={cmd}
            onChange={(e) => setCmd(e.target.value)} onKeyDown={(e) => e.key === "Enter" && start()} />
          <button className="btn btn-accent w-full justify-center" disabled={!cmd.trim()} onClick={start}>拉起进程</button>
        </div>
        <div className="panel flex-1 overflow-auto">
          <div className="mono-sm px-3 py-1.5" style={{ borderBottom: "1px solid var(--border)" }}>
            托管进程 · {tasks.filter((t) => t.state === "running").length} 运行 / {tasks.length} 总
          </div>
          {tasks.length === 0 && <div className="mono-sm px-3 py-3">无托管进程</div>}
          {tasks.map((t) => (
            <div key={t.id} onClick={() => selectTask(t.id)} className="px-3 py-2 cursor-pointer"
              style={{ borderBottom: "1px solid var(--border)", ...(sel === t.id ? { background: "var(--panel-2)" } : {}) }}>
              <div className="flex items-center gap-2">
                <span className="dot" style={{ background: t.state === "running" ? "var(--accent)" : "var(--muted)" }} />
                <span style={{ color: sel === t.id ? "var(--accent)" : "var(--fg)" }}>{t.name}</span>
                <span className="mono-sm ml-auto">{t.state === "running" ? "pid " + t.pid : "exit " + t.exit}</span>
              </div>
              <div className="mono-sm mt-0.5" style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{t.cmd}</div>
            </div>
          ))}
        </div>
      </div>

      <div className="flex-1 flex flex-col gap-2 min-h-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="mono-sm">{current ? `${current.name} · ${current.state}${current.state === "running" ? " · pid " + current.pid : " · exit " + current.exit}` : "选择或拉起一个进程"}</span>
          {current && current.state === "running" && (
            <>
              <button className="btn" style={{ color: "var(--warn)" }} onClick={() => manage("task.signal", { task: current.id, sig: "TERM" })}>停止</button>
              <button className="btn" style={{ color: "var(--danger)" }} onClick={() => manage("task.signal", { task: current.id, sig: "KILL" })}>强杀</button>
            </>
          )}
          {current && <button className="btn" onClick={() => manage("task.restart", { task: current.id }).then(() => selectTask(current.id))}>重启</button>}
          {current && <button className="btn" style={{ color: "var(--danger)" }} onClick={() => remove(current.id)}>移除</button>}
        </div>
        <div className="panel" style={{ flex: 1, padding: 8, minHeight: 0 }}>
          <div ref={termHost} style={{ height: "100%", width: "100%" }} />
        </div>
        <div className="mono-sm">点击上方终端可直接交互（stdin 透传到进程，支持 ANSI / TUI）</div>
      </div>
    </div>
  );
}
