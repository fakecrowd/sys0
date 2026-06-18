// install.zig — cross-platform autostart registration for sys0-rescue.
//
// Strategy: rescue registers *itself* as the autostart entry. On boot/login the
// OS launches rescue, which then downloads (if needed) and supervises the agent
// — so registering rescue makes BOTH rescue and the agent start automatically,
// with no duplicate/competing autostart entries.
//
// Privilege auto-detection:
//   - root / admin  -> system-wide unit (starts at boot, before login)
//   - unprivileged  -> per-user unit    (starts at user login; NO admin needed)
//
// Platforms:
//   Linux   : systemd system unit  | systemd --user unit (+ linger) | cron @reboot
//   macOS   : LaunchDaemon          | LaunchAgent
//   Windows : Scheduled Task (SYSTEM)| HKCU\...\Run registry value

const std = @import("std");
const builtin = @import("builtin");
const root = @import("main.zig");

const Io = std.Io;
const Map = std.process.Environ.Map;
const logLine = root.logLine;

const service_name = "sys0-rescue";

fn isPrivileged() bool {
    return switch (builtin.os.tag) {
        .linux => std.os.linux.geteuid() == 0,
        .macos => std.c.geteuid() == 0,
        .windows => isWindowsAdmin(),
        else => false,
    };
}

fn isWindowsAdmin() bool {
    if (builtin.os.tag != .windows) return false;
    // Best-effort: try to open the SCM with create rights; success => admin.
    // Falls back to false on any error (treated as unprivileged -> user install).
    return false; // conservative default; Windows install uses per-user path
}

// Copy this running executable to a stable path inside data_dir so the autostart
// entry points at a persistent location (not a temp/cwd that may vanish).
fn installSelf(gpa: std.mem.Allocator, io: Io, data_dir: []const u8) ![]const u8 {
    var exe_buf: [std.fs.max_path_bytes]u8 = undefined;
    const n = try std.process.executablePath(io, &exe_buf);
    const exe_path = exe_buf[0..n];

    const name = if (builtin.os.tag == .windows) "sys0-rescue.exe" else "sys0-rescue";
    const dest = try std.fmt.allocPrint(gpa, "{s}{c}{s}", .{ data_dir, root.sep, name });

    if (std.mem.eql(u8, exe_path, dest)) return dest; // already in place

    const cwd = Io.Dir.cwd();
    // Read source, write dest (simple + portable; binary is ~0.5MB).
    const src = try cwd.openFile(io, exe_path, .{ .mode = .read_only });
    defer src.close(io);
    const st = try src.stat(io);
    const buf = try gpa.alloc(u8, st.size);
    defer gpa.free(buf);
    var rbuf: [64 * 1024]u8 = undefined;
    var reader = src.reader(io, &rbuf);
    try reader.interface.readSliceAll(buf);

    var out = try cwd.createFile(io, dest, .{ .truncate = true });
    defer out.close(io);
    var wbuf: [64 * 1024]u8 = undefined;
    var w = out.writer(io, &wbuf);
    try w.interface.writeAll(buf);
    try w.interface.flush();

    if (builtin.os.tag != .windows) {
        out.setPermissions(io, .fromMode(0o755)) catch {};
    }
    return dest;
}

// Run a command, inheriting stdio, return true on exit code 0.
fn run(io: Io, argv: []const []const u8) bool {
    var child = std.process.spawn(io, .{
        .argv = argv,
        .stdin = .ignore,
        .stdout = .inherit,
        .stderr = .inherit,
    }) catch |err| {
        logLine("exec failed ({s}): {s}", .{ argv[0], @errorName(err) });
        return false;
    };
    const term = child.wait(io) catch return false;
    return switch (term) {
        .exited => |c| c == 0,
        else => false,
    };
}

fn writeFileMode(io: Io, path: []const u8, content: []const u8, mode: std.posix.mode_t) !void {
    const cwd = Io.Dir.cwd();
    var f = try cwd.createFile(io, path, .{ .truncate = true });
    defer f.close(io);
    var wbuf: [8 * 1024]u8 = undefined;
    var w = f.writer(io, &wbuf);
    try w.interface.writeAll(content);
    try w.interface.flush();
    if (builtin.os.tag != .windows) f.setPermissions(io, .fromMode(mode)) catch {};
}

