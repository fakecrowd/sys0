package main

import (
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
)

// Stable-ID variants are required for authenticated requests. Username wrappers
// are only for trusted callers that intentionally resolve the current account.
func (s *Store) CreateAccountKeyByUserID(ownerID uint, name string, methods []string, rate int) (string, KeyRecord, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "mcp-http"
	}
	if rate < 0 {
		rate = 0
	}
	secret := "sk_" + randHexS(20)
	var k APIKey
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Where("id = ?", ownerID).First(&u).Error; err != nil {
			return err
		}
		k = APIKey{ID: "k" + randHexS(4), Name: name, Owner: u.Username, OwnerID: u.ID, SecretHash: hashSecret(secret), MethodScope: strings.Join(methods, ","), RateLimit: rate, CreatedAt: time.Now().Unix()}
		return tx.Create(&k).Error
	})
	if err != nil {
		return "", KeyRecord{}, err
	}
	return secret, keyView(k), nil
}
func (s *Store) ListKeysForUserID(ownerID uint) ([]KeyRecord, error) {
	var keys []APIKey
	err := s.db.Where("owner_id = ? AND revoked_at = 0", ownerID).Order("created_at desc").Find(&keys).Error
	out := make([]KeyRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyView(k))
	}
	return out, err
}
func (s *Store) RevokeKeyForUserID(id string, ownerID uint) (bool, error) {
	changed := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Where("id = ?", ownerID).First(&u).Error; err != nil {
			return err
		}
		r := tx.Model(&APIKey{}).Where("id = ? AND owner_id = ? AND revoked_at = 0", id, ownerID).Update("revoked_at", time.Now().Unix())
		changed = r.RowsAffected == 1
		return r.Error
	})
	return changed, err
}
func (s *Store) ChangeOwnPassword(id uint, oldSecret, newSecret string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Where("id = ?", id).First(&u).Error; err != nil {
			return err
		}
		if !verifySecret(oldSecret, u.SecretHash) {
			return fmt.Errorf("current password incorrect")
		}
		return tx.Model(&User{}).Where("id = ?", id).Update("secret_hash", hashSecret(newSecret)).Error
	})
}
