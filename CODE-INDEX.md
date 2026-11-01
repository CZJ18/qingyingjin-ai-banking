# 代码与契约索引

| 内容 | 位置 |
|---|---|
| Vue 页面、API 客户端与生成类型 | frontend/ai-banking/src |
| Go HTTP/OpenAPI | services/banking-service/internal/httpapi；services/banking-service/api/openapi.yaml |
| Go 身份/任务委托 | internal/identity；internal/httpapi/langgraph.go |
| 金融执行网关 | internal/execution |
| 账单/CSV、订阅、规划 | internal/billing、internal/subscription、internal/planning（具体子模块见源码） |
| CSV原始记录与导入 | internal/statement；internal/httpapi/statement.go |
| 收款人、AA与卡片 | internal/recipients；internal/aa；internal/cards |
| 生日、提醒与跨场景 | internal/birthdays；internal/reminders；internal/lifestyle |
| Supervisor 与专业 Agent、引用校验 | internal/orchestrator |
| 模型/工作流契约 | internal/coze；backend/pkg/bankingscope |
| 费用/预占/审批/价格 | internal/modelspend；internal/modelgateway |
| CLI manifest/预检/候选计划/执行确认 | cmd/model-gateway |
| FastAPI / LangGraph / Worker / RLS | services/banking-agent-service/src/banking_agent |
| Agent 数据库迁移与契约 | services/banking-agent-service/migrations；services/banking-agent-service/api/openapi.json |
| 前端实际路由 | frontend/ai-banking/src/main.ts |
| Coze工作流与提示词 | coze/banking |
| Coze Studio 底座源码 | backend、idl（保留上游许可证；未携带其私有部署配置） |

表内 internal 和 cmd 路径均相对于 services/banking-service，其余路径相对于仓库根目录。
