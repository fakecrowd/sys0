const std = @import("std");
const policy = @import("module_command_policy.zig");

test "modular update queues every configured module" {
    var state = policy.State{};
    const queued = state.queueConfigured("core,shell,fs,screen", .update);

    try std.testing.expectEqual(@as(usize, 4), queued);
    inline for (0..policy.module_count) |idx| {
        try std.testing.expectEqual(policy.Command.update, state.peek(idx));
    }
}

test "monolith commands stay on the monolith control path" {
    var state = policy.State{};
    try std.testing.expectEqual(@as(usize, 0), state.queueConfigured("all", .update));
    inline for (0..policy.module_count) |idx| {
        try std.testing.expectEqual(policy.Command.none, state.peek(idx));
    }
}

test "pending module command waits for child and hub peer to stop" {
    var state = policy.State{};
    _ = state.queueConfigured("screen", .update);

    try std.testing.expect(!state.ready(3, true, false));
    try std.testing.expect(!state.ready(3, false, true));
    try std.testing.expect(state.ready(3, false, false));
    try std.testing.expectEqual(policy.Command.update, state.peek(3));
}

test "restart queues only named modules" {
    var state = policy.State{};
    try std.testing.expectEqual(@as(usize, 2), state.queueConfigured("shell,screen", .restart));
    try std.testing.expectEqual(policy.Command.none, state.peek(0));
    try std.testing.expectEqual(policy.Command.restart, state.peek(1));
    try std.testing.expectEqual(policy.Command.none, state.peek(2));
    try std.testing.expectEqual(policy.Command.restart, state.peek(3));
}

test "update supersedes a queued restart" {
    var state = policy.State{};
    _ = state.queueConfigured("screen", .restart);
    _ = state.queueConfigured("screen", .update);
    _ = state.queueConfigured("screen", .restart);
    try std.testing.expectEqual(policy.Command.update, state.peek(3));
}

test "completion cannot clear a newer command" {
    var state = policy.State{};
    _ = state.queueConfigured("screen", .restart);
    _ = state.queueConfigured("screen", .update);
    try std.testing.expect(!state.complete(3, .restart));
    try std.testing.expectEqual(policy.Command.update, state.peek(3));
    try std.testing.expect(state.complete(3, .update));
    try std.testing.expectEqual(policy.Command.none, state.peek(3));
}
