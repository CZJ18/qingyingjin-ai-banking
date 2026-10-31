package modelbuilder

import (
	"context"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
)

type bankingGatewayModel struct{ model ToolCallingChatModel }

func newBankingGatewayModel(ctx context.Context) (ToolCallingChatModel, error) {
	cfg, err := bankingscope.Gateway(ctx)
	if err != nil {
		return nil, err
	}
	client, err := bankingscope.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	m, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{APIKey: cfg.Capability, BaseURL: cfg.Origin + "/v1", Model: cfg.Model, MaxTokens: &cfg.MaxOutputTokens, HTTPClient: client})
	if err != nil {
		return nil, bankingscope.ErrDenied
	}
	return &bankingGatewayModel{m}, nil
}

func (m *bankingGatewayModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	// Runtime model options are deliberately ignored; the reviewed config wins.
	out, err := m.model.Generate(ctx, input)
	// SDK transport errors may contain request metadata. Never expose them to
	// workflow history, callbacks, API responses or logs.
	if err != nil {
		return nil, bankingscope.ErrDenied
	}
	return out, nil
}
func (m *bankingGatewayModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, bankingscope.ErrDenied
}
func (m *bankingGatewayModel) WithTools([]*schema.ToolInfo) (ToolCallingChatModel, error) {
	return nil, bankingscope.ErrDenied
}
