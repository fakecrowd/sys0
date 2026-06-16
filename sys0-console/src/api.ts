// Thin REST client for the sys0-hub API.

export type Node = {
  id: string;
  label: string;
  tags: string[];
  host: { name: string; os: string; arch: string; kernel: string; ip: string };
  version: string;
  state: string;
  lastSeen: number;
};

export type DispatchItem = {
  node: string;
  ok: boolean;
  value?: any;
  error?: { code: number; message: string };
};

export type MethodSpec = {
  name: string;
  scope: string;
  description: string;
  dangerous: boolean;
  paramsSchema: any;
};

const TOKEN_KEY = "sys0_token";

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}
export function setToken(t: string) {
  localStorage.setItem(TOKEN_KEY, t);
}
export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

async function req<T>(method: string, path: string, body?: any): Promise<T> {
  const headers: Record<string, string> = {};
  const tok = getToken();
  if (tok) headers["Authorization"] = "Bearer " + tok;
  if (body) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (res.status === 401) {
    clearToken();
    throw new Error("unauthorized");
  }
  return res.json() as Promise<T>;
}

export const api = {
  login: (username: string, password: string) =>
    req<{ ok: boolean; token?: string; role?: string; error?: string }>(
      "POST",
      "/api/v1/auth/login",
      { username, password }
    ),
  nodes: () => req<{ ok: boolean; nodes: Node[] }>("GET", "/api/v1/nodes"),
  methods: () =>
    req<{ ok: boolean; methods: MethodSpec[] }>("GET", "/api/v1/methods"),
  dispatch: (params: any) =>
    req<{ ok: boolean; items?: DispatchItem[]; error?: string; code?: number }>(
      "POST",
      "/api/v1/dispatch",
      params
    ),
  metrics: (node: string) =>
    req<{ ok: boolean; samples: any[] }>(
      "GET",
      "/api/v1/metrics?node=" + encodeURIComponent(node)
    ),
  audit: (limit = 50) =>
    req<{ ok: boolean; audit: any[] }>("GET", "/api/v1/audit?limit=" + limit),
};

// SSE stream of live events. Token passed via query (EventSource can't set headers).
export function eventStream(topics: string[], onEvent: (type: string, data: any) => void): EventSource {
  const tok = getToken() ?? "";
  const es = new EventSource(
    `/api/v1/events?topics=${topics.join(",")}&token=${encodeURIComponent(tok)}`
  );
  const wire = (name: string) =>
    es.addEventListener(name, (e) => {
      try {
        onEvent(name, JSON.parse((e as MessageEvent).data));
      } catch {
        /* ignore */
      }
    });
  wire("event.node");
  wire("event.metrics");
  return es;
}
