package ppocr

import (
	"context"
	"errors"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDedicatedModeDeniesPaddleOCRHTTP(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Write([]byte(`{"result":{"ocrResults":[{"prunedResult":{"rec_texts":["fixture"]}}]}}`))
		}))
		o := NewOCR(&Config{Client: server.Client(), URL: server.URL})
		for _, call := range []func() error{
			func() error { _, err := o.FromBase64(context.Background(), "fixture"); return err },
			func() error { _, err := o.FromURL(context.Background(), "https://example.invalid/image"); return err },
		} {
			if err := call(); !errors.Is(err, bankingscope.ErrDenied) {
				t.Errorf("Paddle OCR not denied: %v", err)
			}
		}
		server.Close()
		if calls != 0 {
			t.Fatal("Paddle OCR supplier endpoint invoked")
		}
	}
}
func TestGeneralRuntimeKeepsPaddleOCR(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"result":{"ocrResults":[{"prunedResult":{"rec_texts":["fixture"]}}]}}`))
	}))
	defer server.Close()
	o := NewOCR(&Config{Client: server.Client(), URL: server.URL})
	if _, err := o.FromBase64(context.Background(), "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.FromURL(context.Background(), "https://example.invalid/image"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("general OCR changed")
	}
}
