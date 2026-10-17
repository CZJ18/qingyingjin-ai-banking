//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/cards"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
)

func TestCardsGatewayDatabase(t *testing.T) {
	path := os.Getenv("BANKING_CARD_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_CARD_TEST_ENV requires an isolated real database")
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
	if e = conn.DB.Create(&entity.User{ID: uid, Status: "active", DisplayName: "Card integration fixture"}).Error; e != nil {
		t.Fatal(e)
	}
	account := entity.BankAccount{Base: entity.Base{UserID: uid}, DisplayName: "Card account fixture", AccountType: "checking", MaskedAccountNumber: "SIM ****1234", Currency: "CNY", Status: "active", Version: 1, IsSimulated: true}
	if e = conn.DB.Create(&account).Error; e != nil {
		t.Fatal(e)
	}
	if e = conn.DB.Create(&entity.User{ID: uid + 1, Status: "active", DisplayName: "Other card owner fixture"}).Error; e != nil {
		t.Fatal(e)
	}
	foreignAccount := account
	foreignAccount.ID = 0
	foreignAccount.UserID = uid + 1
	if e = conn.DB.Create(&foreignAccount).Error; e != nil {
		t.Fatal(e)
	}
	svc := New(conn.DB, nil)
	meta := service.Meta{UserID: uid, TraceID: uuid.NewString()}
	actor := identity.Actor{Kind: "user", UserID: strconv.FormatInt(uid, 10), TokenID: uuid.NewString(), Scopes: []string{"execution:prepare", "execution:read", "execution:authorize", "execution:execute"}}
	bind := cards.Input{Action: "bind", AccountID: account.ID, Issuer: "Fixture bank", Name: "Fixture card", LastFour: "1234", CardType: "credit", CreditLimitMinor: 100000}
	prepare := func(p cards.Input) View {
		v, e := svc.Prepare(ctx, actor, meta, uuid.NewString(), PrepareInput{Kind: "card_manage", Parameters: marshal(p)})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	approve := func(v View) View {
		v, e := svc.Authorize(ctx, actor, meta, v.Operation.ID, AuthorizeInput{Decision: "approve", IntentDigest: v.Operation.IntentDigest, ConfirmationNonce: v.Operation.Nonce})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	execute := func(v View, key string) (View, error) {
		return svc.Execute(ctx, actor, meta, v.Operation.ID, key, ExecuteInput{AuthorizationID: v.Operation.AuthorizationID, IntentDigest: v.Operation.IntentDigest, ExpectedVersion: v.Operation.Version})
	}
	t.Run("agent_cannot_prepare", func(t *testing.T) {
		agent := actor
		agent.Kind = "agent"
		agent.AgentType = "supervisor"
		_, e := svc.Prepare(ctx, agent, meta, uuid.NewString(), PrepareInput{Kind: "card_manage", Parameters: marshal(bind)})
		if entity.Code(e) != "FORBIDDEN" {
			t.Fatal(e)
		}
	})
	t.Run("account_ownership", func(t *testing.T) {
		other := bind
		other.AccountID = foreignAccount.ID
		_, e := svc.Prepare(ctx, actor, meta, uuid.NewString(), PrepareInput{Kind: "card_manage", Parameters: marshal(other)})
		if entity.Code(e) != "NOT_FOUND" {
			t.Fatal(e)
		}
	})
	t.Run("frozen_account", func(t *testing.T) {
		conn.DB.Model(&account).Update("status", "frozen")
		defer conn.DB.Model(&account).Update("status", "active")
		_, e := svc.Prepare(ctx, actor, meta, uuid.NewString(), PrepareInput{Kind: "card_manage", Parameters: marshal(bind)})
		if entity.Code(e) != "ACCOUNT_FROZEN" {
			t.Fatal(e)
		}
	})
	var first cards.Card
	t.Run("confirmation_replay_and_zero_money_effect", func(t *testing.T) {
		v := prepare(bind)
		_, e := execute(v, uuid.NewString())
		if e == nil {
			t.Fatal("unapproved operation executed")
		}
		v = approve(v)
		out, e := execute(v, uuid.NewString())
		if e != nil || out.Operation.Status != "succeeded" {
			t.Fatal(e, out.Operation.ErrorCode)
		}
		if _, e = execute(v, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		all, e := cards.Read(conn.DB, uid)
		if e != nil || len(all) != 1 {
			t.Fatal(e)
		}
		first = all[0]
		if first.CreditAvailableMinor != 100000 {
			t.Fatal("credit calculation")
		}
		var balance int64
		conn.DB.Model(&account).Select("balance_minor").Scan(&balance)
		if balance != 0 {
			t.Fatal("card increased cash")
		}
		var entries int64
		conn.DB.Model(&entity.LedgerEntry{}).Where("user_id = ?", uid).Count(&entries)
		if entries != 0 {
			t.Fatal("metadata produced money entries")
		}
	})
	if first.ID == "" {
		t.Fatal("card fixture not created")
	}
	t.Run("card_owner_isolation", func(t *testing.T) {
		other, e := cards.Read(conn.DB, uid+1)
		if e != nil || len(other) != 0 {
			t.Fatal("cards leaked to another owner", e)
		}
		otherMeta := meta
		otherMeta.UserID = uid + 1
		otherActor := actor
		otherActor.UserID = strconv.FormatInt(uid+1, 10)
		_, e = svc.Prepare(ctx, otherActor, otherMeta, uuid.NewString(), PrepareInput{Kind: "card_manage", Parameters: marshal(cards.Input{Action: "unbind", CardID: first.ID})})
		if entity.Code(e) != "NOT_FOUND" {
			t.Fatal(e)
		}
	})
	t.Run("duplicate_bind", func(t *testing.T) {
		_, e := svc.Prepare(ctx, actor, meta, uuid.NewString(), PrepareInput{Kind: "card_manage", Parameters: marshal(bind)})
		if entity.Code(e) != "STATE_CONFLICT" {
			t.Fatal(e)
		}
	})
	t.Run("stale_confirmation_and_default", func(t *testing.T) {
		pending := approve(prepare(cards.Input{Action: "set_default", CardID: first.ID}))
		p := bind
		p.LastFour = "5678"
		fresh := approve(prepare(p))
		if v, e := execute(fresh, uuid.NewString()); e != nil || v.Operation.Status != "succeeded" {
			t.Fatal(e)
		}
		v, e := execute(pending, uuid.NewString())
		if e != nil || v.Operation.Status != "failed" || v.Operation.ErrorCode != "STATE_CONFLICT" {
			t.Fatal(e, v.Operation.ErrorCode)
		}
		next := approve(prepare(cards.Input{Action: "set_default", CardID: first.ID}))
		if v, e = execute(next, uuid.NewString()); e != nil || v.Operation.Status != "succeeded" {
			t.Fatal(e)
		}
	})
	t.Run("concurrent_unbind_posts_once", func(t *testing.T) {
		v := approve(prepare(cards.Input{Action: "unbind", CardID: first.ID}))
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, e := execute(v, uuid.NewString())
				if e == nil && out.Operation.Status != "succeeded" {
					e = entity.Fail("STATE_CONFLICT", "unexpected final status")
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
		all, e := cards.Read(conn.DB, uid)
		if e != nil || len(all) != 2 || all[0].Status != "unbound" || all[0].Default {
			t.Fatal(e)
		}
	})
	t.Run("storage_fence_covers_card_metadata", func(t *testing.T) {
		portfolio, e := cards.Read(conn.DB, uid)
		if e != nil {
			t.Fatal(e)
		}
		e = conn.DB.Transaction(func(tx *gorm.DB) error {
			if e := tx.Exec("UPDATE banking_storage_control SET mode='frozen' WHERE id=1").Error; e != nil {
				return e
			}
			return cards.Save(tx, uid, portfolio)
		})
		if e == nil || !strings.Contains(e.Error(), "BANKING_STORAGE_FROZEN") {
			t.Fatal("card metadata bypassed storage fence", e)
		}
	})
	t.Run("owner_row_rollback", func(t *testing.T) {
		before, e := cards.Read(conn.DB, uid)
		if e != nil {
			t.Fatal(e)
		}
		_ = conn.DB.Transaction(func(tx *gorm.DB) error {
			if e := lockUser(tx, uid); e != nil {
				return e
			}
			next := append([]cards.Card{}, before...)
			next[1].Name = "rolled back"
			if e := cards.Save(tx, uid, next); e != nil {
				return e
			}
			return entity.Fail("STATE_CONFLICT", "injected rollback")
		})
		after, e := cards.Read(conn.DB, uid)
		if e != nil {
			t.Fatal(e)
		}
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(after)
		if string(a) != string(b) {
			t.Fatal("rollback lost metadata")
		}
	})
}
