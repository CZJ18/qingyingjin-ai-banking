// Package userauth owns browser credentials. Agents never receive passwords or
// refresh tokens; the existing Banking JWT contract remains the only identity
// forwarded to financial APIs and the separate Agent service.
package userauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/smsverify"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const accessTTL = 15 * time.Minute
const refreshTTL = 24 * time.Hour

type Credentials struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}
type RegisterInput struct {
	Phone       string `json:"phone"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name,omitempty"`
	ChallengeID string `json:"challenge_id,omitempty"`
	Code        string `json:"code,omitempty"`
}
type Session struct {
	AccessToken             string      `json:"access_token"`
	ExpiresIn               int64       `json:"expires_in"`
	User                    entity.User `json:"user"`
	PhoneMasked             string      `json:"phone_masked"`
	PhoneVerified           bool        `json:"phone_verified"`
	PhoneVerificationMethod string      `json:"phone_verification_method"`
	RefreshToken            string      `json:"-"`
}

// This private model deliberately keeps credential columns out of entity.User.
type credentialRecord struct {
	entity.User             `gorm:"embedded"`
	PhoneNumber             *string
	PasswordHash            string
	AuthVersion             int64
	RefreshHash             *string
	RefreshExpiresAt        int64
	PhoneVerifiedAt         int64
	PasswordChangedAt       int64
	PhoneVerificationMethod string
}

func (credentialRecord) TableName() string { return "banking_users" }

type Service struct {
	db    *gorm.DB
	auth  *identity.Auth
	redis *redis.Client
	sms   *smsverify.Verifier
}

func New(db *gorm.DB, auth *identity.Auth, cache *redis.Client, verification ...*smsverify.Verifier) *Service {
	s := &Service{db: db, auth: auth, redis: cache}
	if len(verification) > 0 {
		s.sms = verification[0]
	}
	if auth != nil {
		auth.SetSessionVersionValidator(s.validateSession)
		auth.SetLegacyValidator(s.validateLegacy)
		auth.SetAgentOwnerValidator(s.validateAgentOwner)
	}
	return s
}

func invalidCredentials() error {
	return entity.Fail("AUTH_INVALID_CREDENTIALS", "phone number or password is incorrect, or the session has expired")
}
func phoneUnavailable() error {
	return entity.Fail("AUTH_PHONE_UNAVAILABLE", "this phone number cannot be used")
}

func authDBError(err error) error {
	if err == nil {
		return nil
	}
	err = repository.DBError(err)
	if entity.Code(err) == "IDEMPOTENCY_CONFLICT" {
		return phoneUnavailable()
	}
	if entity.Code(err) == "STORAGE_FROZEN" {
		return err
	}
	return unavailable()
}

func (s *Service) configured() bool {
	return s != nil && s.db != nil && s.auth != nil && s.redis != nil
}

func (s *Service) validateAgentOwner(ctx context.Context, user string) error {
	if s == nil || s.db == nil {
		return unavailable()
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&credentialRecord{}).Where("id=? AND status='active'", user).Count(&count).Error; err != nil {
		return unavailable()
	}
	if count != 1 {
		return invalidCredentials()
	}
	return nil
}

func (s *Service) validateSession(ctx context.Context, user string, version int64) error {
	if s == nil || s.db == nil {
		return unavailable()
	}
	var count int64
	err := s.db.WithContext(ctx).Model(&credentialRecord{}).Where("id=? AND status=? AND auth_version=? AND refresh_expires_at>? AND refresh_hash IS NOT NULL", user, "active", version, time.Now().UnixMilli()).Count(&count).Error
	if err != nil {
		return unavailable()
	}
	if count != 1 {
		return invalidCredentials()
	}
	return nil
}

