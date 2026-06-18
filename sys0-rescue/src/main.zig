// sys0-rescue
//
// A tiny, standalone supervisor/bootstrapper for the sys0-agent. Written in Zig
// (0.16) for the smallest possible static binary.
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
//      gone missing/empty, re-download it.
//   4. Autostart install: register itself to start on boot/login (and thereby
//      the agent too, since rescue supervises it). Auto-detects privilege:
//      root/admin -> system-wide unit; otherwise -> per-user unit (no admin).
//      Cross-platform: systemd (system+user) / launchd / Windows registry+task.
//
// Build tiny:
//   zig build-exe src/main.zig -O ReleaseSmall -target x86_64-linux-musl -fstrip

const std = @import("std");
const builtin = @import("builtin");
const install = @import("install.zig");

const Io = std.Io;

// ---- build-time configurable defaults -------------------------------------
// Overridable at link time via -ldflags-equivalent. The /dl rescue is built
// with the hosted hub + access key baked in (matching the /dl agent), so a
// zero-arg launch reports to the hub. Single-file builds keep these defaults.
const default_hub = "sys0.facrd.xyz";
// default_key matches the agent's pre-shared key so the rescue can report to the
// hub (the hub validates it the same way it validates agent node.hello).
pub const default_key = "devkey";
// rescue_version is injected at link time (-X-style not available in single-file
// builds; release.yml builds patch this via a generated file or leaves "dev").
pub const rescue_version = "dev";

// ---- platform mapping (sys0 release asset naming) -------------------------
pub const os_name = switch (builtin.os.tag) {
    .linux => "linux",
    .macos => "darwin",
    .windows => "windows",
    else => @compileError("unsupported OS for sys0-rescue"),
};

pub const arch_name = switch (builtin.cpu.arch) {
    .x86_64 => "amd64",
    .aarch64 => "arm64",
    else => @compileError("unsupported arch for sys0-rescue"),
};

pub const agent_filename = if (builtin.os.tag == .windows) "sys0-agent.exe" else "sys0-agent";
pub const sep = std.fs.path.sep;

// ---- config ---------------------------------------------------------------
pub const Config = struct {
    hub: []const u8 = default_hub,
    key: []const u8 = default_key,
    data_dir: []const u8 = "",
    once: bool = false,
    min_backoff_ms: u64 = 1_000,
    max_backoff_ms: u64 = 60_000,
    healthy_uptime_ms: u64 = 30_000,
    report_every_s: u64 = 30,
};

const Action = enum { run, install, uninstall, help };

// ---- logging --------------------------------------------------------------
// On normal (console) builds we log to stderr. On Windows GUI-subsystem builds
// (the /dl rescue, linked with --subsystem windows) there is NO console/stderr,
// so logs are also appended to <data_dir>/sys0-rescue.log once the data dir is
// known. setLogIo wires up the io handle + path after startup.
var log_io: ?Io = null;
var log_path: ?[]const u8 = null;

pub fn setLogSink(io: Io, path: []const u8) void {
    log_io = io;
    log_path = path;
}

pub fn logLine(comptime fmt: []const u8, args: anytype) void {
    // Console builds: stderr (std.debug.print is a no-op-safe stderr writer).
    if (builtin.os.tag != .windows) {
        std.debug.print("[sys0-rescue] " ++ fmt ++ "\n", args);
    }
    // File sink (always on Windows, where there's no stderr; harmless elsewhere
    // but we skip it on non-Windows to avoid double-logging).
    if (builtin.os.tag == .windows) {
        logToFile("[sys0-rescue] " ++ fmt ++ "\n", args);
    }
}

fn logToFile(comptime fmt: []const u8, args: anytype) void {
    const io = log_io orelse return;
    const path = log_path orelse return;
    var buf: [1024]u8 = undefined;
    const line = std.fmt.bufPrint(&buf, fmt, args) catch return;
    const cwd = Io.Dir.cwd();
    // append mode: open existing or create
    const f = cwd.openFile(io, path, .{ .mode = .write_only }) catch
        (cwd.createFile(io, path, .{ .truncate = false }) catch return);
    defer f.close(io);
    const end = f.stat(io) catch {
        return;
    };
    var wbuf: [1024]u8 = undefined;
    var w = f.writerStreaming(io, &wbuf);
    w.seekTo(end.size) catch {};
    w.interface.writeAll(line) catch {};
    w.interface.flush() catch {};
}

// ---- agent binary validity check ------------------------------------------
fn agentLooksValid(io: Io, path: []const u8) bool {
    const f = Io.Dir.cwd().openFile(io, path, .{}) catch return false;
    defer f.close(io);
    const st = f.stat(io) catch return false;
    // A real agent binary is well over 1 MiB; anything tiny is a failed
    // download / placeholder and should be re-fetched.
    return st.size > 512 * 1024;
}

