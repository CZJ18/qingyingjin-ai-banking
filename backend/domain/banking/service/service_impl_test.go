package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/repository"
)

type fakeRepository struct {
	repository.Repository
	createAccountCalls  int
	createdAccount      *entity.BankAccount
	getAccountCalls     int
	getUserID           int64
	getID               int64
	updateAccountCalls  int
	updateAccountErr    error
	createTxCalls       int
	createdTx           *entity.Transaction
	createPaymentCalls  int
	createdPayment      *entity.Payment
	createTransferCalls int
	createdTransfer     *entity.Transfer
	createBudgetCalls   int
	createdBudget       *entity.HealthBudget
	createOrderCalls    int
	createdOrder        *entity.FoodOrder
	createAuditCalls    int
	createdAudit        *entity.AgentAudit
}

func TestSentinelErrorMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "invalid user ID", err: ErrInvalidUserID, want: "banking: invalid user id"},
		{name: "invalid amount", err: ErrInvalidAmount, want: "banking: invalid amount"},
		{name: "invalid currency", err: ErrInvalidCurrency, want: "banking: invalid currency"},
		{name: "invalid argument", err: ErrInvalidArgument, want: "banking: invalid argument"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func (f *fakeRepository) CreateBankAccount(_ context.Context, v *entity.BankAccount) (*entity.BankAccount, error) {
	f.createAccountCalls++
	f.createdAccount = v
	return v, nil
}

func (f *fakeRepository) GetBankAccount(_ context.Context, userID, id int64) (*entity.BankAccount, error) {
	f.getAccountCalls++
	f.getUserID, f.getID = userID, id
	return &entity.BankAccount{ID: id, UserID: userID}, nil
}

func (f *fakeRepository) UpdateBankAccount(_ context.Context, _ *entity.BankAccount) error {
	f.updateAccountCalls++
	return f.updateAccountErr
}

func (f *fakeRepository) CreateTransaction(_ context.Context, v *entity.Transaction) (*entity.Transaction, error) {
	f.createTxCalls++
	f.createdTx = v
	return v, nil
}

func (f *fakeRepository) CreatePayment(_ context.Context, v *entity.Payment) (*entity.Payment, error) {
	f.createPaymentCalls++
	f.createdPayment = v
	return v, nil
}

func (f *fakeRepository) CreateTransfer(_ context.Context, v *entity.Transfer) (*entity.Transfer, error) {
	f.createTransferCalls++
	f.createdTransfer = v
	return v, nil
}

func (f *fakeRepository) CreateHealthBudget(_ context.Context, v *entity.HealthBudget) (*entity.HealthBudget, error) {
	f.createBudgetCalls++
	f.createdBudget = v
	return v, nil
}

func (f *fakeRepository) CreateFoodOrder(_ context.Context, v *entity.FoodOrder) (*entity.FoodOrder, error) {
	f.createOrderCalls++
	f.createdOrder = v
	return v, nil
}

func (f *fakeRepository) CreateAgentAudit(_ context.Context, v *entity.AgentAudit) (*entity.AgentAudit, error) {
	f.createAuditCalls++
	f.createdAudit = v
	return v, nil
}

func TestCreateBankAccountValidationAndDefaults(t *testing.T) {
	ctx := context.Background()
	t.Run("invalid user does not delegate", func(t *testing.T) {
		repo := &fakeRepository{}
		_, err := NewBankingService(repo).CreateBankAccount(ctx, &entity.BankAccount{UserID: 0})
		if !errors.Is(err, ErrInvalidUserID) || repo.createAccountCalls != 0 {
			t.Fatalf("err=%v calls=%d", err, repo.createAccountCalls)
		}
	})
	t.Run("normalizes currency and defaults status", func(t *testing.T) {
		repo := &fakeRepository{}
		_, err := NewBankingService(repo).CreateBankAccount(ctx, &entity.BankAccount{UserID: 1, Currency: " cny "})
		if err != nil || repo.createAccountCalls != 1 {
			t.Fatalf("err=%v calls=%d", err, repo.createAccountCalls)
		}
		if repo.createdAccount.Currency != "CNY" || repo.createdAccount.Status != entity.BankAccountStatusActive {
			t.Fatalf("account=%+v", repo.createdAccount)
		}
	})
}

func TestCreateTransactionValidation(t *testing.T) {
	cases := []struct {
		name string
		tx   *entity.Transaction
		want error
	}{
		{"negative amount", &entity.Transaction{UserID: 1, BankAccountID: 2, AmountMinor: -1, Direction: entity.TransactionDirectionExpense}, ErrInvalidAmount},
		{"empty direction", &entity.Transaction{UserID: 1, BankAccountID: 2}, ErrInvalidArgument},
		{"bad direction", &entity.Transaction{UserID: 1, BankAccountID: 2, Direction: "sideways"}, ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			_, err := NewBankingService(repo).CreateTransaction(context.Background(), tc.tx)
			if !errors.Is(err, tc.want) || repo.createTxCalls != 0 {
				t.Fatalf("err=%v calls=%d", err, repo.createTxCalls)
			}
		})
	}
	repo := &fakeRepository{}
	_, err := NewBankingService(repo).CreateTransaction(context.Background(), &entity.Transaction{UserID: 1, BankAccountID: 2, Direction: entity.TransactionDirectionExpense})
	if err != nil || repo.createTxCalls != 1 {
		t.Fatalf("err=%v calls=%d", err, repo.createTxCalls)
	}
}

