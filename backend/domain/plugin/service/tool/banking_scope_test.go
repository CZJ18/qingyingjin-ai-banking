package tool

import (
	"context"
	"errors"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"testing"
)

func TestDedicatedModeDeniesPluginHTTPBeforeAuthAndNetwork(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		err := func() (err error) {
			defer func() {
				if recover() != nil {
					err = errors.New("plugin touched invocation args before scope guard")
				}
			}()
			_, _, err = NewHttpCallImpl(0).Do(context.Background(), nil)
			return
		}()
		if !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("plugin HTTP not denied: %v", err)
		}
	}
}
