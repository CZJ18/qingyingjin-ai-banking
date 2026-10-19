package coze

import "github.com/qingyingjin/banking-service/internal/outbound"

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMalformedReportedCostCannotBecomeSuccessfulOutput(t *testing.T) {
	for _, v := range []string{"-1.00", "NaN", "Infinity", "1e309", "0.123456789", "1,000.00"} {
		t.Run(v, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"code":0,"execute_id":"1","data":"{}","token":3,"cost":"` + v + `"}`))
			}))
			defer s.Close()
			c, e := New(s.URL, "synthetic", time.Second, outbound.TestPolicy())
			if e != nil {
				t.Fatal(e)
			}
			out, e := c.RunSyncWorkflow(context.Background(), RunRequest{WorkflowID: "123"})
			if e == nil || out.ExecuteID != "" {
				t.Fatal("untrusted malformed cost accepted as successful output")
			}
		})
	}
}
