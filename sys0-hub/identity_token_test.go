package main

import "time"

// Test-only convenience: production signs the exact authenticated record.
func (h *Hub) signToken(sub, role string, ttl time.Duration) string {
	u, _ := h.store.GetUser(sub)
	u.Role = role
	return h.signUserToken(u, ttl)
}
