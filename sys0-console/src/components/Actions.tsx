import { useEffect, useState } from "react";
import { api, type MethodSpec, type DispatchItem } from "../api";

// Generic, self-describing action runner: pick any (non-interactive) method,
// fill a form generated from its JSON Schema, run it on the FOCUSED node.
// Node is fixed by the workspace — no batch / all-nodes targeting.
export function Actions({ node }: { node: string }) {
  const [methods, setMethods] = useState<MethodSpec[]>([]);
  const [sel, setSel] = useState<string>("");
  const [form, setForm] = useState<Record<string, any>>({});
  const [dry, setDry] = useState(false);
  const [items, setItems] = useState<DispatchItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    api.methods().then((r) => {
      if (r.ok) {
        const usable = r.methods.filter((m) => !m.interactive);
        setMethods(usable);
        setSel(usable[0]?.name || "");
      }
    });
  }, []);

  const spec = methods.find((m) => m.name === sel);
  const props: Record<string, any> = spec?.paramsSchema?.properties || {};

  useEffect(() => setForm({}), [sel]);

  const setField = (k: string, v: any) => setForm((f) => ({ ...f, [k]: v }));

  const run = async () => {
    setBusy(true); setErr("");
    const params: Record<string, any> = {};
    for (const [k, schema] of Object.entries(props)) {
      const raw = form[k];
      if (raw === undefined || raw === "") continue;
      const t = (schema as any).type;
      params[k] = t === "integer" ? parseInt(raw) : t === "boolean" ? !!raw :
        t === "array" ? String(raw).split(",").map((s) => s.trim()).filter(Boolean) : raw;
    }
    try {
      const r = await api.dispatch({ nodes: [node] }, sel, params, dry);
      if (r.ok) setItems(r.items || []);
      else setErr(`${r.error} (code ${r.code})`);
    } catch (e) { setErr(String(e)); } finally { setBusy(false); }
  };

  return (
    <div className="flex flex-col gap-3 h-full">
      <div className="flex gap-2 items-center flex-wrap">
        <span className="mono-sm">手动动作 →</span>
        <select className="input" style={{ width: 180 }} value={sel} onChange={(e) => setSel(e.target.value)}>
          {methods.map((m) => <option key={m.name} value={m.name}>{m.name}{m.dangerous ? " ⚠" : ""}</option>)}
        </select>
        <label className="flex items-center gap-1 cursor-pointer mono-sm">
          <input type="checkbox" checked={dry} onChange={(e) => setDry(e.target.checked)} /> dryRun
        </label>
        <button className="btn btn-accent" disabled={busy || !sel} onClick={run}>执行 · {node}</button>
      </div>
      {spec && <div className="mono-sm" style={{ color: "var(--muted)" }}>{spec.description}</div>}

      <div className="panel p-3 space-y-2">
        {Object.keys(props).length === 0 && <div className="mono-sm">该方法无参数</div>}
        {Object.entries(props).map(([k, schema]) => {
          const t = (schema as any).type;
          return (
            <div key={k} className="flex items-center gap-2">
              <label className="mono-sm" style={{ width: 110 }}>{k}<span style={{ color: "var(--muted)" }}> :{t}</span></label>
              {t === "boolean" ? (
                <input type="checkbox" checked={!!form[k]} onChange={(e) => setField(k, e.target.checked)} />
              ) : (
                <input className="input" type={t === "integer" ? "number" : "text"}
                  placeholder={t === "array" ? "逗号分隔" : (schema as any).description || ""}
                  value={form[k] ?? ""} onChange={(e) => setField(k, e.target.value)} />
              )}
            </div>
          );
        })}
      </div>

      {err && <div style={{ color: "var(--danger)" }}>{err}</div>}
      <div className="flex-1 space-y-2 overflow-auto">
        {items.map((it) => (
          <div key={it.node} className="panel p-3">
            <div className="flex items-center gap-2 mb-1">
              <span className="dot" style={{ background: it.ok ? "var(--accent)" : "var(--danger)" }} />
              <span className="mono-sm">{it.node}</span>
              {!it.ok && <span className="tag ml-auto" style={{ color: "var(--danger)" }}>{it.error?.message}</span>}
            </div>
            {it.ok && (
              <pre className="mono-sm" style={{ margin: 0, whiteSpace: "pre-wrap", color: "var(--muted)" }}>
                {JSON.stringify(it.value, null, 2)}
              </pre>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