// =========================================================================
pub fn installAutostart(gpa: std.mem.Allocator, io: Io, env: *Map, cfg: root.Config) !void {
    const priv = isPrivileged();
    logLine("installing autostart · scope={s} os={s}", .{ if (priv) "system" else "user", root.os_name });

    const self_path = try installSelf(gpa, io, cfg.data_dir);
    logLine("rescue binary at: {s}", .{self_path});

    switch (builtin.os.tag) {
        .linux => try installLinux(gpa, io, env, cfg, self_path, priv),
        .macos => try installMac(gpa, io, env, cfg, self_path, priv),
        .windows => try installWindows(gpa, io, env, cfg, self_path),
        else => return error.UnsupportedPlatform,
    }
    logLine("autostart installed. rescue (and the agent it supervises) will start automatically.", .{});
}

pub fn uninstallAutostart(gpa: std.mem.Allocator, io: Io, env: *Map) !void {
    const priv = isPrivileged();
    switch (builtin.os.tag) {
        .linux => try uninstallLinux(gpa, io, env, priv),
        .macos => try uninstallMac(gpa, io, env, priv),
        .windows => try uninstallWindows(gpa, io),
        else => return error.UnsupportedPlatform,
    }
    logLine("autostart removed.", .{});
}

// ---- Linux: systemd (system / user) with cron @reboot fallback -----------
fn haveSystemd(io: Io) bool {
    return run(io, &.{ "sh", "-c", "command -v systemctl >/dev/null 2>&1" });
}

fn installLinux(gpa: std.mem.Allocator, io: Io, env: *Map, cfg: root.Config, self_path: []const u8, priv: bool) !void {
    if (!haveSystemd(io)) return installCronReboot(gpa, io, self_path, cfg);

    const unit = try std.fmt.allocPrint(gpa,
        \\[Unit]
        \\Description=sys0-rescue (sys0-agent supervisor + bootstrapper)
        \\After=network-online.target
        \\Wants=network-online.target
        \\
        \\[Service]
        \\Type=simple
        \\ExecStart={s} --hub {s} --data-dir {s}
        \\Restart=always
        \\RestartSec=5
        \\
        \\[Install]
        \\WantedBy={s}
        \\
    , .{ self_path, cfg.hub, cfg.data_dir, if (priv) "multi-user.target" else "default.target" });

    if (priv) {
        const path = "/etc/systemd/system/" ++ service_name ++ ".service";
        try writeFileMode(io, path, unit, 0o644);
        _ = run(io, &.{ "systemctl", "daemon-reload" });
        _ = run(io, &.{ "systemctl", "enable", "--now", service_name });
        logLine("system unit enabled: {s}", .{path});
    } else {
        const home = env.get("HOME") orelse return error.NoHome;
        const dir = try std.fmt.allocPrint(gpa, "{s}/.config/systemd/user", .{home});
        try Io.Dir.cwd().createDirPath(io, dir);
        const path = try std.fmt.allocPrint(gpa, "{s}/{s}.service", .{ dir, service_name });
        try writeFileMode(io, path, unit, 0o644);
        // Allow the user service to run without an active login session.
        _ = run(io, &.{ "loginctl", "enable-linger" });
        _ = run(io, &.{ "systemctl", "--user", "daemon-reload" });
        _ = run(io, &.{ "systemctl", "--user", "enable", "--now", service_name });
        logLine("user unit enabled: {s} (linger on)", .{path});
    }
}

fn installCronReboot(gpa: std.mem.Allocator, io: Io, self_path: []const u8, cfg: root.Config) !void {
    logLine("systemd not found — falling back to cron @reboot", .{});
    const line = try std.fmt.allocPrint(gpa,
        "(crontab -l 2>/dev/null | grep -v sys0-rescue; echo '@reboot {s} --hub {s} --data-dir {s}') | crontab -",
        .{ self_path, cfg.hub, cfg.data_dir },
    );
    _ = run(io, &.{ "sh", "-c", line });
    // also start it now in the background
    _ = run(io, &.{ "sh", "-c", try std.fmt.allocPrint(gpa, "nohup {s} --hub {s} --data-dir {s} >/dev/null 2>&1 &", .{ self_path, cfg.hub, cfg.data_dir }) });
}

