package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Hub) canManageNode(a Actor, id string) bool {
	a, ok := a.refreshed()
	if !ok || a.Kind != "user" {
		return false
	}
	if a.Role == "admin" {
		return true
	}
	var n Node
	return h.store.db.First(&n, "id = ?", id).Error == nil && n.OwnerID == a.UserID
}
func (h *Hub) nodeViewFor(a Actor, v NodeView) (NodeView, bool) {
	a, ok := a.refreshed()
	if !ok {
		return NodeView{}, false
	}
	var n Node
	err := h.store.db.First(&n, "id = ?", v.ID).Error
	persisted := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return NodeView{}, false
	}
	allowed := a.nodeAllowed(v.ID)
	// Keys cannot discover claimable nodes outside their owner's grants.
	if !allowed && (a.Kind != "user" || (persisted && n.OwnerID != 0)) {
		return NodeView{}, false
	}
	if !allowed {
		v = NodeView{ID: v.ID, Label: v.Label, Tags: []string{}, State: v.State}
	}
	if persisted && n.OwnerID != 0 {
		owner, found := h.store.GetUserByID(n.OwnerID)
		if !found {
			return NodeView{}, false
		}
		v.Owner = owner.Username
	}
	v.CanAccess = allowed
	v.CanClaim = persisted && n.OwnerID == 0 && a.Kind == "user"
	v.CanManageAccess = a.Kind == "user" && (a.Role == "admin" || (persisted && n.OwnerID == a.UserID))
	return v, true
}
func ownershipError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	msg := "ownership operation failed"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusNotFound
		msg = "node not found"
	case errors.Is(err, errClaimed):
		status = http.StatusConflict
		msg = err.Error()
	case errors.Is(err, errAccess):
		status = http.StatusForbidden
		msg = err.Error()
	case errors.Is(err, errUnknownUser):
		status = http.StatusBadRequest
		msg = err.Error()
	}
	c.JSON(status, gin.H{"ok": false, "error": msg})
}
func humanAccount(c *gin.Context) bool {
	if actorOf(c).Kind != "user" {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "account login required"})
		return false
	}
	return true
}
func (h *Hub) apiNodeClaim(c *gin.Context) {
	if !humanAccount(c) {
		return
	}
	var body struct{}
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "JSON object required"})
		return
	}
	a := actorOf(c)
	id := c.Param("id")
	if err := h.store.ClaimNode(id, a.UserID); err != nil {
		ownershipError(c, err)
		return
	}
	h.auditACL(a, "node.claim", id)
	for _, v := range h.ListNodesFor(a) {
		if v.ID == id {
			c.JSON(http.StatusOK, gin.H{"ok": true, "node": v})
			return
		}
	}
	c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "node view unavailable"})
}
func (h *Hub) apiGetNodeAccess(c *gin.Context) {
	if !humanAccount(c) {
		return
	}
	owner, users, err := h.store.NodeAccessView(c.Param("id"), actorOf(c).UserID)
	if err != nil {
		ownershipError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "owner": owner, "users": users})
}
func (h *Hub) apiSetNodeAccess(c *gin.Context) {
	if !humanAccount(c) {
		return
	}
	var body struct {
		Users *[]string `json:"users"`
	}
	if c.ShouldBindJSON(&body) != nil || body.Users == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "users array required"})
		return
	}
	if err := h.store.ReplaceNodeAccess(c.Param("id"), actorOf(c).UserID, *body.Users); err != nil {
		ownershipError(c, err)
		return
	}
	h.auditACL(actorOf(c), "node.access", c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
func (h *Hub) auditACL(a Actor, method, id string) {
	now := time.Now().Unix()
	h.store.InsertAudit(a.Kind, a.ID, method, id, "", 1, false, "ok", now, now)
	h.reg.broadcast("node", "event.node", gin.H{"event": "access", "id": id})
}
func (h *Hub) requireHumanAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		a, ok := actorOf(c).refreshed()
		if !ok || a.Role != "admin" || a.Kind != "user" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"ok": false, "error": "instance owner account required"})
			return
		}
		c.Next()
	}
}
func retiredAccessRoute(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{"ok": false, "error": "legacy access policy retired; use /nodes/:id/access"})
}
