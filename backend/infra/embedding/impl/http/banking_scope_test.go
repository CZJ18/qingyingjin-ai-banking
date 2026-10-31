package http

import (
	"context"
	"errors"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDedicatedModeDeniesHTTPEmbeddingAndStartupProbe(t *testing.T) {
	for _, mode := range []string{`{"42":"pinned"}`, "invalid", " "} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path == pathSupportStatus {
				w.Write([]byte("3"))
				return
			}
			w.Write([]byte(`{"dense":[[1]],"sparse":[{"1":1}]}`))
		}))
		e, err := NewEmbedding(server.URL, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.EmbedStrings(context.Background(), []string{"text"}); !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("dense request not denied: %v", err)
		}
		if _, _, err = e.EmbedStringsHybrid(context.Background(), []string{"text"}); !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("hybrid request not denied: %v", err)
		}
		if e.Dimensions() != 0 {
			t.Fatal("dimension probe resolved")
		}
		server.Close()
		if calls != 0 {
			t.Fatal("HTTP embedding endpoint invoked")
		}
	}
}
func TestGeneralRuntimeKeepsHTTPEmbedding(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == pathSupportStatus {
			w.Write([]byte("3"))
			return
		}
		w.Write([]byte(`{"dense":[[1]],"sparse":[{"1":1}]}`))
	}))
	defer server.Close()
	e, err := NewEmbedding(server.URL, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.EmbedStrings(context.Background(), []string{"text"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = e.EmbedStringsHybrid(context.Background(), []string{"text"}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("general runtime HTTP embedding behavior changed")
	}
}
