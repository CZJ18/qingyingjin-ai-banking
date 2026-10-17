package goal

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/service"
	"strings"
	"time"
	"unicode/utf8"
)

// BudgetReader is deliberately narrower than Banking Core; there is no write API.
type BudgetReader interface {
	BudgetUsage(context.Context, int64, int64) (*service.BudgetUsage, error)
}
type Service struct {
	repo    *Repository
	parser  *Parser
	budgets BudgetReader
}

func NewService(repo *Repository, parser *Parser, budgets BudgetReader) *Service {
	return &Service{repo: repo, parser: parser, budgets: budgets}
}
func (s *Service) Configured() bool { return s != nil && s.repo != nil }
func validateInput(in Input) error {
	if strings.TrimSpace(in.Text) == "" || !utf8.ValidString(in.Text) || len(in.Text) > 8000 {
		return entity.Fail("INVALID_ARGUMENT", "text required, maximum 8000 UTF-8 bytes")
	}
	for _, v := range []*int64{in.Context.MonthlyIncomeMinor, in.Context.SpendingBudgetMinor, in.Context.EssentialExpensesMinor} {
		if v != nil && (*v < 0 || *v > 1_000_000_000_000) {
			return entity.Fail("INVALID_ARGUMENT", "financial context must use nonnegative CNY cents within bounds")
		}
	}
	if in.Context.BudgetID != nil && *in.Context.BudgetID <= 0 {
		return entity.Fail("INVALID_ARGUMENT", "positive budget_id required")
	}
	return nil
}
func (s *Service) checkBudget(ctx context.Context, m service.Meta, in Input) error {
	if in.Context.BudgetID != nil {
		if s.budgets == nil {
			return entity.Fail("DEPENDENCY_UNAVAILABLE", "budget reader unavailable")
		}
		_, err := s.budgets.BudgetUsage(ctx, m.UserID, *in.Context.BudgetID)
		return err
	}
	return nil
}
func (s *Service) Create(ctx context.Context, m service.Meta, key string, in Input) (View, error) {
	if err := validateInput(in); err != nil {
		return View{}, err
	}
	if in.ExpectedVersion != 0 {
		return View{}, entity.Fail("INVALID_ARGUMENT", "create must not include expected_version")
	}
	if err := s.checkBudget(ctx, m, in); err != nil {
		return View{}, err
	}
	return s.repo.Create(ctx, m, key, in)
}
func (s *Service) Update(ctx context.Context, m service.Meta, id int64, in Input) (View, error) {
	if err := validateInput(in); err != nil {
		return View{}, err
	}
	if in.ExpectedVersion <= 0 {
		return View{}, entity.Fail("INVALID_ARGUMENT", "expected_version required")
	}
	if err := s.checkBudget(ctx, m, in); err != nil {
		return View{}, err
	}
	return s.repo.Update(ctx, m, id, in)
}
func (s *Service) Parse(ctx context.Context, m service.Meta, id, version int64) (View, error) {
	if version <= 0 {
		return View{}, entity.Fail("INVALID_ARGUMENT", "expected_version required")
	}
	if _, err := s.repo.View(ctx, m.UserID, id, version); err != nil {
		return View{}, err
	}
	if !s.parser.Configured() {
		return View{}, entity.Fail("COZE_UNAVAILABLE", "Coze Goal parser workflow is not configured; goal remains unparsed")
	}
	v, err := s.repo.Claim(ctx, m, id, version, s.parser.WorkflowID)
	if err != nil {
		return View{}, err
	}
	if v.ParseStatus == "parsed" {
		return s.repo.View(ctx, m.UserID, id, version)
	}
	parsed, execute, parseErr := s.parser.Parse(coze.WithIdentity(ctx, m.UserID, "goal:"+v.ParseID), v.Text, m.TraceID)
	var plan Plan
	if parseErr == nil {
		var fc FinancialContext
		if e := json.Unmarshal(v.ContextJSON, &fc); e != nil {
			parseErr = entity.Fail("DEPENDENCY_UNAVAILABLE", "stored goal context invalid")
		}
		var snapshot json.RawMessage
		if parseErr == nil && fc.BudgetID != nil {
			usage, e := s.budgets.BudgetUsage(ctx, m.UserID, *fc.BudgetID)
			if e != nil {
				parseErr = e
			} else {
				snapshot, _ = json.Marshal(usage)
				if fc.SpendingBudgetMinor == nil {
					amount := usage.Budget.TotalMinor
					fc.SpendingBudgetMinor = &amount
				}
				reserve := int64(0)
				for _, c := range usage.Budget.Categories {
					reserve += c.EssentialReserveMinor
				}
				if fc.EssentialExpensesMinor == nil && reserve > 0 {
					fc.EssentialExpensesMinor = &reserve
				}
			}
		}
		if parseErr == nil {
			plan, parseErr = Compile(parsed, fc, snapshot)
		}
	}
	// Persist a failed attempt even if the client disconnected during Coze inference.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = s.repo.Finalize(saveCtx, m, v, parsed, plan, execute, parseErr); err != nil {
		return View{}, err
	}
	if parseErr != nil {
		return View{}, parseErr
	}
	return s.repo.View(ctx, m.UserID, id, version)
}
func (s *Service) View(ctx context.Context, user, id, version int64) (View, error) {
	return s.repo.View(ctx, user, id, version)
}
func (s *Service) Versions(ctx context.Context, user, id int64, limit, offset int) ([]GoalVersion, error) {
	return s.repo.Versions(ctx, user, id, limit, offset)
}
func (s *Service) Compare(ctx context.Context, user, id, from, to int64) (Comparison, error) {
	return s.repo.Compare(ctx, user, id, from, to)
}
