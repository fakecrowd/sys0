// Browser regression against fixture-owned APIs; never contacts a real hub.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { existsSync } from "node:fs";
import { chromium } from "playwright";

const server = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", "0"], { stdio: ["ignore", "pipe", "pipe"] });
let browser;
try {
  const origin = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("Vite did not start")), 15000);
    server.once("exit", (code) => { clearTimeout(timeout); reject(new Error(`Vite exited ${code}`)); });
    server.stdout.on("data", (data) => { const found = data.toString().match(/http:\/\/127\.0\.0\.1:\d+/); if (found) { clearTimeout(timeout); resolve(found[0]); } });
  });
  browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || (existsSync("/usr/bin/chromium") ? "/usr/bin/chromium" : undefined), headless: true, args: ["--no-sandbox"] });
  for (const mobile of [false, true]) {
    const page = await browser.newPage({ viewport: mobile ? { width: 390, height: 844 } : { width: 1280, height: 900 } });
    const errors = []; page.on("pageerror", (error) => errors.push(error.message));
    let nodes = [{ id: "a", label: "Alpha", owner: "", canClaim: true, canAccess: false, canManageAccess: false, state: "offline", tags: [], host: { os: "linux", arch: "amd64", ip: "hidden-host" }, version: "fixture", lastSeen: 0 }];
    const candidates = [{ id: 1, username: "alice", allowed: true, role: "member" }, { id: 2, username: "root", allowed: true, role: "admin" }, { id: 3, username: "bob", allowed: false, role: "member" }];
    const calls = [];
    let slowInventory = false; let pendingInventories = 0; let maxPendingInventories = 0;
    let conflictOnce = true; let userRole = "member";
    const cache = { ok: true, tag: "before-refresh", assets: [{ name: "fixture-agent", kind: "agent", os: "linux", arch: "amd64", cached: true, size: 1024 }], cachedCount: 1, totalCount: 1 };
    await page.addInitScript(() => {
      localStorage.setItem("sys0_user", "alice"); localStorage.setItem("sys0_role", "member"); localStorage.setItem("sys0_token", "fixture");
      window.streams = [];
      window.EventSource = class extends EventTarget { constructor() { super(); window.streams.push(this); } close() { window.streams = window.streams.filter((stream) => stream !== this); } };
    });
    await page.route("**/api/**", async (route) => {
      const url = new URL(route.request().url()); const method = route.request().method();
      const body = route.request().postDataJSON(); calls.push({ path: url.pathname, method, body });
      let result = { ok: true };
      if (url.pathname === "/api/v1/nodes") {
        result.nodes = nodes;
        if (slowInventory) {
          pendingInventories++; maxPendingInventories = Math.max(maxPendingInventories, pendingInventories);
          await new Promise(resolve => setTimeout(resolve, 6000));
          pendingInventories--;
          if (page.isClosed()) return;
        }
      }
      else if (url.pathname.endsWith("/claim")) {
        if (conflictOnce) { conflictOnce = false; await route.fulfill({ status: 409, json: { ok: false, error: "fixture conflict" } }); return; }
        nodes[0] = { ...nodes[0], owner: "alice", canClaim: false, canAccess: true, canManageAccess: true }; result.node = nodes[0]; }
      else if (url.pathname.endsWith("/access")) {
        if (method === "POST") candidates[2].allowed = body.users.includes("bob");
        result = { ok: true, owner: "alice", users: candidates };
      } else if (url.pathname === "/api/v1/me") result.user = { username: "alice", role: userRole };
      else if (url.pathname === "/api/v1/cache") result = cache;
      else if (url.pathname === "/api/v1/cache/refresh") result = { ok: true, refreshed: 1, status: { ...cache, tag: "after-refresh" } };
      else if (url.pathname.endsWith("/keys")) result.keys = [];
      else if (url.pathname === "/api/v1/methods") result.methods = [];
      else if (url.pathname === "/api/v1/audit") result.audit = [];
      else if (url.pathname === "/api/v1/metrics") result.samples = [];
      await route.fulfill({ json: result });
    });
    await page.goto(origin);
    if (mobile) await page.getByRole("button", { name: "节点列表", exact: true }).click();
    await page.getByText("Alpha", { exact: true }).click();
    assert.equal(await page.getByText("请选择节点", { exact: true }).count(), 1);
    assert.equal(await page.getByText("hidden-host", { exact: false }).count(), 0);
    assert.equal(await page.getByRole("button", { name: "访问管理", exact: true }).count(), 0);
    await page.getByRole("button", { name: "认领", exact: true }).click();
    await page.getByText("已被其他用户认领", { exact: true }).waitFor();
    await page.getByRole("button", { name: "认领", exact: true }).click();
    await page.getByRole("button", { name: "访问管理", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "节点访问 · Alpha" });
    await dialog.waitFor();
    await dialog.getByRole("checkbox", { name: /bob/ }).check();
    assert.equal(await dialog.getByRole("checkbox", { name: /alice/ }).isDisabled(), true);
    assert.equal(await dialog.getByRole("checkbox", { name: /root/ }).isDisabled(), true);
    await dialog.getByRole("button", { name: "保存", exact: true }).click();
    await dialog.getByText("已保存", { exact: true }).waitFor();
    assert.deepEqual(calls.find((call) => call.path.endsWith("/access") && call.method === "POST").body, { users: ["bob"] });
    const bounds = await dialog.boundingBox();
    assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= (mobile ? 390 : 1280));
    await dialog.getByRole("button", { name: "关闭", exact: true }).click();
    await page.getByText("Alpha", { exact: true }).click();
    await page.getByText("节点离线 · 显示最近保存的信息", { exact: true }).waitFor();
    await page.evaluate(() => localStorage.setItem("sys0_recent_v1:alice:a:shell", JSON.stringify({ savedAt: Date.now(), data: "sensitive fixture" })));
    slowInventory = true; nodes = [];
    await page.evaluate(() => window.streams.forEach((stream) => stream.dispatchEvent(new MessageEvent("event.node", { data: JSON.stringify({ event: "access", id: "a" }) }))));
    await page.getByText("请选择节点", { exact: true }).waitFor();
    assert.equal(await page.getByText("节点离线 · 显示最近保存的信息", { exact: true }).count(), 0);
    assert.equal(await page.evaluate(() => localStorage.getItem("sys0_recent_v1:alice:a:shell")), null);
    assert.ok(maxPendingInventories >= 2, "revocation committed while a newer poll was pending");
    slowInventory = false;
    await page.getByRole("button", { name: "alice", exact: true }).click();
    assert.equal(await page.getByRole("button", { name: "用户管理", exact: true }).count(), 0);
    assert.equal(calls.some((call) => /default-access|\/scope$|register/.test(call.path)), false);
    userRole = "admin";
    await page.evaluate(() => localStorage.setItem("sys0_role", "admin"));
    await page.reload();
    await page.evaluate(() => localStorage.setItem("sys0_role", "admin"));
    await page.getByRole("button", { name: "镜像", exact: true }).click();
    await page.getByText("before-refresh", { exact: true }).waitFor();
    await page.getByRole("button", { name: "强制更新", exact: true }).click();
    await page.getByRole("button", { name: "确定", exact: true }).click();
    await page.getByText("after-refresh", { exact: true }).waitFor();
    assert.equal(await page.getByRole("cell", { name: "linux/amd64", exact: true }).count(), 1);
    assert.deepEqual(errors, []);
    console.log(`PASS ${mobile ? "mobile" : "desktop"}: unowned gate, HTTP 409 claim, member-owner ACL, implicit grants, six-second revoke workspace/history with pending poll, account boundary, cache refresh domain status`);
    await page.close();
  }
} finally {
  if (browser) await browser.close();
  const exited = once(server, "exit"); server.kill(); await exited;
}
