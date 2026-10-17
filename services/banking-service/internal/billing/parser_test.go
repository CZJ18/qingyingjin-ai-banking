package billing

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestModelOutputAbnormal(t *testing.T) {
	for _, raw := range []string{`{}`, `{"tool":"execute_transfer"}`, "```json {} ```", `null`, `{"user_id":"999"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := json.Marshal(map[string]string{"output": raw})
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "execute_id": "456", "data": string(data)})
		}))
		client, _ := coze.New(server.URL, "test-pat", time.Second)
		parser := QueryParser{Runner: client, WorkflowID: "123"}
		_, err := parser.Parse(context.Background(), QueryInput{Question: "查询2026年1月消费", Timezone: "UTC"}, "billing-test")
		server.Close()
		if entity.Code(err) != "MODEL_OUTPUT_INVALID" {
			t.Fatal(raw, err)
		}
	}
	var p *QueryParser
	if _, e := p.Parse(context.Background(), QueryInput{Question: "最近花了多少"}, ""); entity.Code(e) != "COZE_UNAVAILABLE" {
		t.Fatal(e)
	}
}
func TestValidModelRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IsAsync    bool   `json:"is_async"`
			Parameters string `json:"parameters"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.IsAsync || req.Parameters == "" {
			t.Error("wrong synchronous contract")
		}
		route := Route{Tool: "analyze_spending", From: "2026-01-01T00:00:00Z", To: "2026-02-01T00:00:00Z", Timezone: "UTC", MissingInformation: []string{}, Evidence: "1月消费"}
		raw, _ := json.Marshal(route)
		data, _ := json.Marshal(map[string]string{"output": string(raw)})
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "execute_id": "456", "data": string(data)})
	}))
	defer server.Close()
	client, _ := coze.New(server.URL, "test-pat", time.Second)
	parser := QueryParser{Runner: client, WorkflowID: "123"}
	out, e := parser.Parse(context.Background(), QueryInput{Question: "查询1月消费", Timezone: "UTC"}, "test")
	if e != nil || out.Route.Tool != "analyze_spending" {
		t.Fatal(out, e)
	}
}
