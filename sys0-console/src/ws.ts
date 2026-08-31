// Minimal JSON-RPC client over the console WebSocket, used for the low-latency
// interactive shell (and live event delivery).
import { getToken } from "./api";

type Pending = { resolve: (v: any) => void; reject: (e: any) => void };

export class WSClient {
  private ws?: WebSocket;
  private seq = 0;
  private pending = new Map<string, Pending>();
  private notifyHandlers = new Map<string, (params: any) => void>();
  private ready: Promise<void>;
  private resolveReady!: () => void;

  constructor() {
    this.ready = new Promise((r) => (this.resolveReady = r));
  }

  connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const tok = getToken() ?? "";
    this.ws = new WebSocket(`${proto}://${location.host}/ws?token=${encodeURIComponent(tok)}`);
    this.ws.onopen = () => this.resolveReady();
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

  on(method: string, fn: (params: any) => void) {
    this.notifyHandlers.set(method, fn);
  }

  async call(method: string, params: any): Promise<any> {
    await this.ready;
    const id = "w" + ++this.seq;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws!.send(JSON.stringify({ jsonrpc: "2.0", id, method, params }));
    });
  }

  close() {
    this.ws?.close();
  }
}
