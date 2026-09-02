import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";

function sourceFiles(dir: URL): URL[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const url = new URL(entry.name + (entry.isDirectory() ? "/" : ""), dir);
    if (entry.isDirectory()) return sourceFiles(url);
    return entry.name.endsWith(".tsx") ? [url] : [];
  });
}

const uiSource = sourceFiles(new URL("../src/", import.meta.url))
  .map((file) => readFileSync(file, "utf8"))
  .join("\n");

const implementationCopy = [
  "窗口布局、位置、大小与内容均与该节点绑定",
  "聚焦工作区",
  "agent 侧常驻",
  "stdin 透传",
  "已伪装进程名",
  "hosted hub address is baked in",
  "后端 {sel.tool}",
];

test("UI does not expose implementation or design-discussion copy", () => {
  for (const phrase of implementationCopy) {
    assert.doesNotMatch(uiSource, new RegExp(phrase), phrase);
  }
});