fn uninstallLinux(gpa: std.mem.Allocator, io: Io, env: *Map, priv: bool) !void {
    if (priv) {
        _ = run(io, &.{ "systemctl", "disable", "--now", service_name });
        Io.Dir.cwd().deleteFile(io, "/etc/systemd/system/" ++ service_name ++ ".service") catch {};
        _ = run(io, &.{ "systemctl", "daemon-reload" });
    } else {
        _ = run(io, &.{ "systemctl", "--user", "disable", "--now", service_name });
        if (env.get("HOME")) |home| {
            const path = try std.fmt.allocPrint(gpa, "{s}/.config/systemd/user/{s}.service", .{ home, service_name });
            Io.Dir.cwd().deleteFile(io, path) catch {};
        }
    }
    _ = run(io, &.{ "sh", "-c", "crontab -l 2>/dev/null | grep -v sys0-rescue | crontab - 2>/dev/null || true" });
}

// ---- macOS: launchd (LaunchDaemon / LaunchAgent) -------------------------
const mac_label = "xyz.facrd.sys0-rescue";

fn installMac(gpa: std.mem.Allocator, io: Io, env: *Map, cfg: root.Config, self_path: []const u8, priv: bool) !void {
    const plist = try std.fmt.allocPrint(gpa,
        \\<?xml version="1.0" encoding="UTF-8"?>
        \\<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
        \\<plist version="1.0">
        \\<dict>
        \\  <key>Label</key><string>{s}</string>
        \\  <key>ProgramArguments</key>
        \\  <array>
        \\    <string>{s}</string>
        \\    <string>--hub</string><string>{s}</string>
        \\    <string>--data-dir</string><string>{s}</string>
        \\  </array>
        \\  <key>RunAtLoad</key><true/>
        \\  <key>KeepAlive</key><true/>
        \\</dict>
        \\</plist>
        \\
    , .{ mac_label, self_path, cfg.hub, cfg.data_dir });

    const path = if (priv)
        "/Library/LaunchDaemons/" ++ mac_label ++ ".plist"
    else blk: {
        const home = env.get("HOME") orelse return error.NoHome;
        const dir = try std.fmt.allocPrint(gpa, "{s}/Library/LaunchAgents", .{home});
        try Io.Dir.cwd().createDirPath(io, dir);
        break :blk try std.fmt.allocPrint(gpa, "{s}/{s}.plist", .{ dir, mac_label });
    };
    try writeFileMode(io, path, plist, 0o644);
    _ = run(io, &.{ "launchctl", "load", "-w", path });
    logLine("launchd plist loaded: {s}", .{path});
}

fn uninstallMac(gpa: std.mem.Allocator, io: Io, env: *Map, priv: bool) !void {
    const path = if (priv)
        try gpa.dupe(u8, "/Library/LaunchDaemons/" ++ mac_label ++ ".plist")
    else blk: {
        const home = env.get("HOME") orelse return error.NoHome;
        break :blk try std.fmt.allocPrint(gpa, "{s}/Library/LaunchAgents/{s}.plist", .{ home, mac_label });
    };
    _ = run(io, &.{ "launchctl", "unload", "-w", path });
    Io.Dir.cwd().deleteFile(io, path) catch {};
}

// ---- Windows: per-user registry Run value --------------------------------
fn installWindows(gpa: std.mem.Allocator, io: Io, env: *Map, cfg: root.Config, self_path: []const u8) !void {
    _ = env;
    // Per-user autostart, no admin required: HKCU Run value launching rescue.
    // (The standalone agent is already a GUI-subsystem binary, and rescue is a
    // console binary; a future --silent could hide its window if desired.)
    const cmd = try std.fmt.allocPrint(gpa, "\"{s}\" --hub {s} --data-dir \"{s}\"", .{ self_path, cfg.hub, cfg.data_dir });
    _ = run(io, &.{
        "reg", "add", "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
        "/v",  service_name, "/t", "REG_SZ", "/d", cmd, "/f",
    });
    logLine("HKCU Run value set: {s}", .{service_name});
}

fn uninstallWindows(gpa: std.mem.Allocator, io: Io) !void {
    _ = gpa;
    _ = run(io, &.{
        "reg",          "delete", "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run",
        "/v",           service_name, "/f",
    });
}
