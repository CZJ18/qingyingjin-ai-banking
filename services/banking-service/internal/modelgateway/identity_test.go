package modelgateway

import (
	"github.com/qingyingjin/banking-service/internal/outbound"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPaidGatewayRefusesMissingUserVerifierOutsideTest(t *testing.T) {
	_, err := New(Config{Policy: policy(), GatewayToken: token, SupplierKey: supplier, Outbound: outbound.Policy{Environment: "production"}}, &memoryBudget{})
	if err == nil {
		t.Fatal("paid gateway started without authenticated user attribution")
	}
}

func TestIdentityIntrospectionBindsOnlyAuthenticatedOwner(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path != "/api/v1/context" && r.URL.Path != "/api/v1/users/me") || r.Header.Get("Authorization") != "Bearer user-session" {
			t.Error("identity endpoint/credential")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/users/me" {
			w.Write([]byte(`{"code":"OK","data":{"id":"42","status":"active"}}`))
			return
		}
		w.Write([]byte(`{"code":"OK","data":{"actor":{"kind":"user","user_id":"42"}}}`))
	}))
	defer upstream.Close()
	verify, err := NewIdentityVerifier(upstream.URL, outbound.TestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	user, err := verify(t.Context(), "Bearer user-session")
	if err != nil || user != 42 {
		t.Fatal(user, err)
	}
}
