// REST client for the sys0-hub API.

export type RescueCommand = {
  id: string;
  kind: string; // update-agent | restart-agent
  status: string; // pending | acked | running | done | error
  detail: string;
  createdAt: number;
  updatedAt: number;
};

export type TraceEvent = {
  t: number; // unix seconds
  m: string; // message
};

export type RescueInfo = {
  live: boolean;
  version: string;
  status: string; // phase: starting|downloading|starting-agent|supervising|restarting|error
  detail: string;
  restarts: number;
  lastExit: number;
  lastUptimeMs: number;
  cwd?: string; // rescue work dir (download/stage/decoy location)
  agentPid?: number; // pid of the supervised agent (-1 = none)
  trace?: TraceEvent[]; // recent rescue activity (agent startup sequence)
  sinceSec: number; // continuous-reporting uptime
  ageSec: number; // seconds since last report
  commands?: RescueCommand[]; // recent operator commands + their status
};

export type ModuleView = {
  name: string;
  online: boolean;
  version?: string;
};

export type Node = {
  id: string;
  label: string;
  tags: string[];
  host: { name: string; os: string; arch: string; kernel: string; ip: string };
  version: string;
  state: string;
  lastSeen: number;
  agentCwd?: string; // agent's working directory
  agentPid?: number; // agent's own pid
  modules?: ModuleView[]; // per-module connection state
  rescue?: boolean;
  rescueVersion?: string;
  rescueInfo?: RescueInfo | null;
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
const CRED_KEY = "sys0_cred"; // remembered credentials (obfuscated)

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

// --- "remember password": store creds locally so a 12h token expiry is
// transparently refreshed via silent re-login. Obfuscated (base64), NOT
// encryption — localStorage is already same-origin; this just avoids
// plaintext-at-a-glance. The browser's own password manager is the real vault.
export function rememberCreds(username: string, password: string) {
  try { localStorage.setItem(CRED_KEY, btoa(unescape(encodeURIComponent(JSON.stringify([username, password]))))); } catch {}
}
export function forgetCreds() { localStorage.removeItem(CRED_KEY); }
export function hasRemembered() { return !!localStorage.getItem(CRED_KEY); }
export function getRememberedUser(): string {
  const c = readCreds(); return c ? c[0] : "";
}
function readCreds(): [string, string] | null {
  const raw = localStorage.getItem(CRED_KEY);
  if (!raw) return null;
  try { const a = JSON.parse(decodeURIComponent(escape(atob(raw)))); return Array.isArray(a) && a.length === 2 ? [a[0], a[1]] : null; } catch { return null; }
}

let reloginPromise: Promise<boolean> | null = null;
// Attempt a silent re-login using remembered creds. De-duped so concurrent
// 401s only fire one login. Returns true if a fresh token was installed.
async function trySilentRelogin(): Promise<boolean> {
  if (reloginPromise) return reloginPromise;
  const creds = readCreds();
  if (!creds) return false;
  reloginPromise = (async () => {
    try {
      const res = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: creds[0], password: creds[1] }),
      });
      const r = await res.json();
      if (r && r.ok && r.token) {
        setSession(r.token, r.role || "member", r.username || creds[0]);
        return true;
      }
    } catch {}
    forgetCreds(); // creds no longer valid (password changed etc.)
    return false;
  })();
  try { return await reloginPromise; } finally { reloginPromise = null; }
}

async function req<T>(method: string, path: string, body?: any, _retried = false): Promise<T> {
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
    // Token expired (12h TTL). If we have remembered creds, silently
    // re-login and replay the request once before giving up.
    if (!_retried && (await trySilentRelogin())) {
      return req<T>(method, path, body, true);
    }
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
  dismissRescue: (id: string) => req<{ ok: boolean; error?: string }>("POST", `/api/v1/nodes/${id}/dismiss-rescue`, {}),
  rescueCommand: (id: string, kind: string) =>
    req<{ ok: boolean; error?: string; command?: any }>("POST", `/api/v1/nodes/${id}/rescue-command`, { kind }),
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

  // --- hub release-binary cache ---
  cacheStatus: () => req<CacheStatus>("GET", "/api/v1/cache"),
  cacheRefresh: () =>
    req<{ ok: boolean; refreshed: number; failed?: string[]; status: CacheStatus }>("POST", "/api/v1/cache/refresh", {}),
};

export type User_ = {
  id: number;
  username: string;
  role: string; // admin | member
  nodeScope: string[];
  createdAt: number;
};

export type ReleaseAsset = {
  name: string; url: string; size: number; downloadCount: number; os: string; arch: string; kind?: string;
};
export type ReleaseList = {
  ok: boolean; error?: string; tag?: string; name?: string; releaseUrl?: string;
  publishedAt?: string; hubVersion?: string; assets: ReleaseAsset[];
};

export type CachedAsset = {
  name: string; kind: string; os: string; arch: string; url: string;
  cached: boolean; size?: number; ageSec?: number;
};
export type CacheStatus = {
  ok: boolean; tag?: string; releaseName?: string; releaseUrl?: string;
  publishedAt?: string; releaseAgeSec?: number; hubVersion?: string;
  assets: CachedAsset[]; cachedCount: number; totalCount: number;
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
