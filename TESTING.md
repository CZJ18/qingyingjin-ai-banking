# 测试与验收

## 不读取真实配置的本地回归

在新解压目录中执行，明确不要添加真实 env-file、供应商 Key 或集成测试数据库。仅使用仓库内合成测试 fixture。

```powershell
cd services/banking-service
go test -buildvcs=false ./internal/orchestrator ./internal/coze ./internal/modelgateway
go test -buildvcs=false ./cmd/model-gateway ./internal/modelspend
cd ../../frontend/ai-banking
npm ci
npx vitest run --config vitest.nonpaid.config.ts
npm run build
cd ../../services/banking-agent-service
uv sync --locked
uv run pytest -m "not integration"
```

本次只对导出的 Go 关键包进行实际回归/编译与前端类型检查，实际命令结果随外层验收记录提供。Python/所有前端测试命令是复现指南，不因列出而算作本次通过。

Python 集成测试与 Go `-tags=integration` 只能绑定全新专用测试数据库，运行前阅读每个测试的 env 要求，禁止连接保留的验收库、费用账本或生产库。

## 关键回归索引

- `internal/orchestrator/billing_explanation_evidence_test.go`：整月61笔证据压缩、报告引用唯一性、不能引用未披露来源、实际网关上下文边界。
- `cmd/model-gateway/*test.go`：manifest/Approval/Policy/执行计划一致性、独立摘要确认和配置读取顺序。
- `internal/modelspend/*test.go`：十进制金额、审批范围、费用约束；SQL 并发测试需要专用数据库，不在材料制作时重跑。
- `services/banking-agent-service/tests`：任务、RLS仓储、模型未知状态、恢复、服务接口与身份隔离。
- `frontend/ai-banking/src` 和 tests：来源、账户范围、草稿、方案确认、错误显示和真实浏览器脚本。

## 实际浏览器检查清单

1. 登录/退出、刷新恢复；不应出现他人账户。
2. 合成 CSV 预览不入账；重复记录明确拒绝。
3. 查询月份/类别与源报告一致；空数据不当零。
4. 少花500元仅对愿意调整类别给条件方案，必要支出保护；保存试行后刷新可读。
5. 卡片/转账/理财/订阅/生日入口明确模拟；规划确认不直接执行。
6. 无有效模型准入时返回明确拒绝；不在 fixture 上伪报真实模型成功。
7. 失败/unknown 任务保留；不重放未知模型请求、不删除费用占额。

历史验收记录与原始失败日志由操作者在本地保留，不纳入源码仓库或源码ZIP。历史v0.0.9验证仅覆盖一项精确合成账单任务；新部署应执行上述测试重新验证，不能将历史通过视为本机复现通过。实际省500元、真实七天复盘及正式供应商账单对账仍未完成。
