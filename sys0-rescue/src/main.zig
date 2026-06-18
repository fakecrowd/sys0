// sys0-rescue
//
// A tiny, standalone supervisor/bootstrapper for the sys0-agent.
//
// Responsibilities:
//   1. Bootstrap: ensure a sys0-agent binary exists locally. If it's missing
//      (or zero-length / corrupt), download the latest build matching this
//      host's OS/arch from the hub:
//          GET https://<hub>/api/v1/agent?os=<os>&arch=<arch>
//      The hub answers with a 302 redirect to the actual release asset, which
//      std.http.Client.fetch follows automatically — so this binary needs NO
//      JSON parser, keeping it tiny.
//   2. Daemon / keepalive: spawn the agent as a child and supervise it. If it
//      dies, restart it with capped exponential backoff.
//   3. Rescue: before every (re)start, re-validate the agent binary; if it has
//      gone missing/empty, re-download it. Periodically (every updateEvery
//      restarts of a long-lived agent is not needed — we re-check on each
//      respawn) the agent self-heals.
//
// Designed to be built tiny:
//   zig build-exe src/main.zig -O ReleaseSmall -target x86_64-linux-musl -fstrip
//
// Build-time defaults (override at link time, mirroring sys0-agent):
//   -ldflags equivalent:
//   -Dhub=sys0.facrd.xyz
// (passed through build options; see overridable consts below)

const std = @import("std");
const builtin = @import("builtin");

// ---- build-time configurable defaults -------------------------------------
// Overridable via `zig build-exe -Dkey=val` would require build.zig options;
// for a single-file build these are the compiled-in defaults. The hub is the
// only thing most installs need to change, and it can also be supplied at
// runtime with --hub / SYS0_HUB.
const default_hub = "sys0.facrd.xyz";

// ---- platform mapping (sys0 release asset naming) -------------------------
const os_name = switch (builtin.os.tag) {
    .linux => "linux",
    .macos => "darwin",
    .windows => "windows",
    else => @compileError("unsupported OS for sys0-rescue"),
};

const arch_name = switch (builtin.cpu.arch) {
    .x86_64 => "amd64",
    .aarch64 => "arm64",
    else => @compileError("unsupported arch for sys0-rescue"),
};

const agent_filename = if (builtin.os.tag == .windows) "sys0-agent.exe" else "sys0-agent";

// ---- config ---------------------------------------------------------------
const Config = struct {
    hub: []const u8 = default_hub,
    data_dir: []const u8 = "", // resolved at runtime
    once: bool = false, // run one bootstrap+spawn cycle then exit (testing)
    min_backoff_ms: u64 = 1_000,
    max_backoff_ms: u64 = 60_000,
    // If the agent stays up at least this long, reset backoff to minimum.
    healthy_uptime_ms: u64 = 30_000,
};

// ---- logging --------------------------------------------------------------
fn logLine(comptime fmt: []const u8, args: anytype) void {
    const stderr = std.fs.File.stderr();
    var buf: [512]u8 = undefined;
    const line = std.fmt.bufPrint(&buf, "[sys0-rescue] " ++ fmt ++ "\n", args) catch return;
    _ = stderr.writeAll(line) catch {};
}

// ---- agent binary validity check ------------------------------------------
fn agentLooksValid(path: []const u8) bool {
    const f = std.fs.cwd().openFile(path, .{}) catch return false;
    defer f.close();
    const st = f.stat() catch return false;
    // A real agent binary is well over 1 MiB; anything tiny is a failed
    // download / placeholder and should be re-fetched.
    return st.size > 512 * 1024;
}

