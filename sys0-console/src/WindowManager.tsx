import { useEffect, useRef, useState, type ReactNode } from "react";

// A "page" the console can show. On desktop each app is a draggable/resizable/
// minimizable window; on mobile they collapse back to a single full-screen
// page switched by a tab bar.
export type WinApp = {
  key: string;
  title: string;
  render: () => ReactNode;
};

type WinState = {
  key: string;
  x: number; y: number; w: number; h: number;
  z: number;
  open: boolean;
  min: boolean;
  max: boolean;
  // saved geometry to restore from maximize
  px?: number; py?: number; pw?: number; ph?: number;
};

const LAYOUT_KEY = "sys0_winlayout_v1";
const MOBILE_TAB_KEY = "sys0_mobiletab";

function useIsMobile(breakpoint = 768) {
  const [m, setM] = useState(() => window.innerWidth <= breakpoint);
  useEffect(() => {
    const onR = () => setM(window.innerWidth <= breakpoint);
    window.addEventListener("resize", onR);
    return () => window.removeEventListener("resize", onR);
  }, [breakpoint]);
  return m;
}

function loadLayout(): Record<string, Partial<WinState>> {
  try { return JSON.parse(localStorage.getItem(LAYOUT_KEY) || "{}") || {}; }
  catch { return {}; }
}
function saveLayout(wins: WinState[]) {
  const o: Record<string, Partial<WinState>> = {};
  for (const w of wins) o[w.key] = { x: w.x, y: w.y, w: w.w, h: w.h, open: w.open, min: w.min, max: w.max };
  try { localStorage.setItem(LAYOUT_KEY, JSON.stringify(o)); } catch { /* ignore */ }
}

// Cascade default positions so freshly-opened windows don't perfectly overlap.
function defaultGeom(i: number): { x: number; y: number; w: number; h: number } {
  const offset = (i % 6) * 34;
  return { x: 40 + offset, y: 24 + offset, w: 760, h: 520 };
}

