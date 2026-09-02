import test from "node:test";
import assert from "node:assert/strict";
import {
  GenerationFence,
  SnapshotEventBuffer,
  mergeBase64SnapshotAndEvents,
  trimChunkHistory,
} from "../src/workspaceLifecycle.ts";

const enc = (value: string) => Buffer.from(value).toString("base64");
const dec = (value: string) => Buffer.from(value, "base64").toString();

test("generation fence rejects stale async continuations and offline work", () => {
  const fence = new GenerationFence();
  const first = fence.begin();
  assert.equal(fence.current(first, true), true);
  const second = fence.begin();
  assert.equal(fence.current(first, true), false);
  assert.equal(fence.current(second, false), false);
  assert.equal(fence.current(second, true), true);
  fence.invalidate();
  assert.equal(fence.current(second, true), false);
});

test("snapshot merge uses sequence numbers and preserves legitimate repeated output", () => {
  const merged = mergeBase64SnapshotAndEvents(
    { data: enc("repeat"), seq: 1 },
    [{ chunk: enc("repeat"), seq: 2 }],
  );
  assert.equal(dec(merged!), "repeatrepeat");
});

test("snapshot merge discards covered events and detects sequence gaps", () => {
  assert.equal(dec(mergeBase64SnapshotAndEvents(
    { data: enc("abcdef"), seq: 2 },
    [{ chunk: enc("duplicate"), seq: 2 }, { chunk: enc("ghi"), seq: 3 }],
  )!), "abcdefghi");
  assert.equal(mergeBase64SnapshotAndEvents(
    { data: enc("abcdef"), seq: 2 },
    [{ chunk: enc("gap"), seq: 4 }],
  ), null);
});

test("snapshot buffer generation-fences stale replies and buffers every event", () => {
  const buffer = new SnapshotEventBuffer();
  const stale = buffer.begin("task-1");
  const current = buffer.begin("task-1");
  buffer.event("task-1", enc("two"), 2);
  assert.equal(buffer.complete("task-1", stale, { data: enc("one"), seq: 1 }), null);
  assert.equal(dec(buffer.complete("task-1", current, { data: enc("onetwo"), seq: 2 })!), "onetwo");
});

test("snapshot buffer enforces sequence after hydration", () => {
  const buffer = new SnapshotEventBuffer();
  const generation = buffer.begin("shell-1");
  assert.equal(buffer.event("shell-1", enc("two"), 2), "buffered");
  assert.equal(dec(buffer.complete("shell-1", generation, { data: enc("one"), seq: 1 })!), "onetwo");
  assert.equal(buffer.event("shell-1", enc("duplicate"), 2), "duplicate");
  assert.equal(buffer.event("shell-1", enc("three"), 3), "accept");
  assert.equal(buffer.event("shell-1", enc("gap"), 5), "gap");
});

test("chunk histories retain newest bounded output for every stream", () => {
  const outputs = {
    one: ["a".repeat(6), "b".repeat(6)],
    two: ["c".repeat(6)],
  };
  assert.deepEqual(trimChunkHistory(outputs, "two", "d".repeat(6), 18), {
    one: ["b".repeat(6)],
    two: ["c".repeat(6), "d".repeat(6)],
  });
});


test("a single oversized snapshot retains its newest bounded tail", () => {
  assert.deepEqual(trimChunkHistory({}, "one", "abcdefgh", 4), { one: ["efgh"] });
});
