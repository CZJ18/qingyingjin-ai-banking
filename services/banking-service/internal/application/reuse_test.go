package application_test

import (
	"context"
	"github.com/coze-dev/coze-studio/backend/domain/banking/repository"
	"github.com/coze-dev/coze-studio/backend/domain/banking/service"
	"gorm.io/gorm"
	"testing"
)

type compileIDGenerator struct{}

func (compileIDGenerator) GenID(context.Context) (int64, error) { return 1, nil }
func (compileIDGenerator) GenMultiIDs(_ context.Context, n int) ([]int64, error) {
	return make([]int64, n), nil
}
func TestExistingBankingPublicPackagesCompileAndValidate(t *testing.T) {
	repo := repository.NewBankingRepository(&gorm.DB{}, compileIDGenerator{})
	banking := service.NewBankingService(repo)
	if _, err := banking.GetBankAccount(context.Background(), 0, 1); err != service.ErrInvalidUserID {
		t.Fatalf("existing service validation = %v", err)
	}
}
