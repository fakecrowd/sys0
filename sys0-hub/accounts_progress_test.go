package main

import "testing"

func TestNormalizeRescueProgressClampsAndDerivesPercent(t *testing.T) {
	got := normalizeRescueProgress(rescueProgress{Active: true, Module: "core", Downloaded: 150, Total: 100, Completed: 9, Modules: 4})
	if got.Downloaded != 100 || got.Percent != 100 || got.Completed != 4 {
		t.Fatalf("unexpected normalized progress: %+v", got)
	}
}

func TestNormalizeRescueProgressAllowsUnknownTotal(t *testing.T) {
	got := normalizeRescueProgress(rescueProgress{Active: true, Module: "shell", Downloaded: 42, Total: 0, Completed: 1, Modules: 4})
	if got.Percent != 0 || got.Downloaded != 42 || got.Module != "shell" {
		t.Fatalf("unexpected unknown-total progress: %+v", got)
	}
}

func TestNormalizeRescueProgressHandlesHugeValuesWithoutOverflow(t *testing.T) {
	const maxInt64 = int64(^uint64(0) >> 1)
	got := normalizeRescueProgress(rescueProgress{Downloaded: maxInt64 - 1, Total: maxInt64})
	if got.Percent != 99 {
		t.Fatalf("expected bounded 99%%, got %+v", got)
	}
}
