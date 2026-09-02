export const RECENT_PREFIX = "sys0_recent_v1:";
export const LEGACY_SCREENSHOT_PREFIX = "sys0_shots_v1:";
export const RECENT_TTL_MS = 7 * 24 * 60 * 60 * 1000;
// Conservative character budget for all operational history on this origin.
export const RECENT_MAX_BYTES = 2 * 1024 * 1024;

export type StorageLike = {
  readonly length: number;
  key(index: number): string | null;
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
};

export type RecentValue<T> = {
  savedAt: number;
  data: T;
};

type Entry = { key: string; savedAt: number; size: number };

export function canFocusNode(state: string): boolean {
  return state !== "bootstrapping";
}

function recentKey(account: string, node: string, surface: string): string {
  return `${RECENT_PREFIX}${encodeURIComponent(account)}:${node}:${surface}`;
}

function accountIsActive(storage: StorageLike, account: string): boolean {
  return !!account && storage.getItem("sys0_user") === account;
}

function keys(storage: StorageLike): string[] {
  const out: string[] = [];
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i);
    if (key) out.push(key);
  }
  return out;
}

function entries(storage: StorageLike, now: number): Entry[] {
  const out: Entry[] = [];
  for (const key of keys(storage)) {
    if (!key.startsWith(RECENT_PREFIX)) continue;
    const raw = storage.getItem(key);
    try {
      if (!raw) throw new Error("missing history");
      const value = JSON.parse(raw) as RecentValue<unknown>;
      if (!value || typeof value.savedAt !== "number" || !("data" in value)) throw new Error("invalid history");
      if (now - value.savedAt > RECENT_TTL_MS) throw new Error("expired history");
      out.push({ key, savedAt: value.savedAt, size: raw.length });
    } catch {
      try { storage.removeItem(key); } catch {}
    }
  }
  return out.sort((a, b) => a.savedAt - b.savedAt || a.key.localeCompare(b.key));
}

export function sweepRecent(storage: StorageLike, now = Date.now(), maxBytes = RECENT_MAX_BYTES): void {
  const found = entries(storage, now);
  let total = found.reduce((sum, entry) => sum + entry.size, 0);
  for (const entry of found) {
    if (total <= maxBytes) break;
    try { storage.removeItem(entry.key); } catch {}
    total -= entry.size;
  }
}

export function takeLegacyScreenshotHistory<T>(storage: StorageLike, node: string): T | null {
  const key = `${LEGACY_SCREENSHOT_PREFIX}${node}`;
  try {
    const raw = storage.getItem(key);
    storage.removeItem(key);
    return raw ? JSON.parse(raw) as T : null;
  } catch {
    try { storage.removeItem(key); } catch {}
    return null;
  }
}


export function migrateLegacyScreenshotHistory(storage: StorageLike, account: string, now = Date.now()): void {
  if (!accountIsActive(storage, account)) return;
  for (const key of keys(storage)) {
    if (!key.startsWith(LEGACY_SCREENSHOT_PREFIX)) continue;
    const node = key.slice(LEGACY_SCREENSHOT_PREFIX.length);
    try {
      const raw = storage.getItem(key);
      const shots = raw ? JSON.parse(raw) as Array<{ ts?: number }> : [];
      const savedAt = shots.reduce((latest, shot) => Math.max(latest, typeof shot.ts === "number" ? shot.ts : now), 0);
      if (savedAt && now - savedAt <= RECENT_TTL_MS) saveRecent(storage, account, node, "screenshots", { shots }, savedAt);
    } catch {}
    try { storage.removeItem(key); } catch {}
  }
  sweepRecent(storage, now);
}

export function clearRecent(storage: StorageLike): void {
  for (const key of keys(storage)) {
    if (key.startsWith(RECENT_PREFIX) || key.startsWith(LEGACY_SCREENSHOT_PREFIX)) {
      try { storage.removeItem(key); } catch {}
    }
  }
}

export function saveRecent<T>(storage: StorageLike, account: string, node: string, surface: string, data: T, now = Date.now()): boolean {
  if (!accountIsActive(storage, account)) return false;
  const key = recentKey(account, node, surface);
  const raw = JSON.stringify({ savedAt: now, data });
  if (raw.length > RECENT_MAX_BYTES) {
    try { storage.removeItem(key); } catch {}
    return false;
  }

  const found = entries(storage, now).filter((entry) => entry.key !== key);
  let total = found.reduce((sum, entry) => sum + entry.size, 0);
  for (const entry of found) {
    if (total + raw.length <= RECENT_MAX_BYTES) break;
    try { storage.removeItem(entry.key); } catch {}
    total -= entry.size;
  }

  const retry = entries(storage, now).filter((entry) => entry.key !== key);
  for (;;) {
    try {
      storage.setItem(key, raw);
      return true;
    } catch {
      const oldest = retry.shift();
      if (!oldest) {
        try { storage.removeItem(key); } catch {}
        try { storage.setItem(key, raw); return true; } catch { return false; }
      }
      try { storage.removeItem(oldest.key); } catch {}
    }
  }
}

export function loadRecent<T>(storage: StorageLike, account: string, node: string, surface: string, now = Date.now()): RecentValue<T> | null {
  if (!accountIsActive(storage, account)) return null;
  const key = recentKey(account, node, surface);
  try {
    const raw = storage.getItem(key);
    if (!raw) return null;
    const value = JSON.parse(raw) as RecentValue<T>;
    if (!value || typeof value.savedAt !== "number" || !("data" in value)) throw new Error("invalid history");
    if (now - value.savedAt > RECENT_TTL_MS) {
      storage.removeItem(key);
      return null;
    }
    return value;
  } catch {
    try { storage.removeItem(key); } catch {}
    return null;
  }
}

type TimerHandle = ReturnType<typeof setTimeout>;
type TimerApi = {
  set(fn: () => void, delay: number): TimerHandle | number;
  clear(handle: TimerHandle | number): void;
};

export function createThrottledSaver<T>(
  save: (value: T) => void,
  delay = 1_000,
  timers: TimerApi = { set: (fn, ms) => setTimeout(fn, ms), clear: (handle) => clearTimeout(handle as TimerHandle) },
) {
  let timer: TimerHandle | number | null = null;
  let latest: T;
  let pending = false;
  const flush = () => {
    if (timer !== null) timers.clear(timer);
    timer = null;
    if (!pending) return;
    pending = false;
    save(latest);
  };
  return {
    schedule(value: T) {
      latest = value;
      pending = true;
      if (timer === null) timer = timers.set(() => { timer = null; flush(); }, delay);
    },
    flush,
    dispose: flush,
    cancel() {
      if (timer !== null) timers.clear(timer);
      timer = null;
      pending = false;
    },
  };
}
