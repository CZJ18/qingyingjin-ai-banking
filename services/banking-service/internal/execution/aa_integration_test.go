//go:build integration

package execution

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/aa"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/platform"
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

func TestAAGatewayDatabase(t *testing.T) {
	path := os.Getenv("BANKING_AA_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_AA_TEST_ENV requires an isolated real database")
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
	accounts := map[int64]entity.BankAccount{}
	for i := int64(0); i < 4; i++ {
		id := uid + i
		if e := conn.DB.Create(&entity.User{ID: id, Status: "active", DisplayName: "AA fixture"}).Error; e != nil {
			t.Fatal(e)
		}
		a, e := core.CreateAccount(ctx, service.Meta{UserID: id, TraceID: uuid.NewString()}, uuid.NewString(), service.AccountInput{DisplayName: "AA account", AccountType: "checking", Currency: "CNY", InitialBalanceMinor: 100000, ConfirmSimulatedFunds: true})
		if e != nil {
			t.Fatal(e)
		}
		accounts[id] = a
	}
	svc := New(conn.DB, nil)
	bills := aa.New(conn.DB)
	actor := func(user int64) identity.Actor {
		return identity.Actor{Kind: "user", UserID: strconv.FormatInt(user, 10), TokenID: uuid.NewString(), Scopes: []string{"execution:prepare", "execution:read", "execution:authorize", "execution:execute"}}
	}
	meta := func(user int64) service.Meta { return service.Meta{UserID: user, TraceID: uuid.NewString()} }
	create := func() aa.View {
		v, e := bills.Create(ctx, uid, uuid.NewString(), aa.Input{Title: "AA聚餐", TotalMinor: 3000, Mode: "equal", AccountID: accounts[uid].ID, AlreadyPaid: true, Participants: []aa.ShareInput{{Name: "甲"}, {Name: "乙"}}})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	join := func(v aa.View, user int64, index int) aa.PaymentInput {
		v, e := bills.Join(ctx, user, v.Invitation, v.Participants[index].ID)
		if e != nil {
			t.Fatal(e)
		}
		return aa.PaymentInput{OwnerID: uid, BillID: v.ID, ParticipantID: v.Participants[index].ID, SourceAccountID: accounts[user].ID}
	}
	prepare := func(user int64, p aa.PaymentInput) View {
		v, e := svc.Prepare(ctx, actor(user), meta(user), uuid.NewString(), PrepareInput{Kind: "aa_payment", Parameters: marshal(p)})
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
	t.Run("creation_replay_and_custom_sum", func(t *testing.T) {
		key := uuid.NewString()
		in := aa.Input{Title: "AA", TotalMinor: 101, Mode: "equal", AccountID: accounts[uid].ID, AlreadyPaid: true, Participants: []aa.ShareInput{{Name: "甲"}}}
		a, e := bills.Create(ctx, uid, key, in)
		if e != nil {
			t.Fatal(e)
		}
		b, e := bills.Create(ctx, uid, key, in)
		if e != nil || a.ID != b.ID {
			t.Fatal(b, e)
		}
		in.TotalMinor++
		if _, e := bills.Create(ctx, uid, key, in); entity.Code(e) != "IDEMPOTENCY_CONFLICT" {
			t.Fatal(e)
		}
	})
	t.Run("invite_claim_and_tenant_isolation", func(t *testing.T) {
		b := create()
		if _, e := bills.Get(ctx, uid+3, uid, b.ID); entity.Code(e) != "NOT_FOUND" {
			t.Fatal(e)
		}
		bad := b.Invitation[:len(b.Invitation)-1] + "x"
		if _, e := bills.Preview(ctx, uid+1, bad); e == nil {
			t.Fatal("bad invite accepted")
		}
		p := join(b, uid+1, 1)
		if _, e := bills.Join(ctx, uid+2, b.Invitation, p.ParticipantID); entity.Code(e) != "STATE_CONFLICT" {
			t.Fatal(e)
		}
		if _, e := bills.Join(ctx, uid+1, b.Invitation, b.Participants[2].ID); entity.Code(e) != "STATE_CONFLICT" {
			t.Fatal(e)
		}
		member, e := bills.Get(ctx, uid+1, uid, b.ID)
		if e != nil || member.Invitation != "" || !member.Participants[1].IsMe {
			t.Fatal(member, e)
		}
		p.SourceAccountID = accounts[uid+2].ID
		if _, e := svc.Prepare(ctx, actor(uid+1), meta(uid+1), uuid.NewString(), PrepareInput{Kind: "aa_payment", Parameters: marshal(p)}); entity.Code(e) != "NOT_FOUND" {
			t.Fatal(e)
		}
	})
	t.Run("concurrent_invitation_claim_once", func(t *testing.T) {
		b := create()
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, user := range []int64{uid + 1, uid + 2} {
			wg.Add(1)
			go func(user int64) {
				defer wg.Done()
				_, e := bills.Join(ctx, user, b.Invitation, b.Participants[1].ID)
				results <- e
			}(user)
		}
		wg.Wait()
		close(results)
		success, conflict := 0, 0
		for e := range results {
			if e == nil {
				success++
			} else if entity.Code(e) == "STATE_CONFLICT" {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal("invitation slot was claimed more than once")
		}
	})
	t.Run("agent_and_creator_cannot_pay_for_member", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		a := actor(uid + 1)
		a.Kind = "agent"
		a.AgentType = "supervisor"
		if _, e := svc.Prepare(ctx, a, meta(uid+1), uuid.NewString(), PrepareInput{Kind: "aa_payment", Parameters: marshal(p)}); entity.Code(e) != "FORBIDDEN" {
			t.Fatal(e)
		}
		if _, e := svc.Prepare(ctx, actor(uid), meta(uid), uuid.NewString(), PrepareInput{Kind: "aa_payment", Parameters: marshal(p)}); e == nil {
			t.Fatal("creator paid for member")
		}
	})
	t.Run("bound_confirmation_and_double_intent", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		first, second := prepare(uid+1, p), prepare(uid+1, p)
		if _, e := execute(uid+1, first); e == nil {
			t.Fatal("executed without authorization")
		}
		bad := marshal(map[string]any{"owner_id": strconv.FormatInt(uid, 10), "bill_id": b.ID, "participant_id": p.ParticipantID, "source_account_id": strconv.FormatInt(p.SourceAccountID, 10), "amount_minor": 1})
		if _, e := svc.Prepare(ctx, actor(uid+1), meta(uid+1), uuid.NewString(), PrepareInput{Kind: "aa_payment", Parameters: bad}); entity.Code(e) != "INVALID_ARGUMENT" {
			t.Fatal(e)
		}
		before, received := balance(uid+1), balance(uid)
		first = approve(uid+1, first)
		out, e := execute(uid+1, first)
		if e != nil || out.Operation.Status != "succeeded" {
			t.Fatal(out, e)
		}
		if balance(uid+1) != before-1000 || balance(uid) != received+1000 {
			t.Fatal("wrong money movement")
		}
		if _, e := execute(uid+1, first); e != nil {
			t.Fatal(e)
		}
		second = approve(uid+1, second)
		out, e = execute(uid+1, second)
		if e != nil || out.Operation.Status != "failed" || out.Operation.ErrorCode != "STATE_CONFLICT" {
			t.Fatal(out, e)
		}
		if balance(uid+1) != before-1000 {
			t.Fatal("duplicate debit")
		}
	})
	t.Run("concurrent_same_authorization_once", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		v := approve(uid+1, prepare(uid+1, p))
		before := balance(uid + 1)
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, e := execute(uid+1, v)
				if e == nil && out.Operation.Status != "succeeded" {
					e = errors.New("not succeeded")
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
		if balance(uid+1) != before-1000 {
			t.Fatal("concurrent debit duplicated")
		}
	})
	t.Run("concurrent_different_intents_one_share", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		vs := []View{}
		for i := 0; i < 4; i++ {
			vs = append(vs, approve(uid+1, prepare(uid+1, p)))
		}
		before := balance(uid + 1)
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for _, v := range vs {
			wg.Add(1)
			go func(v View) { defer wg.Done(); _, e := execute(uid+1, v); errs <- e }(v)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		if balance(uid+1) != before-1000 {
			t.Fatal("distinct concurrent intents charged the same share more than once", before, balance(uid+1))
		}
	})
	t.Run("two_members_complete_bill", func(t *testing.T) {
		b := create()
		p1 := join(b, uid+1, 1)
		p2 := join(b, uid+2, 2)
		v1, v2 := approve(uid+1, prepare(uid+1, p1)), approve(uid+2, prepare(uid+2, p2))
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i, v := range []View{v1, v2} {
			wg.Add(1)
			go func(user int64, v View) {
				defer wg.Done()
				out, e := execute(user, v)
				if e == nil && out.Operation.Status != "succeeded" {
					e = errors.New("AA settlement failed")
				}
				errs <- e
			}(uid+int64(i)+1, v)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		out, e := bills.Get(ctx, uid, uid, b.ID)
		if e != nil || out.Status != "settled" || out.CollectedMinor != 2000 || out.OutstandingMinor != 0 {
			t.Fatal(out, e)
		}
	})
	t.Run("cancel_after_confirmation_blocks_execution", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		v := approve(uid+1, prepare(uid+1, p))
		current, _ := bills.Get(ctx, uid, uid, b.ID)
		if _, e := bills.Cancel(ctx, uid, b.ID, current.Version); e != nil {
			t.Fatal(e)
		}
		before := balance(uid + 1)
		out, e := execute(uid+1, v)
		if e != nil || out.Operation.Status != "failed" || balance(uid+1) != before {
			t.Fatal(out, e)
		}
	})
	t.Run("cancel_racing_payment_is_atomic", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		v := approve(uid+1, prepare(uid+1, p))
		current, _ := bills.Get(ctx, uid, uid, b.ID)
		before := balance(uid + 1)
		var wg sync.WaitGroup
		var out View
		var payErr, cancelErr error
		wg.Add(2)
		go func() { defer wg.Done(); out, payErr = execute(uid+1, v) }()
		go func() { defer wg.Done(); _, cancelErr = bills.Cancel(ctx, uid, b.ID, current.Version) }()
		wg.Wait()
		if payErr != nil {
			t.Fatal(payErr)
		}
		if cancelErr == nil {
			if out.Operation.Status != "failed" || balance(uid+1) != before {
				t.Fatal("cancelled AA was charged")
			}
		} else if entity.Code(cancelErr) != "VERSION_CONFLICT" || out.Operation.Status != "succeeded" || balance(uid+1) != before-1000 {
			t.Fatal("inconsistent concurrent outcome", entity.Code(cancelErr), out.Operation.Status)
		}
	})
	t.Run("frozen_recipient", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		v := approve(uid+1, prepare(uid+1, p))
		if e := conn.DB.Model(&entity.BankAccount{}).Where("id=?", accounts[uid].ID).Update("status", "frozen").Error; e != nil {
			t.Fatal(e)
		}
		defer conn.DB.Model(&entity.BankAccount{}).Where("id=?", accounts[uid].ID).Update("status", "active")
		out, e := execute(uid+1, v)
		if e != nil || out.Operation.ErrorCode != "ACCOUNT_FROZEN" {
			t.Fatal(out, e)
		}
	})
	t.Run("insufficient_funds", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		a, e := core.CreateAccount(ctx, meta(uid+1), uuid.NewString(), service.AccountInput{DisplayName: "Poor account", Currency: "CNY", InitialBalanceMinor: 1, ConfirmSimulatedFunds: true})
		if e != nil {
			t.Fatal(e)
		}
		p.SourceAccountID = a.ID
		out, e := execute(uid+1, approve(uid+1, prepare(uid+1, p)))
		if e != nil || out.Operation.ErrorCode != "INSUFFICIENT_FUNDS" {
			t.Fatal(out, e)
		}
	})
	t.Run("recipient_failure_rolls_back_payer_and_share", func(t *testing.T) {
		b := create()
		p := join(b, uid+1, 1)
		v := approve(uid+1, prepare(uid+1, p))
		before, received := balance(uid+1), balance(uid)
		callback := "aa_test_" + uuid.NewString()
		if e := conn.DB.Callback().Create().Before("gorm:create").Register(callback, func(db *gorm.DB) {
			if row, ok := db.Statement.Dest.(*entity.Transaction); ok && row.UserID == uid && row.Category == "aa_settlement" {
				db.AddError(errors.New("injected recipient write failure"))
			}
		}); e != nil {
			t.Fatal(e)
		}
		_, e := execute(uid+1, v)
		conn.DB.Callback().Create().Remove(callback)
		if e == nil || balance(uid+1) != before || balance(uid) != received {
			t.Fatal("partial money committed", e)
		}
		current, _ := bills.Get(ctx, uid, uid, b.ID)
		if current.Participants[1].Status != "joined" {
			t.Fatal(current)
		}
		out, e := execute(uid+1, v)
		if e != nil || out.Operation.Status != "succeeded" {
			t.Fatal(out, e)
		}
	})
	t.Run("necessary_living_reserve", func(t *testing.T) {
		a, e := core.CreateAccount(ctx, meta(uid+2), uuid.NewString(), service.AccountInput{DisplayName: "Protected", Currency: "CNY", InitialBalanceMinor: 1000, ConfirmSimulatedFunds: true})
		if e != nil {
			t.Fatal(e)
		}
		_, e = core.CreateBudget(ctx, meta(uid+2), uuid.NewString(), service.BudgetInput{Month: time.Now().UTC().Format("2006-01"), Currency: "CNY", TotalMinor: 1000, Categories: []service.CategoryInput{{Category: "food", AllocatedMinor: 1000, EssentialReserveMinor: 1000}}, EnforceReserve: true})
		if e != nil {
			t.Fatal(e)
		}
		b := create()
		p := join(b, uid+2, 1)
		p.SourceAccountID = a.ID
		out, e := execute(uid+2, approve(uid+2, prepare(uid+2, p)))
		if e != nil || out.Operation.ErrorCode != "NECESSARY_RESERVE" {
			t.Fatal(out, e)
		}
	})
	t.Run("balanced_journals_and_conserved_cash", func(t *testing.T) {
		var bad int64
		if e := conn.DB.Raw("SELECT COUNT(*) FROM (SELECT journal_id FROM banking_ledger_entries WHERE user_id IN (?,?,?,?) GROUP BY journal_id HAVING SUM(delta_minor)<>0 OR COUNT(*)<>2) j", uid, uid+1, uid+2, uid+3).Scan(&bad).Error; e != nil || bad != 0 {
			t.Fatal(bad, e)
		}
		var sum int64
		if e := conn.DB.Model(&entity.BankAccount{}).Where("user_id IN (?,?,?,?)", uid, uid+1, uid+2, uid+3).Select("SUM(balance_minor)").Scan(&sum).Error; e != nil || sum != 401001 {
			t.Fatal(sum, e)
		}
	})
}
