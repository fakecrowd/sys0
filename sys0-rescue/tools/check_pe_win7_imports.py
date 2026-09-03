#!/usr/bin/env python3
"""Reject rescue PE imports unavailable on Windows 7.

The parser intentionally uses only Python's standard library so it can inspect
both x86-64 and ARM64 PE files on an ordinary Linux GitHub runner; GNU objdump
commonly cannot decode the ARM64 variant.
"""

from __future__ import annotations

import argparse
import struct
from pathlib import Path


# Audited against the Windows 7 6.1 ntdll export table. Keeping this closed
# allowlist means a future Zig upgrade cannot silently add another newer API.
WIN7_NTDLL_IMPORTS = {
    "LdrGetProcedureAddress",
    "LdrLoadDll",
    "LdrUnloadDll",
    "NtAlertThread",
    "NtAllocateVirtualMemory",
    "NtCancelIoFileEx",
    "NtCancelSynchronousIoFile",
    "NtClose",
    "NtCreateFile",
    "NtCreateNamedPipeFile",
    "NtCreateSection",
    "NtCreateThreadEx",
    "NtDelayExecution",
    "NtDeviceIoControlFile",
    "NtFlushBuffersFile",
    "NtFreeVirtualMemory",
    "NtFsControlFile",
    "NtLockFile",
    "NtMapViewOfSection",
    "NtOpenFile",
    "NtOpenThread",
    "NtQueryAttributesFile",
    "NtQueryDirectoryFile",
    "NtQueryInformationFile",
    "NtQueryInformationProcess",
    "NtQueryInformationThread",
    "NtQueryObject",
    "NtQuerySystemInformation",
    "NtQuerySystemTime",
    "NtQueryTimerResolution",
    "NtQueryVolumeInformationFile",
    "NtReadFile",
    "NtReleaseKeyedEvent",
    "NtResumeThread",
    "NtSetInformationFile",
    "NtTerminateProcess",
    "NtUnlockFile",
    "NtUnmapViewOfSection",
    "NtWaitForKeyedEvent",
    "NtWaitForSingleObject",
    "NtWriteFile",
    "RtlActivateActivationContextEx",
    "RtlAllocateHeap",
    "RtlEnterCriticalSection",
    "RtlEqualUnicodeString",
    "RtlExitUserProcess",
    "RtlFreeHeap",
    "RtlGetActiveActivationContext",
    "RtlGetCurrentDirectory_U",
    "RtlGetFullPathName_U",
    "RtlLeaveCriticalSection",
    "RtlQueryPerformanceCounter",
    "RtlQueryPerformanceFrequency",
    "RtlReleaseActivationContext",
    "RtlReportSilentProcessExit",
    "RtlSetCurrentDirectory_U",
    "RtlUpcaseUnicodeChar",
}
REQUIRED_FALLBACK_IMPORTS = {
    "NtQuerySystemTime",
    "NtQueryTimerResolution",
    "NtReleaseKeyedEvent",
    "NtWaitForKeyedEvent",
}


def unpack(data: bytes, fmt: str, offset: int) -> tuple[int, ...]:
    try:
        return struct.unpack_from(fmt, data, offset)
    except struct.error as error:
        raise RuntimeError(f"truncated PE data at offset {offset:#x}") from error