func TestCreatePaymentAndTransferAreRecordOnly(t *testing.T) {
	ctx := context.Background()
	t.Run("payment requires key", func(t *testing.T) {
		repo := &fakeRepository{}
		_, err := NewBankingService(repo).CreatePayment(ctx, &entity.Payment{UserID: 1, FoodOrderID: 2, BankAccountID: 3, IdempotencyKey: "  "})
		if !errors.Is(err, ErrInvalidArgument) || repo.createPaymentCalls != 0 {
			t.Fatalf("err=%v calls=%d", err, repo.createPaymentCalls)
		}
	})
	t.Run("payment defaults and delegates once", func(t *testing.T) {
		repo := &fakeRepository{}
		_, err := NewBankingService(repo).CreatePayment(ctx, &entity.Payment{UserID: 1, FoodOrderID: 2, BankAccountID: 3, IdempotencyKey: " key "})
		if err != nil || repo.createPaymentCalls != 1 || repo.updateAccountCalls != 0 {
			t.Fatalf("err=%v create=%d updateAccount=%d", err, repo.createPaymentCalls, repo.updateAccountCalls)
		}
		if repo.createdPayment.IdempotencyKey != "key" || repo.createdPayment.Currency != "CNY" || repo.createdPayment.Status != entity.PaymentStatusCreated {
			t.Fatalf("payment=%+v", repo.createdPayment)
		}
	})
	t.Run("transfer defaults and delegates once", func(t *testing.T) {
		repo := &fakeRepository{}
		_, err := NewBankingService(repo).CreateTransfer(ctx, &entity.Transfer{UserID: 1, SourceBankAccountID: 3, TargetReference: " target ", IdempotencyKey: " key "})
		if err != nil || repo.createTransferCalls != 1 || repo.updateAccountCalls != 0 {
			t.Fatalf("err=%v create=%d updateAccount=%d", err, repo.createTransferCalls, repo.updateAccountCalls)
		}
		if repo.createdTransfer.IdempotencyKey != "key" || repo.createdTransfer.TargetReference != "target" || repo.createdTransfer.Currency != "CNY" || repo.createdTransfer.Status != entity.TransferStatusCreated {
			t.Fatalf("transfer=%+v", repo.createdTransfer)
		}
	})
}

