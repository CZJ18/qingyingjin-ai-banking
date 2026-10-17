# 轻盈金 AI Banking

Vue 3 + TypeScript 前端、Go 金融后端、Python Agent 服务与 Coze 工作流集成。

## 目录

- `frontend/ai-banking`：Vue 页面、API 客户端与测试。
- `services/banking-service`：认证、金融业务、Skill Registry、Scheduler、Executor、Trusted Execution Gateway，包含 schema 018。
- `services/banking-agent-service`：FastAPI/LangGraph 产品 API 与用户隔离投影。
- `backend/domain/banking`、`backend/infra/idgen`：Go 后端依赖的既有领域代码，保留原 module 路径。
- `coze/banking`：Agent 提示词、JSON Schema、工作流生成及测试。
- `deploy`：Docker 和本地部署脚本，私有配置不随仓库上传。

## 开发验证

前端：进入 `frontend/ai-banking` 后运行 `npm ci`、`npm test`、`npm run build`。

Go 后端：进入 `services/banking-service` 后运行 `go test ./...`。需要 Go 1.26.9；集成测试需另行准备独立测试数据库。`go.mod` 的本地 replace 依赖本仓库 `backend` 目录。

Python Agent：进入 `services/banking-agent-service` 后按其 README 使用 Python 3.13 与 uv 配置环境。

部署从各 `deploy/*/.env.example` 创建自己的私有配置，不要提交数据库密码、JWT签名密钥或模型 API Key。Compose 生产运行前须核对完整功能 overlay，并执行与后端版本一致的迁移。现有安全修复镜像 overlay 为 `compose.security-20261017.yaml`。

## 已知边界

模型调用已具备调用次数和 token 预占预算；货币账单硬额度尚未实现，不应承诺供应商消费绝不超过某金额。仅模拟金融业务，不用于真实资金交易。原始安全测试证据和本机数据库备份未上传。

上游 Coze 衍生文件保留原版权头与 Apache 2.0 许可证，见 `LICENSE-APACHE`。本目录只导出 Banking 构建依赖的领域子集，不包含完整 Coze Studio 平台；运行 Coze 集成需另行准备服务。
