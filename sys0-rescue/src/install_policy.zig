const std = @import("std");

pub const WindowsTargets = struct {
    hkcu_run: bool,
    hklm_run: bool,
    system_startup_task: bool,
};

pub fn windowsTargets(is_admin: bool) WindowsTargets {
    return .{
        .hkcu_run = true,
        .hklm_run = is_admin,
        .system_startup_task = is_admin,
    };
}

pub fn singletonName(data_dir: []const u8) [35]u8 {
    var out: [35]u8 = undefined;
    const hash = std.hash.Fnv1a_64.hash(data_dir);
    _ = std.fmt.bufPrint(&out, "Global\\sys0-rescue-{x:0>16}", .{hash}) catch unreachable;
    return out;
}
