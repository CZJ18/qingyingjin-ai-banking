package coze

import "github.com/qingyingjin/banking-service/internal/outbound"

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunWorkflowContract(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workflow/run" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer pat-test" || r.Header.Get("X-Trace-ID") != "trace-1" {
			t.Error("wrong transport")
		}
		var body struct {
			WorkflowID string `json:"workflow_id"`
			Parameters string `json:"parameters"`
			IsAsync    bool   `json:"is_async"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var params map[string]any
		if json.Unmarshal([]byte(body.Parameters), &params) != nil || params["task_id"] != "task-1" || body.WorkflowID != "123" || !body.IsAsync {
			t.Error("wrong Coze contract")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"execute_id":"456"}`))
	}))
	defer s.Close()
	c, err := New(s.URL, "pat-test", time.Second, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.RunWorkflow(context.Background(), RunRequest{WorkflowID: "123", Parameters: map[string]any{"task_id": "task-1"}, TraceID: "trace-1"})
	if err != nil || out.ExecuteID != "456" {
		t.Fatalf("%#v %v", out, err)
	}
}
func TestRunWorkflowRejectsBusinessFailureAndRedirect(t *testing.T) {
	for _, status := range []int{200, 302, 401} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == 302 {
				w.Header().Set("Location", "http://127.0.0.1:1")
			}
			w.WriteHeader(status)
			w.Write([]byte(`{"code":401,"msg":"secret upstream detail"}`))
		}))
		c, err := New(s.URL, "pat-test", time.Second, outbound.TestPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.RunWorkflow(context.Background(), RunRequest{WorkflowID: "123", Parameters: map[string]any{}}); err == nil {
			t.Fatalf("accepted %d", status)
		}
		s.Close()
	}
}

func TestRunWorkflowRejectsIDOverflowBeforeNetwork(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"code":0,"execute_id":"456"}`))
	}))
	defer s.Close()
	c, err := New(s.URL, "pat-test", time.Second, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunWorkflow(context.Background(), RunRequest{WorkflowID: "9999999999999999999", Parameters: map[string]any{}}); err == nil {
		t.Fatal("accepted overflowing ID")
	}
	if calls != 0 {
		t.Fatal("invalid ID reached Coze")
	}
}
