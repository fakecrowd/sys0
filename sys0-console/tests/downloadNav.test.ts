import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { DOWNLOAD_PATH, DOWNLOAD_SECTIONS } from "../src/downloadNav.ts";

test("download entry points use the public /dl route", () => {
  assert.equal(DOWNLOAD_PATH, "/dl");
});

test("login and dashboard both render download links", () => {
  const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
  assert.equal(app.match(/href=\{DOWNLOAD_PATH\}/g)?.length, 2);
});

test("rescue downloads are shown before agent downloads", () => {
  assert.deepEqual(DOWNLOAD_SECTIONS.map((section) => section.kind), ["rescue", "agent"]);
});
