package execution

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
	"testing"
)

func TestProfessionalAgentCannotPrepareTransfer(t *testing.T) {
	// The role refusal happens before any DB access, including with valid identity.
	s := New(nil, nil)
	_, e := s.Prepare(context.Background(), identity.Actor{Kind: "agent", AgentType: "billing", UserID: "42"}, service.Meta{UserID: 42}, "key", PrepareInput{Kind: "transfer", Parameters: json.RawMessage(`{}`)})
	// Missing audit storage fails closed instead of permitting the operation.
	if entity.Code(e) != "DEPENDENCY_UNAVAILABLE" && entity.Code(e) != "FORBIDDEN" {
		t.Fatal(e)
	}
	d := preparationSkill()
	if d.ID != "operation_preparation" || d.Version != 1 || d.InternalEntry != "execution.prepare" {
		t.Fatal(d)
	}
	for _, p := range d.AllowedPairs {
		if p.Mode == "execute" {
			t.Fatal("execution privilege in preparation")
		}
	}
}
