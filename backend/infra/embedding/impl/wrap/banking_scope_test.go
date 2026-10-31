package wrap

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
func TestDedicatedModeDeniesDenseEmbeddingsAndDimensionProbe(t *testing.T) {
	for _, mode := range []string{`{"42":"pinned"}`, "invalid", " "} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		lower := &countingEmbedder{}
		e := &denseOnlyWrap{batchSize: 1, Embedder: lower}
		if _, err := e.EmbedStrings(context.Background(), []string{"text"}); !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("embedding not denied: %v", err)
		}
		if got := e.Dimensions(); got != 0 {
			t.Fatal("unknown dimensions unexpectedly resolved")
		}
		if lower.calls != 0 {
			t.Fatal("supplier embedding or dimension probe invoked")
		}
	}
}
func TestGeneralRuntimeKeepsDenseEmbeddingsAndDimensionProbe(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	lower := &countingEmbedder{}
	e := &denseOnlyWrap{batchSize: 1, Embedder: lower}
	if _, err := e.EmbedStrings(context.Background(), []string{"text"}); err != nil {
		t.Fatal(err)
	}
	if e.Dimensions() != 1 || lower.calls != 2 {
		t.Fatal("general runtime behavior changed")
	}
}
