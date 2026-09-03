#!/usr/bin/env python3
"""Patch Zig 0.16's Windows std.Io backend for Windows 7.

Zig 0.16 targets Windows 10 and statically imports three ntdll exports that do
not exist on Windows 7. sys0-rescue still needs the threaded Io backend, so its
Windows release build uses the older keyed-event primitive and system clock.
The patch is intentionally exact and version-specific: it fails loudly if the
pinned Zig source changes instead of producing an unverified binary.
"""

from __future__ import annotations

import argparse
from pathlib import Path


PARK_OLD = '''        .windows => {
            const raw_timeout = timeoutToWindowsInterval(timeout);
            // `RtlWaitOnAddress` passes the futex address in as the first argument to this call,
            // but it's unclear what that actually does, especially since `NtAlertThreadByThreadId`
            // does *not* accept the address so the kernel can't really be using it as a hint. An
            // old Microsoft blog post discusses a more traditional futex-like mechanism in the
            // kernel which definitely isn't how `RtlWaitOnAddress` works today:
            //
            // https://devblogs.microsoft.com/oldnewthing/20160826-00/?p=94185
            //
            // ...so it's possible this argument is simply a remnant which no longer does anything
            // (perhaps the implementation changed during development but someone forgot to remove
            // this parameter). However, to err on the side of caution, let's match the behavior of
            // `RtlWaitOnAddress` and pass the pointer, in case the kernel ever does something
            // stupid such as trying to dereference it.
            switch (windows.ntdll.NtWaitForAlertByThreadId(
                addr_hint,
                if (raw_timeout) |*t| t else null,
            )) {
                .ALERTED => return,
                .TIMEOUT => return error.Timeout,
                else => unreachable,
            }
        },'''

PARK_NEW = '''        .windows => {
            // Windows 7 does not export NtWaitForAlertByThreadId. Keyed events
            // have provided the same waiter/releaser rendezvous since XP.
            const raw_timeout = timeoutToWindowsInterval(timeout);
            const key: ?*const anyopaque = @ptrFromInt(std.Thread.getCurrentId());
            switch (windows.ntdll.NtWaitForKeyedEvent(
                null,
                key,
                .FALSE,
                if (raw_timeout) |*t| t else null,
            )) {
                .SUCCESS => return,
                .TIMEOUT => return error.Timeout,
                else => unreachable,
            }
        },'''

UNPARK_OLD = '''        .windows => {
            // TODO: this condition is currently disabled because mingw-w64 does not contain this
            // symbol. Once it's added, enable this check to use the new bulk API where possible.
            if (false and (builtin.os.version_range.windows.isAtLeast(.win11_dt) orelse false)) {
                _ = windows.ntdll.NtAlertMultipleThreadByThreadId(tids.ptr, @intCast(tids.len), null, null);
            } else {
                for (tids) |tid| {
                    _ = windows.ntdll.NtAlertThreadByThreadId(@intCast(tid));
                }
            }
        },'''

UNPARK_NEW = '''        .windows => {
            // Pair with NtWaitForKeyedEvent above. NtReleaseKeyedEvent may wait
            // until the selected thread enters the wait, avoiding a lost wakeup.
            for (tids) |tid| {
                const key: ?*const anyopaque = @ptrFromInt(tid);
                if (windows.ntdll.NtReleaseKeyedEvent(null, key, .FALSE, null) != .SUCCESS)
                    unreachable;
            }
        },'''

NTDLL_MARKER = '''pub extern "ntdll" fn NtWaitForKeyedEvent(
    EventHandle: ?HANDLE,
    Key: ?*const anyopaque,
    Alertable: BOOLEAN,
    Timeout: ?*const LARGE_INTEGER,
) callconv(.winapi) NTSTATUS;
'''

