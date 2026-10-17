package userauth

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/smsverify"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PhoneVerificationInput struct {
	ChallengeID     string `json:"challenge_id"`
	Code            string `json:"code"`
	CurrentPassword string `json:"current_password"`
}
type PasswordResetInput struct {
	Phone       string `json:"phone"`
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

func smsUnavailable() error {
	return entity.Fail("SMS_UNAVAILABLE", "phone verification is not configured")
}
func invalidVerification() error {
	return entity.Fail("AUTH_CODE_INVALID", "verification code is invalid, expired or already used")
}
func ownerActor(a identity.Actor) bool {
	return a.Kind == "user" && config.PositiveID(a.UserID) && a.Subject == a.UserID
}
func (s *Service) smsReady() bool { return s != nil && s.sms.Enabled() }

// SMSMode distinguishes local development verification from proof of control
// through an actual phone network. No provider secret or code is exposed.
func (s *Service) SMSMode() string {
	if !s.smsReady() {
		return "disabled"
	}
	return s.sms.Mode()
}
func (s *Service) SMSEnabled() bool { return s.smsReady() }

func (s *Service) validateLegacy(ctx context.Context, user string, issuedAt int64) error {
	if s == nil || s.db == nil {
		return unavailable()
	}
	var row struct {
		PasswordChangedAt int64
		Status            string
	}
	err := s.db.WithContext(ctx).Model(&credentialRecord{}).Select("password_changed_at", "status").Where("id=?", user).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} // Operator CLI seeds may initialize a new user.
	if err != nil {
		return unavailable()
	}
	if row.Status != "active" || row.PasswordChangedAt > 0 && (issuedAt <= 0 || issuedAt <= row.PasswordChangedAt/1000) {
		return invalidCredentials()
	}
	return nil
}

func (s *Service) RequestRegistrationCode(ctx context.Context, raw, ip string) (smsverify.Challenge, error) {
	var out smsverify.Challenge
	phone, err := normalizePhone(raw)
	if err != nil {
		return out, err
	}
	if !s.smsReady() {
		return out, smsUnavailable()
	}
	return s.sms.Issue(ctx, phone, smsverify.RegisterPhone, ip, smsverify.Binding{})
}

func (s *Service) RequestPhoneVerification(ctx context.Context, a identity.Actor, ip string) (smsverify.Challenge, error) {
	var out smsverify.Challenge
	if !ownerActor(a) {
		return out, entity.Fail("FORBIDDEN", "only the authenticated owner can verify this phone")
	}
	if !s.smsReady() {
		return out, smsUnavailable()
	}
	row, err := s.ownedCredential(ctx, a)
	if err != nil {
		return out, err
	}
	return s.sms.Issue(ctx, *row.PhoneNumber, smsverify.VerifyPhone, ip, smsverify.Binding{UserID: a.UserID, AuthVersion: row.AuthVersion})
}

func (s *Service) ownedCredential(ctx context.Context, a identity.Actor) (credentialRecord, error) {
	var row credentialRecord
	if s == nil || s.db == nil {
		return row, unavailable()
	}
	if err := s.db.WithContext(ctx).First(&row, "id=?", a.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return row, invalidCredentials()
		}
		return row, authDBError(err)
	}
	if row.Status != "active" || row.PhoneNumber == nil || row.PasswordHash == "" || (a.AuthVersion > 0 && a.AuthVersion != row.AuthVersion) {
		return row, invalidCredentials()
	}
	return row, nil
}

