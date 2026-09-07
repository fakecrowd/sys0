package main

import (
	"encoding/json"
	"github.com/fakecrowd/sys0/internal/wire"
)

func (a Actor) scopedKey() bool { return a.Kind == "key" && len(a.MethodScope) > 0 }

// Every transport applies this policy immediately before delivering an event.
func (h *Hub) filterEvent(actor Actor, method string, data json.RawMessage) (json.RawMessage, bool) {
	a, ok := actor.refreshed()
	if !ok {
		return nil, false
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(data, &payload) != nil {
		return nil, false
	}
	var id string
	json.Unmarshal(payload["node"], &id)
	if id == "" {
		json.Unmarshal(payload["id"], &id)
	}
	if method == "event.node" {
		var v NodeView
		nested := json.Unmarshal(payload["node"], &v) == nil && v.ID != ""
		if nested {
			id = v.ID
		}
		if id == "" {
			return nil, false
		}
		if !nested {
			var n Node
			if h.store.db.First(&n, "id = ?", id).Error != nil {
				return nil, false
			}
			v = nodeViewFromRecord(n)
		}
		view, visible := h.nodeViewFor(a, v)
		if !visible {
			return nil, false
		}
		if nested {
			payload["node"], _ = json.Marshal(view)
		}
		if !view.CanAccess {
			// Never preserve arbitrary private fields from a discovery event.
			payload = map[string]json.RawMessage{"node": mustEventJSON(view)}
		}
		out, err := json.Marshal(payload)
		return out, err == nil
	}
	if id == "" || !a.nodeAllowed(id) {
		return nil, false
	}
	var required string
	switch method {
	case "event.metrics":
		required = wire.MethodHostWatch
	case "event.shell":
		if a.methodAllowed("shell.open") {
			return data, true
		}
		required = "shell.output"
	case "event.task":
		if a.methodAllowed("task.start") {
			return data, true
		}
		required = "task.output"
	default:
		return nil, false
	}
	if !a.methodAllowed(required) {
		return nil, false
	}
	return data, true
}
func mustEventJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
