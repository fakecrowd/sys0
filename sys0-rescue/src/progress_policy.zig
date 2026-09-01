pub const Item = struct {
    planned: bool = false,
    active: bool = false,
    completed: bool = false,
    downloaded: u64 = 0,
    total: u64 = 0,
};

pub const Snapshot = struct {
    active: bool = false,
    downloaded: u64 = 0,
    total: u64 = 0,
    percent: u8 = 0,
    completed: u8 = 0,
    modules: u8 = 0,
};

pub fn summarize(items: []const Item) Snapshot {
    var out: Snapshot = .{};
    var totals_known = true;
    for (items) |item| {
        if (!item.planned) continue;
        out.modules +|= 1;
        if (item.active) out.active = true;
        if (item.completed) out.completed +|= 1;
        out.downloaded +|= item.downloaded;
        if (item.total == 0) totals_known = false else out.total +|= item.total;
    }
    if (!totals_known) out.total = 0;
    if (out.total > 0) {
        const bounded = @min(out.downloaded, out.total);
        out.percent = @intCast(@min(@as(u64, 100), bounded * 100 / out.total));
    }
    return out;
}