func (s *Service) ConfirmPhoneVerification(ctx context.Context, a identity.Actor, in PhoneVerificationInput, ip string) error {
	if !ownerActor(a) {
		return entity.Fail("FORBIDDEN", "only the authenticated owner can verify this phone")
	}
	if !s.smsReady() {
		return smsUnavailable()
	}
	if err := validatePassword(in.CurrentPassword); err != nil {
		return invalidCredentials()
	}
	row, err := s.ownedCredential(ctx, a)
	if err != nil {
		return err
	}
	phone := *row.PhoneNumber
	if err = s.rateLimit(ctx, phone, ip); err != nil {
		return err
	}
	if !passwordMatches(row.PasswordHash, in.CurrentPassword) {
		return invalidCredentials()
	}
	binding, err := s.sms.Consume(ctx, phone, smsverify.VerifyPhone, in.ChallengeID, in.Code, ip)
	if err != nil {
		return err
	}
	if binding.UserID != a.UserID || binding.AuthVersion != row.AuthVersion {
		return invalidVerification()
	}
	checkedHash, checkedVersion := row.PasswordHash, row.AuthVersion
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id=?", a.UserID).Error; e != nil {
			return authDBError(e)
		}
		if row.Status != "active" || row.PhoneNumber == nil || *row.PhoneNumber != phone || row.PasswordHash != checkedHash || row.AuthVersion != checkedVersion {
			return invalidVerification()
		}
		return authDBError(tx.Model(&credentialRecord{}).Where("id=?", row.ID).Updates(map[string]any{"phone_verified_at": time.Now().UnixMilli(), "phone_verification_method": s.sms.Mode()}).Error)
	})
}

func (s *Service) RequestPasswordReset(ctx context.Context, raw, ip string) (smsverify.Challenge, error) {
	var out smsverify.Challenge
	phone, err := normalizePhone(raw)
	if err != nil {
		return out, err
	}
	if !s.smsReady() {
		return out, smsUnavailable()
	}
	if s.db == nil {
		return out, unavailable()
	}
	var row credentialRecord
	err = s.db.WithContext(ctx).Where("phone_number=?", phone).First(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, authDBError(err)
	}
	binding := smsverify.Binding{}
	if err == nil && row.Status == "active" && row.PhoneVerifiedAt > 0 && row.PhoneVerificationMethod == s.sms.Mode() && row.AuthVersion > 0 {
		binding = smsverify.Binding{UserID: strconv.FormatInt(row.ID, 10), AuthVersion: row.AuthVersion}
	}
	// Unknown and unverified phones take exactly the same delivery path. Their
	// unbound challenges can never claim an existing account during consumption.
	return s.sms.Issue(ctx, phone, smsverify.ResetPassword, ip, binding)
}

func (s *Service) ResetPassword(ctx context.Context, in PasswordResetInput, ip string) error {
	phone, err := normalizePhone(in.Phone)
	if err != nil {
		return err
	}
	if err = validatePassword(in.NewPassword); err != nil {
		return err
	}
	if !s.smsReady() {
		return smsUnavailable()
	}
	if s.db == nil {
		return unavailable()
	}
	binding, err := s.sms.Consume(ctx, phone, smsverify.ResetPassword, in.ChallengeID, in.Code, ip)
	if err != nil {
		return err
	}
	if !config.PositiveID(binding.UserID) || binding.AuthVersion <= 0 {
		return invalidVerification()
	}
	hash, err := hashPassword(in.NewPassword)
	if err != nil {
		return err
	}
	// OTP is already consumed. A SQL failure rolls back the password, without
	// restoring the code; the user must request a fresh challenge before retry.
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row credentialRecord
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id=?", binding.UserID).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return invalidVerification()
			}
			return authDBError(e)
		}
		if row.Status != "active" || row.PhoneNumber == nil || *row.PhoneNumber != phone || row.AuthVersion != binding.AuthVersion || row.AuthVersion == math.MaxInt64 || row.PhoneVerifiedAt <= 0 || row.PhoneVerificationMethod != s.sms.Mode() {
			return invalidVerification()
		}
		return authDBError(tx.Model(&credentialRecord{}).Where("id=?", row.ID).Updates(map[string]any{"password_hash": hash, "auth_version": row.AuthVersion + 1, "refresh_hash": nil, "refresh_expires_at": int64(0), "password_changed_at": time.Now().UnixMilli()}).Error)
	})
}
