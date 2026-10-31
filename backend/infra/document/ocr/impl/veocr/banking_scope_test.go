package veocr

import (
	"context"
	"errors"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"testing"
)

func TestDedicatedModeDeniesVolcengineOCRBeforeSDKAccess(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		for _, call := range []func() error{
			func() error { _, err := (&ocrImpl{}).FromBase64(context.Background(), "fixture"); return err },
			func() error {
				_, err := (&ocrImpl{}).FromURL(context.Background(), "https://example.invalid/image")
				return err
			},
		} {
			err := func() (err error) {
				defer func() {
					if recover() != nil {
						err = errors.New("OCR touched supplier config")
					}
				}()
				return call()
			}()
			if !errors.Is(err, bankingscope.ErrDenied) {
				t.Fatalf("Volcengine OCR not denied: %v", err)
			}
		}
	}
}
