package sandbox

import (
	"context"
	"errors"
	"github.com/coze-dev/coze-studio/backend/infra/coderunner"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"testing"
)

func TestDedicatedModeDeniesSandboxCodeBeforeProcess(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		err := func() (err error) {
			defer func() {
				if recover() != nil {
					err = errors.New("sandbox runner touched request before scope guard")
				}
			}()
			_, err = (&runner{}).Run(context.Background(), nil)
			return
		}()
		if !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("sandbox code runner not denied: %v", err)
		}
	}
}

func TestGeneralRuntimeKeepsSandboxCodeDispatch(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	_, err := (&runner{}).Run(context.Background(), &coderunner.RunRequest{Language: coderunner.JavaScript})
	if err == nil || errors.Is(err, bankingscope.ErrDenied) {
		t.Fatal("general sandbox runner dispatch changed")
	}
}
