import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { WSClient } from "../ws";
import { b64encode, b64decode, type Node } from "../api";

// Interactive PTY shell on a single node, streamed live (pass-through to the
// agent's system shell), GhostJ-style.
export function Shell({ nodes, primary }: { nodes: Node[]; primary: string }) {
  const [node, setNode] = useState(primary);
  const [connected, setConnected] = useState(false);
  const termRef = useRef<HTMLDivElement>(null);
  const stateRef = useRef<{ ws?: WSClient; term?: XTerm; session?: string; fit?: FitAddon }>({});

  useEffect(() => setNode(primary || nodes[0]?.id || ""), [primary, nodes.length]);

  const start = async () => {
    if (!node) return;
    const term = new XTerm({
      fontFamily: '"JetBrains Mono", monospace', fontSize: 13,
      theme: { background: "#0a0e0f", foreground: "#c8d3d6", cursor: "#38e07b" },
      cursorBlink: true,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(termRef.current!);
    fit.fit();

    const ws = new WSClient();
    ws.connect();
    await ws.call("hub.subscribe", { topics: ["shell"] });

    const r = await ws.call("dispatch", {
      select: { nodes: [node] },
      call: { method: "shell.open", params: { cols: term.cols, rows: term.rows } },
    });
    const item = r.items?.[0];
    if (!item?.ok) { term.writeln("\x1b[31mfailed to open shell: " + (item?.error?.message || "?") + "\x1b[0m"); return; }
    const session = item.value.session;

    ws.on("event.shell", (p: any) => {
      if (p.node !== node || p.session !== session) return;
      if (p.closed) { term.writeln("\r\n\x1b[33m[session closed]\x1b[0m"); setConnected(false); return; }
      if (p.chunk) term.write(b64decode(p.chunk));
    });

    term.onData((d) => {
      ws.call("dispatch", {
        select: { nodes: [node] },
        call: { method: "shell.input", params: { session, data: b64encode(new TextEncoder().encode(d)) } },
      });
    });

    const onResize = () => {
      fit.fit();
      ws.call("dispatch", {
        select: { nodes: [node] },
        call: { method: "shell.resize", params: { session, cols: term.cols, rows: term.rows } },
      });
    };
    window.addEventListener("resize", onResize);

    stateRef.current = { ws, term, session, fit };
    setConnected(true);
    term.focus();
  };

  const stop = () => {
    const s = stateRef.current;
    if (s.ws && s.session) {
      s.ws.call("dispatch", { select: { nodes: [node] }, call: { method: "shell.close", params: { session: s.session } } });
    }
    s.term?.dispose();
    s.ws?.close();
    stateRef.current = {};
    setConnected(false);
  };

  useEffect(() => () => stop(), []); // cleanup on unmount

  return (
    <div className="flex flex-col gap-2 h-full">
      <div className="flex gap-2 items-center">
        <span className="mono-sm">交互 Shell（PTY 透传系统控制台）·</span>
        <select className="input" style={{ width: 200 }} value={node} disabled={connected}
          onChange={(e) => setNode(e.target.value)}>
          {nodes.map((n) => <option key={n.id} value={n.id}>{n.label} · {n.id}</option>)}
        </select>
        {!connected
          ? <button className="btn btn-accent" disabled={!node} onClick={start}>连接</button>
          : <button className="btn" style={{ color: "var(--danger)" }} onClick={stop}>断开</button>}
        <span className="mono-sm">{connected ? "● 已连接" : "○ 未连接"}</span>
      </div>
      <div className="panel" style={{ flex: 1, padding: 8, minHeight: 0 }}>
        <div ref={termRef} style={{ height: "100%", width: "100%" }} />
      </div>
    </div>
  );
}
