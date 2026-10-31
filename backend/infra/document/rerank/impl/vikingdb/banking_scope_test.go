package vikingdb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coze-dev/coze-studio/backend/api/model/admin/config"
	"github.com/coze-dev/coze-studio/backend/infra/document/rerank"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
)

func TestDedicatedModeDeniesRerankBeforeCredentialsOrHTTP(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		err := func() (err error) {
			defer func() {
				if recover() != nil {
					err = errors.New("rerank touched request or supplier config")
				}
			}()
			_, err = (&reranker{}).Rerank(context.Background(), nil)
			return
		}()
		if !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("rerank not denied: %v", err)
		}
	}
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGeneralRuntimeKeepsRerank(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	calls := 0
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"scores":[],"token_usage":0}}`)), Header: make(http.Header)}, nil
	})}
	_, err := NewReranker(&config.VikingDBConfig{Ak: "fixture-ak", Sk: "fixture-sk"}).Rerank(context.Background(), &rerank.Request{})
	if err != nil || calls != 1 {
		t.Fatalf("general rerank changed: %v", err)
	}
}
