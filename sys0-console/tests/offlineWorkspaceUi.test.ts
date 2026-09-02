import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
const app = read("../src/App.tsx");

test("offline node cards open their workspace", () => {
  assert.match(app, /canFocusNode\(n\.state\)/);
  assert.match(app, /节点离线 · 显示最近保存的信息/);
});

test("offline-aware surfaces receive connection state", () => {
  for (const name of ["Shell", "Tasks", "Processes", "Files", "Monitor", "Screenshot", "Actions"]) {
    assert.match(app, new RegExp(`<${name}[^>]*online=\\{focusedOnline\\}`), name);
  }
});

test("recent operational views restore cached data", () => {
  for (const file of ["Shell.tsx", "Monitor.tsx", "Processes.tsx", "Files.tsx", "Tasks.tsx", "Screenshot.tsx"]) {
    const source = read(`../src/components/${file}`);
    assert.match(source, /loadRecent/, file);
    assert.match(source, /saveRecent/, file);
  }
});

test("screenshot history shares the bounded workspace cache", () => {
  const source = read("../src/components/Screenshot.tsx");
  assert.doesNotMatch(source, /sys0_shots_v1|4 \* 1024 \* 1024/);
  assert.match(source, /"screenshots"/);
});

test("logout clears operational history before clearing the session", () => {
  assert.match(app, /clearRecent\(localStorage\);\s*clearSession\(\)/);
});
