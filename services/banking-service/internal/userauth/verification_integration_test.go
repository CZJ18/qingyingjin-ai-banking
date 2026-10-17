//go:build integration

package userauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/smsprovider"
	"github.com/qingyingjin/banking-service/internal/smsverify"
	"gorm.io/gorm"
)

type liveSMSAuth struct {
	service           *Service
	auth              *identity.Auth
	conn              *platform.Connections
	inbox             *smsprovider.Development
	verifier          *smsverify.Verifier
	phone, ip, secret string
}

func smsHarness(t *testing.T) *liveSMSAuth {
	t.Helper()
	_, a, c := authHarness(t)
	var version int
	if err := c.SQL.QueryRow("SELECT MAX(version) FROM banking_schema_migrations").Scan(&version); err != nil || version < 13 {
		t.Fatal("SMS authentication acceptance requires migration 013")
	}
	secret := "test-sms-" + uuid.NewString() + uuid.NewString()
	inbox, err := smsprovider.NewDevelopment(c.Redis, secret)
	if err != nil {
		t.Fatal(err)
	}
	v := smsverify.New(c.Redis, inbox, secret)
	return &liveSMSAuth{service: New(c.DB, a, c.Redis, v), auth: a, conn: c, inbox: inbox, verifier: v, phone: uniquePhone(), ip: "sms-test-" + uuid.NewString(), secret: secret}
}
func (h *liveSMSAuth) code(t *testing.T, phone string, p smsverify.Purpose) string {
	t.Helper()
	msg, err := h.inbox.Read(context.Background(), "+86"+strings.TrimPrefix(phone, "+86"), string(p))
	if err != nil || msg.Code == "" {
		t.Fatal("local encrypted test inbox did not contain issued challenge")
	}
	return msg.Code
}
func (h *liveSMSAuth) register(t *testing.T, password string) Session {
	t.Helper()
	ctx := context.Background()
	ch, err := h.service.RequestRegistrationCode(ctx, h.phone, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	v, err := h.service.Register(ctx, RegisterInput{Phone: h.phone, Password: password, ChallengeID: ch.ChallengeID, Code: h.code(t, h.phone, smsverify.RegisterPhone)}, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Each verifier secret is an isolated test installation, avoiding production
// cooldown removal and making purpose transitions deterministic without sleeps.
func (h *liveSMSAuth) freshVerifier(t *testing.T) {
	t.Helper()
	h.secret = "test-sms-" + uuid.NewString() + uuid.NewString()
	var err error
	h.inbox, err = smsprovider.NewDevelopment(h.conn.Redis, h.secret)
	if err != nil {
		t.Fatal(err)
	}
	h.verifier = smsverify.New(h.conn.Redis, h.inbox, h.secret)
	h.service = New(h.conn.DB, h.auth, h.conn.Redis, h.verifier)
}

func TestSMSVerifiedRegistrationRequiresOneTimeChallenge(t *testing.T) {
	h := smsHarness(t)
	ctx := context.Background()
	if _, err := h.service.Register(ctx, RegisterInput{Phone: h.phone, Password: "sms-registration-123"}, h.ip); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatalf("missing challenge accepted: %v", err)
	}
	ch, err := h.service.RequestRegistrationCode(ctx, h.phone, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	code := h.code(t, h.phone, smsverify.RegisterPhone)
	in := RegisterInput{Phone: h.phone, Password: "sms-registration-123", ChallengeID: ch.ChallengeID, Code: code}
	registered, err := h.service.Register(ctx, in, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	if !registered.PhoneVerified || registered.PhoneVerificationMethod != "development" {
		t.Fatal("development verification state was not explicit")
	}
	if _, err = h.service.Register(ctx, in, h.ip); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatalf("registration OTP replayed: %v", err)
	}
	var row credentialRecord
	if err = h.conn.DB.First(&row, registered.User.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.PhoneVerifiedAt <= 0 || row.PasswordChangedAt != 0 || row.PhoneVerificationMethod != "development" {
		t.Fatal("verification metadata not persisted")
	}
}

func TestSMSPasswordResetRevokesAllOldCredentialsAndLegacyToken(t *testing.T) {
	h := smsHarness(t)
	ctx := context.Background()
	v := h.register(t, "old-password-123")
	legacy, err := h.auth.IssueUser(fmt.Sprint(v.User.ID), "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h.freshVerifier(t)
	ch, err := h.service.RequestPasswordReset(ctx, h.phone, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	input := PasswordResetInput{Phone: h.phone, ChallengeID: ch.ChallengeID, Code: h.code(t, h.phone, smsverify.ResetPassword), NewPassword: "new-password-123"}
	if err = h.service.ResetPassword(ctx, input, h.ip); err != nil {
		t.Fatal(err)
	}
	if _, err = h.auth.User(v.AccessToken); err == nil {
		t.Fatal("password reset did not revoke old browser token")
	}
	if _, err = h.auth.User(legacy); err == nil {
		t.Fatal("password reset did not revoke operator token")
	}
	if _, err = h.service.Refresh(ctx, v.RefreshToken); entity.Code(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatal("password reset left refresh credential active")
	}
	if err = h.service.ResetPassword(ctx, input, h.ip); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatal("reset challenge replayed")
	}
	if _, err = h.service.Login(ctx, Credentials{Phone: h.phone, Password: "old-password-123"}, h.ip); entity.Code(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatal("old password still authenticates")
	}
	if _, err = h.service.Login(ctx, Credentials{Phone: h.phone, Password: "new-password-123"}, h.ip); err != nil {
		t.Fatal("new password cannot authenticate", err)
	}
	// Test seconds precision deterministically, without waiting or logging JWTs.
	var row credentialRecord
	if err = h.conn.DB.First(&row, v.User.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.PasswordChangedAt <= 0 || row.RefreshHash == nil {
		t.Fatal("reset/login metadata missing")
	}
	if err = h.conn.DB.Model(&credentialRecord{}).Where("id=?", v.User.ID).Update("password_changed_at", time.Now().Add(-2*time.Second).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	fresh, err := h.auth.IssueUser(fmt.Sprint(v.User.ID), "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.auth.User(fresh); err != nil {
		t.Fatal("later operator token should remain usable", err)
	}
}

func TestSMSUnverifiedUnknownAndMismatchedModeCannotReset(t *testing.T) {
	for _, kind := range []string{"unknown", "unverified", "different_provider"} {
		t.Run(kind, func(t *testing.T) {
			h := smsHarness(t)
			ctx := context.Background()
			var id int64
			if kind != "unknown" {
				plain := New(h.conn.DB, h.auth, h.conn.Redis)
				v, err := plain.Register(ctx, RegisterInput{Phone: h.phone, Password: "original-password-123"}, h.ip)
				if err != nil {
					t.Fatal(err)
				}
				id = v.User.ID
				if kind == "different_provider" {
					if err = h.conn.DB.Model(&credentialRecord{}).Where("id=?", id).Updates(map[string]any{"phone_verified_at": time.Now().UnixMilli(), "phone_verification_method": "tencent"}).Error; err != nil {
						t.Fatal(err)
					}
				}
				h.service = New(h.conn.DB, h.auth, h.conn.Redis, h.verifier)
			}
			ch, err := h.service.RequestPasswordReset(ctx, h.phone, h.ip)
			if err != nil || ch.ChallengeID == "" {
				t.Fatalf("uniform challenge response failed: %v", err)
			}
			err = h.service.ResetPassword(ctx, PasswordResetInput{Phone: h.phone, ChallengeID: ch.ChallengeID, Code: h.code(t, h.phone, smsverify.ResetPassword), NewPassword: "attacker-password-123"}, h.ip)
			if entity.Code(err) != "AUTH_CODE_INVALID" {
				t.Fatalf("unbound challenge changed password: %v", err)
			}
			if id > 0 {
				var row credentialRecord
				if err = h.conn.DB.First(&row, id).Error; err != nil || !passwordMatches(row.PasswordHash, "original-password-123") {
					t.Fatal("unverified user's password changed")
				}
			}
		})
	}
}

func TestSMSPhoneVerificationRequiresCurrentPasswordAndBoundOwner(t *testing.T) {
	h := smsHarness(t)
	ctx := context.Background()
	plain := New(h.conn.DB, h.auth, h.conn.Redis)
	v, err := plain.Register(ctx, RegisterInput{Phone: h.phone, Password: "current-password-123"}, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	h.service = New(h.conn.DB, h.auth, h.conn.Redis, h.verifier)
	actor, err := h.auth.User(v.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := h.service.RequestPhoneVerification(ctx, actor, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	in := PhoneVerificationInput{ChallengeID: ch.ChallengeID, Code: h.code(t, h.phone, smsverify.VerifyPhone), CurrentPassword: "wrong-password-123"}
	if err = h.service.ConfirmPhoneVerification(ctx, actor, in, h.ip); entity.Code(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatal("verification accepted wrong current password")
	}
	in.CurrentPassword = "current-password-123"
	if err = h.service.ConfirmPhoneVerification(ctx, actor, in, h.ip); err != nil {
		t.Fatal(err)
	}
	if _, err = h.auth.User(v.AccessToken); err != nil {
		t.Fatal("phone verification unexpectedly revoked existing session")
	}
	if err = h.service.ConfirmPhoneVerification(ctx, actor, in, h.ip); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatal("phone verification replayed")
	}
	refreshed, err := h.service.Refresh(ctx, v.RefreshToken)
	if err != nil || !refreshed.PhoneVerified {
		t.Fatal("verified state not available after refresh")
	}
}

func TestSMSPasswordResetRejectsStaleVersionAndConcurrentReplay(t *testing.T) {
	h := smsHarness(t)
	ctx := context.Background()
	v := h.register(t, "initial-password-123")
	h.freshVerifier(t)
	ch, err := h.service.RequestPasswordReset(ctx, h.phone, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	stale := PasswordResetInput{Phone: h.phone, ChallengeID: ch.ChallengeID, Code: h.code(t, h.phone, smsverify.ResetPassword), NewPassword: "stale-password-123"}
	if _, err = h.service.Login(ctx, Credentials{Phone: h.phone, Password: "initial-password-123"}, h.ip); err != nil {
		t.Fatal(err)
	}
	if err = h.service.ResetPassword(ctx, stale, h.ip); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatalf("stale auth version reset accepted: %v", err)
	}
	h.freshVerifier(t)
	ch, err = h.service.RequestPasswordReset(ctx, h.phone, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	current := PasswordResetInput{Phone: h.phone, ChallengeID: ch.ChallengeID, Code: h.code(t, h.phone, smsverify.ResetPassword), NewPassword: "current-password-123"}
	var wg sync.WaitGroup
	var wins, denied atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := h.service.ResetPassword(ctx, current, h.ip)
			if e == nil {
				wins.Add(1)
			} else if entity.Code(e) == "AUTH_CODE_INVALID" {
				denied.Add(1)
			} else {
				t.Errorf("concurrent reset error: %v", e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || denied.Load() != 1 {
		t.Fatalf("reset wins=%d denied=%d", wins.Load(), denied.Load())
	}
	var row credentialRecord
	if err = h.conn.DB.First(&row, v.User.ID).Error; err != nil || !passwordMatches(row.PasswordHash, "current-password-123") {
		t.Fatal("concurrent reset did not persist exactly one result")
	}
}

func TestSMSResetSQLFailureRollsBackPasswordButConsumesChallenge(t *testing.T) {
	h := smsHarness(t)
	ctx := context.Background()
	v := h.register(t, "rollback-password-123")
	h.freshVerifier(t)
	ch, err := h.service.RequestPasswordReset(ctx, h.phone, h.ip)
	if err != nil {
		t.Fatal(err)
	}
	in := PasswordResetInput{Phone: h.phone, ChallengeID: ch.ChallengeID, Code: h.code(t, h.phone, smsverify.ResetPassword), NewPassword: "should-never-persist"}
	var before credentialRecord
	if err = h.conn.DB.First(&before, v.User.ID).Error; err != nil {
		t.Fatal(err)
	}
	name := "sms-reset-failure-" + uuid.NewString()
	if err = h.conn.DB.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "banking_users" {
			if m, ok := tx.Statement.Dest.(map[string]any); ok {
				if _, reset := m["password_changed_at"]; reset {
					tx.AddError(errors.New("injected reset transaction failure"))
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	err = h.service.ResetPassword(ctx, in, h.ip)
	if removeErr := h.conn.DB.Callback().Update().Remove(name); removeErr != nil {
		t.Fatal(removeErr)
	}
	if err == nil {
		t.Fatal("injected transaction failure unexpectedly succeeded")
	}
	var after credentialRecord
	if err = h.conn.DB.First(&after, v.User.ID).Error; err != nil {
		t.Fatal(err)
	}
	if before.PasswordHash != after.PasswordHash || before.AuthVersion != after.AuthVersion || after.PasswordChangedAt != 0 {
		t.Fatal("SQL transaction failure partially updated credentials")
	}
	if err = h.service.ResetPassword(ctx, in, h.ip); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatal("failed SQL reset restored consumed OTP")
	}
}

// Missing iat cannot bypass post-reset revocation even for a valid signature.
func TestSMSLegacyMissingIssuedAtFailsAfterReset(t *testing.T) {
	h := smsHarness(t)
	ctx := context.Background()
	v := h.register(t, "legacy-password-123")
	if err := h.conn.DB.Model(&credentialRecord{}).Where("id=?", v.User.ID).Update("password_changed_at", time.Now().UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.service.validateLegacy(ctx, fmt.Sprint(v.User.ID), 0); entity.Code(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatal("missing issued-at allowed after reset")
	}
}