func TestAgentAuditValidationAndSensitiveJSON(t *testing.T) {
	cases := []struct {
		name  string
		audit *entity.AgentAudit
	}{
		{"empty action", &entity.AgentAudit{UserID: 1, Outcome: entity.AgentAuditOutcomeSuccess}},
		{"invalid outcome", &entity.AgentAudit{UserID: 1, Action: "read", Outcome: "unknown"}},
		{"sensitive json", &entity.AgentAudit{UserID: 1, Action: "read", Outcome: entity.AgentAuditOutcomeSuccess, RequestJSON: json.RawMessage(`{"access_token":"x"}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			_, err := NewBankingService(repo).CreateAgentAudit(context.Background(), tc.audit)
			if !errors.Is(err, ErrInvalidArgument) || repo.createAuditCalls != 0 {
				t.Fatalf("err=%v calls=%d", err, repo.createAuditCalls)
			}
		})
	}
	repo := &fakeRepository{}
	audit := &entity.AgentAudit{UserID: 1, Action: " read ", Outcome: entity.AgentAuditOutcomeSuccess, RequestJSON: json.RawMessage(`{"query":"ok"}`)}
	_, err := NewBankingService(repo).CreateAgentAudit(context.Background(), audit)
	if err != nil || repo.createAuditCalls != 1 || repo.createdAudit.Action != "read" {
		t.Fatalf("err=%v calls=%d audit=%+v", err, repo.createAuditCalls, repo.createdAudit)
	}
}

func TestGetAndUpdateBankAccountValidation(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepository{}
	svc := NewBankingService(repo)
	_, err := svc.GetBankAccount(ctx, 12, 34)
	if err != nil || repo.getAccountCalls != 1 || repo.getUserID != 12 || repo.getID != 34 {
		t.Fatalf("err=%v calls=%d args=%d,%d", err, repo.getAccountCalls, repo.getUserID, repo.getID)
	}
	for _, args := range [][2]int64{{0, 1}, {1, 0}} {
		_, err = svc.GetBankAccount(ctx, args[0], args[1])
		if err == nil {
			t.Fatalf("GetBankAccount(%d,%d) error=nil", args[0], args[1])
		}
	}
	wantErr := errors.New("repo update")
	repo.updateAccountErr = wantErr
	err = svc.UpdateBankAccount(ctx, &entity.BankAccount{ID: 5, UserID: 6, Currency: "CNY", Status: entity.BankAccountStatusActive})
	if !errors.Is(err, wantErr) || repo.updateAccountCalls != 1 {
		t.Fatalf("err=%v calls=%d", err, repo.updateAccountCalls)
	}
}

func TestHealthBudgetValidationAndDefaults(t *testing.T) {
	invalid := []*entity.HealthBudget{
		{UserID: 1, PeriodStart: 0, PeriodEnd: 2},
		{UserID: 1, PeriodStart: 2, PeriodEnd: 2},
		{UserID: 1, PeriodStart: 1, PeriodEnd: 2, SpentMinor: -1},
	}
	for _, budget := range invalid {
		repo := &fakeRepository{}
		_, err := NewBankingService(repo).CreateHealthBudget(context.Background(), budget)
		if err == nil || repo.createBudgetCalls != 0 {
			t.Fatalf("budget=%+v err=%v calls=%d", budget, err, repo.createBudgetCalls)
		}
	}
	repo := &fakeRepository{}
	_, err := NewBankingService(repo).CreateHealthBudget(context.Background(), &entity.HealthBudget{UserID: 1, PeriodStart: 1, PeriodEnd: 2})
	if err != nil || repo.createdBudget.Currency != "CNY" || repo.createdBudget.Status != entity.HealthBudgetStatusActive {
		t.Fatalf("err=%v budget=%+v", err, repo.createdBudget)
	}
}

func TestFoodOrderValidationDefaultsAndItems(t *testing.T) {
	cases := []*entity.FoodOrder{
		{UserID: 1, Provider: "p", MerchantName: "m", TotalAmountMinor: -1},
		{UserID: 1, Provider: "p", MerchantName: "m", HealthBudgetID: -1},
		{UserID: 1, Provider: "p", MerchantName: "m", Items: []*entity.FoodOrderItem{nil}},
		{UserID: 1, Provider: "p", MerchantName: "m", Items: []*entity.FoodOrderItem{{Quantity: 0}}},
	}
	for _, order := range cases {
		repo := &fakeRepository{}
		_, err := NewBankingService(repo).CreateFoodOrder(context.Background(), order)
		if err == nil || repo.createOrderCalls != 0 {
			t.Fatalf("order=%+v err=%v calls=%d", order, err, repo.createOrderCalls)
		}
	}
	repo := &fakeRepository{}
	_, err := NewBankingService(repo).CreateFoodOrder(context.Background(), &entity.FoodOrder{UserID: 1, Provider: " p ", MerchantName: " m ", Items: []*entity.FoodOrderItem{{Quantity: 1}}})
	if err != nil || repo.createdOrder.Status != entity.FoodOrderStatusDraft || repo.createdOrder.Currency != "CNY" || repo.createdOrder.Items[0].Currency != "CNY" {
		t.Fatalf("err=%v order=%+v", err, repo.createdOrder)
	}
}

func TestNilRepositoryAndRawJSONValidation(t *testing.T) {
	_, err := NewBankingService(nil).GetBankAccount(context.Background(), 1, 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil repository err=%v", err)
	}
	repo := &fakeRepository{}
	tooLarge := make(json.RawMessage, 64*1024+1)
	_, err = NewBankingService(repo).CreateTransaction(context.Background(), &entity.Transaction{UserID: 1, BankAccountID: 1, Direction: entity.TransactionDirectionExpense, SourceMetadata: tooLarge})
	if !errors.Is(err, ErrInvalidArgument) || repo.createTxCalls != 0 {
		t.Fatalf("oversize err=%v calls=%d", err, repo.createTxCalls)
	}
}
