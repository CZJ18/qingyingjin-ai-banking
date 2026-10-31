package ark

import (
	"context"
	"errors"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"testing"
)

type countingEmbedder struct{ calls int }

func (e *countingEmbedder) EmbedStrings(context.Context, []string, ...embedding.Option) ([][]float64, error) {
	e.calls++
	return [][]float64{{1}}, nil
}
func TestDedicatedModeDeniesArkEmbeddingsAndDimensionProbe(t *testing.T) {
	for _, mode := range []string{`{"42":"pinned"}`, "invalid", " "} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		lower := &countingEmbedder{}
		e := &embWrap{batchSize: 1, Embedder: lower}
		if _, err := e.EmbedStrings(context.Background(), []string{"text"}); !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("embedding not denied: %v", err)
		}
		if e.Dimensions() != 0 || lower.calls != 0 {
			t.Fatal("supplier dimension probe invoked")
		}
	}
}
func TestGeneralRuntimeKeepsArkEmbeddingsAndDimensionProbe(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	lower := &countingEmbedder{}
	e := &embWrap{dims: 1, batchSize: 1, Embedder: lower}
	if _, err := e.EmbedStrings(context.Background(), []string{"text"}); err != nil {
		t.Fatal(err)
	}
	e.dims = 0
	if e.Dimensions() != 1 || lower.calls != 2 {
		t.Fatal("general runtime behavior changed")
	}
}
