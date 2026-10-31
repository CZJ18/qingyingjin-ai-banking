package coze

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/outbound"
)

func TestUnpricedCozeWorkflowCannotDispatchInDevelopment(t *testing.T) {
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"execute_id":"synthetic-execution","data":"{}"}`))
	}))
	defer up.Close()
	client, e := New(up.URL, "synthetic-token", time.Second, outbound.Policy{Environment: "development"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.RunSyncWorkflow(WithIdentity(context.Background(), 42, "unpriced-workflow"), RunRequest{WorkflowID: "123"})
	if !errors.Is(e, ErrMonetaryAdmissionRequired) || calls.Load() != 0 {
		t.Fatal("unpriced workflow dispatched", e, calls.Load())
	}
}
