//go:build !modular || (mod_shell && mod_task)

package main

import "testing"

func TestShellOutputSequenceSurvivesRingTruncation(t *testing.T) {
	s := &shellSession{}
	if seq := s.append(make([]byte, shellBufferCap+3)); seq != 1 {
		t.Fatalf("event seq = %d", seq)
	}
	data, seq := s.snapshot()
	if len(data) != shellBufferCap || seq != 1 {
		t.Fatalf("snapshot len/seq = %d/%d", len(data), seq)
	}
}

func TestTaskOutputSequenceIsPersistentAcrossBufferReset(t *testing.T) {
	task := &managedTask{}
	if task.append([]byte("repeat")) != 1 || task.append([]byte("repeat")) != 2 {
		t.Fatal("sequence did not increment")
	}
	task.mu.Lock()
	task.buf = nil
	task.mu.Unlock()
	if task.append([]byte("again")) != 3 {
		t.Fatal("sequence reset with buffer")
	}
	data, seq := task.snapshot()
	if string(data) != "again" || seq != 3 {
		t.Fatalf("snapshot = %q seq %d", data, seq)
	}
}