NTDLL_REPLACEMENT = NTDLL_MARKER + '''pub extern "ntdll" fn NtQuerySystemTime(
    SystemTime: *LARGE_INTEGER,
) callconv(.winapi) NTSTATUS;
pub extern "ntdll" fn NtQueryTimerResolution(
    MinimumResolution: *ULONG,
    MaximumResolution: *ULONG,
    CurrentResolution: *ULONG,
) callconv(.winapi) NTSTATUS;
'''

CLOCK_OLD = 'windows.ntdll.RtlGetSystemTimePrecise()'
CLOCK_NEW = 'querySystemTimeWindows()'
CLOCK_RESOLUTION_OLD = '''            .awake, .boot, .real => {
                // We don't need to cache QPF as it's internally just a memory read to KUSER_SHARED_DATA'''
CLOCK_RESOLUTION_NEW = '''            .real => {
                // NtQuerySystemTime is available on Windows 7 but follows the
                // system timer rather than QPC. Report that timer's real
                // resolution instead of claiming QPC precision.
                var minimum: windows.ULONG = undefined;
                var maximum: windows.ULONG = undefined;
                var current: windows.ULONG = undefined;
                if (windows.ntdll.NtQueryTimerResolution(&minimum, &maximum, &current) != .SUCCESS)
                    return .zero;
                return .fromNanoseconds(@as(u64, current) * 100);
            },
            .awake, .boot => {
                // We don't need to cache QPF as it's internally just a memory read to KUSER_SHARED_DATA'''
CLOCK_HELPER_MARKER = 'fn nowWindows(clock: Io.Clock) Io.Timestamp {'
CLOCK_HELPER = '''fn querySystemTimeWindows() windows.LARGE_INTEGER {
    var system_time: windows.LARGE_INTEGER = undefined;
    assert(windows.ntdll.NtQuerySystemTime(&system_time) == .SUCCESS);
    return system_time;
}

''' + CLOCK_HELPER_MARKER


def replace_exact(text: str, old: str, new: str, expected: int, label: str) -> str:
    count = text.count(old)
    if count != expected:
        raise RuntimeError(f"{label}: expected {expected} match(es), found {count}")
    return text.replace(old, new)


def patch(lib_dir: Path) -> None:
    threaded_path = lib_dir / "std" / "Io" / "Threaded.zig"
    ntdll_path = lib_dir / "std" / "os" / "windows" / "ntdll.zig"
    threaded = threaded_path.read_text()
    ntdll = ntdll_path.read_text()

    already = (
        PARK_NEW in threaded
        and UNPARK_NEW in threaded
        and CLOCK_OLD not in threaded
        and CLOCK_RESOLUTION_NEW in threaded
        and CLOCK_HELPER in threaded
        and "pub extern \"ntdll\" fn NtQuerySystemTime(" in ntdll
        and "pub extern \"ntdll\" fn NtQueryTimerResolution(" in ntdll
    )
    if already:
        print(f"Zig Windows 7 compatibility patch already applied: {lib_dir}")
        return

    threaded = replace_exact(threaded, PARK_OLD, PARK_NEW, 1, "parking wait")
    threaded = replace_exact(threaded, UNPARK_OLD, UNPARK_NEW, 1, "parking wake")
    threaded = replace_exact(threaded, CLOCK_OLD, CLOCK_NEW, 2, "system clock")
    threaded = replace_exact(
        threaded,
        CLOCK_RESOLUTION_OLD,
        CLOCK_RESOLUTION_NEW,
        1,
        "real-time clock resolution",
    )
    threaded = replace_exact(
        threaded, CLOCK_HELPER_MARKER, CLOCK_HELPER, 1, "system clock helper"
    )
    ntdll = replace_exact(ntdll, NTDLL_MARKER, NTDLL_REPLACEMENT, 1, "ntdll declaration")

    threaded_path.write_text(threaded)
    ntdll_path.write_text(ntdll)
    print(f"Applied Zig Windows 7 compatibility patch: {lib_dir}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("lib_dir", type=Path, help="Zig lib directory (contains std/)")
    args = parser.parse_args()
    patch(args.lib_dir.resolve())


if __name__ == "__main__":
    main()
