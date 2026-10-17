package httpapi

import (
	"context"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/execution"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRuntimeRoutesAndResponsesMatchOpenAPI(t *testing.T) {
	h, a, _ := testHandler(t, false)
	h = NewWithOrchestration(a, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, execution.New(nil, nil))
	doc, err := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := a.IssueUser("42", "7", time.Hour)
	for path, item := range doc.Paths {
		if (strings.HasPrefix(path, "/api/v1/") || strings.HasPrefix(path, "/api/v2/")) && path != "/api/v1/context" {
			continue
		}
		if strings.HasPrefix(path, "/tools/v1/lifestyle/") || strings.HasPrefix(path, "/tools/v1/billing/") || strings.HasPrefix(path, "/tools/v1/subscriptions/") || strings.HasPrefix(path, "/tools/v1/savings/") || strings.HasPrefix(path, "/tools/v1/orchestrations/") || strings.HasPrefix(path, "/tools/v1/execution/") {
			continue
		}
		if item.Get == nil {
			t.Fatalf("non-implemented method in spec: %s", path)
		}
		req := httptest.NewRequest("GET", "http://localhost:8090"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Banking-Tool-Key", strings.Repeat("t", 32))
		found, params, err := router.FindRoute(req)
		if err != nil {
			t.Fatal(err)
		}
		requestInput := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: found, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if err := openapi3filter.ValidateRequest(context.Background(), requestInput); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		input := &openapi3filter.ResponseValidationInput{RequestValidationInput: requestInput, Status: w.Code, Header: w.Header(), Body: io.NopCloser(strings.NewReader(w.Body.String()))}
		if err := openapi3filter.ValidateResponse(context.Background(), input); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestRegisteredRoutesExactlyMatchOpenAPI(t *testing.T) {
	h, a, _ := testHandler(t, false)
	h = NewWithOrchestration(a, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, nil, nil, nil, nil, nil, nil, execution.New(nil, nil))
	engine := h.(*gin.Engine)
	doc, err := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]bool{}
	for _, route := range engine.Routes() {
		actual[route.Method+" "+strings.ReplaceAll(strings.ReplaceAll(route.Path, ":id", "{id}"), ":owner", "{owner}")] = true
	}
	for path, item := range doc.Paths {
		for method := range item.Operations() {
			key := method + " " + path
			if !actual[key] {
				t.Errorf("documented route not registered: %s", key)
			}
			delete(actual, key)
		}
	}
	for route := range actual {
		t.Errorf("registered route not documented: %s", route)
	}
}
