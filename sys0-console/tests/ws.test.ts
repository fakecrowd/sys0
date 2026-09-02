import test from "node:test";
import assert from "node:assert/strict";
import { WSClient } from "../src/ws.ts";

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  sent: string[] = [];
  url: string;
  constructor(url: string) { this.url = url; FakeWebSocket.instances.push(this); }
  send(value: string) { this.sent.push(value); }
  close() { this.onclose?.(); }
}
Object.assign(globalThis, {
  location: { protocol: "https:", host: "example.test" },
  localStorage: { getItem: () => "token" },
  WebSocket: FakeWebSocket,
});

test("socket close rejects calls waiting for readiness and exposes closure", async () => {
  const client = new WSClient();
  let closed = false;
  client.onClose(() => { closed = true; });
  client.connect();
  const call = client.call("pending-ready", {});
  FakeWebSocket.instances.at(-1)!.onclose?.();
  await assert.rejects(call, /closed/);
  assert.equal(closed, true);
  assert.equal(client.connected, false);
});

test("socket error rejects unresolved RPC calls", async () => {
  const client = new WSClient();
  client.connect();
  const socket = FakeWebSocket.instances.at(-1)!;
  socket.onopen?.();
  const call = client.call("pending-rpc", {});
  await Promise.resolve();
  socket.onerror?.();
  await assert.rejects(call, /error/);
  assert.equal(client.connected, false);
});
