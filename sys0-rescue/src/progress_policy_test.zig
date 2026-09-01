const std = @import("std");
const policy = @import("progress_policy.zig");

test "aggregate progress sums concurrent module downloads" {
    const items = [_]policy.Item{
        .{ .planned = true, .active = false, .completed = true, .downloaded = 100, .total = 100 },
        .{ .planned = true, .active = true, .downloaded = 50, .total = 200 },
    };
    const got = policy.summarize(&items);
    try std.testing.expect(got.active);
    try std.testing.expectEqual(@as(u64, 150), got.downloaded);
    try std.testing.expectEqual(@as(u64, 300), got.total);
    try std.testing.expectEqual(@as(u8, 50), got.percent);
    try std.testing.expectEqual(@as(u8, 1), got.completed);
    try std.testing.expectEqual(@as(u8, 2), got.modules);
}

test "aggregate progress does not invent percent before every total is known" {
    const items = [_]policy.Item{
        .{ .planned = true, .active = true, .downloaded = 25, .total = 100 },
        .{ .planned = true, .active = true, .downloaded = 10, .total = 0 },
    };
    const got = policy.summarize(&items);
    try std.testing.expectEqual(@as(u64, 35), got.downloaded);
    try std.testing.expectEqual(@as(u64, 0), got.total);
    try std.testing.expectEqual(@as(u8, 0), got.percent);
}
