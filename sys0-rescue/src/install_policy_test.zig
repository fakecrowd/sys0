const std = @import("std");
const policy = @import("install_policy.zig");

test "non-admin installs only the current-user entry" {
    const targets = policy.windowsTargets(false);
    try std.testing.expect(targets.hkcu_run);
    try std.testing.expect(!targets.hklm_run);
    try std.testing.expect(!targets.system_startup_task);
}

test "admin installs all redundant Windows entries" {
    const targets = policy.windowsTargets(true);
    try std.testing.expect(targets.hkcu_run);
    try std.testing.expect(targets.hklm_run);
    try std.testing.expect(targets.system_startup_task);
}

test "singleton name is stable per data directory" {
    const a = policy.singletonName("C:\\Users\\a\\AppData\\Roaming\\sys0-agent");
    const b = policy.singletonName("C:\\Users\\a\\AppData\\Roaming\\sys0-agent");
    const c = policy.singletonName("D:\\sys0-agent");
    try std.testing.expectEqualStrings(&a, &b);
    try std.testing.expect(!std.mem.eql(u8, &a, &c));
    try std.testing.expect(std.mem.startsWith(u8, &a, "Global\\sys0-rescue-"));
}
