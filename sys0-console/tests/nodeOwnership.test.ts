import test from "node:test";
import assert from "node:assert/strict";
import React from "react";
import { act, create } from "react-test-renderer";
import { api } from "../src/api.ts";
import { NodeOwnership } from "../src/components/NodeOwnership.tsx";
import { Accounts } from "../src/components/Accounts.tsx";

const base = { id: "node-a", label: "Alpha", owner: "", canClaim: true, canAccess: false, canManageAccess: false, state: "online" };
const text = (view: any) => JSON.stringify(view.toJSON());
const button = (view: any, label: string) => view.root.findAllByType("button").find((b: any) => b.children.join("") === label);
const mount = async (props: any) => { let view: any; await act(async () => { view = create(React.createElement(NodeOwnership, props)); }); return view; };

test("unowned node offers claim; duplicate submits are disabled and success refreshes inventory", async () => {
  let finish: any; let calls = 0; let refreshed = 0;
  api.claimNode = async (id) => { assert.equal(id, base.id); calls++; return new Promise(resolve => { finish = resolve; }); };
  const view = await mount({ node: base, onChanged: async () => { refreshed++; } });
  assert.match(text(view), /未认领/);
  assert.equal(button(view, "访问管理"), undefined);
  await act(async () => { button(view, "认领").props.onClick(); });
  assert.equal(view.root.findAllByType("button")[0].props.disabled, true);
  await act(async () => { finish({ ok: true, node: { ...base, owner: "alice", canAccess: true } }); });
  assert.equal(calls, 1); assert.equal(refreshed, 1);
  await act(async () => view.unmount());
});

test("claim conflict refreshes and shows a compact inline message", async () => {
  let refreshed = 0;
  api.claimNode = async () => ({ ok: false, httpStatus: 409, error: "conflict" });
  const view = await mount({ node: base, onChanged: async () => { refreshed++; } });
  await act(async () => button(view, "认领").props.onClick());
  assert.equal(refreshed, 1); assert.match(text(view), /已被其他用户认领/);
  await act(async () => view.unmount());
});

test("node owner manages binary grants; owner and instance owners cannot be unchecked", async () => {
  const users = [
    { id: 1, username: "alice", allowed: true },
    { id: 2, username: "root", role: "admin", allowed: true },
    { id: 3, username: "bob", allowed: false },
  ];
  api.nodeAccess = async () => ({ ok: true, owner: "alice", users });
  let grants: string[] | undefined; let refreshed = 0;
  api.setNodeAccess = async (id, selected) => { assert.equal(id, base.id); grants = selected; users[2].allowed = true; return { ok: true }; };
  const view = await mount({ node: { ...base, owner: "alice", canClaim: false, canAccess: true, canManageAccess: true }, onChanged: async () => { refreshed++; } });
  assert.equal(button(view, "认领"), undefined);
  await act(async () => button(view, "访问管理").props.onClick());
  const checks = view.root.findAllByType("input");
  assert.equal(checks[0].props.disabled, true); assert.equal(checks[0].props.checked, true);
  assert.equal(checks[1].props.disabled, true); assert.equal(checks[1].props.checked, true);
  await act(async () => checks[2].props.onChange({ target: { checked: true } }));
  await act(async () => button(view, "保存").props.onClick());
  assert.deepEqual(grants, ["bob"]); assert.equal(refreshed, 1);
  assert.match(text(view), /已保存/);
  await act(async () => view.unmount());
});

test("granted member cannot manage ACL and permission removal closes an open access dialog", async () => {
  const allowed = { ...base, owner: "alice", canAccess: true, canClaim: false, canManageAccess: true };
  api.nodeAccess = async () => ({ ok: true, owner: "alice", users: [] });
  const view = await mount({ node: allowed, onChanged: async () => {} });
  await act(async () => button(view, "访问管理").props.onClick());
  assert.equal(view.root.findAllByProps({ role: "dialog" }).length, 1);
  await act(async () => view.update(React.createElement(NodeOwnership, { node: { ...allowed, canManageAccess: false }, onChanged: async () => {} })));
  assert.equal(button(view, "访问管理"), undefined);
  assert.equal(view.root.findAllByProps({ role: "dialog" }).length, 0);
  await act(async () => view.unmount());
});

test("account management creates member accounts without a second node grant surface", async () => {
  let defaultReads = 0; let payload: any;
  api.getDefaultAccess = async () => { defaultReads++; return { ok: true, users: [] }; };
  api.usersList = async () => ({ ok: true, users: [{ id: 1, username: "root", role: "admin", createdAt: 0, nodeScope: [] }] });
  api.userCreate = async (body) => { payload = body; return { ok: true }; };
  let view: any;
  await act(async () => { view = create(React.createElement(Accounts, { nodes: [], meName: "root" })); });
  assert.equal(defaultReads, 0); assert.doesNotMatch(text(view), /new-node default access|host access/);
  assert.match(text(view), /实例 owner/); assert.match(text(view), /普通用户/);
  const inputs = view.root.findAllByType("input");
  await act(async () => { inputs[0].props.onChange({ target: { value: "bob" } }); inputs[1].props.onChange({ target: { value: "test-password" } }); });
  await act(async () => view.root.findByType("form").props.onSubmit({ preventDefault() {} }));
  assert.deepEqual(payload, { username: "bob", password: "test-password", role: "member", nodeScope: [] });
  assert.equal(button(view, "删除").props.disabled, true);
  assert.equal(button(view, "切换角色").props.disabled, true);
  await act(async () => view.unmount());
});


test("minimal access candidates are not mislabeled as ordinary users; failed saves retain selection", async () => {
  api.nodeAccess = async () => ({ ok: true, owner: "alice", users: [{ id: 2, username: "unknown-role", allowed: true }] });
  api.setNodeAccess = async () => ({ ok: false, error: "permission changed", httpStatus: 403 });
  let refreshed = 0;
  const view = await mount({ node: { ...base, owner: "alice", canManageAccess: true }, onChanged: async () => { refreshed++; } });
  await act(async () => button(view, "访问管理").props.onClick());
  assert.doesNotMatch(text(view), /普通用户/);
  await act(async () => view.root.findByType("input").props.onChange({ target: { checked: false } }));
  await act(async () => button(view, "保存").props.onClick());
  assert.match(text(view), /permission changed/); assert.doesNotMatch(text(view), /已保存/);
  assert.equal(view.root.findByType("input").props.checked, false); assert.equal(refreshed, 1);
  await act(async () => view.unmount());
});

test("claim network failure is visible and retry is enabled", async () => {
  api.claimNode = async () => { throw new Error("offline"); };
  const view = await mount({ node: base, onChanged: async () => {} });
  await act(async () => button(view, "认领").props.onClick());
  assert.match(text(view), /无法认领/); assert.equal(button(view, "认领").props.disabled, false);
  await act(async () => view.unmount());
});
