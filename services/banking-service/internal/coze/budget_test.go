package coze

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testMeter struct {
	denied, errorOnComplete error
	cached                  []byte
	owner                   int64
	key                     string
	completed, unknown      int
	token                   int64
	cancelled               bool
}

func (m *testMeter) Begin(ctx context.Context, owner int64, key, workflow, digest string) (string, []byte, error) {
	m.owner, m.key = owner, key
	if owner <= 0 || key == "" || workflow != "123" || len(digest) != 64 {
		return "", nil, errors.New("unbound request")
	}
	return "reservation", m.cached, m.denied
}
func (m *testMeter) Complete(ctx context.Context, owner int64, id string, raw []byte, tokens *int64, cost string, complete bool) error {
	m.cancelled = ctx.Err() != nil
	if complete {
		m.completed++
		if tokens != nil {
			m.token = *tokens
		}
	} else {
		m.unknown++
	}
	return m.errorOnComplete
}
func TestBudgetDenialNeverCallsUpstream(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer s.Close()
	meter := &testMeter{denied: ErrQuotaExceeded}
	c, e := NewBudgeted(s.URL, "synthetic-pat", time.Second, meter)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.RunSyncWorkflow(WithIdentity(context.Background(), 42, "task-one"), RunRequest{WorkflowID: "123", Parameters: map[string]any{}})
	if !errors.Is(e, ErrQuotaExceeded) || calls != 0 || meter.owner != 42 {
		t.Fatalf("quota denied=%v calls=%d owner=%d", e, calls, meter.owner)
	}
	_, e = c.RunSyncWorkflow(context.Background(), RunRequest{WorkflowID: "123"})
	if e == nil || calls != 0 {
		t.Fatal("unbound model owner reached network")
	}
}
func TestBudgetCachesMeteredResponseAndDoesNotRepeatCall(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"code":0,"execute_id":"456","data":"{}","token":42,"cost":"0.00000"}`))
	}))
	defer s.Close()
	meter := &testMeter{}
	c, _ := NewBudgeted(s.URL, "synthetic-pat", time.Second, meter)
	ctx := WithIdentity(context.Background(), 42, "stable-owned-task")
	out, e := c.RunSyncWorkflow(ctx, RunRequest{WorkflowID: "123", Parameters: map[string]any{"synthetic": true}})
	if e != nil || out.ExecuteID != "456" || meter.completed != 1 || meter.token != 42 || calls != 1 {
		t.Fatalf("response=%+v error=%v meter=%+v", out, e, meter)
	}
	meter.cached = []byte(`{"code":0,"execute_id":"456","data":"{}","token":42,"cost":"0.00000"}`)
	out, e = c.RunSyncWorkflow(ctx, RunRequest{WorkflowID: "123", Parameters: map[string]any{"synthetic": true}})
	if e != nil || out.ExecuteID != "456" || calls != 1 || meter.completed != 1 {
		t.Fatal("completed response invoked or charged upstream twice")
	}
}
func TestBudgetTimeoutKeepsUnknownReservationAfterCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond)
		w.Write([]byte(`{"code":0,"execute_id":"456","data":"{}"}`))
	}))
	defer s.Close()
	meter := &testMeter{}
	c, _ := NewBudgeted(s.URL, "synthetic-pat", time.Second, meter)
	ctx, cancel := context.WithTimeout(WithIdentity(context.Background(), 42, "lost-result"), 10*time.Millisecond)
	defer cancel()
	_, e := c.RunSyncWorkflow(ctx, RunRequest{WorkflowID: "123"})
	if e == nil || meter.unknown != 1 || meter.completed != 0 || meter.cancelled {
		t.Fatalf("unknown accounting lost after cancellation: %+v error=%v", meter, e)
	}
}
func TestBudgetAccountingFailureCannotReturnSuccessfulModelOutput(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"execute_id":"456","data":"{}","token":1}`))
	}))
	defer s.Close()
	meter := &testMeter{errorOnComplete: errors.New("synthetic SQL fault")}
	c, _ := NewBudgeted(s.URL, "synthetic-pat", time.Second, meter)
	out, e := c.RunSyncWorkflow(WithIdentity(context.Background(), 42, "accounting-fault"), RunRequest{WorkflowID: "123"})
	if e == nil || out.ExecuteID != "" {
		t.Fatal("unaccounted model output reported success")
	}
}

func TestCachedModelResponseMustObeyLiveResponseValidation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cached response used network") }))
	defer s.Close()
	for _, raw := range []string{`{"code":0,"execute_id":"456","data":"{}","token":-1}`, `{"code":0,"execute_id":"456"}`} {
		meter := &testMeter{cached: []byte(raw)}
		c, _ := NewBudgeted(s.URL, "synthetic-pat", time.Second, meter)
		if _, e := c.RunSyncWorkflow(WithIdentity(context.Background(), 42, "corrupt-cache"), RunRequest{WorkflowID: "123"}); e == nil {
			t.Fatal("invalid cached usage/output accepted")
		}
	}
}
