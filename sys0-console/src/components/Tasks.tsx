import { useEffect, useRef, useState, useCallback } from "react";
import { api, b64encode, b64decode, type Node } from "../api";
import { WSClient } from "../ws";
import { confirmDialog } from "./dialogs";

// Managed processes: launch & supervise long-running child processes on a node,
// view live output and interact (stdin). Replaces the terminal's run-command.
export function Tasks({ nodes, primary }: { nodes: Node[]; primary: string }) {
  const [node, setNode] = useState(primary);
  const [tasks, setTasks] = useState<any[]>([]);
  const [sel, setSel] = useState<string>("");
  const [name, setName] = useState("");
  const [cmd, setCmd] = useState("");
  const [input, setInput] = useState("");
  const [out, setOut] = useState("");
  const outRef = useRef<HTMLPreElement>(null);
  const wsRef = useRef<WSClient>();

  useEffect(() => setNode(primary || nodes[0]?.id || ""), [primary, nodes.length]);

  // single WS for live task output + low-latency input
  useEffect(() => {
    const ws = new WSClient();
    ws.connect();
    ws.call("hub.subscribe", { topics: ["task"] });
    ws.on("event.task", (p: any) => {
      if (p.node !== node || p.task !== selRef.current) return;
      if (p.chunk) appendOut(new TextDecoder().decode(b64decode(p.chunk)));
      if (p.exited) { appendOut(`\n[exited code ${p.code}]\n`); refresh(); }
    });
    wsRef.current = ws;
    return () => ws.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node]);

  const selRef = useRef(sel);
  useEffect(() => { selRef.current = sel; }, [sel]);

  const appendOut = (s: string) => setOut((o) => (o + s).slice(-20000));
  useEffect(() => { if (outRef.current) outRef.current.scrollTop = outRef.current.scrollHeight; }, [out]);

  const refresh = useCallback(async () => {
    if (!node) return;
    try { const v = await api.one(node, "task.list"); setTasks((v.tasks || []).sort((a: any, b: any) => b.started - a.started)); }
    catch { setTasks([]); }
  }, [node]);

  useEffect(() => { refresh(); const t = setInterval(refresh, 2000); return () => clearInterval(t); }, [refresh]);

  const start = async () => {
    if (!cmd.trim() || !node) return;
    const r = await wsRef.current!.call("dispatch", {
      select: { nodes: [node] },
      call: { method: "task.start", params: { name: name || cmd, cmd } },
    });
    const id = r.items?.[0]?.value?.task;
    if (id) { setOut(""); setSel(id); setCmd(""); setName(""); refresh(); }
  };

  const send = async () => {
    if (!sel) return;
    await wsRef.current!.call("dispatch", {
      select: { nodes: [node] },
      call: { method: "task.input", params: { task: sel, data: b64encode(new TextEncoder().encode(input + "\n")) } },
    });
    appendOut(input + "\n");
    setInput("");
  };

  const signal = async (sig: string) => {
    if (sel) await api.one(node, "task.signal", { task: sel, sig }).catch(() => {});
    refresh();
  };
  const remove = async (id: string) => {
    if (!(await confirmDialog("移除任务 " + id + "?", { title: "移除任务", danger: true }))) return;
    await api.one(node, "task.remove", { task: id }).catch(() => {});
    if (sel === id) { setSel(""); setOut(""); }
    refresh();
  };

  const selectTask = (id: string) => { setSel(id); setOut(""); };
  const current = tasks.find((t) => t.id === sel);

  return (
    <div className="flex gap-3 h-full min-h-0">
      <div className="w-[280px] flex flex-col gap-2 min-h-0">
        <select className="input" value={node} onChange={(e) => setNode(e.target.value)}>
          {nodes.filter((n) => n.state !== "offline").map((n) => <option key={n.id} value={n.id}>{n.label} · {n.id}</option>)}
        </select>
        <div className="panel p-2 space-y-2">
          <input className="input" placeholder="任务名 (可选)" value={name} onChange={(e) => setName(e.target.value)} />
          <input className="input" placeholder="命令，如 ping 1.1.1.1" value={cmd}
            onChange={(e) => setCmd(e.target.value)} onKeyDown={(e) => e.key === "Enter" && start()} />
          <button className="btn btn-accent w-full justify-center" disabled={!cmd.trim()} onClick={start}>拉起进程</button>
        </div>
        <div className="panel flex-1 overflow-auto">
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
        <div className="flex items-center gap-2">
          <span className="mono-sm">{current ? `${current.name} · ${current.state}` : "选择或拉起一个进程"}</span>
          {current && current.state === "running" && (
            <>
              <button className="btn" style={{ color: "var(--warn)" }} onClick={() => signal("TERM")}>停止</button>
              <button className="btn" style={{ color: "var(--danger)" }} onClick={() => signal("KILL")}>强杀</button>
            </>
          )}
          {current && <button className="btn" style={{ color: "var(--danger)" }} onClick={() => remove(current.id)}>移除</button>}
        </div>
        <pre ref={outRef} className="panel flex-1 overflow-auto" style={{ margin: 0, padding: 10, whiteSpace: "pre-wrap" }}>{out}</pre>
        <div className="flex gap-2">
          <span className="mono-sm" style={{ color: "var(--accent)", paddingTop: 8 }}>›</span>
          <input className="input" placeholder="向进程 stdin 发送一行 (回车)" value={input} disabled={!current || current.state !== "running"}
            onChange={(e) => setInput(e.target.value)} onKeyDown={(e) => e.key === "Enter" && send()} />
          <button className="btn" disabled={!current || current.state !== "running"} onClick={send}>发送</button>
        </div>
      </div>
    </div>
  );
}