export function WindowManager({ apps }: { apps: WinApp[] }) {
  const isMobile = useIsMobile();

  // ---- desktop window state ----
  const [wins, setWins] = useState<WinState[]>(() => {
    const saved = loadLayout();
    return apps.map((a, i) => {
      const g = defaultGeom(i);
      const s = saved[a.key] || {};
      return {
        key: a.key,
        x: s.x ?? g.x, y: s.y ?? g.y, w: s.w ?? g.w, h: s.h ?? g.h,
        z: i + 1,
        open: s.open ?? (i === 0), // first app open by default on a fresh session
        min: s.min ?? false,
        max: s.max ?? false,
      };
    });
  });
  const topZ = useRef(apps.length + 1);

  useEffect(() => { saveLayout(wins); }, [wins]);

  const byKey = (k: string) => wins.find((w) => w.key === k)!;

  const focus = (k: string) =>
    setWins((ws) => ws.map((w) => (w.key === k ? { ...w, z: ++topZ.current } : w)));

  const openApp = (k: string) =>
    setWins((ws) => ws.map((w) => (w.key === k ? { ...w, open: true, min: false, z: ++topZ.current } : w)));

  const closeApp = (k: string) =>
    setWins((ws) => ws.map((w) => (w.key === k ? { ...w, open: false } : w)));

  const toggleMin = (k: string) =>
    setWins((ws) => ws.map((w) => (w.key === k ? { ...w, min: !w.min, z: ++topZ.current } : w)));

  const toggleMax = (k: string) =>
    setWins((ws) => ws.map((w) => {
      if (w.key !== k) return w;
      if (w.max) return { ...w, max: false, x: w.px ?? w.x, y: w.py ?? w.y, w: w.pw ?? w.w, h: w.ph ?? w.h, z: ++topZ.current };
      return { ...w, max: true, px: w.x, py: w.y, pw: w.w, ph: w.h, z: ++topZ.current };
    }));

  const setGeom = (k: string, g: Partial<WinState>) =>
    setWins((ws) => ws.map((w) => (w.key === k ? { ...w, ...g } : w)));

  // launcher / taskbar button: open if closed, minimize/restore if open
  const launcherClick = (k: string) => {
    const w = byKey(k);
    if (!w.open) openApp(k);
    else if (w.min) setGeom(k, { min: false }), focus(k);
    else {
      // if it's the topmost already, minimize; otherwise just focus
      const isTop = wins.every((o) => o.key === k || o.z <= w.z);
      if (isTop) toggleMin(k);
      else focus(k);
    }
  };

  // ---------------- MOBILE: paged tab view ----------------
  const [mtab, setMtab] = useState<string>(() => localStorage.getItem(MOBILE_TAB_KEY) || apps[0]?.key || "");
  useEffect(() => { if (mtab) localStorage.setItem(MOBILE_TAB_KEY, mtab); }, [mtab]);

  if (isMobile) {
    const active = apps.find((a) => a.key === mtab) || apps[0];
    return (
      <div className="wm-mobile">
        <nav className="wm-mobile-tabs">
          {apps.map((a) => (
            <button key={a.key} className="btn"
              onClick={() => setMtab(a.key)}
              style={mtab === a.key ? { borderColor: "var(--accent)", color: "var(--accent)" } : {}}>
              {a.title}
            </button>
          ))}
        </nav>
        <div className="wm-mobile-body">
          {active && active.render()}
        </div>
      </div>
    );
  }

  // ---------------- DESKTOP: window manager ----------------
  return (
    <div className="wm-root">
      <div className="wm-canvas">
        {apps.map((a) => {
          const w = byKey(a.key);
          if (!w.open || w.min) return null;
          return (
            <WindowFrame
              key={a.key}
              app={a}
              state={w}
              onFocus={() => focus(a.key)}
              onClose={() => closeApp(a.key)}
              onMin={() => toggleMin(a.key)}
              onMax={() => toggleMax(a.key)}
              onGeom={(g) => setGeom(a.key, g)}
            />
          );
        })}
      </div>
      {/* taskbar / launcher */}
      <div className="wm-taskbar">
        {apps.map((a) => {
          const w = byKey(a.key);
          const cls = "wm-task" + (w.open ? (w.min ? " min" : " active") : "");
          return (
            <button key={a.key} className={cls} onClick={() => launcherClick(a.key)} title={a.title}>
              <span className="wm-task-dot" />
              {a.title}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function WindowFrame({
  app, state, onFocus, onClose, onMin, onMax, onGeom,
}: {
  app: WinApp;
  state: WinState;
  onFocus: () => void;
  onClose: () => void;
  onMin: () => void;
  onMax: () => void;
  onGeom: (g: Partial<WinState>) => void;
}) {
  const dragRef = useRef<{ mode: "move" | "resize"; sx: number; sy: number; ox: number; oy: number; ow: number; oh: number } | null>(null);

  const onTitleDown = (e: React.MouseEvent) => {
    if (state.max) return; // no drag while maximized
    if ((e.target as HTMLElement).closest(".wm-btn")) return; // clicking a control
    onFocus();
    dragRef.current = { mode: "move", sx: e.clientX, sy: e.clientY, ox: state.x, oy: state.y, ow: state.w, oh: state.h };
    e.preventDefault();
  };
  const onResizeDown = (e: React.MouseEvent) => {
    if (state.max) return;
    onFocus();
    dragRef.current = { mode: "resize", sx: e.clientX, sy: e.clientY, ox: state.x, oy: state.y, ow: state.w, oh: state.h };
    e.preventDefault();
    e.stopPropagation();
  };

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      const d = dragRef.current;
      if (!d) return;
      const dx = e.clientX - d.sx, dy = e.clientY - d.sy;
      if (d.mode === "move") {
        const nx = Math.max(0, Math.min(window.innerWidth - 80, d.ox + dx));
        const ny = Math.max(0, Math.min(window.innerHeight - 60, d.oy + dy));
        onGeom({ x: nx, y: ny });
      } else {
        const nw = Math.max(340, d.ow + dx);
        const nh = Math.max(200, d.oh + dy);
        onGeom({ w: nw, h: nh });
      }
    };
    const onUp = () => { dragRef.current = null; };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => { window.removeEventListener("mousemove", onMove); window.removeEventListener("mouseup", onUp); };
  }, [onGeom]);

  const style: React.CSSProperties = state.max
    ? { left: 0, top: 0, width: "100%", height: "100%", zIndex: state.z }
    : { left: state.x, top: state.y, width: state.w, height: state.h, zIndex: state.z };

  return (
    <div className="wm-window" style={style} onMouseDown={onFocus}>
      <div className="wm-title" onMouseDown={onTitleDown} onDoubleClick={onMax}>
        <span className="wm-title-text">{app.title}</span>
        <div className="wm-controls">
          <button className="wm-btn" title="最小化" onClick={onMin}>—</button>
          <button className="wm-btn" title={state.max ? "还原" : "最大化"} onClick={onMax}>{state.max ? "❐" : "▢"}</button>
          <button className="wm-btn wm-close" title="关闭" onClick={onClose}>✕</button>
        </div>
      </div>
      <div className="wm-body">
        {app.render()}
      </div>
      {!state.max && <div className="wm-resize" onMouseDown={onResizeDown} title="拖动改变大小" />}
    </div>
  );
}