func (s *Service) Register(ctx context.Context, in RegisterInput, ip string) (Session, error) {
	var out Session
	phone, err := normalizePhone(in.Phone)
	if err != nil {
		return out, err
	}
	if err = validatePassword(in.Password); err != nil {
		return out, err
	}
	name := strings.TrimSpace(in.DisplayName)
	if name == "" {
		name = "轻融金用户"
	}
	if len(name) > 128 {
		return out, entity.Fail("INVALID_ARGUMENT", "display_name must contain at most 128 UTF-8 bytes")
	}
	if !s.configured() {
		return out, unavailable()
	}
	if err = s.rateLimit(ctx, phone, ip); err != nil {
		return out, err
	}
	verifiedAt := int64(0)
	method := ""
	if s.sms.Enabled() {
		if _, err = s.sms.Consume(ctx, phone, smsverify.RegisterPhone, in.ChallengeID, in.Code, ip); err != nil {
			return out, err
		}
		verifiedAt = time.Now().UnixMilli()
		method = s.sms.Mode()
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return out, err
	}
	refresh, digest, err := newRefresh()
	if err != nil {
		return out, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for attempt := 0; attempt < 4; attempt++ {
			id, e := newUserID()
			if e != nil {
				return e
			}
			row := credentialRecord{User: entity.User{ID: id, DisplayName: name, Status: "active"}, PhoneNumber: &phone, PasswordHash: hash, AuthVersion: 1, RefreshHash: &digest, RefreshExpiresAt: time.Now().Add(refreshTTL).UnixMilli()}
			row.PhoneVerifiedAt, row.PhoneVerificationMethod = verifiedAt, method
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return authDBError(result.Error)
			}
			if result.RowsAffected == 0 {
				var count int64
				if e = tx.Model(&credentialRecord{}).Where("phone_number=?", phone).Count(&count).Error; e != nil {
					return authDBError(e)
				}
				if count > 0 {
					return phoneUnavailable()
				}
				continue
			}
			out, e = s.session(row, refresh)
			return e
		}
		return unavailable()
	})
	return out, err
}

func (s *Service) Login(ctx context.Context, in Credentials, ip string) (Session, error) {
	var out Session
	phone, err := normalizePhone(in.Phone)
	if err != nil {
		return out, err
	}
	if err = validatePassword(in.Password); err != nil {
		return out, err
	}
	if !s.configured() {
		return out, unavailable()
	}
	if err = s.rateLimit(ctx, phone, ip); err != nil {
		return out, err
	}
	var row credentialRecord
	err = s.db.WithContext(ctx).Where("phone_number=?", phone).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		dummyPasswordWork(in.Password)
		return out, invalidCredentials()
	}
	if err != nil {
		return out, authDBError(err)
	}
	if !passwordMatches(row.PasswordHash, in.Password) {
		return out, invalidCredentials()
	}
	if row.Status != "active" {
		return out, invalidCredentials()
	}
	checkedHash := row.PasswordHash
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, row.ID).Error; e != nil {
			return authDBError(e)
		}
		if row.Status != "active" || row.PasswordHash != checkedHash {
			return invalidCredentials()
		}
		var e error
		out, e = s.rotate(tx, &row)
		return e
	})
	return out, err
}

func (s *Service) Bind(ctx context.Context, actor identity.Actor, in Credentials, ip string) (Session, error) {
	var out Session
	if actor.Kind != "user" || !config.PositiveID(actor.UserID) || actor.Subject != actor.UserID {
		return out, entity.Fail("FORBIDDEN", "only the authenticated account owner can bind credentials")
	}
	phone, err := normalizePhone(in.Phone)
	if err != nil {
		return out, err
	}
	if err = validatePassword(in.Password); err != nil {
		return out, err
	}
	if !s.configured() {
		return out, unavailable()
	}
	if err = s.rateLimit(ctx, phone, ip); err != nil {
		return out, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return out, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row credentialRecord
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id=?", actor.UserID).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return invalidCredentials()
			}
			return authDBError(e)
		}
		if row.Status != "active" {
			return invalidCredentials()
		}
		if row.PhoneNumber != nil || row.PasswordHash != "" {
			return entity.Fail("STATE_CONFLICT", "this account already has login credentials")
		}
		if e := tx.Model(&credentialRecord{}).Where("id=?", row.ID).Updates(map[string]any{"phone_number": phone, "password_hash": hash}).Error; e != nil {
			return authDBError(e)
		}
		row.PhoneNumber = &phone
		row.PasswordHash = hash
		var e error
		out, e = s.rotate(tx, &row)
		return e
	})
	return out, err
}

