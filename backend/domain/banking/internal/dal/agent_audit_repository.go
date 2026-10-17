package dal

import (
	"context"
	"errors"

	"github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/convertor"
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal/model"
	"gorm.io/gorm"
)

func (d *BankingDAO) CreateAgentAudit(ctx context.Context, audit *entity.AgentAudit) (*entity.AgentAudit, error) {
	if audit == nil {
		return nil, errors.New("agent audit must not be nil")
	}
	if err := prepareCreate(ctx, d, &audit.ID, &audit.CreatedAt, nil); err != nil {
		return nil, err
	}
	po := convertor.AgentAuditToPO(audit)
	if err := d.dbFor(ctx).WithContext(ctx).Create(po).Error; err != nil {
		return nil, err
	}
	return convertor.AgentAuditToEntity(po), nil
}

func (d *BankingDAO) GetAgentAudit(ctx context.Context, userID, id int64) (*entity.AgentAudit, error) {
	var po model.AgentAudit
	err := d.dbFor(ctx).WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return convertor.AgentAuditToEntity(&po), nil
}

func (d *BankingDAO) ListAgentAudits(ctx context.Context, userID int64, options *entity.ListOptions) ([]*entity.AgentAudit, error) {
	var rows []*model.AgentAudit
	if err := applyList(d.dbFor(ctx).WithContext(ctx).Where("user_id = ?", userID), options).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*entity.AgentAudit, len(rows))
	for i, row := range rows {
		result[i] = convertor.AgentAuditToEntity(row)
	}
	return result, nil
}
