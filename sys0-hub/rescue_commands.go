package main

import (
	"sync"
	"time"
)

// ---- rescue command buffer (hub -> rescue, HTTPS long-poll style) ----
//
// The rescue talks to the hub over plain HTTPS only (no WebSocket): it POSTs a
// liveness report every ~30s. To let an operator drive the rescue (e.g. force
// an agent update), we buffer commands per node here. They ride DOWN on the
// /rescue/report response; the rescue executes them and reports results back on
// its next report. Everything is in-memory — commands are ephemeral control
// actions, not durable state.

// rescueCmdKind enumerates the operations a rescue can perform on request.
type rescueCmdKind string

const (
	// rescueCmdUpdateAgent forces the rescue to re-download the latest agent
	// from the hub and restart it (even if the current binary looks valid).
	rescueCmdUpdateAgent rescueCmdKind = "update-agent"
	// rescueCmdRestartAgent restarts the supervised agent without re-downloading.
	rescueCmdRestartAgent rescueCmdKind = "restart-agent"
)

func validRescueCmd(k rescueCmdKind) bool {
	switch k {
	case rescueCmdUpdateAgent, rescueCmdRestartAgent:
		return true
	}
	return false
}

// rescueCommand is one queued instruction plus its lifecycle/result.
type rescueCommand struct {
	ID            string        `json:"id"`
	Kind          rescueCmdKind `json:"kind"`
	Status        string        `json:"status"` // pending | acked | done | error
	Detail        string        `json:"detail"` // result/error text from the rescue
	CreatedAt     time.Time     `json:"-"`
	UpdatedAt     time.Time     `json:"-"`
	CreatedAtUnix int64         `json:"createdAt"`
	UpdatedAtUnix int64         `json:"updatedAt"`
}

var (
	rescueCmdMu sync.Mutex
	// rescueCommands holds, per node id, the recent command history (pending +
	// completed). Bounded per node; old completed entries are trimmed.
	rescueCommands = map[string][]*rescueCommand{}
	rescueCmdSeq   uint64
)

// rescueCmdRetain caps how many commands we keep per node (history for the UI).
const rescueCmdRetain = 12

// rescueCmdTTL is how long a completed command stays in the history.
const rescueCmdTTL = 10 * time.Minute

// enqueueRescueCommand queues a command for a node and returns it. De-dupes:
// if an identical-kind command is already pending, returns that instead of
// piling up duplicates (clicking 更新 twice shouldn't queue two updates).
func enqueueRescueCommand(nodeID string, kind rescueCmdKind) *rescueCommand {
	rescueCmdMu.Lock()
	defer rescueCmdMu.Unlock()
	for _, c := range rescueCommands[nodeID] {
		if c.Kind == kind && (c.Status == "pending" || c.Status == "acked") {
			return c
		}
	}
	rescueCmdSeq++
	now := time.Now()
	cmd := &rescueCommand{
		ID:            "c" + itoa(rescueCmdSeq),
		Kind:          kind,
		Status:        "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
		CreatedAtUnix: now.Unix(),
		UpdatedAtUnix: now.Unix(),
	}
	rescueCommands[nodeID] = append(rescueCommands[nodeID], cmd)
	trimRescueCommandsLocked(nodeID)
	return cmd
}

// pendingRescueCommands returns the commands a rescue should execute now
// (status pending), marking them "acked" so they aren't re-sent on every poll.
// The rescue reports terminal results (done/error) on a later report.
func pendingRescueCommands(nodeID string) []rescueCommand {
	rescueCmdMu.Lock()
	defer rescueCmdMu.Unlock()
	out := []rescueCommand{}
	now := time.Now()
	for _, c := range rescueCommands[nodeID] {
		if c.Status == "pending" {
			c.Status = "acked"
			c.UpdatedAt = now
			c.UpdatedAtUnix = now.Unix()
			out = append(out, *c)
		}
	}
	return out
}

// applyRescueResults records terminal results reported by the rescue.
func applyRescueResults(nodeID string, results []struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}) {
	if len(results) == 0 {
		return
	}
	rescueCmdMu.Lock()
	defer rescueCmdMu.Unlock()
	now := time.Now()
	for _, r := range results {
		for _, c := range rescueCommands[nodeID] {
			if c.ID != r.ID {
				continue
			}
			if r.Status == "done" || r.Status == "error" {
				c.Status = r.Status
			} else if c.Status == "acked" {
				c.Status = r.Status // e.g. "running"
			}
			if r.Detail != "" {
				if len(r.Detail) > 240 {
					r.Detail = r.Detail[:240]
				}
				c.Detail = r.Detail
			}
			c.UpdatedAt = now
			c.UpdatedAtUnix = now.Unix()
		}
	}
	trimRescueCommandsLocked(nodeID)
}

// rescueCommandHistory returns a snapshot of a node's recent commands (newest
// last) for the console detail view.
func rescueCommandHistory(nodeID string) []rescueCommand {
	rescueCmdMu.Lock()
	defer rescueCmdMu.Unlock()
	src := rescueCommands[nodeID]
	out := make([]rescueCommand, 0, len(src))
	for _, c := range src {
		out = append(out, *c)
	}
	return out
}

// trimRescueCommandsLocked drops aged-out completed commands and caps length.
// Caller holds rescueCmdMu.
func trimRescueCommandsLocked(nodeID string) {
	src := rescueCommands[nodeID]
	kept := src[:0]
	now := time.Now()
	for _, c := range src {
		// drop old terminal commands
		if (c.Status == "done" || c.Status == "error") && now.Sub(c.UpdatedAt) > rescueCmdTTL {
			continue
		}
		kept = append(kept, c)
	}
	// cap: keep the most recent rescueCmdRetain
	if len(kept) > rescueCmdRetain {
		kept = kept[len(kept)-rescueCmdRetain:]
	}
	if len(kept) == 0 {
		delete(rescueCommands, nodeID)
	} else {
		rescueCommands[nodeID] = kept
	}
}

// itoa is a tiny uint64 -> decimal string (avoids importing strconv here).
func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
