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
    no_install: bool = false,
    min_backoff_ms: u64 = 1_000,
    max_backoff_ms: u64 = 60_000,
    healthy_uptime_ms: u64 = 30_000,
    report_every_s: u64 = 30,
};

const Action = enum { run, install, uninstall, help };

// ---- shared rescue status (reported to the hub) ---------------------------
// The supervise loop writes Status; the reporter thread reads it. Single
// writer + single reader, but we guard with a tiny mutex for safety on the
// threaded Io backend. Phase describes what the rescue is currently doing so
// the console can show a live detail view, not just "rescue: yes".
const Phase = enum {
    starting, // process just launched, nothing done yet
    downloading, // fetching the agent binary from the hub
    starting_agent, // spawning the agent child
    supervising, // agent running, being kept alive
    restarting, // agent died, backing off before relaunch
    error_state, // cannot obtain/keep the agent

    pub fn label(self: Phase) []const u8 {
        return switch (self) {
            .starting => "starting",
            .downloading => "downloading",
            .starting_agent => "starting-agent",
            .supervising => "supervising",
            .restarting => "restarting",
            .error_state => "error",
        };
    }
};

const Status = struct {
    mu: Io.Mutex = .init,
    phase: Phase = .starting,
    restarts: u32 = 0, // how many times the agent has been (re)started
    last_exit: i64 = -1, // last agent exit code (-1 = none yet)
    last_uptime_ms: u64 = 0, // how long the agent ran last time
    detail_buf: [192]u8 = undefined,
    detail_len: usize = 0, // free-text detail (last log-worthy event)

    fn lock(self: *Status, io: Io) void {
        self.mu.lockUncancelable(io);
    }
    fn unlock(self: *Status, io: Io) void {
        self.mu.unlock(io);
    }

    // set updates phase + detail atomically. io required by Io.Mutex.
    fn set(self: *Status, io: Io, phase: Phase, comptime fmt: []const u8, args: anytype) void {
        self.lock(io);
        defer self.unlock(io);
        self.phase = phase;
        const w = std.fmt.bufPrint(&self.detail_buf, fmt, args) catch {
            self.detail_len = 0;
            return;
        };
        self.detail_len = w.len;
    }

    fn detail(self: *Status) []const u8 {
        return self.detail_buf[0..self.detail_len];
    }
};

// Global status the reporter thread reads. Lives for the process lifetime.
var g_status: Status = .{};

// ---- operator commands (hub -> rescue, HTTPS long-poll) -------------------
// The rescue speaks HTTPS only (no WebSocket): it POSTs a report every ~30s and
// the hub answers with any pending operator commands in the response body. The
// reporter thread parses them (tiny hand-rolled scan — no JSON parser, keeping
// the binary small), the supervise loop executes them, and results are sent on
// the next report. Commands supported: "update-agent" (force re-download +
// restart), "restart-agent" (restart without re-download).
const CmdKind = enum {
    update_agent,
    restart_agent,
    unknown,

    fn parse(s_: []const u8) CmdKind {
        if (std.mem.eql(u8, s_, "update-agent")) return .update_agent;
        if (std.mem.eql(u8, s_, "restart-agent")) return .restart_agent;
        return .unknown;
    }
};

const max_cmd_id = 24;
const cmd_queue_cap = 8;
const result_cap = 8;

// A command the rescue has accepted and must act on / report.
const Cmd = struct {
    id_buf: [max_cmd_id]u8 = undefined,
    id_len: usize = 0,
    kind: CmdKind = .unknown,
    fn id(self: *const Cmd) []const u8 {
        return self.id_buf[0..self.id_len];
    }
};

// A terminal result to report back to the hub on the next report.
const CmdResult = struct {
    id_buf: [max_cmd_id]u8 = undefined,
    id_len: usize = 0,
    status_buf: [16]u8 = undefined,
    status_len: usize = 0,
    detail_buf: [160]u8 = undefined,
    detail_len: usize = 0,
    fn id(self: *const CmdResult) []const u8 {
        return self.id_buf[0..self.id_len];
    }
    fn status(self: *const CmdResult) []const u8 {
        return self.status_buf[0..self.status_len];
    }
    fn detail(self: *const CmdResult) []const u8 {
        return self.detail_buf[0..self.detail_len];
    }
};

