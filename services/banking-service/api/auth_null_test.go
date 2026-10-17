package api

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestAuthEmptyResponseAcceptsExplicitNull(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	response := doc.Components.Schemas["AuthEmptyResponse"].Value
	if err = response.VisitJSON(map[string]any{"code": "OK", "message": "done", "request_id": "test", "trace_id": "test", "data": nil}); err != nil {
		t.Fatal("explicit null rejected by authentication contract", err)
	}
}
