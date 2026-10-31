package vikingdb

import (
	"context"
	"errors"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"testing"
)

func TestDedicatedModeDeniesVikingServerSideEmbedding(t *testing.T) {
	for _, mode := range []string{`{"42":"pinned"}`, "invalid", " "} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		store := &vkSearchStore{}
		for _, call := range []func() error{
			func() error { _, err := store.Store(context.Background(), nil); return err },
			func() error { _, err := store.Retrieve(context.Background(), "query"); return err },
		} {
			err := func() (err error) {
				defer func() {
					if recover() != nil {
						err = errors.New("touched supplier configuration")
					}
				}()
				return call()
			}()
			if !errors.Is(err, bankingscope.ErrDenied) {
				t.Fatalf("server side embedding not denied: %v", err)
			}
		}
	}
}
