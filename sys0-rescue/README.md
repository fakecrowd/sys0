# sys0-rescue

A tiny, standalone supervisor/bootstrapper for `sys0-agent`, written in Zig for
the smallest possible static binary (~0.5 MB, fully static, no libc dependency).

It is intentionally **independent of the agent and hub processes** so it can
recover them even when they are broken.

## What it does

1. **Bootstrap** — ensures a `sys0-agent` binary exists in the data dir. If it's
   missing or looks invalid (truncated/corrupt), it downloads the latest build
   matching this host's OS/arch from the hub:

   ```
   GET https://<hub>/api/v1/agent?os=<os>&arch=<arch>
   ```

   The hub replies with a **302 redirect** to the matching GitHub release asset,
   which the client follows automatically — so this binary carries **no JSON
   parser**, keeping it tiny. The download is streamed to `sys0-agent.tmp` and
   atomically renamed into place, so a half-written file can never be executed.

2. **Daemon / keepalive** — spawns the agent as a child and supervises it.
   On exit it restarts with capped exponential backoff (1s → 60s). A process
   that stays healthy for >=30s resets the backoff, so a crash-looping agent
   can't hammer the box or the hub.

3. **Rescue** — before every (re)start it re-validates the agent binary and
   re-downloads if it's gone missing or corrupt. A spawn failure (e.g. a corrupt
   executable) deletes the binary to force a clean re-fetch.

## Usage

```
sys0-rescue [--hub HOST] [--data-dir DIR] [--once]
  --hub HOST       hub hostname (default sys0.facrd.xyz, env SYS0_HUB)
  --data-dir DIR   agent run dir (env SYS0_DATA_DIR; default per-user config dir)
  --once           bootstrap + spawn once, then exit (no supervision; for tests)
```

The default data dir mirrors the agent's own choice (per-user config dir) so the
two share identity/lock files.

## Build

Requires Zig 0.15.x.

```sh
make small      # x86_64-linux-musl, ReleaseSmall, stripped, static (~0.5 MB)
make native     # host target
make all        # cross-build the full sys0 matrix into dist/
```

The musl build is fully static (`ldd` → "not a dynamic executable") so it runs
on any Linux even if the system libc is broken — exactly what a rescue tool
needs. HTTPS uses Zig's bundled TLS + the system CA store (`/etc/ssl/certs`), so
no OpenSSL dependency.

## Why Zig (not Go like the rest of sys0)

The agent/hub are Go (GC + runtime → ~2 MB binary floor). A deploy/daemon/rescue
helper wants the opposite: minimal size, zero dynamic deps, fast cold start.
Zig `ReleaseSmall` + static musl delivers ~0.5 MB with no libc dependency.