// ---- download the latest matching agent from the hub ----------------------
// Writes to <data_dir>/sys0-agent.tmp, then renames into place atomically so a
// half-written download can never be executed.
fn downloadAgent(gpa: std.mem.Allocator, cfg: Config, dest_path: []const u8) !void {
    var url_buf: [256]u8 = undefined;
    const url = try std.fmt.bufPrint(&url_buf, "https://{s}/api/v1/agent?os={s}&arch={s}", .{ cfg.hub, os_name, arch_name });

    logLine("downloading agent: {s}", .{url});

    var tmp_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const tmp_path = try std.fmt.bufPrint(&tmp_path_buf, "{s}{c}sys0-agent.tmp", .{ cfg.data_dir, std.fs.path.sep });

    // Open the destination temp file and stream the HTTP body straight into it.
    var out_file = try std.fs.cwd().createFile(tmp_path, .{ .truncate = true });
    var file_closed = false;
    defer if (!file_closed) out_file.close();

    var write_buf: [64 * 1024]u8 = undefined;
    var fw = out_file.writer(&write_buf);

    var client: std.http.Client = .{ .allocator = gpa };
    defer client.deinit();

    const res = client.fetch(.{
        .location = .{ .url = url },
        .method = .GET,
        .response_writer = &fw.interface,
        // follow the hub's 302 to the release asset (and any GitHub CDN hops)
        .redirect_behavior = @enumFromInt(10),
    }) catch |err| {
        logLine("fetch error: {s}", .{@errorName(err)});
        return err;
    };

    try fw.interface.flush();
    out_file.close();
    file_closed = true;

    if (res.status != .ok) {
        logLine("hub returned HTTP {d}", .{@intFromEnum(res.status)});
        std.fs.cwd().deleteFile(tmp_path) catch {};
        return error.BadHttpStatus;
    }

    // Make executable (no-op semantics on Windows) before swapping into place.
    if (builtin.os.tag != .windows) {
        const f = try std.fs.cwd().openFile(tmp_path, .{});
        defer f.close();
        try f.chmod(0o755);
    }

    // Atomic swap: rename tmp -> final.
    try std.fs.cwd().rename(tmp_path, dest_path);

    if (!agentLooksValid(dest_path)) {
        logLine("downloaded agent failed validity check", .{});
        return error.DownloadInvalid;
    }
    logLine("agent installed: {s}", .{dest_path});
}

// ---- ensure an agent binary is present, downloading if needed -------------
fn ensureAgent(gpa: std.mem.Allocator, cfg: Config, dest_path: []const u8) !void {
    if (agentLooksValid(dest_path)) return;
    logLine("agent missing or invalid — fetching from hub", .{});
    // a couple of retries for transient network failures
    var attempt: u8 = 0;
    while (attempt < 3) : (attempt += 1) {
        downloadAgent(gpa, cfg, dest_path) catch |err| {
            logLine("download attempt {d} failed: {s}", .{ attempt + 1, @errorName(err) });
            std.Thread.sleep(2 * std.time.ns_per_s);
            continue;
        };
        return;
    }
    return error.AgentUnavailable;
}

// ---- supervise: spawn agent, wait, restart with backoff -------------------
fn supervise(gpa: std.mem.Allocator, cfg: Config, agent_path: []const u8) !void {
    var backoff: u64 = cfg.min_backoff_ms;

    while (true) {
        // RESCUE: re-validate (and re-download) before every (re)start.
        ensureAgent(gpa, cfg, agent_path) catch |err| {
            logLine("cannot obtain agent ({s}); retrying in {d}ms", .{ @errorName(err), backoff });
            std.Thread.sleep(backoff * std.time.ns_per_ms);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };

        logLine("starting agent: {s} --data-dir {s}", .{ agent_path, cfg.data_dir });

        var child = std.process.Child.init(&.{
            agent_path,
            "--data-dir",
            cfg.data_dir,
        }, gpa);
        child.stdin_behavior = .Ignore;
        child.stdout_behavior = .Inherit;
        child.stderr_behavior = .Inherit;

        const start_ms = std.time.milliTimestamp();

        child.spawn() catch |err| {
            logLine("spawn failed: {s}", .{@errorName(err)});
            // Spawn failure often means a corrupt binary — force re-download.
            std.fs.cwd().deleteFile(agent_path) catch {};
            std.Thread.sleep(backoff * std.time.ns_per_ms);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };

        if (cfg.once) {
            logLine("--once: agent spawned (pid started), exiting supervisor", .{});
            // Detach: leave the child running, don't wait.
            return;
        }

        const term = child.wait() catch |err| {
            logLine("wait failed: {s}", .{@errorName(err)});
            std.Thread.sleep(backoff * std.time.ns_per_ms);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };

        const uptime_ms: u64 = @intCast(@max(0, std.time.milliTimestamp() - start_ms));

        switch (term) {
            .Exited => |code| logLine("agent exited code={d} after {d}ms", .{ code, uptime_ms }),
            .Signal => |s| logLine("agent killed by signal={d} after {d}ms", .{ s, uptime_ms }),
            .Stopped => |s| logLine("agent stopped signal={d}", .{s}),
            .Unknown => |u| logLine("agent ended unknown={d}", .{u}),
        }

        // If it ran healthily for a while, reset backoff. Otherwise grow it so
        // a crash-looping agent doesn't hammer the box / the hub.
        if (uptime_ms >= cfg.healthy_uptime_ms) {
            backoff = cfg.min_backoff_ms;
        } else {
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
        }

        logLine("restarting agent in {d}ms", .{backoff});
        std.Thread.sleep(backoff * std.time.ns_per_ms);
    }
}

