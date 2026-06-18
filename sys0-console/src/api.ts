// REST client for the sys0-hub API.

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
  interactive: boolean;
  paramsSchema: any;
};

const TOKEN_KEY = "sys0_token";
const ROLE_KEY = "sys0_role";
const USER_KEY = "sys0_user";

export const getToken = () => localStorage.getItem(TOKEN_KEY);
export const getRole = () => localStorage.getItem(ROLE_KEY) || "member";
export const getUser = () => localStorage.getItem(USER_KEY) || "";
export function setSession(t: string, role: string, username?: string) {
  localStorage.setItem(TOKEN_KEY, t);
  localStorage.setItem(ROLE_KEY, role);
  if (username !== undefined) localStorage.setItem(USER_KEY, username);
}
export function clearSession() {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(ROLE_KEY);
  localStorage.removeItem(USER_KEY);
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
    clearSession();
    location.reload();
    throw new Error("unauthorized");
  }
  return res.json() as Promise<T>;
}

export type Select = { nodes?: string[]; tags?: string[]; all?: boolean };

export const api = {
  login: (username: string, password: string) =>
    req<{ ok: boolean; token?: string; role?: string; username?: string; error?: string }>(
      "POST",
      "/api/v1/auth/login",
      { username, password }
    ),
  nodes: () => req<{ ok: boolean; nodes: Node[] }>("GET", "/api/v1/nodes"),
  methods: () => req<{ ok: boolean; methods: MethodSpec[] }>("GET", "/api/v1/methods"),
  dispatch: (select: Select, method: string, params: any = {}, dryRun = false) =>
    req<{ ok: boolean; items?: DispatchItem[]; error?: string; code?: number }>(
      "POST",
      "/api/v1/dispatch",
      { select, call: { method, params }, dryRun }
    ),
  // run on a single node, return its item (or throw)
  one: async (node: string, method: string, params: any = {}) => {
    const r = await api.dispatch({ nodes: [node] }, method, params);
    if (!r.ok) throw new Error(r.error || "dispatch failed");
    const it = r.items?.[0];
    if (!it) throw new Error("no result");
    if (!it.ok) throw new Error(it.error?.message || "node error");
    return it.value;
  },
  metrics: (node: string) =>
    req<{ ok: boolean; samples: any[] }>("GET", "/api/v1/metrics?node=" + encodeURIComponent(node)),
  audit: (limit = 80) =>
    req<{ ok: boolean; audit: any[] }>("GET", "/api/v1/audit?limit=" + limit),
  setLabel: (id: string, label: string, tags: string[]) =>
    req<{ ok: boolean }>("POST", `/api/v1/nodes/${id}/label`, { label, tags }),
  detach: (id: string) => req<{ ok: boolean }>("POST", `/api/v1/nodes/${id}/detach`, {}),
  deleteNode: (id: string) => req<{ ok: boolean; error?: string }>("DELETE", `/api/v1/nodes/${id}`),
  keysList: () => req<{ ok: boolean; keys: any[] }>("GET", "/api/v1/keys"),
  keyCreate: (body: any) =>
    req<{ ok: boolean; key?: string; id?: string; error?: string }>("POST", "/api/v1/keys", body),
  keyRevoke: (id: string) => req<{ ok: boolean }>("DELETE", "/api/v1/keys/" + id),

  // --- first-run setup ---
  setupStatus: () => req<{ ok: boolean; needsSetup: boolean }>("GET", "/api/v1/setup/status"),
  setup: (username: string, password: string) =>
    req<{ ok: boolean; token?: string; role?: string; username?: string; error?: string }>(
      "POST", "/api/v1/setup", { username, password }),

  // --- current user ---
  me: () => req<{ ok: boolean; user: User_ }>("GET", "/api/v1/me"),
  changeOwnPassword: (oldPassword: string, newPassword: string) =>
    req<{ ok: boolean; error?: string }>("POST", "/api/v1/me/password", { oldPassword, newPassword }),

  // --- user management (admin) ---
  usersList: () => req<{ ok: boolean; users: User_[] }>("GET", "/api/v1/users"),
  userCreate: (body: { username: string; password: string; role: string; nodeScope: string[] }) =>
    req<{ ok: boolean; user?: User_; error?: string }>("POST", "/api/v1/users", body),
  userSetScope: (id: number, nodeScope: string[]) =>
    req<{ ok: boolean; error?: string }>("POST", `/api/v1/users/${id}/scope`, { nodeScope }),
  userSetRole: (id: number, role: string) =>
    req<{ ok: boolean; error?: string }>("POST", `/api/v1/users/${id}/role`, { role }),
  userSetPassword: (id: number, password: string) =>
    req<{ ok: boolean; error?: string }>("POST", `/api/v1/users/${id}/password`, { password }),
  userDelete: (id: number) =>
    req<{ ok: boolean; error?: string }>("DELETE", `/api/v1/users/${id}`),

  // --- new-node default access policy (admin) ---
  getDefaultAccess: () => req<{ ok: boolean; users: string[] }>("GET", "/api/v1/settings/default-access"),
  setDefaultAccess: (users: string[]) =>
    req<{ ok: boolean; error?: string }>("POST", "/api/v1/settings/default-access", { users }),

  // --- agent downloads (/dl) ---
  releases: () => req<ReleaseList>("GET", "/api/v1/releases"),
};

export type User_ = {
  id: number;
  username: string;
  role: string; // admin | member
  nodeScope: string[];
  createdAt: number;
};

export type ReleaseAsset = {
  name: string; url: string; size: number; downloadCount: number; os: string; arch: string;
};
export type ReleaseList = {
  ok: boolean; error?: string; tag?: string; name?: string; releaseUrl?: string;
  publishedAt?: string; hubVersion?: string; assets: ReleaseAsset[];
};

// SSE stream of live node/metrics events.
export function eventStream(onEvent: (type: string, data: any) => void): EventSource {
  const tok = getToken() ?? "";
  const es = new EventSource(`/api/v1/events?topics=node,metrics&token=${encodeURIComponent(tok)}`);
  for (const name of ["event.node", "event.metrics"]) {
    es.addEventListener(name, (e) => {
      try { onEvent(name, JSON.parse((e as MessageEvent).data)); } catch {}
    });
  }
  return es;
}

// base64 helpers for binary-safe shell/file transfer
export function b64encode(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000)
    s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(s);
}
export function b64decode(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
