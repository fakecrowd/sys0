const std = @import("std");

pub const module_names = [_][]const u8{ "core", "shell", "fs", "screen" };
pub const module_count = module_names.len;

pub const Command = enum(u8) {
    none,
    restart,
    update,
};

pub const State = struct {
    pending: [module_count]Command = .{.none} ** module_count,

    pub fn queueConfigured(self: *State, configured: []const u8, command: Command) usize {
        if (std.mem.eql(u8, std.mem.trim(u8, configured, " \t"), "all")) return 0;
        var queued: usize = 0;
        var it = std.mem.tokenizeScalar(u8, configured, ',');
        while (it.next()) |raw| {
            const name = std.mem.trim(u8, raw, " \t");
            for (module_names, 0..) |known, idx| {
                if (std.mem.eql(u8, name, known)) {
                    if (@intFromEnum(command) > @intFromEnum(self.pending[idx])) {
                        self.pending[idx] = command;
                    }
                    queued += 1;
                    break;
                }
            }
        }
        return queued;
    }

    pub fn peek(self: *const State, idx: usize) Command {
        return self.pending[idx];
    }

    pub fn ready(self: *const State, idx: usize, child_alive: bool, hub_online: bool) bool {
        return self.pending[idx] != .none and !child_alive and !hub_online;
    }

    pub fn complete(self: *State, idx: usize, command: Command) bool {
        if (self.pending[idx] != command) return false;
        self.pending[idx] = .none;
        return true;
    }
};