// ---- download the latest matching agent from the hub ----------------------
fn downloadAgent(gpa: std.mem.Allocator, io: Io, cfg: Config, dest_path: []const u8) !void {
    var url_buf: [256]u8 = undefined;
    const url = try std.fmt.bufPrint(&url_buf, "https://{s}/api/v1/agent?os={s}&arch={s}", .{ cfg.hub, os_name, arch_name });
    logLine("downloading agent: {s}", .{url});

    var tmp_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const tmp_path = try std.fmt.bufPrint(&tmp_path_buf, "{s}{c}sys0-agent.tmp", .{ cfg.data_dir, sep });

    const cwd = Io.Dir.cwd();
    var out_file = try cwd.createFile(io, tmp_path, .{ .truncate = true });
    var file_closed = false;
    defer if (!file_closed) out_file.close(io);

    var write_buf: [64 * 1024]u8 = undefined;
    var fw = out_file.writer(io, &write_buf);

    var client: std.http.Client = .{ .allocator = gpa, .io = io };
    defer client.deinit();

    const res = client.fetch(.{
        .location = .{ .url = url },
        .method = .GET,
        .response_writer = &fw.interface,
        .redirect_behavior = @enumFromInt(10), // follow hub 302 + CDN hops
    }) catch |err| {
        logLine("fetch error: {s}", .{@errorName(err)});
        return err;
    };

    try fw.interface.flush();
    out_file.close(io);
    file_closed = true;

    if (res.status != .ok) {
        logLine("hub returned HTTP {d}", .{@intFromEnum(res.status)});
        cwd.deleteFile(io, tmp_path) catch {};
        return error.BadHttpStatus;
    }

    if (builtin.os.tag != .windows) {
        const f = try cwd.openFile(io, tmp_path, .{ .mode = .read_only });
        defer f.close(io);
        f.setPermissions(io, .fromMode(0o755)) catch |err| {
            logLine("chmod warn: {s}", .{@errorName(err)});
        };
    }

    try cwd.rename(tmp_path, cwd, dest_path, io);

    if (!agentLooksValid(io, dest_path)) {
        logLine("downloaded agent failed validity check", .{});
        return error.DownloadInvalid;
    }
    logLine("agent installed: {s}", .{dest_path});
}

fn ensureAgent(gpa: std.mem.Allocator, io: Io, cfg: Config, dest_path: []const u8) !void {
    if (agentLooksValid(io, dest_path)) return;
    logLine("agent missing or invalid — fetching from hub", .{});
    var attempt: u8 = 0;
    while (attempt < 3) : (attempt += 1) {
        downloadAgent(gpa, io, cfg, dest_path) catch |err| {
            logLine("download attempt {d} failed: {s}", .{ attempt + 1, @errorName(err) });
            sleepMs(io, 2000);
            continue;
        };
        return;
    }
    return error.AgentUnavailable;
}

fn sleepMs(io: Io, ms: u64) void {
    Io.sleep(io, Io.Duration.fromMilliseconds(@intCast(ms)), .awake) catch {};
}

// ---- hub reporting (rescue <-> hub binding) -------------------------------
// Minimal hub link: once the supervised agent has produced its identity file
// (sys0-agent.id — only created after the agent runs), the rescue POSTs a small
// report to the hub keyed by that fingerprint. The hub derives the SAME node id
// the agent uses (n + fingerprint[:6]) and marks the node as having a live
// rescue, so the pairing shows up in the console. This deliberately reuses the
// agent's pre-shared key and needs no WebSocket — basic functionality only.
//
// Runs on its own thread (io.concurrent) so it doesn't block the supervise loop.
fn reportLoop(io: Io, gpa: std.mem.Allocator, cfg: Config) void {
    var id_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const id_path = std.fmt.bufPrint(&id_path_buf, "{s}{c}sys0-agent.id", .{ cfg.data_dir, sep }) catch return;

    while (true) {
        sleepMs(io, cfg.report_every_s * 1000);
        // Read the agent's fingerprint. Absent => agent hasn't run yet; skip
        // this cycle (this is exactly the "after the agent connects" binding).
        const fp = readAgentFingerprint(io, gpa, id_path) catch continue;
        defer gpa.free(fp);
        if (fp.len < 6) continue;
        postReport(gpa, io, cfg, fp) catch |err| {
            logLine("report failed: {s}", .{@errorName(err)});
        };
    }
}

