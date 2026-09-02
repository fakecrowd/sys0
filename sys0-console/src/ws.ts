// Minimal JSON-RPC client over the console WebSocket, used for the low-latency
// interactive shell (and live event delivery).
import { getToken } from "./api.ts";

type Pending = { resolve: (v: any) => void; reject: (e: any) => void };

export class WSClient {
  private ws?: WebSocket;
  private seq = 0;
  private pending = new Map<string, Pending>();
  private notifyHandlers = new Map<string, (params: any) => void>();
  private ready: Promise<void>;
  private resolveReady!: () => void;
  private rejectReady!: (error: Error) => void;
  private closed = false;
  private closeHandlers = new Set<(error: Error) => void>();

  constructor() {
    this.ready = new Promise((resolve, reject) => { this.resolveReady = resolve; this.rejectReady = reject; });
    this.ready.catch(() => {});
  }

  connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const tok = getToken() ?? "";
    this.ws = new WebSocket(`${proto}://${location.host}/ws?token=${encodeURIComponent(tok)}`);
    this.ws.onopen = () => { if (!this.closed) this.resolveReady(); };
    this.ws.onerror = () => this.terminate(new Error("websocket error"));
    this.ws.onclose = () => this.terminate(new Error("websocket closed"));
    this.ws.onmessage = (ev) => {
      let m: any;
      try { m = JSON.parse(ev.data); } catch { return; }
      if (m.id && (m.result !== undefined || m.error !== undefined)) {
        const p = this.pending.get(m.id);
        if (p) {
          this.pending.delete(m.id);
          m.error ? p.reject(m.error) : p.resolve(m.result);
        }
      } else if (m.method) {
        this.notifyHandlers.get(m.method)?.(m.params);
      }
    };
  }

  get connected() { return !this.closed && this.ws?.readyState === WebSocket.OPEN; }

  onClose(fn: (error: Error) => void) { this.closeHandlers.add(fn); }

  private terminate(error: Error) {
    if (this.closed) return;
    this.closed = true;
    this.rejectReady(error);
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
    for (const handler of this.closeHandlers) handler(error);
  }

  on(method: string, fn: (params: any) => void) {
    this.notifyHandlers.set(method, fn);
  }

  async call(method: string, params: any): Promise<any> {
    await this.ready;
    const id = "w" + ++this.seq;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      try { this.ws!.send(JSON.stringify({ jsonrpc: "2.0", id, method, params })); }
      catch (error) { this.pending.delete(id); reject(error); }
    });
  }

  close() {
    this.ws?.close();
    this.terminate(new Error("websocket closed"));
  }
}
