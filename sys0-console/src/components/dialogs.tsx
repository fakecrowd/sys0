import { useEffect, useState } from "react";

// shadcn-style modal dialogs with an imperative API so they drop-in replace the
// native confirm()/prompt()/alert(). Render <Dialogs/> once at the app root.

type Req =
  | { kind: "confirm"; title: string; message: string; danger?: boolean; okText?: string; resolve: (v: boolean) => void }
  | { kind: "prompt"; title: string; message?: string; def?: string; resolve: (v: string | null) => void }
  | { kind: "alert"; title: string; message: string; resolve: () => void };

let push: (r: Req) => void = () => {};

export function confirmDialog(message: string, opts?: { title?: string; danger?: boolean; okText?: string }): Promise<boolean> {
  return new Promise((resolve) =>
    push({ kind: "confirm", title: opts?.title || "确认操作", message, danger: opts?.danger, okText: opts?.okText, resolve })
  );
}
export function promptDialog(title: string, def = "", message?: string): Promise<string | null> {
  return new Promise((resolve) => push({ kind: "prompt", title, def, message, resolve }));
}
export function alertDialog(message: string, opts?: { title?: string }): Promise<void> {
  return new Promise((resolve) => push({ kind: "alert", title: opts?.title || "提示", message, resolve }));
}

export function Dialogs() {
  const [req, setReq] = useState<Req | null>(null);
  const [val, setVal] = useState("");

  useEffect(() => {
    push = (r) => {
      if (r.kind === "prompt") setVal(r.def || "");
      setReq(r);
    };
    return () => { push = () => {}; };
  }, []);

  if (!req) return null;

  const cancel = () => {
    if (req.kind === "confirm") req.resolve(false);
    else if (req.kind === "prompt") req.resolve(null);
    else req.resolve();
    setReq(null);
  };
  const ok = () => {
    if (req.kind === "confirm") req.resolve(true);
    else if (req.kind === "prompt") req.resolve(val);
    else req.resolve();
    setReq(null);
  };

  return (
    <div
      className="fixed inset-0 flex items-center justify-center"
      style={{ background: "rgba(0,0,0,.6)", backdropFilter: "blur(2px)", zIndex: 2147483600 }}
      onMouseDown={(e) => e.target === e.currentTarget && cancel()}
    >
      <div
        className="panel"
        style={{ width: 380, padding: 0, boxShadow: "0 12px 40px rgba(0,0,0,.5)" }}
        onKeyDown={(e) => { if (e.key === "Escape") cancel(); if (e.key === "Enter" && req.kind !== "alert") ok(); }}
      >
        <div className="px-4 py-3" style={{ borderBottom: "1px solid var(--border)" }}>
          <div style={{ color: req.kind === "confirm" && req.danger ? "var(--danger)" : "var(--accent)" }}>{req.title}</div>
        </div>
        <div className="px-4 py-4">
          {req.kind !== "prompt" && <div style={{ color: "var(--fg)", whiteSpace: "pre-wrap" }}>{req.message}</div>}
          {req.kind === "prompt" && (
            <>
              {req.message && <div className="mono-sm mb-2">{req.message}</div>}
              <input className="input" autoFocus value={val} onChange={(e) => setVal(e.target.value)} />
            </>
          )}
        </div>
        <div className="px-4 py-3 flex justify-end gap-2" style={{ borderTop: "1px solid var(--border)" }}>
          {req.kind !== "alert" && <button className="btn" onClick={cancel}>取消</button>}
          <button
            className="btn btn-accent"
            autoFocus={req.kind !== "prompt"}
            style={req.kind === "confirm" && req.danger ? { borderColor: "var(--danger)", color: "var(--danger)", background: "color-mix(in srgb, var(--danger) 16%, transparent)" } : {}}
            onClick={ok}
          >
            {req.kind === "confirm" ? (req.okText || "确定") : req.kind === "prompt" ? "确定" : "知道了"}
          </button>
        </div>
      </div>
    </div>
  );
}
