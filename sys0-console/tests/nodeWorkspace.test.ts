import test from "node:test";
import assert from "node:assert/strict";
import {
  RECENT_MAX_BYTES,
  canFocusNode,
  clearRecent,
  createThrottledSaver,
  loadRecent,
  migrateLegacyScreenshotHistory,
  saveRecent,
  sweepRecent,
  takeLegacyScreenshotHistory,
} from "../src/nodeWorkspace.ts";

class MemoryStorage {
  protected values = new Map<string, string>([["sys0_user", "alice"]]);
  get length() { return this.values.size; }
  key(index: number) { return [...this.values.keys()][index] ?? null; }
  getItem(key: string) { return this.values.get(key) ?? null; }
  setItem(key: string, value: string) { this.values.set(key, value); }
  removeItem(key: string) { this.values.delete(key); }
  keys() { return [...this.values.keys()]; }
}

class QuotaStorage extends MemoryStorage {
  throws = 1;
  override setItem(key: string, value: string) {
    if (this.throws-- > 0) throw new Error("QuotaExceededError");
    super.setItem(key, value);
  }
}

test("offline nodes remain focusable while bootstrapping nodes do not", () => {
  assert.equal(canFocusNode("online"), true);
  assert.equal(canFocusNode("offline"), true);
  assert.equal(canFocusNode("bootstrapping"), false);
});

test("recent workspace data survives remounts within the retention window", () => {
  const storage = new MemoryStorage();
  saveRecent(storage, "alice", "node-1", "monitor", { samples: [1, 2] }, 1_000);
  assert.deepEqual(loadRecent(storage, "alice", "node-1", "monitor", 1_500), {
    savedAt: 1_000,
    data: { samples: [1, 2] },
  });
});

test("expired or malformed workspace history is discarded", () => {
  const storage = new MemoryStorage();
  saveRecent(storage, "alice", "node-1", "files", { path: "/tmp" }, 1_000);
  assert.equal(loadRecent(storage, "alice", "node-1", "files", 8 * 24 * 60 * 60 * 1000), null);
  storage.setItem("sys0_recent_v1:alice:node-1:files", "not-json");
  assert.equal(loadRecent(storage, "alice", "node-1", "files", 1_500), null);
});

test("logout clearing removes workspace history without touching other preferences", () => {
  const storage = new MemoryStorage();
  saveRecent(storage, "alice", "node-1", "shell", { secret: "output" }, 1_000);
  storage.setItem("sys0_nodesort", "label");
  storage.setItem("sys0_shots_v1:node-1", "legacy secret");
  clearRecent(storage);
  assert.equal(loadRecent(storage, "alice", "node-1", "shell", 1_500), null);
  assert.equal(storage.getItem("sys0_shots_v1:node-1"), null);
  assert.equal(storage.getItem("sys0_nodesort"), "label");
});

test("saving sweeps expired entries and evicts oldest history to the global budget", () => {
  const storage = new MemoryStorage();
  const now = 8 * 24 * 60 * 60 * 1000;
  storage.setItem("sys0_recent_v1:alice:expired:files", JSON.stringify({ savedAt: 1, data: "old" }));
  const payload = "x".repeat(Math.floor(RECENT_MAX_BYTES * 0.55));
  saveRecent(storage, "alice", "node-old", "shell", payload, now);
  saveRecent(storage, "alice", "node-new", "tasks", payload, now + 1);
  sweepRecent(storage, now + 1);
  assert.equal(storage.getItem("sys0_recent_v1:alice:expired:files"), null);
  assert.equal(storage.getItem("sys0_recent_v1:alice:node-old:shell"), null);
  assert.notEqual(storage.getItem("sys0_recent_v1:alice:node-new:tasks"), null);
});

test("quota failure evicts oldest cache and retries the write", () => {
  const storage = new QuotaStorage();
  storage.throws = 0;
  saveRecent(storage, "alice", "old", "files", { value: 1 }, 1_000);
  storage.throws = 1;
  saveRecent(storage, "alice", "new", "files", { value: 2 }, 2_000);
  assert.equal(storage.getItem("sys0_recent_v1:alice:old:files"), null);
  assert.deepEqual(loadRecent(storage, "alice", "new", "files", 2_000)?.data, { value: 2 });
});

test("terminal persistence is trailing-throttled and flushable", () => {
  const callbacks: Array<() => void> = [];
  const calls: string[] = [];
  const saver = createThrottledSaver((value: string) => calls.push(value), 1_000, {
    set: (fn) => { callbacks.push(fn); return callbacks.length; },
    clear: () => {},
  });
  saver.schedule("a");
  saver.schedule("b");
  assert.deepEqual(calls, []);
  callbacks[0]();
  assert.deepEqual(calls, ["b"]);
  saver.schedule("c");
  saver.flush();
  assert.deepEqual(calls, ["b", "c"]);
});


test("legacy screenshot history is migrated once into the bounded cache", () => {
  const storage = new MemoryStorage();
  storage.setItem("sys0_shots_v1:node-1", JSON.stringify([{ id: "shot" }]));
  assert.deepEqual(takeLegacyScreenshotHistory(storage, "node-1"), [{ id: "shot" }]);
  assert.equal(storage.getItem("sys0_shots_v1:node-1"), null);
  assert.equal(takeLegacyScreenshotHistory(storage, "node-1"), null);
});


test("recent workspaces are account-scoped and stale tabs cannot repopulate them", () => {
  const storage = new MemoryStorage();
  storage.setItem("sys0_user", "alice");
  assert.equal(saveRecent(storage, "alice", "node-1", "shell", { output: "alice secret" }, 1_000), true);
  storage.setItem("sys0_user", "bob");
  assert.equal(loadRecent(storage, "bob", "node-1", "shell", 1_500), null);
  assert.equal(loadRecent(storage, "alice", "node-1", "shell", 1_500), null);
  assert.equal(saveRecent(storage, "alice", "node-1", "shell", { output: "stale" }, 1_500), false);
  storage.setItem("sys0_user", "alice");
  assert.deepEqual(loadRecent(storage, "alice", "node-1", "shell", 1_500)?.data, { output: "alice secret" });
});

test("all legacy screenshot histories are migrated through global TTL", () => {
  const storage = new MemoryStorage();
  const now = 8 * 24 * 60 * 60 * 1_000;
  storage.setItem("sys0_user", "alice");
  storage.setItem("sys0_shots_v1:old", JSON.stringify([{ id: "old", ts: 1 }]));
  storage.setItem("sys0_shots_v1:fresh", JSON.stringify([{ id: "fresh", ts: now }]));
  migrateLegacyScreenshotHistory(storage, "alice", now);
  assert.equal(storage.keys().filter((key) => key.startsWith("sys0_shots_v1:")).length, 0);
  assert.equal(loadRecent(storage, "alice", "old", "screenshots", now), null);
  assert.equal(loadRecent<{ shots: Array<{ id: string }> }>(storage, "alice", "fresh", "screenshots", now)?.data.shots[0].id, "fresh");
});

test("throttled saver disposal flushes the latest tail while cancel drops it", () => {
  const calls: string[] = [];
  const saver = createThrottledSaver((value: string) => calls.push(value), 1_000, {
    set: () => 1,
    clear: () => {},
  });
  saver.schedule("tail");
  saver.dispose();
  assert.deepEqual(calls, ["tail"]);
  saver.schedule("secret");
  saver.cancel();
  assert.deepEqual(calls, ["tail"]);
});
