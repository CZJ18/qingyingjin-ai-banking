package direct

import (
	"context"
	"errors"
	"github.com/coze-dev/coze-studio/backend/infra/coderunner"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"testing"
)

func TestDedicatedModeDeniesDirectCodeBeforeProcess(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		err := func() (err error) {
			defer func() {
				if recover() != nil {
					err = errors.New("direct runner touched request before scope guard")
				}
			}()
			_, err = NewRunner().Run(context.Background(), nil)
			return
		}()
		if !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("direct code runner not denied: %v", err)
		}
	}
}

func TestGeneralRuntimeKeepsDirectCodeDispatch(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	_, err := NewRunner().Run(context.Background(), &coderunner.RunRequest{Language: "unsupported-fixture-language"})
	if err == nil || errors.Is(err, bankingscope.ErrDenied) {
		t.Fatal("general code runner dispatch changed")
	}
}