func (s *Service) rotate(tx *gorm.DB, row *credentialRecord) (Session, error) {
	var out Session
	if row.AuthVersion < 0 || row.AuthVersion == math.MaxInt64 {
		return out, unavailable()
	}
	refresh, digest, err := newRefresh()
	if err != nil {
		return out, err
	}
	row.AuthVersion++
	row.RefreshHash = &digest
	row.RefreshExpiresAt = time.Now().Add(refreshTTL).UnixMilli()
	if err = tx.Model(&credentialRecord{}).Where("id=?", row.ID).Updates(map[string]any{"auth_version": row.AuthVersion, "refresh_hash": digest, "refresh_expires_at": row.RefreshExpiresAt}).Error; err != nil {
		return out, authDBError(err)
	}
	return s.session(*row, refresh)
}

func (s *Service) session(row credentialRecord, refresh string) (Session, error) {
	var out Session
	token, err := s.auth.IssueSession(strconv.FormatInt(row.ID, 10), row.AuthVersion, accessTTL)
	if err != nil {
		return out, unavailable()
	}
	masked := ""
	if row.PhoneNumber != nil {
		masked = maskPhone(*row.PhoneNumber)
	}
	return Session{AccessToken: token, ExpiresIn: int64(accessTTL / time.Second), User: row.User, PhoneMasked: masked, PhoneVerified: row.PhoneVerifiedAt > 0, PhoneVerificationMethod: row.PhoneVerificationMethod, RefreshToken: refresh}, nil
}

func (s *Service) Refresh(ctx context.Context, raw string) (Session, error) {
	var out Session
	digest, ok := refreshDigest(raw)
	if !ok {
		return out, invalidCredentials()
	}
	if s == nil || s.db == nil || s.auth == nil {
		return out, unavailable()
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row credentialRecord
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("refresh_hash=?", digest).First(&row).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return invalidCredentials()
			}
			return authDBError(e)
		}
		if row.Status != "active" || row.AuthVersion <= 0 || row.RefreshExpiresAt <= time.Now().UnixMilli() {
			return invalidCredentials()
		}
		var e error
		out, e = s.session(row, raw)
		return e
	})
	return out, err
}

func (s *Service) Logout(ctx context.Context, raw string) error {
	digest, ok := refreshDigest(raw)
	if !ok {
		return nil
	}
	if s == nil || s.db == nil {
		return unavailable()
	}
	err := s.db.WithContext(ctx).Model(&credentialRecord{}).Where("refresh_hash=?", digest).Updates(map[string]any{"auth_version": gorm.Expr("CASE WHEN auth_version < ? THEN auth_version + 1 ELSE auth_version END", int64(math.MaxInt64)), "refresh_hash": nil, "refresh_expires_at": int64(0)}).Error
	return authDBError(err)
}

func newUserID() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, unavailable()
	}
	id := int64(binary.BigEndian.Uint64(b[:]) & math.MaxInt64)
	if id == 0 {
		id = 1
	}
	return id, nil
}
func newRefresh() (string, string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", unavailable()
	}
	raw := base64.RawURLEncoding.EncodeToString(b[:])
	digest, _ := refreshDigest(raw)
	return raw, digest, nil
}
func refreshDigest(raw string) (string, bool) {
	if len(raw) != 43 {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return "", false
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:]), true
}
