//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecipientGatewayDatabase(t *testing.T) {
	path := os.Getenv("BANKING_RECIPIENT_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_RECIPIENT_TEST_ENV requires an isolated real database")
	}
	env, e := godotenv.Read(path)
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := config.Load(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal(e)
	}
	dbName := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		dbName = cfg.PostgresDatabase
	}
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatal("isolated *_test database required")
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal("test database connection failed")
	}
	defer conn.Close()
	ctx := context.Background()
	if e = migration.Apply(ctx, conn.SQL); e != nil {
		t.Fatal(e)
	}
	if e = migration.Apply(ctx, conn.SQL); e != nil {
		t.Fatal("migration replay", e)
	}

	uid := time.Now().UnixNano()%1000000000000000 + 1000000000000
	core := service.New(repository.New(conn.DB), risk.SimulationGateway{})
	directory := recipients.New(conn.DB)
	svc := New(conn.DB, nil)
	accounts := map[int64]entity.BankAccount{}
	phones := map[int64]string{}
	sameName := "同名收款" + strconv.FormatInt(uid, 10)
	for i := int64(0); i < 4; i++ {
		id := uid + i
		if e := conn.DB.Create(&entity.User{ID: id, Status: "active", DisplayName: sameName}).Error; e != nil {
			t.Fatal(e)
		}
		phone := fmt.Sprintf("+86139%08d", (uid+i)%100000000)
		phones[id] = phone
		if e := conn.DB.Model(&entity.User{}).Where("id=?", id).Updates(map[string]any{"phone_number": phone, "phone_verified_at": time.Now().UnixMilli(), "phone_verification_method": "development"}).Error; e != nil {
			t.Fatal(e)
		}
		a, e := core.CreateAccount(ctx, service.Meta{UserID: id}, uuid.NewString(), service.AccountInput{DisplayName: "目录转账账户", Currency: "CNY", InitialBalanceMinor: 100000, ConfirmSimulatedFunds: true})
		if e != nil {
			t.Fatal(e)
		}
		accounts[id] = a
	}
	actor := func(user int64) identity.Actor {
		return identity.Actor{Kind: "user", UserID: strconv.FormatInt(user, 10), TokenID: uuid.NewString(), Scopes: []string{"execution:prepare", "execution:read", "execution:authorize", "execution:execute"}}
	}
	meta := func(user int64) service.Meta { return service.Meta{UserID: user, TraceID: uuid.NewString()} }
	configure := func(user int64, enabled bool, account int64) {
		t.Helper()
		v, e := directory.Settings(ctx, user)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = directory.Configure(ctx, user, recipients.SettingsInput{Enabled: enabled, AccountID: account, ExpectedVersion: v.Version}); e != nil {
			t.Fatal(e)
		}
	}
	payment := func(user, target int64) recipients.PaymentInput {
		v, e := directory.Settings(ctx, target)
		if e != nil {
			t.Fatal(e)
		}
		return recipients.PaymentInput{RecipientID: target, RecipientVersion: v.Version, SourceAccountID: accounts[user].ID, AmountMinor: 1000, Description: "聚餐备注 <保留原文>"}
	}
	prepare := func(user int64, p recipients.PaymentInput) View {
		v, e := svc.Prepare(ctx, actor(user), meta(user), uuid.NewString(), PrepareInput{Kind: "recipient_transfer", Parameters: marshal(p)})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	approve := func(user int64, v View) View {
		v, e := svc.Authorize(ctx, actor(user), meta(user), v.Operation.ID, AuthorizeInput{Decision: "approve", IntentDigest: v.Operation.IntentDigest, ConfirmationNonce: v.Operation.Nonce})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	execute := func(user int64, v View) (View, error) {
		return svc.Execute(ctx, actor(user), meta(user), v.Operation.ID, uuid.NewString(), ExecuteInput{AuthorizationID: v.Operation.AuthorizationID, IntentDigest: v.Operation.IntentDigest, ExpectedVersion: v.Operation.Version})
	}
	balance := func(user int64) int64 {
		var a entity.BankAccount
		if e := conn.DB.First(&a, accounts[user].ID).Error; e != nil {
			t.Fatal(e)
		}
		return a.BalanceMinor
	}
	t.Run("default_optout_and_owned_account", func(t *testing.T) {
		v, e := directory.Settings(ctx, uid+1)
		if e != nil || v.Enabled || v.AccountID != 0 || v.Version != 1 {
			t.Fatal(v, e)
		}
		r, e := directory.Search(ctx, uid, recipients.SearchInput{Kind: "name", Query: sameName})
		if e != nil || len(r.Items) != 0 {
			t.Fatal("default receiving exposed", e)
		}
		if _, e := directory.Configure(ctx, uid+1, recipients.SettingsInput{Enabled: true, AccountID: accounts[uid].ID, ExpectedVersion: 1}); entity.Code(e) != "NOT_FOUND" {
			t.Fatal(e)
		}
		configure(uid+1, true, accounts[uid+1].ID)
		configure(uid+2, true, accounts[uid+2].ID)
	})
	t.Run("exact_name_phone_mask_and_self_exclusion", func(t *testing.T) {
		r, e := directory.Search(ctx, uid, recipients.SearchInput{Kind: "name", Query: sameName})
		if e != nil || len(r.Items) != 2 || r.Limited {
			t.Fatal("same-name candidates missing", e)
		}
		for _, item := range r.Items {
			if !strings.Contains(item.PhoneMasked, "****") {
				t.Fatal("phone not masked")
			}
		}
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), phones[uid+1]) || strings.Contains(string(raw), "balance_minor") || strings.Contains(string(raw), "account_id") {
			t.Fatal("directory leaks private fields")
		}
		r, e = directory.Search(ctx, uid, recipients.SearchInput{Kind: "phone", Query: strings.TrimPrefix(phones[uid+1], "+86")})
		if e != nil || len(r.Items) != 1 || r.Items[0].ID != uid+1 {
			t.Fatal("phone exact lookup failed", e)
		}
		r, e = directory.Search(ctx, uid+1, recipients.SearchInput{Kind: "name", Query: sameName})
		if e != nil || len(r.Items) != 1 || r.Items[0].ID != uid+2 {
			t.Fatal("self included", e)
		}
	})
	t.Run("unverified_phone_cannot_enable", func(t *testing.T) {
		if e := conn.DB.Model(&entity.User{}).Where("id=?", uid+3).Update("phone_verified_at", 0).Error; e != nil {
			t.Fatal(e)
		}
		if _, e := directory.Configure(ctx, uid+3, recipients.SettingsInput{Enabled: true, AccountID: accounts[uid+3].ID, ExpectedVersion: 1}); entity.Code(e) != "INVALID_ARGUMENT" {
			t.Fatal(e)
		}
	})
	t.Run("agent_and_foreign_source_rejected", func(t *testing.T) {
		p := payment(uid, uid+1)
		a := actor(uid)
		a.Kind = "agent"
		a.AgentType = "supervisor"
		if _, e := svc.Prepare(ctx, a, meta(uid), uuid.NewString(), PrepareInput{Kind: "recipient_transfer", Parameters: marshal(p)}); entity.Code(e) != "FORBIDDEN" {
			t.Fatal(e)
		}
		p.SourceAccountID = accounts[uid+2].ID
		if _, e := svc.Prepare(ctx, actor(uid), meta(uid), uuid.NewString(), PrepareInput{Kind: "recipient_transfer", Parameters: marshal(p)}); entity.Code(e) != "NOT_FOUND" {
			t.Fatal(e)
		}
	})
	t.Run("bound_authorization_prepare_replay_and_description", func(t *testing.T) {
		p := payment(uid, uid+1)
		key := uuid.NewString()
		in := PrepareInput{Kind: "recipient_transfer", Parameters: marshal(p)}
		v, e := svc.Prepare(ctx, actor(uid), meta(uid), key, in)
		if e != nil {
			t.Fatal(e)
		}
		again, e := svc.Prepare(ctx, actor(uid), meta(uid), key, in)
		if e != nil || again.Operation.ID != v.Operation.ID {
			t.Fatal("prepare replay", e)
		}
		if _, e := execute(uid, v); e == nil {
			t.Fatal("executed without authorization")
		}
		before, target := balance(uid), balance(uid+1)
		v = approve(uid, v)
		out, e := execute(uid, v)
		if e != nil || out.Operation.Status != "succeeded" {
			t.Fatal(out.Operation.Status, e)
		}
		if balance(uid) != before-1000 || balance(uid+1) != target+1000 {
			t.Fatal("wrong posting")
		}
		var rows []entity.Transaction
		if e := conn.DB.Where("external_transaction_id IN (?,?)", "peer:"+v.Operation.ID+":expense", "peer:"+v.Operation.ID+":income").Find(&rows).Error; e != nil || len(rows) != 2 {
			t.Fatal("missing bilateral entries", e)
		}
		for _, row := range rows {
			if row.Description != p.Description {
				t.Fatal("description lost")
			}
		}
		if _, e := execute(uid, v); e != nil {
			t.Fatal(e)
		}
		if balance(uid) != before-1000 {
			t.Fatal("duplicate charge")
		}
		p.AmountMinor++
		if _, e := svc.Prepare(ctx, actor(uid), meta(uid), key, PrepareInput{Kind: "recipient_transfer", Parameters: marshal(p)}); entity.Code(e) != "IDEMPOTENCY_CONFLICT" {
			t.Fatal(e)
		}
	})
	t.Run("concurrent_replay_posts_once", func(t *testing.T) {
		v := approve(uid, prepare(uid, payment(uid, uid+1)))
		before := balance(uid)
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, e := execute(uid, v)
				if e == nil && out.Operation.Status != "succeeded" {
					e = errors.New("failed replay")
				}
				errs <- e
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		if balance(uid) != before-1000 {
			t.Fatal("concurrent duplicate debit")
		}
	})
	t.Run("disable_invalidates_authorized_transfer", func(t *testing.T) {
		v := approve(uid, prepare(uid, payment(uid, uid+1)))
		configure(uid+1, false, 0)
		before := balance(uid)
		out, e := execute(uid, v)
		if e != nil || out.Operation.Status != "failed" || out.Operation.ErrorCode != "STATE_CONFLICT" || balance(uid) != before {
			t.Fatal("closed directory debited", e)
		}
		configure(uid+1, true, accounts[uid+1].ID)
	})
	t.Run("recipient_account_change_invalidates_old_version", func(t *testing.T) {
		v := approve(uid, prepare(uid, payment(uid, uid+1)))
		a, e := core.CreateAccount(ctx, meta(uid+1), uuid.NewString(), service.AccountInput{DisplayName: "New receiving", Currency: "CNY"})
		if e != nil {
			t.Fatal(e)
		}
		configure(uid+1, true, a.ID)
		before := balance(uid)
		out, e := execute(uid, v)
		if e != nil || out.Operation.ErrorCode != "VERSION_CONFLICT" || balance(uid) != before {
			t.Fatal("changed destination executed", e)
		}
		configure(uid+1, true, accounts[uid+1].ID)
	})
	t.Run("identity_change_invalidates_confirmation", func(t *testing.T) {
		v := approve(uid, prepare(uid, payment(uid, uid+1)))
		if e := conn.DB.Model(&entity.User{}).Where("id=?", uid+1).Update("display_name", "Renamed recipient").Error; e != nil {
			t.Fatal(e)
		}
		before := balance(uid)
		out, e := execute(uid, v)
		if e != nil || out.Operation.ErrorCode != "AUTHORIZATION_MISMATCH" || balance(uid) != before {
			t.Fatal("unbound identity", e)
		}
		if e := conn.DB.Model(&entity.User{}).Where("id=?", uid+1).Update("display_name", sameName).Error; e != nil {
			t.Fatal(e)
		}
	})
	t.Run("frozen_account_not_searchable_or_payable", func(t *testing.T) {
		v := approve(uid, prepare(uid, payment(uid, uid+1)))
		if e := conn.DB.Model(&entity.BankAccount{}).Where("id=?", accounts[uid+1].ID).Update("status", "frozen").Error; e != nil {
			t.Fatal(e)
		}
		defer conn.DB.Model(&entity.BankAccount{}).Where("id=?", accounts[uid+1].ID).Update("status", "active")
		r, e := directory.Search(ctx, uid, recipients.SearchInput{Kind: "phone", Query: phones[uid+1]})
		if e != nil || len(r.Items) != 0 {
			t.Fatal("frozen searchable", e)
		}
		out, e := execute(uid, v)
		if e != nil || out.Operation.ErrorCode != "ACCOUNT_FROZEN" {
			t.Fatal(out.Operation.ErrorCode, e)
		}
	})
	t.Run("concurrent_transfers_cannot_overdraw", func(t *testing.T) {
		a, e := core.CreateAccount(ctx, meta(uid), uuid.NewString(), service.AccountInput{DisplayName: "Limited", Currency: "CNY", InitialBalanceMinor: 2000, ConfirmSimulatedFunds: true})
		if e != nil {
			t.Fatal(e)
		}
		vs := []View{}
		for i := 0; i < 3; i++ {
			p := payment(uid, uid+1)
			p.SourceAccountID = a.ID
			vs = append(vs, approve(uid, prepare(uid, p)))
		}
		var wg sync.WaitGroup
		results := make(chan View, 3)
		errs := make(chan error, 3)
		for _, v := range vs {
			wg.Add(1)
			go func(v View) { defer wg.Done(); out, e := execute(uid, v); results <- out; errs <- e }(v)
		}
		wg.Wait()
		close(results)
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		success, denied := 0, 0
		for r := range results {
			if r.Operation.Status == "succeeded" {
				success++
			} else if r.Operation.ErrorCode == "INSUFFICIENT_FUNDS" {
				denied++
			}
		}
		if success != 2 || denied != 1 {
			t.Fatal("incorrect concurrent balance enforcement", success, denied)
		}
	})
	t.Run("recipient_failure_rolls_back_all_posting", func(t *testing.T) {
		v := approve(uid, prepare(uid, payment(uid, uid+1)))
		before, target := balance(uid), balance(uid+1)
		name := "peer_failure_" + uuid.NewString()
		if e := conn.DB.Callback().Create().Before("gorm:create").Register(name, func(db *gorm.DB) {
			if row, ok := db.Statement.Dest.(*entity.Transaction); ok && row.UserID == uid+1 && row.Category == "peer_transfer" {
				db.AddError(errors.New("injected recipient failure"))
			}
		}); e != nil {
			t.Fatal(e)
		}
		_, e := execute(uid, v)
		conn.DB.Callback().Create().Remove(name)
		if e == nil || balance(uid) != before || balance(uid+1) != target {
			t.Fatal("partial posting committed", e)
		}
		out, e := execute(uid, v)
		if e != nil || out.Operation.Status != "succeeded" {
			t.Fatal("retry after rollback", e)
		}
	})
	t.Run("necessary_reserve_protected", func(t *testing.T) {
		a, e := core.CreateAccount(ctx, meta(uid), uuid.NewString(), service.AccountInput{DisplayName: "Protected", Currency: "CNY", InitialBalanceMinor: 1000, ConfirmSimulatedFunds: true})
		if e != nil {
			t.Fatal(e)
		}
		_, e = core.CreateBudget(ctx, meta(uid), uuid.NewString(), service.BudgetInput{Month: time.Now().UTC().Format("2006-01"), Currency: "CNY", TotalMinor: 1000, Categories: []service.CategoryInput{{Category: "food", AllocatedMinor: 1000, EssentialReserveMinor: 1000}}, EnforceReserve: true})
		if e != nil {
			t.Fatal(e)
		}
		p := payment(uid, uid+1)
		p.SourceAccountID = a.ID
		out, e := execute(uid, approve(uid, prepare(uid, p)))
		if e != nil || out.Operation.ErrorCode != "NECESSARY_RESERVE" {
			t.Fatal(out.Operation.ErrorCode, e)
		}
	})
	t.Run("search_result_cap_is_explicit", func(t *testing.T) {
		name := "目录上限" + strconv.FormatInt(uid, 10)
		for i := int64(10); i < 31; i++ {
			id := uid + i
			if e := conn.DB.Create(&entity.User{ID: id, Status: "active", DisplayName: name}).Error; e != nil {
				t.Fatal(e)
			}
			phone := fmt.Sprintf("+86139%08d", (uid+i)%100000000)
			if e := conn.DB.Model(&entity.User{}).Where("id=?", id).Updates(map[string]any{"phone_number": phone, "phone_verified_at": time.Now().UnixMilli(), "phone_verification_method": "development"}).Error; e != nil {
				t.Fatal(e)
			}
			a := entity.BankAccount{Base: entity.Base{UserID: id}, DisplayName: "Directory fixture", AccountType: "checking", MaskedAccountNumber: "SIM ****1234", Currency: "CNY", Status: "active", Version: 1, IsSimulated: true}
			if e := conn.DB.Create(&a).Error; e != nil {
				t.Fatal(e)
			}
			configure(id, true, a.ID)
		}
		r, e := directory.Search(ctx, uid, recipients.SearchInput{Kind: "name", Query: name})
		if e != nil || !r.Limited || len(r.Items) != 20 {
			t.Fatal("directory result cap not explicit", len(r.Items), e)
		}
	})
	t.Run("balanced_journals_and_cash_conserved", func(t *testing.T) {
		var bad int64
		e := conn.DB.Raw("SELECT COUNT(*) FROM (SELECT journal_id FROM banking_ledger_entries WHERE user_id IN (?,?,?,?) GROUP BY journal_id HAVING SUM(delta_minor)<>0 OR COUNT(*)<>2) j", uid, uid+1, uid+2, uid+3).Scan(&bad).Error
		if e != nil || bad != 0 {
			t.Fatal(bad, e)
		}
		var total int64
		e = conn.DB.Model(&entity.BankAccount{}).Where("user_id IN (?,?,?,?)", uid, uid+1, uid+2, uid+3).Select("SUM(balance_minor)").Scan(&total).Error
		if e != nil || total != 403000 {
			t.Fatal("cash not conserved", total, e)
		}
	})
}