// Cmds holds the shared command/result state between the reporter thread (which
// receives commands + sends results) and the supervise loop (which executes).
// Guarded by a mutex; both producers and consumers are detached workers.
const Cmds = struct {
    mu: Io.Mutex = .init,
    // queue: accepted commands awaiting execution by the supervise loop.
    queue: [cmd_queue_cap]Cmd = undefined,
    qlen: usize = 0,
    // results: terminal outcomes awaiting the next report.
    results: [result_cap]CmdResult = undefined,
    rlen: usize = 0,
    // seen: ids we've already accepted, so re-sent (still-pending on the hub)
    // commands aren't enqueued twice. Small ring of recent ids.
    seen: [16][max_cmd_id]u8 = undefined,
    seen_len: [16]usize = [_]usize{0} ** 16,
    seen_pos: usize = 0,

    fn lock(self: *Cmds, io: Io) void {
        self.mu.lockUncancelable(io);
    }
    fn unlock(self: *Cmds, io: Io) void {
        self.mu.unlock(io);
    }

    // alreadySeen reports whether id was accepted recently. Caller holds lock.
    fn alreadySeen(self: *Cmds, id: []const u8) bool {
        var i: usize = 0;
        while (i < self.seen.len) : (i += 1) {
            if (self.seen_len[i] == id.len and std.mem.eql(u8, self.seen[i][0..self.seen_len[i]], id))
                return true;
        }
        return false;
    }
    fn markSeen(self: *Cmds, id: []const u8) void {
        const n = @min(id.len, max_cmd_id);
        @memcpy(self.seen[self.seen_pos][0..n], id[0..n]);
        self.seen_len[self.seen_pos] = n;
        self.seen_pos = (self.seen_pos + 1) % self.seen.len;
    }

    // accept enqueues a freshly-received command (dedup by id). Caller locks.
    fn accept(self: *Cmds, id: []const u8, kind: CmdKind) void {
        if (self.alreadySeen(id)) return;
        if (self.qlen >= cmd_queue_cap) return;
        var c = &self.queue[self.qlen];
        const n = @min(id.len, max_cmd_id);
        @memcpy(c.id_buf[0..n], id[0..n]);
        c.id_len = n;
        c.kind = kind;
        self.qlen += 1;
        self.markSeen(id);
    }

    // takeQueue copies and clears the pending queue. Caller locks.
    fn takeQueue(self: *Cmds, out: []Cmd) usize {
        const n = @min(self.qlen, out.len);
        var i: usize = 0;
        while (i < n) : (i += 1) out[i] = self.queue[i];
        self.qlen = 0;
        return n;
    }

    // pushResult records a terminal outcome to report next. Caller locks.
    fn pushResult(self: *Cmds, id: []const u8, status_: []const u8, detail_: []const u8) void {
        if (self.rlen >= result_cap) return;
        var r = &self.results[self.rlen];
        const idn = @min(id.len, max_cmd_id);
        @memcpy(r.id_buf[0..idn], id[0..idn]);
        r.id_len = idn;
        const sn = @min(status_.len, r.status_buf.len);
        @memcpy(r.status_buf[0..sn], status_[0..sn]);
        r.status_len = sn;
        const dn = @min(detail_.len, r.detail_buf.len);
        @memcpy(r.detail_buf[0..dn], detail_[0..dn]);
        r.detail_len = dn;
        self.rlen += 1;
    }

    // takeResults copies and clears pending results. Caller locks.
    fn takeResults(self: *Cmds, out: []CmdResult) usize {
        const n = @min(self.rlen, out.len);
        var i: usize = 0;
        while (i < n) : (i += 1) out[i] = self.results[i];
        self.rlen = 0;
        return n;
    }
};

var g_cmds: Cmds = .{};

// scanCommands extracts {id,kind} pairs from a /rescue/report response body and
// accepts them into g_cmds. The body looks like:
//   {"ok":true,"node":"nXXXX","commands":[{"id":"c1","kind":"update-agent"},...]}
// We scan for "commands": then walk each object's "id"/"kind" string fields. No
// JSON parser is pulled in (keeps the binary tiny).
fn scanCommands(io: Io, body: []const u8) void {
    const marker = "\"commands\"";
    const start = std.mem.indexOf(u8, body, marker) orelse return;
    var rest = body[start + marker.len ..];
    // Walk objects: each has an "id" and a "kind" string. Iterate as long as we
    // can find another "id": before the array obviously ends.
    while (true) {
        const id_val = findStringField(rest, "id") orelse break;
        const kind_val = findStringField(rest, "kind") orelse break;
        if (id_val.value.len > 0 and id_val.value.len <= max_cmd_id) {
            g_cmds.lock(io);
            g_cmds.accept(id_val.value, CmdKind.parse(kind_val.value));
            g_cmds.unlock(io);
        }
        // advance past whichever field ended later
        const adv = @max(id_val.end, kind_val.end);
        if (adv >= rest.len) break;
        rest = rest[adv..];
    }
}

