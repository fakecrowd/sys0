import test from "node:test";
import assert from "node:assert/strict";
import { accessibleFocus, acceptsNodeMetrics } from "../src/nodeAccess.ts";
import { loadRecent, saveRecent, setRecentNodeAccess } from "../src/nodeWorkspace.ts";

const allowed = { id: "a", state: "offline", canAccess: true };
test("offline authorized nodes retain focus; missing, unowned and bootstrapping nodes never open history", () => {
  assert.equal(accessibleFocus([allowed], "a"), "a");
  assert.equal(accessibleFocus([], "a"), "");
  assert.equal(accessibleFocus([{ ...allowed, canAccess: false }], "a"), "");
  assert.equal(accessibleFocus([{ ...allowed, canAccess: undefined }], "a"), "");
  assert.equal(accessibleFocus([{ ...allowed, state: "bootstrapping" }], "a"), "");
});
test("metrics events cannot expose unknown or unowned nodes", () => {
  assert.equal(acceptsNodeMetrics([allowed], "a"), true);
  assert.equal(acceptsNodeMetrics([{ ...allowed, canAccess: false }], "a"), false);
  assert.equal(acceptsNodeMetrics([], "a"), false);
});
test("revocation deletes cached history and fences delayed writers, without losing legitimate offline caches", () => {
  const values = new Map<string, string>([["sys0_user", "alice"]]);
  const storage = { get length() { return values.size; }, key: (i: number) => [...values.keys()][i] ?? null,
    getItem: (k: string) => values.get(k) ?? null, setItem: (k: string, v: string) => { values.set(k, v); }, removeItem: (k: string) => { values.delete(k); } };
  saveRecent(storage, "alice", "a", "shell", "secret");
  saveRecent(storage, "alice", "b", "shell", "offline data");
  setRecentNodeAccess(storage, "alice", ["b"]);
  assert.equal(loadRecent(storage, "alice", "a", "shell"), null);
  assert.equal(saveRecent(storage, "alice", "a", "shell", "late secret"), false);
  assert.equal(loadRecent(storage, "alice", "b", "shell")?.data, "offline data");
  setRecentNodeAccess(storage, "alice", ["a", "b"]);
  assert.equal(loadRecent(storage, "alice", "a", "shell"), null);
  assert.equal(saveRecent(storage, "alice", "a", "shell", "new authorized data"), true);
});
