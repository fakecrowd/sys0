package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// --- minimal HS256 JWT-style token (header.payload.sig, base64url) ---

type tokenClaims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Exp  int64  `json:"exp"`
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (h *Hub) signToken(sub, role string, ttl time.Duration) string {
	header := b64([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pc, _ := json.Marshal(tokenClaims{Sub: sub, Role: role, Exp: time.Now().Add(ttl).Unix()})
	payload := b64(pc)
	signing := header + "." + payload
	mac := hmac.New(sha256.New, []byte(h.cfg.JWTSecret))
	mac.Write([]byte(signing))
	return signing + "." + b64(mac.Sum(nil))
}

func (h *Hub) verifyToken(tok string) (tokenClaims, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return tokenClaims{}, false
	}
	signing := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(h.cfg.JWTSecret))
	mac.Write([]byte(signing))
	if !hmac.Equal([]byte(parts[2]), []byte(b64(mac.Sum(nil)))) {
		return tokenClaims{}, false
	}
	pc, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, false
	}
	var c tokenClaims
	if json.Unmarshal(pc, &c) != nil || time.Now().Unix() > c.Exp {
		return tokenClaims{}, false
	}
	return c, true
}

// actorFromRequest resolves the caller into an Actor from either a JWT
// (human/console) or an API key (machine). Returns ok=false if unauthenticated.
func (h *Hub) actorFromRequest(r *http.Request) (Actor, bool) {
	auth := r.Header.Get("Authorization")
	var tok string
	if strings.HasPrefix(auth, "Bearer ") {
		tok = strings.TrimPrefix(auth, "Bearer ")
	} else {
		// allow ?token= for SSE/EventSource which can't set headers
		tok = r.URL.Query().Get("token")
	}
	if tok == "" {
		return Actor{}, false
	}
	// Account-owned API key path. Resolve the owner on EVERY request so account
	// role/scope changes and deletion take effect immediately. The key may narrow
	// methods, but it can never carry an independent role/node/dangerous grant.
	if strings.HasPrefix(tok, "sk_") {
		rec, ok := h.store.AuthKey(tok)
		if !ok || rec.Owner == "" {
			return Actor{}, false
		}
		u, found := h.store.GetUser(rec.Owner)
		if !found {
			return Actor{}, false
		}
		isAdmin := u.Role == "admin"
		scopeAll, nodeScope := isAdmin, u.NodeScope
		allowDangerous := isAdmin
		// Keys created before account ownership could carry narrower node and
		// dangerous-method restrictions. Preserve those as an intersection during
		// migration; they may only reduce the owner's live permissions.
		if rec.legacyRole != "" {
			if legacyScope := splitScope(rec.legacyNodeScope); len(legacyScope) > 0 {
				scopeAll = false
				if isAdmin {
					nodeScope = legacyScope
				} else {
					nodeScope = intersectScope(u.NodeScope, legacyScope)
				}
			}
			allowDangerous = isAdmin && rec.legacyAllowDangerous
		}
		return Actor{
			Kind: "key", ID: rec.ID, Role: u.Role,
			ScopeAll: scopeAll, NodeScope: nodeScope,
			MethodScope: splitScope(rec.MethodScope), AllowDangerous: allowDangerous,
		}, true
	}
	// JWT path — resolve the live user so role/scope changes take effect.
	if c, ok := h.verifyToken(tok); ok {
		u, found := h.store.GetUser(c.Sub)
		if !found {
			return Actor{}, false // user deleted since token issued
		}
		isAdmin := u.Role == "admin"
		return Actor{
			Kind: "user", ID: u.Username, Role: u.Role,
			ScopeAll:       isAdmin,     // admins see every node
			NodeScope:      u.NodeScope, // members restricted to their list
			AllowDangerous: isAdmin,     // only admins may run dangerous methods
		}, true
	}
	return Actor{}, false
}

func intersectScope(a, b []string) []string {
	allowed := make(map[string]struct{}, len(b))
	for _, id := range b {
		allowed[id] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, id := range a {
		if _, ok := allowed[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