const FieldHit = struct { value: []const u8, end: usize };

// findStringField finds  "name":"value"  in s and returns the value slice plus
// the index just past the closing quote. Minimal: assumes no escaped quotes in
// these short control values (ids are cN, kinds are fixed tokens).
fn findStringField(s_: []const u8, name: []const u8) ?FieldHit {
    var pat_buf: [24]u8 = undefined;
    const pat = std.fmt.bufPrint(&pat_buf, "\"{s}\"", .{name}) catch return null;
    const k = std.mem.indexOf(u8, s_, pat) orelse return null;
    var i = k + pat.len;
    // skip spaces and the colon
    while (i < s_.len and (s_[i] == ' ' or s_[i] == ':')) i += 1;
    if (i >= s_.len or s_[i] != '"') return null;
    i += 1;
    const vstart = i;
    while (i < s_.len and s_[i] != '"') i += 1;
    if (i >= s_.len) return null;
    return FieldHit{ .value = s_[vstart..i], .end = i + 1 };
}


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

// ---- identity (shared with the agent) -------------------------------------
// Generate (or load) the per-host fingerprint and persist it to sys0-agent.id
// BEFORE downloading the agent. The agent's loadOrCreateID() reads this same
// file if it exists, so the agent inherits this exact fingerprint — letting the
// rescue announce itself to the hub (under the final node id) from cold start,
// before the agent binary is even present. Returns the 32-char hex id.
fn ensureFingerprint(io: Io, gpa: std.mem.Allocator, data_dir: []const u8) ![]u8 {
    var id_path_buf: [std.fs.max_path_bytes]u8 = undefined;
    const id_path = try std.fmt.bufPrint(&id_path_buf, "{s}{c}sys0-agent.id", .{ data_dir, sep });

    // Existing id wins (agent may have already created it).
    if (readAgentFingerprint(io, gpa, id_path)) |fp| {
        if (fp.len >= 8) return fp;
        gpa.free(fp);
    } else |_| {}

    // Generate 16 random bytes -> 32 hex chars (same shape as the agent).
    var raw: [16]u8 = undefined;
    io.random(&raw);
    const hex = std.fmt.bytesToHex(raw, .lower);
    const id = try gpa.dupe(u8, &hex);
    errdefer gpa.free(id);

    // Persist atomically: write .tmp then rename, mode 0600 like the agent.
    var tmp_buf: [std.fs.max_path_bytes]u8 = undefined;
    const tmp_path = try std.fmt.bufPrint(&tmp_buf, "{s}{c}sys0-agent.id.tmp", .{ data_dir, sep });
    const cwd = Io.Dir.cwd();
    {
        var f = try cwd.createFile(io, tmp_path, .{ .truncate = true });
        defer f.close(io);
        var wbuf: [64]u8 = undefined;
        var w = f.writer(io, &wbuf);
        try w.interface.writeAll(&hex);
        try w.interface.writeAll("\n");
        try w.interface.flush();
        if (builtin.os.tag != .windows) f.setPermissions(io, .fromMode(0o600)) catch {};
    }
    try cwd.rename(tmp_path, cwd, id_path, io);
    logLine("generated agent fingerprint {s} (node n{s})", .{ id, id[0..6] });
    return id;
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
// The fingerprint is generated/loaded up front (ensureFingerprint) and passed
// in, so the rescue reports to the hub from cold start — BEFORE the agent is
// downloaded — and keeps reporting every report_every_s through every phase
// (download, start, supervise, restart). Each report carries the live Status
// so the console can show a detail view.
fn reportLoop(io: Io, gpa: std.mem.Allocator, cfg: Config, fingerprint: []const u8) void {
    // Report immediately so the node appears the instant the rescue starts,
    // then on a fixed cadence for the process lifetime.
    while (true) {
        postReport(gpa, io, cfg, fingerprint) catch |err| {
            logLine("report failed: {s}", .{@errorName(err)});
        };
        sleepMs(io, cfg.report_every_s * 1000);
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

// jsonEscape writes src into dst as a JSON-safe string (no surrounding quotes),
// escaping the characters JSON requires (notably backslash, common in Windows
// paths, and double-quote). Returns the written slice.
fn jsonEscape(dst: []u8, src: []const u8) []const u8 {
    var n: usize = 0;
    for (src) |c| {
        const esc: ?[]const u8 = switch (c) {
            '"' => "\\\"",
            '\\' => "\\\\",
            '\n' => "\\n",
            '\r' => "\\r",
            '\t' => "\\t",
            else => null,
        };
        if (esc) |e| {
            if (n + e.len > dst.len) break;
            @memcpy(dst[n .. n + e.len], e);
            n += e.len;
        } else if (c < 0x20) {
            // control char -> skip (keep payload compact)
            continue;
        } else {
            if (n + 1 > dst.len) break;
            dst[n] = c;
            n += 1;
        }
    }
    return dst[0..n];
}

fn postReport(gpa: std.mem.Allocator, io: Io, cfg: Config, fingerprint: []const u8) !void {
    var url_buf: [256]u8 = undefined;
    const url = try std.fmt.bufPrint(&url_buf, "https://{s}/api/v1/rescue/report", .{cfg.hub});

    // Snapshot the live status under lock.
    g_status.lock(io);
    const phase = g_status.phase;
    const restarts = g_status.restarts;
    const last_exit = g_status.last_exit;
    const last_uptime_ms = g_status.last_uptime_ms;
    var detail_raw_buf: [192]u8 = undefined;
    const detail_raw = blk: {
        const d = g_status.detail();
        @memcpy(detail_raw_buf[0..d.len], d);
        break :blk detail_raw_buf[0..d.len];
    };
    g_status.unlock(io);

    var detail_buf: [384]u8 = undefined;
    const detail = jsonEscape(&detail_buf, detail_raw);

    // Drain any pending command results to report back to the hub.
    var results: [result_cap]CmdResult = undefined;
    g_cmds.lock(io);
    const rn = g_cmds.takeResults(&results);
    g_cmds.unlock(io);

    var results_buf: [1024]u8 = undefined;
    const results_json = buildResultsJson(&results_buf, results[0..rn]);

    var body_buf: [2048]u8 = undefined;
    const body = try std.fmt.bufPrint(&body_buf,
        \\{{"key":"{s}","fingerprint":"{s}","version":"{s}","os":"{s}","arch":"{s}","status":"{s}","detail":"{s}","restarts":{d},"lastExit":{d},"lastUptimeMs":{d},"results":{s}}}
    , .{ cfg.key, fingerprint, rescue_version, os_name, arch_name, phase.label(), detail, restarts, last_exit, last_uptime_ms, results_json });

    var client: std.http.Client = .{ .allocator = gpa, .io = io };
    defer client.deinit();

    // Capture the response so we can scan it for pending operator commands.
    var resp_buf: [4096]u8 = undefined;
    var rw = std.Io.Writer.fixed(&resp_buf);
    const res = try client.fetch(.{
        .location = .{ .url = url },
        .method = .POST,
        .payload = body,
        .response_writer = &rw,
        .redirect_behavior = @enumFromInt(5),
        .extra_headers = &.{.{ .name = "content-type", .value = "application/json" }},
    });
    if (res.status != .ok) {
        logLine("hub /rescue/report returned HTTP {d}", .{@intFromEnum(res.status)});
        return;
    }
    const resp = rw.buffered();
    scanCommands(io, resp);
}

// buildResultsJson renders a results array for the report body. Empty -> "[]".
fn buildResultsJson(buf: []u8, results: []const CmdResult) []const u8 {
    if (results.len == 0) return "[]";
    var w = std.Io.Writer.fixed(buf);
    w.writeAll("[") catch return "[]";
    for (results, 0..) |r, i| {
        if (i > 0) w.writeAll(",") catch return "[]";
        var d_buf: [320]u8 = undefined;
        const d = jsonEscape(&d_buf, r.detail());
        w.print("{{\"id\":\"{s}\",\"status\":\"{s}\",\"detail\":\"{s}\"}}", .{ r.id(), r.status(), d }) catch return "[]";
    }
    w.writeAll("]") catch return "[]";
    return w.buffered();
}


// g_child shares the currently-running agent child with the command watcher so
// a manual update/restart can interrupt the supervise loop's blocking wait by
// killing the child. Guarded by g_child_mu.
var g_child_mu: Io.Mutex = .init;
var g_child: ?*std.process.Child = null;
// g_force_update is set by the watcher when an update-agent command fires, so
// the supervise loop re-downloads the binary before the next spawn.
var g_force_update: bool = false;

fn setCurrentChild(io: Io, child: ?*std.process.Child) void {
    g_child_mu.lockUncancelable(io);
    g_child = child;
    g_child_mu.unlock(io);
}

// commandWatcher runs on its own thread. It polls the accepted-command queue and
// executes each: kills the running agent (so the supervise loop relaunches it),
// setting g_force_update for update-agent so the binary is re-fetched first.
// Results are pushed back for the reporter thread to deliver. Best-effort.
fn commandWatcher(io: Io) void {
    while (true) {
        var batch: [cmd_queue_cap]Cmd = undefined;
        g_cmds.lock(io);
        const n = g_cmds.takeQueue(&batch);
        g_cmds.unlock(io);

        var i: usize = 0;
        while (i < n) : (i += 1) {
            const c = batch[i];
            switch (c.kind) {
                .update_agent => {
                    g_child_mu.lockUncancelable(io);
                    g_force_update = true;
                    g_child_mu.unlock(io);
                    logLine("operator command: update-agent ({s})", .{c.id()});
                    killCurrentChild(io);
                    pushCmdResult(io, c.id(), "done", "agent update triggered (re-download + restart)");
                },
                .restart_agent => {
                    logLine("operator command: restart-agent ({s})", .{c.id()});
                    killCurrentChild(io);
                    pushCmdResult(io, c.id(), "done", "agent restart triggered");
                },
                .unknown => {
                    pushCmdResult(io, c.id(), "error", "unknown command");
                },
            }
        }
        sleepMs(io, 2000);
    }
}

fn killCurrentChild(io: Io) void {
    g_child_mu.lockUncancelable(io);
    const ch = g_child;
    g_child_mu.unlock(io);
    if (ch) |child| {
        child.kill(io); // breaks the supervise loop's child.wait -> relaunch
    }
}

fn pushCmdResult(io: Io, id: []const u8, status_: []const u8, detail_: []const u8) void {
    g_cmds.lock(io);
    g_cmds.pushResult(id, status_, detail_);
    g_cmds.unlock(io);
}

fn supervise(io: Io, cfg: Config, gpa: std.mem.Allocator, agent_path: []const u8) !void {
    var backoff: u64 = cfg.min_backoff_ms;

    while (true) {
        // Manual update-agent command: force a re-download by removing the
        // current binary so ensureAgent fetches the latest from the hub.
        g_child_mu.lockUncancelable(io);
        const force_update = g_force_update;
        g_force_update = false;
        g_child_mu.unlock(io);
        if (force_update) {
            logLine("forced agent update: removing current binary", .{});
            Io.Dir.cwd().deleteFile(io, agent_path) catch {};
            g_status.set(io, .downloading, "updating agent from {s}", .{cfg.hub});
        }

        // RESCUE: re-validate (and re-download) before every (re)start.
        // Mark "downloading" only when the binary is actually absent/invalid so
        // the console shows the bootstrap phase (the common cold-start case).
        if (!agentLooksValid(io, agent_path)) {
            g_status.set(io, .downloading, "fetching agent from {s}", .{cfg.hub});
        }
        ensureAgent(gpa, io, cfg, agent_path) catch |err| {
            g_status.set(io, .error_state, "cannot obtain agent: {s}", .{@errorName(err)});
            logLine("cannot obtain agent ({s}); retrying in {d}ms", .{ @errorName(err), backoff });
            sleepMs(io, backoff);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };

        g_status.set(io, .starting_agent, "spawning agent", .{});
        logLine("starting agent: {s} --data-dir {s}", .{ agent_path, cfg.data_dir });

        const start_ts = Io.Timestamp.now(io, .awake);

        var child = std.process.spawn(io, .{
            .argv = &.{ agent_path, "--data-dir", cfg.data_dir },
            .stdin = .ignore,
            .stdout = .inherit,
            .stderr = .inherit,
        }) catch |err| {
            g_status.set(io, .error_state, "spawn failed: {s}", .{@errorName(err)});
            logLine("spawn failed: {s}", .{@errorName(err)});
            // corrupt binary? force re-download
            Io.Dir.cwd().deleteFile(io, agent_path) catch {};
            sleepMs(io, backoff);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };

        // Agent is up. Count the (re)start and flip to the supervising phase.
        g_status.lock(io);
        g_status.restarts += 1;
        g_status.phase = .supervising;
        const detail = std.fmt.bufPrint(&g_status.detail_buf, "agent running (pid-tracked)", .{}) catch "";
        g_status.detail_len = detail.len;
        g_status.unlock(io);

        if (cfg.once) {
            logLine("--once: agent spawned, exiting supervisor", .{});
            return;
        }

        // Expose the child so the command watcher can interrupt the wait below
        // (manual restart/update kills it -> we relaunch).
        setCurrentChild(io, &child);
        const term = child.wait(io) catch |err| {
            setCurrentChild(io, null);
            logLine("wait failed: {s}", .{@errorName(err)});
            sleepMs(io, backoff);
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
            continue;
        };
        setCurrentChild(io, null);

        const now_ts = Io.Timestamp.now(io, .awake);
        const uptime_ms: u64 = @intCast(@max(0, start_ts.durationTo(now_ts).toMilliseconds()));

        var exit_code: i64 = -1;
        switch (term) {
            .exited => |code| {
                exit_code = code;
                logLine("agent exited code={d} after {d}ms", .{ code, uptime_ms });
            },
            .signal => |sg| {
                exit_code = -@as(i64, @intFromEnum(sg));
                logLine("agent killed by signal={d} after {d}ms", .{ @intFromEnum(sg), uptime_ms });
            },
            .stopped => |sg| logLine("agent stopped signal={d}", .{@intFromEnum(sg)}),
            .unknown => |u| logLine("agent ended unknown={d}", .{u}),
        }

        if (uptime_ms >= cfg.healthy_uptime_ms) {
            backoff = cfg.min_backoff_ms;
        } else {
            backoff = @min(backoff * 2, cfg.max_backoff_ms);
        }

        // Record the exit for the console detail view, then announce the
        // restart backoff phase.
        g_status.lock(io);
        g_status.last_exit = exit_code;
        g_status.last_uptime_ms = uptime_ms;
        g_status.phase = .restarting;
        const rd = std.fmt.bufPrint(&g_status.detail_buf, "agent exited ({d}) after {d}ms; restart in {d}ms", .{ exit_code, uptime_ms, backoff }) catch "";
        g_status.detail_len = rd.len;
        g_status.unlock(io);

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
        } else if (std.mem.eql(u8, arg, "--no-install")) {
            cfg.no_install = true;
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
        \\  --no-install     do NOT auto-register autostart on first run
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

    // AUTO-REGISTER AUTOSTART on a normal run (idempotent, best-effort). The
    // whole point of rescue is "install it once and it keeps the agent alive
    // across reboots" — users shouldn't have to remember a separate `install`
    // subcommand. Skipped for --once (throwaway) and --no-install (opt-out).
    // Never fatal: a failure here must not stop the supervisor from running.
    if (!cfg.once and !cfg.no_install) {
        install.installAutostart(gpa, io, env, cfg) catch |err| {
            logLine("autostart registration failed (continuing): {s}", .{@errorName(err)});
        };
    }

    // CONNECT FIRST: generate/load the agent fingerprint and announce to the hub
    // BEFORE downloading anything. The agent inherits this exact fingerprint
    // (loadOrCreateID reads the file), so the rescue reports under the final node
    // id from cold start and the operator can watch the bootstrap live.
    const fingerprint = ensureFingerprint(io, gpa, cfg.data_dir) catch |err| blk: {
        logLine("fingerprint unavailable ({s}); reporting disabled", .{@errorName(err)});
        break :blk &[_]u8{};
    };

    g_status.set(io, .starting, "rescue online; preparing agent", .{});

    // Synchronous initial report so the node appears the instant the rescue
    // starts — before the (potentially slow) first download. Best-effort.
    if (!cfg.once and fingerprint.len >= 6) {
        postReport(gpa, io, cfg, fingerprint) catch |err| {
            logLine("initial report failed: {s}", .{@errorName(err)});
        };
    }

    // Start the continuous hub-reporting thread (rescue<->agent binding).
    // Detached: it loops for the process lifetime, re-reporting the live Status
    // through every phase (download/start/supervise/restart). Best-effort.
    if (!cfg.once and fingerprint.len >= 6) {
        _ = io.concurrent(reportLoop, .{ io, gpa, cfg, fingerprint }) catch |err| {
            logLine("hub reporter unavailable: {s}", .{@errorName(err)});
        };
        // Command watcher: executes operator commands (update/restart agent)
        // delivered via the report responses. Detached, best-effort.
        _ = io.concurrent(commandWatcher, .{io}) catch |err| {
            logLine("command watcher unavailable: {s}", .{@errorName(err)});
        };
    }

    try supervise(io, cfg, gpa, agent_path);
}