def pe_imports(binary: Path) -> dict[str, set[str]]:
    data = binary.read_bytes()
    if data[:2] != b"MZ":
        raise RuntimeError(f"{binary}: missing DOS header")
    (pe_offset,) = unpack(data, "<I", 0x3C)
    if data[pe_offset : pe_offset + 4] != b"PE\0\0":
        raise RuntimeError(f"{binary}: missing PE header")

    coff_offset = pe_offset + 4
    _, section_count, _, _, _, optional_size, _ = unpack(data, "<HHIIIHH", coff_offset)
    optional_offset = coff_offset + 20
    (magic,) = unpack(data, "<H", optional_offset)
    if magic == 0x20B:  # PE32+
        pointer_size = 8
        ordinal_mask = 1 << 63
        data_directories_offset = optional_offset + 112
    elif magic == 0x10B:  # PE32
        pointer_size = 4
        ordinal_mask = 1 << 31
        data_directories_offset = optional_offset + 96
    else:
        raise RuntimeError(f"{binary}: unsupported optional-header magic {magic:#x}")

    import_rva, import_size = unpack(data, "<II", data_directories_offset + 8)
    if import_rva == 0 or import_size == 0:
        raise RuntimeError(f"{binary}: PE has no import directory")

    sections: list[tuple[int, int, int]] = []
    section_offset = optional_offset + optional_size
    for index in range(section_count):
        offset = section_offset + index * 40
        virtual_size, virtual_address, raw_size, raw_offset = unpack(data, "<IIII", offset + 8)
        sections.append((virtual_address, max(virtual_size, raw_size), raw_offset))

    def file_offset(rva: int) -> int:
        for virtual_address, mapped_size, raw_offset in sections:
            if virtual_address <= rva < virtual_address + mapped_size:
                offset = raw_offset + rva - virtual_address
                if offset >= len(data):
                    break
                return offset
        raise RuntimeError(f"{binary}: unmapped RVA {rva:#x}")

    def c_string(rva: int) -> str:
        offset = file_offset(rva)
        end = data.find(b"\0", offset)
        if end < 0:
            raise RuntimeError(f"{binary}: unterminated string at RVA {rva:#x}")
        return data[offset:end].decode("ascii")

    imports: dict[str, set[str]] = {}
    descriptor_offset = file_offset(import_rva)
    descriptor_end = descriptor_offset + import_size
    while descriptor_offset + 20 <= descriptor_end:
        original_thunk, timestamp, forwarder, name_rva, first_thunk = unpack(
            data, "<IIIII", descriptor_offset
        )
        if not any((original_thunk, timestamp, forwarder, name_rva, first_thunk)):
            break
        dll_name = c_string(name_rva).lower()
        dll_imports = imports.setdefault(dll_name, set())
        thunk_rva = original_thunk or first_thunk
        thunk_offset = file_offset(thunk_rva)
        while True:
            fmt = "<Q" if pointer_size == 8 else "<I"
            (value,) = unpack(data, fmt, thunk_offset)
            thunk_offset += pointer_size
            if value == 0:
                break
            if value & ordinal_mask:
                # Keep ordinal imports visible. In particular, an ntdll ordinal
                # cannot be approved by the named-export allowlist below.
                dll_imports.add(f"ordinal:{value & 0xFFFF}")
                continue
            dll_imports.add(c_string(value + 2))  # skip IMAGE_IMPORT_BY_NAME.Hint
        descriptor_offset += 20

    if not imports:
        raise RuntimeError(f"{binary}: no PE imports found")
    return imports


def check(binary: Path) -> None:
    imports = pe_imports(binary)
    ntdll_imports = imports.get("ntdll.dll")
    if not ntdll_imports:
        raise RuntimeError(f"{binary}: no ntdll.dll imports found")
    unsupported = ntdll_imports - WIN7_NTDLL_IMPORTS
    missing = REQUIRED_FALLBACK_IMPORTS - ntdll_imports
    if unsupported or missing:
        details = []
        if unsupported:
            details.append("non-Windows-7 ntdll imports: " + ", ".join(sorted(unsupported)))
        if missing:
            details.append("missing ntdll fallbacks: " + ", ".join(sorted(missing)))
        raise RuntimeError(f"{binary}: " + "; ".join(details))
    total = sum(len(names) for names in imports.values())
    print(f"Windows 7 PE import check passed: {binary} ({total} imported functions)")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("binaries", nargs="+", type=Path)
    args = parser.parse_args()
    for binary in args.binaries:
        check(binary.resolve())


if __name__ == "__main__":
    main()
