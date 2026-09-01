import test from "node:test";
import assert from "node:assert/strict";
import { formatBytes, progressSummary } from "../src/rescueProgress.ts";

test("formats rescue deployment progress with module and aggregate count", () => {
  assert.equal(progressSummary({ active: true, module: "shell", downloaded: 5_242_880, total: 10_485_760, percent: 50, completed: 1, modules: 4 }), "shell · 50% · 5.0 MB / 10.0 MB · 1/4");
});

test("unknown content length still exposes downloaded bytes", () => {
  assert.equal(progressSummary({ active: true, module: "core", downloaded: 1536, total: 0, percent: 0, completed: 0, modules: 4 }), "core · 1.5 KB · 0/4");
});

test("byte formatter uses bounded binary units", () => {
  assert.equal(formatBytes(0), "0 B");
  assert.equal(formatBytes(1024), "1.0 KB");
});
