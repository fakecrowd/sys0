import test from "node:test";
import assert from "node:assert/strict";
import React from "react";
import { create, act } from "react-test-renderer";
import { api } from "../src/api.ts";
import { useNodeInventory } from "../src/useNodeInventory.ts";

const handlers = new Map<string, any>();
const data = new Map([["sys0_user", "alice"]]);
Object.assign(globalThis, { localStorage: { get length() { return data.size; }, key: (i: number) => [...data.keys()][i] ?? null,
  getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => data.set(k, v), removeItem: (k: string) => data.delete(k) },
  EventSource: class { addEventListener(name: string, fn: any) { handlers.set(name, fn); } close() {} } });
const node = { id: "a", state: "online", canAccess: true, canManageAccess: true, owner: "alice" };

test("inventory ignores partial event payloads, filters metrics and removes access on the next authoritative response", async () => {
  let value: any; let resolve: any; let calls = 0;
  api.nodes = async () => { calls++; if (calls === 1) return { ok: true, nodes: [node] }; return new Promise(r => { resolve = r; }); };
  function Probe() { value = useNodeInventory(); return null; }
  let view: any;
  await act(async () => { view = create(React.createElement(Probe)); });
  await act(async () => handlers.get("event.node")({ data: JSON.stringify({ node: { id: "a", state: "offline" } }) }));
  assert.equal(value.nodes[0].canManageAccess, true);
  await act(async () => handlers.get("event.metrics")({ data: JSON.stringify({ node: "unknown", metrics: { cpuPct: 90 } }) }));
  assert.equal(value.live.unknown, undefined);
  await act(async () => handlers.get("event.metrics")({ data: JSON.stringify({ node: "a", metrics: { cpuPct: 1 } }) }));
  assert.equal(value.live.a.cpuPct, 1);
  await act(async () => resolve({ ok: true, nodes: [] }));
  assert.deepEqual(value.nodes, []); assert.deepEqual(value.live, {});
  await act(async () => view.unmount());
});

test("late inventory response cannot restore revoked authorization", async () => {
  let value: any; const replies: any[] = [];
  api.nodes = async () => new Promise(resolve => replies.push(resolve));
  function Probe() { value = useNodeInventory(); return null; }
  let view: any;
  await act(async () => { view = create(React.createElement(Probe)); });
  await act(async () => handlers.get("event.access")({ data: JSON.stringify({ node: "a" }) }));
  await act(async () => replies[1]({ ok: true, nodes: [] }));
  await act(async () => replies[0]({ ok: true, nodes: [node] }));
  assert.deepEqual(value.nodes, []);
  await act(async () => view.unmount());
});


test("six-second inventory responses commit during five-second polling and purge revoked history", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval"] });
  let value: any; const replies: any[] = [];
  t.mock.method(api, "nodes", () => new Promise(resolve => replies.push(resolve)));
  function Probe() { value = useNodeInventory(); return null; }
  let view: any;
  t.after(async () => { await act(async () => view?.unmount()); t.mock.timers.reset(); });
  await act(async () => { view = create(React.createElement(Probe)); });
  await act(async () => replies[0]({ ok: true, nodes: [node] }));
  data.set("sys0_recent_v1:alice:a:shell", JSON.stringify({ savedAt: Date.now(), data: "private history" }));
  await act(async () => handlers.get("event.metrics")({ data: JSON.stringify({ node: "a", metrics: { cpuPct: 1 } }) }));
  await act(async () => t.mock.timers.tick(5000));
  await act(async () => t.mock.timers.tick(5000));
  await act(async () => t.mock.timers.tick(1000));
  for (let i = 1; i <= 3; i++) {
    if (i > 1) await act(async () => t.mock.timers.tick(5000));
    assert.ok(replies.length > i + 1, "next poll is pending when this six-second response completes");
    await act(async () => replies[i]({ ok: true, nodes: [] }));
    assert.deepEqual(value.nodes, [], `completed revoked inventory ${i} must not starve`);
    assert.deepEqual(value.live, {});
    assert.equal(data.has("sys0_recent_v1:alice:a:shell"), false);
  }
});

test("a newer failed inventory does not suppress an older successful revocation", async (t) => {
  let value: any; const replies: any[] = [];
  t.mock.method(api, "nodes", () => new Promise(resolve => replies.push(resolve)));
  function Probe() { value = useNodeInventory(); return null; }
  let view: any;
  t.after(async () => { await act(async () => view?.unmount()); });
  await act(async () => { view = create(React.createElement(Probe)); });
  await act(async () => replies[0]({ ok: true, nodes: [node] }));
  let first: any; let second: any;
  await act(async () => { first = value.refresh(); second = value.refresh(); });
  await act(async () => { replies[2]({ ok: false }); await second; });
  await act(async () => { replies[1]({ ok: true, nodes: [] }); await first; });
  assert.deepEqual(value.nodes, []);
});

test("inventory completed after unmount cannot purge current account history", async (t) => {
  const replies: any[] = [];
  t.mock.method(api, "nodes", () => new Promise(resolve => replies.push(resolve)));
  function Probe() { useNodeInventory(); return null; }
  let view: any;
  await act(async () => { view = create(React.createElement(Probe)); });
  await act(async () => view.unmount());
  data.set("sys0_recent_v1:alice:a:shell", "keep");
  await act(async () => replies[0]({ ok: true, nodes: [] }));
  assert.equal(data.get("sys0_recent_v1:alice:a:shell"), "keep");
  data.delete("sys0_recent_v1:alice:a:shell");
});