fn readAgentFingerprint(io: Io, gpa: std.mem.Allocator, path: []const u8) ![]u8 {
    const f = try Io.Dir.cwd().openFile(io, path, .{ .mode = .read_only });
    defer f.close(io);
    const st = try f.stat(io);
    if (st.size == 0 or st.size > 256) return error.BadId;
    const raw = try gpa.alloc(u8, st.size);
    errdefer gpa.free(raw);
    var rbuf: [256]u8 = undefined;
    var reader = f.reader(io, &rbuf);
    try reader.interface.readSliceAll(raw);
    // trim trailing whitespace/newline
    var end: usize = raw.len;
    while (end > 0 and (raw[end - 1] == '\n' or raw[end - 1] == '\r' or raw[end - 1] == ' ')) end -= 1;
    const trimmed = try gpa.dupe(u8, raw[0..end]);
    gpa.free(raw);
    return trimmed;
}

fn postReport(gpa: std.mem.Allocator, io: Io, cfg: Config, fingerprint: []const u8) !void {
    var url_buf: [256]u8 = undefined;
    const url = try std.fmt.bufPrint(&url_buf, "https://{s}/api/v1/rescue/report", .{cfg.hub});

    var body_buf: [512]u8 = undefined;
    const body = try std.fmt.bufPrint(&body_buf,
        \\{{"key":"{s}","fingerprint":"{s}","version":"{s}","os":"{s}","arch":"{s}"}}
    , .{ cfg.key, fingerprint, rescue_version, os_name, arch_name });

    var client: std.http.Client = .{ .allocator = gpa, .io = io };
    defer client.deinit();

    var sink: std.Io.Writer.Discarding = .init(&.{});
    const res = try client.fetch(.{
        .location = .{ .url = url },
        .method = .POST,
        .payload = body,
        .response_writer = &sink.writer,
        .redirect_behavior = @enumFromInt(5),
        .extra_headers = &.{.{ .name = "content-type", .value = "application/json" }},
    });
    if (res.status != .ok) {
        logLine("hub /rescue/report returned HTTP {d}", .{@intFromEnum(res.status)});
    }
}


fn supervise(io: Io, cfg: Config, gpa: std.mem.Allocator, agent_path: []const u8) !void {
    var backoff: u64 = cfg.min_backoff_ms;

    while (true) {
        // RESCUE: re-validate (and re-download) before every (re)start.
        ensureAgent(gpa, io, cfg, agent_path) catch |err| {
            logLine("cannot obtain agent ({s}); retrying in {d}ms", .{ @errorName(err), backoff });
            sleepMs(io, backoff);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };

        logLine("starting agent: {s} --data-dir {s}", .{ agent_path, cfg.data_dir });

        const start_ts = Io.Timestamp.now(io, .awake);

        var child = std.process.spawn(io, .{
            .argv = &.{ agent_path, "--data-dir", cfg.data_dir },
            .stdin = .ignore,
            .stdout = .inherit,
            .stderr = .inherit,
        }) catch |err| {
            logLine("spawn failed: {s}", .{@errorName(err)});
            // corrupt binary? force re-download
            Io.Dir.cwd().deleteFile(io, agent_path) catch {};
            sleepMs(io, backoff);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };

        if (cfg.once) {
            logLine("--once: agent spawned, exiting supervisor", .{});
            return;
        }

        const term = child.wait(io) catch |err| {
            logLine("wait failed: {s}", .{@errorName(err)});
            sleepMs(io, backoff);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };

        const now_ts = Io.Timestamp.now(io, .awake);
        const uptime_ms: u64 = @intCast(@max(0, start_ts.durationTo(now_ts).toMilliseconds()));

        switch (term) {
            .exited => |code| logLine("agent exited code={d} after {d}ms", .{ code, uptime_ms }),
            .signal => |s| logLine("agent killed by signal={d} after {d}ms", .{ @intFromEnum(s), uptime_ms }),
            .stopped => |s| logLine("agent stopped signal={d}", .{@intFromEnum(s)}),
            .unknown => |u| logLine("agent ended unknown={d}", .{u}),
        }

        if (uptime_ms >= cfg.healthy_uptime_ms) {
            backoff = cfg.min_backoff_ms;
        } else {
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
        }

        logLine("restarting agent in {d}ms", .{backoff});
        sleepMs(io, backoff);
    }
}

// ---- data dir resolution --------------------------------------------------
fn envGet(env: *std.process.Environ.Map, key: []const u8) ?[]const u8 {
    return env.get(key);
}

