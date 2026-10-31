package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	workflowModel "github.com/coze-dev/coze-studio/backend/crossdomain/workflow/model"
	"github.com/coze-dev/coze-studio/backend/domain/workflow/entity"
	"github.com/coze-dev/coze-studio/backend/domain/workflow/entity/vo"
	mock_workflow "github.com/coze-dev/coze-studio/backend/internal/mock/domain/workflow"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"go.uber.org/mock/gomock"
)

func TestBankingSyncExecuteChecksActualPublishedCanvasBeforeExecution(t *testing.T) {
	pinned := `{"nodes":[],"edges":[]}`
	sum := sha256.Sum256([]byte(pinned))
	digest := hex.EncodeToString(sum[:])
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", `{"42":"`+digest+`"}`)
	t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", "http://127.0.0.1:8099")
	t.Setenv("BANKING_MODEL_NAME", "reviewed-model")
	t.Setenv("BANKING_MODEL_MAX_OUTPUT_TOKENS", "512")
	ctx, err := bankingscope.Attach(context.Background(), "bgc1.opaque", digest, "42", false)
	if err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	repo := mock_workflow.NewMockRepository(ctrl)
	repo.EXPECT().GetEntity(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p *vo.GetPolicy) (*entity.Workflow, error) {
		if p.ID != 42 || p.Version != "v1" || p.QType != workflowModel.FromSpecificVersion {
			t.Fatal("published version not pinned")
		}
		return &entity.Workflow{ID: 42, Meta: &vo.Meta{}, CanvasInfo: &vo.CanvasInfo{Canvas: pinned + " "}}, nil
	})
	svc := NewWorkflowService(repo)
	result, _, err := svc.SyncExecute(ctx, workflowModel.ExecuteConfig{ID: 42, From: workflowModel.FromSpecificVersion, Version: "v1"}, map[string]any{"user_id": "spoofed"})
	if result != nil || !errors.Is(err, bankingscope.ErrDenied) {
		t.Fatalf("changed published graph was not refused: %v", err)
	}
}

func TestDedicatedModeDeniesUnscopedSyncExecuteBeforeRepository(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		err := func() (err error) {
			defer func() {
				if recover() != nil {
					err = errors.New("workflow touched repository before requiring scope")
				}
			}()
			_, _, err = NewWorkflowService(nil).SyncExecute(context.Background(), workflowModel.ExecuteConfig{}, nil)
			return
		}()
		if !errors.Is(err, bankingscope.ErrDenied) {
			t.Fatalf("unscoped internal workflow not denied: %v", err)
		}
	}
}

func TestGeneralRuntimeKeepsSyncExecuteRepositoryPath(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	ctrl := gomock.NewController(t)
	repo := mock_workflow.NewMockRepository(ctrl)
	sentinel := errors.New("repository fixture")
	repo.EXPECT().GetEntity(gomock.Any(), gomock.Any()).Return(nil, sentinel)
	_, _, err := NewWorkflowService(repo).SyncExecute(context.Background(), workflowModel.ExecuteConfig{}, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("general workflow dispatch changed: %v", err)
	}
}

func TestDedicatedModeDeniesNonSyncWorkflowEntrypoints(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		svc := NewWorkflowService(nil)
		calls := map[string]func() error{
			"AsyncExecute": func() error {
				_, err := svc.AsyncExecute(context.Background(), workflowModel.ExecuteConfig{}, nil)
				return err
			},
			"AsyncExecuteNode": func() error {
				_, err := svc.AsyncExecuteNode(context.Background(), "", workflowModel.ExecuteConfig{}, nil)
				return err
			},
			"StreamExecute": func() error {
				_, err := svc.StreamExecute(context.Background(), workflowModel.ExecuteConfig{}, nil)
				return err
			},
			"AsyncResume": func() error { return svc.AsyncResume(context.Background(), nil, workflowModel.ExecuteConfig{}) },
			"StreamResume": func() error {
				_, err := svc.StreamResume(context.Background(), nil, workflowModel.ExecuteConfig{})
				return err
			},
		}
		for name, call := range calls {
			err := func() (err error) {
				defer func() {
					if recover() != nil {
						err = errors.New("non-sync workflow touched repository or request")
					}
				}()
				return call()
			}()
			if !errors.Is(err, bankingscope.ErrDenied) {
				t.Fatalf("%s not denied: %v", name, err)
			}
		}
	}
}
