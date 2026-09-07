package main

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NodeAccess is the sole explicit-grant source. User IDs never follow a reused
// username. Owners and instance administrators are implicit, not grant rows.
type NodeAccess struct {
	NodeID string `gorm:"primaryKey"`
	UserID uint   `gorm:"primaryKey;index"`
}

var (
	errClaimed     = errors.New("node already claimed")
	errAccess      = errors.New("node access management not permitted")
	errLastAdmin   = errors.New("cannot remove the last admin")
	errInitialized = errors.New("already initialized")
	errUnknownUser = errors.New("unknown user")
)

func (s *Store) migrateNodeACL() error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var marker Setting
		err := tx.First(&marker, "key = ?", "node_acl_v1").Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var users []User
		if err := tx.Find(&users).Error; err != nil {
			return err
		}
		for _, u := range users {
			// Exact IDs from the historical CSV; no trimming/wildcard expansion.
			for _, id := range splitScope(u.NodeScope) {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&NodeAccess{NodeID: id, UserID: u.ID}).Error; err != nil {
					return err
				}
			}
		}
		// Retire CSV as an authority and prevent reopen from resurrecting revocations.
		if err := tx.Model(&User{}).Where("1 = 1").Update("node_scope", "").Error; err != nil {
			return err
		}
		return tx.Create(&Setting{Key: "node_acl_v1", Value: "1"}).Error
	})
}
func replaceUserGrants(tx *gorm.DB, id uint, scope []string) error {
	if err := tx.Where("user_id = ?", id).Delete(&NodeAccess{}).Error; err != nil {
		return err
	}
	for _, node := range scope {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&NodeAccess{NodeID: node, UserID: id}).Error; err != nil {
			return err
		}
	}
	return tx.Model(&User{}).Where("id = ?", id).Update("node_scope", "").Error
}
func protectLastAdmin(tx *gorm.DB) error {
	var count int64
	if err := tx.Model(&User{}).Where("role = ?", "admin").Count(&count).Error; err != nil {
		return err
	}
	if count <= 1 {
		return errLastAdmin
	}
	return nil
}
func (s *Store) SetupAdmin(username, password string) error {
	_, err := s.SetupAdminRecord(username, password)
	return err
}

func (s *Store) SetupAdminRecord(username, password string) (UserRecord, error) {
	u := User{Username: username, SecretHash: hashSecret(password), Role: "admin", CreatedAt: time.Now().Unix()}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&User{}).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errInitialized
		}
		return tx.Create(&u).Error
	})
	return UserRecord{ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt}, err
}
func (s *Store) DeleteUserAs(id, actingID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.First(&u, id).Error; err != nil {
			return err
		}
		if u.Role == "admin" {
			if err := protectLastAdmin(tx); err != nil {
				return err
			}
		}
		var successor User
		q := tx.Where("role = ? AND id <> ?", "admin", id)
		if actingID != 0 {
			q = q.Where("id = ?", actingID)
		}
		err := q.Order("id").First(&successor).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if actingID != 0 && err != nil {
			return errAccess
		}
		var owned int64
		if err := tx.Model(&Node{}).Where("owner_id = ?", id).Count(&owned).Error; err != nil {
			return err
		}
		if owned > 0 {
			if successor.ID == 0 {
				return fmt.Errorf("no instance owner available for owned nodes")
			}
			if err := tx.Model(&Node{}).Where("owner_id = ?", id).Update("owner_id", successor.ID).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("user_id = ?", id).Delete(&NodeAccess{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&APIKey{}).Where("owner_id = ? AND revoked_at = 0", u.ID).Update("revoked_at", time.Now().Unix()).Error; err != nil {
			return err
		}
		return tx.Delete(&u).Error
	})
}
func (s *Store) ClaimNode(nodeID string, userID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.First(&u, userID).Error; err != nil {
			return err
		}
		// Compare-and-set: only the winner can change zero to its stable user ID.
		r := tx.Model(&Node{}).Where("id = ? AND owner_id = 0", nodeID).Update("owner_id", userID)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 1 {
			return nil
		}
		var n Node
		if err := tx.First(&n, "id = ?", nodeID).Error; err != nil {
			return err
		}
		if n.OwnerID == userID {
			return nil
		}
		return errClaimed
	})
}
func manageNodeAccess(tx *gorm.DB, nodeID string, userID uint) (Node, error) {
	var n Node
	if err := tx.First(&n, "id = ?", nodeID).Error; err != nil {
		return n, err
	}
	var u User
	if err := tx.First(&u, userID).Error; err != nil {
		return n, err
	}
	if u.Role != "admin" && n.OwnerID != u.ID {
		return n, errAccess
	}
	return n, nil
}
func (s *Store) ReplaceNodeAccess(nodeID string, userID uint, usernames []string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := manageNodeAccess(tx, nodeID, userID); err != nil {
			return err
		}
		ids := map[uint]bool{}
		for _, name := range usernames {
			var u User
			err := tx.First(&u, "username = ?", name).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUnknownUser
			}
			if err != nil {
				return err
			}
			ids[u.ID] = true
		}
		if err := tx.Where("node_id = ?", nodeID).Delete(&NodeAccess{}).Error; err != nil {
			return err
		}
		for id := range ids {
			if err := tx.Create(&NodeAccess{NodeID: nodeID, UserID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type NodeAccessUser struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Allowed  bool   `json:"allowed"`
}

func (s *Store) NodeAccessView(nodeID string, userID uint) (string, []NodeAccessUser, error) {
	var owner string
	out := []NodeAccessUser{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		n, err := manageNodeAccess(tx, nodeID, userID)
		if err != nil {
			return err
		}
		var users []User
		if err := tx.Order("id").Find(&users).Error; err != nil {
			return err
		}
		var grants []NodeAccess
		if err := tx.Where("node_id = ?", nodeID).Find(&grants).Error; err != nil {
			return err
		}
		allowed := map[uint]bool{}
		for _, g := range grants {
			allowed[g.UserID] = true
		}
		for _, u := range users {
			if n.OwnerID == u.ID {
				owner = u.Username
			}
			out = append(out, NodeAccessUser{ID: u.ID, Username: u.Username, Role: u.Role, Allowed: u.Role == "admin" || u.ID == n.OwnerID || allowed[u.ID]})
		}
		return nil
	})
	return owner, out, err
}
