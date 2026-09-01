import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

const account = fs.readFileSync(new URL("../src/components/AccountModal.tsx", import.meta.url), "utf8");
const app = fs.readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");

test("HTTP/MCP keys are managed from My Account", () => {
  assert.match(account, /我的密钥/);
  assert.match(account, /<Keys\s+mode="self"/);
});

test("keys are not a node-workspace app", () => {
  assert.doesNotMatch(app, /key:\s*"keys"/);
  assert.doesNotMatch(app, /render:\s*\(\)\s*=>\s*<Keys/);
});