// ---- data dir resolution --------------------------------------------------
// Pick a stable, writable per-user dir (mirrors the agent's own choice so they
// share identity/lock files). Order: explicit --data-dir > $SYS0_DATA_DIR >
// platform user-config dir > ./sys0-agent
fn resolveDataDir(gpa: std.mem.Allocator, explicit: []const u8) ![]const u8 {
    if (explicit.len > 0) return gpa.dupe(u8, explicit);

    if (std.process.getEnvVarOwned(gpa, "SYS0_DATA_DIR")) |v| {
        return v;
    } else |_| {}

    // Platform user-config base.
    const base_env: ?[]const u8 = switch (builtin.os.tag) {
        .windows => "APPDATA",
        .macos => "HOME", // -> ~/Library/Application Support
        else => "XDG_CONFIG_HOME", // fallback to HOME below
    };

    if (base_env) |env_key| {
        if (std.process.getEnvVarOwned(gpa, env_key)) |base| {
            defer gpa.free(base);
            const sub = switch (builtin.os.tag) {
                .macos => "/Library/Application Support/sys0-agent",
                else => "/sys0-agent",
            };
            return std.fmt.allocPrint(gpa, "{s}{s}", .{ base, sub });
        } else |_| {}
    }

    // Fallback: $HOME/.config/sys0-agent on unix.
    if (std.process.getEnvVarOwned(gpa, "HOME")) |home| {
        defer gpa.free(home);
        return std.fmt.allocPrint(gpa, "{s}/.config/sys0-agent", .{home});
    } else |_| {}

    return gpa.dupe(u8, "sys0-agent");
}

// ---- arg parsing ----------------------------------------------------------
fn parseArgs(gpa: std.mem.Allocator, cfg: *Config) !void {
    var it = try std.process.argsWithAllocator(gpa);
    defer it.deinit();
    _ = it.next(); // argv[0]
    while (it.next()) |arg| {
        if (std.mem.eql(u8, arg, "--hub")) {
            cfg.hub = try gpa.dupe(u8, it.next() orelse return error.MissingValue);
        } else if (std.mem.eql(u8, arg, "--data-dir")) {
            cfg.data_dir = try gpa.dupe(u8, it.next() orelse return error.MissingValue);
        } else if (std.mem.eql(u8, arg, "--once")) {
            cfg.once = true;
        } else if (std.mem.eql(u8, arg, "--help") or std.mem.eql(u8, arg, "-h")) {
            printUsage();
            std.process.exit(0);
        }
    }
    // env override for hub if not set on CLI
    if (std.mem.eql(u8, cfg.hub, default_hub)) {
        if (std.process.getEnvVarOwned(gpa, "SYS0_HUB")) |v| {
            cfg.hub = v;
        } else |_| {}
    }
}

fn printUsage() void {
    logLine(
        \\usage: sys0-rescue [--hub HOST] [--data-dir DIR] [--once]
        \\  --hub HOST       hub hostname (default {s}, env SYS0_HUB)
        \\  --data-dir DIR   agent run dir (env SYS0_DATA_DIR; default per-user)
        \\  --once           bootstrap + spawn once, then exit (no supervision)
    , .{default_hub});
}

pub fn main() !void {
    var gpa_state = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa_state.deinit();
    const gpa = gpa_state.allocator();

    var cfg = Config{};
    try parseArgs(gpa, &cfg);

    const dir = try resolveDataDir(gpa, cfg.data_dir);
    cfg.data_dir = dir;

    // Ensure the data dir exists.
    std.fs.cwd().makePath(cfg.data_dir) catch |err| {
        logLine("cannot create data dir {s}: {s}", .{ cfg.data_dir, @errorName(err) });
        return err;
    };

    var agent_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const agent_path = try std.fmt.bufPrint(&agent_path_buf, "{s}{c}{s}", .{ cfg.data_dir, std.fs.path.sep, agent_filename });

    logLine("starting · hub={s} os={s} arch={s} data_dir={s}", .{ cfg.hub, os_name, arch_name, cfg.data_dir });

    try supervise(gpa, cfg, agent_path);
}
