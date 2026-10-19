package coze

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCertificateFailureNeverDowngradesOrReturnsOutput(t *testing.T) {
	calls := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"code":0,"execute_id":"1","data":"{}"}`))
	}))
	defer s.Close()
	c, e := New(s.URL, "synthetic-pat-must-not-leak", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	out, e := c.RunSyncWorkflow(context.Background(), RunRequest{WorkflowID: "123"})
	if e == nil || out.ExecuteID != "" || calls != 0 {
		t.Fatal("TLS failure failed open")
	}
	if e.Error() != "Coze transport unavailable" {
		t.Fatal("unsafe transport error details")
	}
}
func TestRemoteHTTPConstructorIsRejected(t *testing.T) {
	if _, e := New("http://remote.example", "synthetic", time.Second); e == nil {
		t.Fatal("remote HTTP accepted")
	}
}
func TestReportedCostDecimalGrammar(t *testing.T) {
	for _, v := range []string{"0", "0.00000", "1.12345678", "999999999999999999.99999999"} {
		if !ValidReportedCost(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"", "-0", "-1.00", "NaN", "Infinity", "1e309", "0.123456789", "1000000000000000000", " 1.0", "01.00", "1,000", ".1"} {
		if ValidReportedCost(v) {
			t.Fatal(v)
		}
	}
}
