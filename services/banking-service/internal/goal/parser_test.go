package goal

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

func TestParserProtocolAndAbnormalOutputs(t *testing.T) {
	raw, _ := json.Marshal(fixture())
	for _, tc := range []struct {
		name, output, code string
		status             int
	}{
		{"valid", string(raw), "", 200}, {"malformed", "{", "MODEL_OUTPUT_INVALID", 200},
		{"markdown", "```json\n" + string(raw) + "\n```", "MODEL_OUTPUT_INVALID", 200},
		{"extra financial task", `{"task_type":"transfer"}`, "MODEL_OUTPUT_INVALID", 200},
		{"unavailable", "secret upstream error", "COZE_UNAVAILABLE", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/workflow/run" || r.Header.Get("Authorization") != "Bearer test-pat" {
					t.Error("invalid route or credential")
				}
				var request struct {
					WorkflowID string `json:"workflow_id"`
					Parameters string `json:"parameters"`
					IsAsync    bool   `json:"is_async"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.IsAsync || request.WorkflowID != "123" {
					t.Error("invalid sync request")
				}
				var parameters map[string]string
				if json.Unmarshal([]byte(request.Parameters), &parameters) != nil || len(parameters) != 1 || parameters["input_string"] == "" {
					t.Error("invalid parameters string")
				}
				data, _ := json.Marshal(map[string]string{"output": tc.output})
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "execute_id": "456", "data": string(data)})
			}))
			defer server.Close()
			client, _ := coze.New(server.URL, "test-pat", time.Second)
			p := Parser{Runner: client, WorkflowID: "123"}
			_, execute, err := p.Parse(context.Background(), "未来三个月每月储蓄200元", "goal-test")
			if tc.code == "" {
				if err != nil || execute != "456" {
					t.Fatal(execute, err)
				}
			} else if err == nil || entity.Code(err) != tc.code {
				t.Fatal(err)
			}
		})
	}
}
func TestHealthAndProhibitionSafety(t *testing.T) {
	p := fixture()
	raw, _ := json.Marshal(p)
	for _, text := range []string{"未来三个月每月储蓄200元，体重下降奖励奖金", "未来三个月每月储蓄200元，减少正常饮食开支"} {
		parsed, err := DecodeParsed(raw, text)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := Compile(parsed, FinancialContext{}, nil)
		if err != nil || plan.Status != "conflict" {
			t.Fatal(plan, err)
		}
	}
	parsed, err := DecodeParsed(raw, "未来三个月每月储蓄200元，禁止转账")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(parsed, FinancialContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range plan.Tasks {
		if task.TaskType == "propose_financial_operation" {
			t.Fatal("prohibited operation proposed")
		}
	}
	p.MonthlyIncomeMinor = number(20000)
	p.Evidence["monthly_income_minor"] = "每月储蓄200元"
	raw, _ = json.Marshal(p)
	if _, err := DecodeParsed(raw, "三个月每月储蓄200元"); err == nil {
		t.Fatal("used savings as income")
	}
}
func TestAmbiguousGoal(t *testing.T) {
	p := fixture()
	p.GoalType = "unclear"
	p.PeriodMonths = nil
	p.SavingsMonthlyMinor = nil
	p.Evidence = map[string]string{}
	raw, _ := json.Marshal(p)
	parsed, err := DecodeParsed(raw, "我想健康生活，合理存点钱")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(parsed, FinancialContext{}, nil)
	if err != nil || plan.Status != "awaiting_clarification" || len(plan.MissingInformation) < 5 {
		t.Fatal(plan, err)
	}
}
