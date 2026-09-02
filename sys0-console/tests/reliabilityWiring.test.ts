import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (name: string) => readFileSync(new URL(`../src/components/${name}`, import.meta.url), "utf8");

test("shell lifecycle fences stale sockets and snapshot replay", () => {
  const source = read("Shell.tsx");
  assert.match(source, /GenerationFence/);
  assert.match(source, /SnapshotEventBuffer/);
  assert.match(source, /createThrottledSaver/);
  assert.match(source, /connecting.*close|close\(\).*connecting/s);
});

test("shell and task streams cache all events and hydrate listed outputs", () => {
  for (const name of ["Shell.tsx", "Tasks.tsx"]) {
    const source = read(name);
    assert.match(source, /trimChunkHistory/, name);
    assert.match(source, /Promise\.allSettled/, name);
    assert.match(source, /SnapshotEventBuffer/, name);
  }
  assert.doesNotMatch(read("Tasks.tsx"), /p\.task !== selRef\.current\) return/);
});

test("remote operations recheck current online state after awaits", () => {
  for (const name of ["Files.tsx", "Processes.tsx", "Tasks.tsx", "Actions.tsx", "Screenshot.tsx"]) {
    assert.match(read(name), /onlineRef\.current/, name);
  }
  for (const name of ["Files.tsx", "Processes.tsx", "Actions.tsx", "Screenshot.tsx"]) {
    assert.match(read(name), /return \(\) => \{[^}]*onlineRef\.current = false/s, name);
  }
  assert.match(read("Files.tsx"), /cancelRef\.current = true/);
});


test("session changes in another tab tear down the current workspace", () => {
  const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
  assert.match(app, /addEventListener\("storage"/);
  assert.match(app, /event\.key === "sys0_user"/);
  assert.match(app, /event\.key === "sys0_token"/);
  assert.match(app, /location\.reload\(\)/);
});

test("unexpected websocket closure reaches shell and task lifecycle handlers", () => {
  for (const name of ["Shell.tsx", "Tasks.tsx"]) {
    assert.match(read(name), /socket\.onClose\(/, name);
  }
});


test("live streams recover gaps and shell reconnects automatically", () => {
  for (const name of ["Shell.tsx", "Tasks.tsx"]) {
    const source = read(name);
    assert.match(source, /=== "gap"/, name);
    assert.match(source, /=== "duplicate"/, name);
  }
  assert.match(read("Shell.tsx"), /setReconnect\(/);
});

test("monitor avoids a disable dispatch during offline transition", () => {
  const source = read("Monitor.tsx");
  assert.doesNotMatch(source, /return \(\) => \{ onlineRef\.current = false/);
  assert.match(source, /if \(onlineRef\.current\).*host\.watch.*enable: false/s);
});

test("node actions refetch and verify the live node after confirmation", () => {
  const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
  assert.match(app, /await api\.nodes\(\)/);
  assert.match(app, /nodeConnectionSignature/);
});


test("shell replays a fresh snapshot after returning online", () => {
  const source = read("Shell.tsx");
  assert.match(source, /if \(entry\) \{\s*entry\.term\.clear\(\);\s*entry\.term\.reset\(\);/s);
  assert.doesNotMatch(source, /if \(entry && !entry\.loaded\)/);
});
