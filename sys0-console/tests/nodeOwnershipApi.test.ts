import test from "node:test";
import assert from "node:assert/strict";
import { api, eventStream } from "../src/api.ts";
Object.assign(globalThis, { localStorage: { getItem: () => null } });
test("ownership API sends encoded node paths, explicit replacement grants and preserves conflict status", async () => {
  assert.equal(typeof api.claimNode, "function");
  const calls: any[] = [];
  globalThis.fetch = async (url, init) => { calls.push([url, init]); return new Response(JSON.stringify({ ok: false, error: "claimed" }), { status: 409 }); };
  const r = await api.claimNode("a/b");
  assert.equal(r.httpStatus, 409); assert.equal(calls[0][0], "/api/v1/nodes/a%2Fb/claim");
  assert.equal(calls[0][1].body, "{}");
  await api.nodeAccess("a/b"); assert.equal(calls[1][1].method, "GET");
  await api.setNodeAccess("a/b", ["bob"]);
  assert.equal(calls[2][0], "/api/v1/nodes/a%2Fb/access");
  assert.deepEqual(JSON.parse(calls[2][1].body), { users: ["bob"] });
});
test("SSE subscribes to access changes as inventory invalidations", () => {
  const handlers = new Map(); let observed = "";
  Object.assign(globalThis, { EventSource: class { addEventListener(name: string, fn: any) { handlers.set(name, fn); } } });
  eventStream(type => { observed = type; });
  assert.equal(handlers.has("event.access"), true);
  handlers.get("event.access")({ data: JSON.stringify({ node: "a" }) });
  assert.equal(observed, "event.access");
});


test("cache refresh preserves its domain status object instead of replacing it with HTTP metadata", async (t) => {
  const status = { ok: true, tag: "fixture-release", assets: [{ name: "agent", kind: "agent", os: "linux", arch: "amd64", url: "/fixture", cached: true }], cachedCount: 1, totalCount: 1 };
  t.mock.method(globalThis, "fetch", async (url: string, init: RequestInit) => {
    assert.equal(url, "/api/v1/cache/refresh"); assert.equal(init.method, "POST");
    return new Response(JSON.stringify({ ok: true, refreshed: 1, status }), { status: 200 });
  });
  const result = await api.cacheRefresh();
  assert.deepEqual(result.status, status);
  assert.deepEqual(result.status.assets.map(asset => asset.name), ["agent"]);
});