fn resolveDataDir(gpa: std.mem.Allocator, env: *std.process.Environ.Map, explicit: []const u8) ![]const u8 {
    if (explicit.len > 0) return gpa.dupe(u8, explicit);
    if (envGet(env, "SYS0_DATA_DIR")) |v| return gpa.dupe(u8, v);

    switch (builtin.os.tag) {
        .windows => {
            if (envGet(env, "APPDATA")) |base|
                return std.fmt.allocPrint(gpa, "{s}\\sys0-agent", .{base});
        },
        .macos => {
            if (envGet(env, "HOME")) |home|
                return std.fmt.allocPrint(gpa, "{s}/Library/Application Support/sys0-agent", .{home});
        },
        else => {
            if (envGet(env, "XDG_CONFIG_HOME")) |base|
                return std.fmt.allocPrint(gpa, "{s}/sys0-agent", .{base});
            if (envGet(env, "HOME")) |home|
                return std.fmt.allocPrint(gpa, "{s}/.config/sys0-agent", .{home});
        },
    }
    return gpa.dupe(u8, "sys0-agent");
}

// ---- arg parsing ----------------------------------------------------------
fn parseArgs(gpa: std.mem.Allocator, env: *std.process.Environ.Map, args: std.process.Args, cfg: *Config) !Action {
    var action: Action = .run;
    var it = try std.process.Args.Iterator.initAllocator(args, gpa);
    defer it.deinit();
    _ = it.next(); // argv[0]
    while (it.next()) |arg| {
        if (std.mem.eql(u8, arg, "--hub")) {
            cfg.hub = try gpa.dupe(u8, it.next() orelse return error.MissingValue);
        } else if (std.mem.eql(u8, arg, "--data-dir")) {
            cfg.data_dir = try gpa.dupe(u8, it.next() orelse return error.MissingValue);
        } else if (std.mem.eql(u8, arg, "--key")) {
            cfg.key = try gpa.dupe(u8, it.next() orelse return error.MissingValue);
        } else if (std.mem.eql(u8, arg, "--once")) {
            cfg.once = true;
        } else if (std.mem.eql(u8, arg, "install")) {
            action = .install;
        } else if (std.mem.eql(u8, arg, "uninstall")) {
            action = .uninstall;
        } else if (std.mem.eql(u8, arg, "--help") or std.mem.eql(u8, arg, "-h")) {
            action = .help;
        }
    }
    if (std.mem.eql(u8, cfg.hub, default_hub)) {
        if (envGet(env, "SYS0_HUB")) |v| cfg.hub = try gpa.dupe(u8, v);
    }
    if (std.mem.eql(u8, cfg.key, default_key)) {
        if (envGet(env, "SYS0_KEY")) |v| cfg.key = try gpa.dupe(u8, v);
    }
    return action;
}

fn printUsage() void {
    logLine(
        \\usage: sys0-rescue [COMMAND] [--hub HOST] [--data-dir DIR] [--once]
        \\
        \\commands:
        \\  (none)      run the supervisor (download + keepalive + rescue)
        \\  install     register autostart on boot/login (auto: system if admin, else user)
        \\  uninstall   remove the autostart registration
        \\
        \\options:
        \\  --hub HOST       hub hostname (default {s}, env SYS0_HUB)
        \\  --data-dir DIR   agent run dir (env SYS0_DATA_DIR; default per-user)
        \\  --once           bootstrap + spawn once, then exit (no supervision)
    , .{default_hub});
}

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const gpa = init.gpa;
    const env = init.environ_map;

    var cfg = Config{};
    const action = try parseArgs(gpa, env, init.minimal.args, &cfg);

    if (action == .help) {
        printUsage();
        return;
    }

    cfg.data_dir = try resolveDataDir(gpa, env, cfg.data_dir);

    Io.Dir.cwd().createDirPath(io, cfg.data_dir) catch |err| {
        logLine("cannot create data dir {s}: {s}", .{ cfg.data_dir, @errorName(err) });
        return err;
    };

    // On Windows (GUI subsystem, no console) tee logs to a file in the data dir.
    if (builtin.os.tag == .windows) {
        const lp = try std.fmt.allocPrint(gpa, "{s}{c}sys0-rescue.log", .{ cfg.data_dir, sep });
        setLogSink(io, lp);
    }

    switch (action) {
        .install => return install.installAutostart(gpa, io, env, cfg),
        .uninstall => return install.uninstallAutostart(gpa, io, env),
        .help => unreachable,
        .run => {},
    }

    var agent_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const agent_path = try std.fmt.bufPrint(&agent_path_buf, "{s}{c}{s}", .{ cfg.data_dir, sep, agent_filename });

    logLine("starting · hub={s} os={s} arch={s} data_dir={s}", .{ cfg.hub, os_name, arch_name, cfg.data_dir });

    // Start the hub-reporting thread (rescue<->agent binding). Detached: it
    // loops for the process lifetime. Best-effort — if concurrency is somehow
    // unavailable, the supervisor still runs without hub reporting.
    if (!cfg.once) {
        _ = io.concurrent(reportLoop, .{ io, gpa, cfg }) catch |err| {
            logLine("hub reporter unavailable: {s}", .{@errorName(err)});
        };
    }

    try supervise(io, cfg, gpa, agent_path);
}
