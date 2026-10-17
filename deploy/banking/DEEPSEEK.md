# DeepSeek 模型接入（2026-10-14）

当前金融核心仍是 Go Banking Service；P5 调度仍是 LangGraph。模型推理由 Coze 原生工作流调用 DeepSeek，金融金额计算、账户权限、授权、账本与执行网关保持原有职责。

本次实际注册的模型为 `deepseek-flash`，Coze 模型 ID `100003`。这些是本机实际注册结果，其他安装不得复用 ID。官方模型是否可用以账号的 `GET https://api.deepseek.com/v1/models` 响应为准。[DeepSeek Chat Completions 文档](https://api-docs.deepseek.com/api/create-chat-completion/) 列出当前模型及请求参数；脚本允许在私有配置中选择接口实际返回的模型。

## 配置与执行

从仓库根目录执行：

```powershell
# 创建被忽略的私有配置；已有文件保留原值。此步骤不访问网络。
powershell -NoProfile -File deploy/banking/Configure-CozeDeepSeek.ps1 -Mode Prepare

# 在本机编辑 deploy/banking/.env.deepseek.local，填写 API key。
# 不把 key 放到命令行、聊天、Prompt 或业务任务参数中。

# 只查询官方 GET /models，不生成聊天，也不修改 Coze。
powershell -NoProfile -File deploy/banking/Configure-CozeDeepSeek.ps1 -Mode Verify

# 原生模型注册会真实调用一次 1+1 测试，产生供应商费用。
# 随后为已有银行工作流发布新版本；不执行金融业务。
powershell -NoProfile -File deploy/banking/Configure-CozeDeepSeek.ps1 -Mode Apply
```

私有文件只读取以下字段，不读取或复用农业项目的模型凭据：

| 字段 | 用途 |
| --- | --- |
| `DEEPSEEK_API_KEY` | 用户提供的 DeepSeek 私有密钥；空值或模板值立即失败 |
| `DEEPSEEK_BASE_URL` | 必须为 `https://api.deepseek.com/v1`，拒绝其他地址、HTTP、查询串与 URL 内凭据 |
| `DEEPSEEK_MODEL` | 默认 `deepseek-flash`，执行前必须出现在实际 `/models` 响应中 |
| `DEEPSEEK_MAX_TOKENS` | 回复上限整数，默认 4096，允许 1–8192；不扩大 Go 金融任务的超时和执行权限 |

`Apply` 依赖当前实例的 `output/coze-local-private.json`：已有银行用户登录资料、所属空间 ID、七角色工作流 ID。凭据仅在内存和被忽略的私有文件中处理。不会新建占位工作流、替换已有 ID 或改写历史金融任务。

## 真实源码契约

脚本调用以下已在源码中确认的 Coze 接口：

- `POST /api/admin/config/model/create`：原生 `model_class=19`（`DeekSeek`）；`connection.base_conn_info` 只发送 `base_url`、`api_key`、`model`。
- `POST /api/workflow_api/canvas`：读取 `data.workflow.schema_json`、`data.workflow_version` 与 `data.vcs_data.submit_commit_id`。
- `POST /api/workflow_api/workflow_detail_info`：请求 `space_id` 和 `workflow_filter_list`；返回 `data` 数组，源于 `entity/vo/workflow_info.go` 的 `MarshalJSON`。指定 `workflow_version` 可核实旧发布版本仍存在。
- `POST /api/workflow_api/save` 与 `/publish`：保存后重新读取原生生成的 CommitID，再将该 CommitID 绑定发布。`Save` 忽略客户端提交的 `submit_commit_id`，不能把客户端 UUID 当作发布 CommitID。

模型连接器来源为 `backend/bizpkg/llm/modelbuilder/deepseek.go`。虽然原生 `BaseConnectionInfo` 包含 `thinking_type`，当前 DeepSeek builder 没有传递它，也没有传递 `reasoning_effort` 或 `thinking`。因此脚本不发送虚构的关闭思考参数。供应商的默认思考行为保留，实际性能需通过 P5 模型验收判断。[官方请求参数说明](https://api-docs.deepseek.com/api/create-chat-completion/) 规定 `thinking` 默认启用，`reasoning_effort=none` 可关闭；当前原生 builder 尚未暴露这一能力。

不修改 Coze 核心、不增加模型代理。原生工作流中仅修改 LLM 的 `modelType`、`modleName`、`maxTokens` 三项；原有 Prompt、JSON Schema、金额单位转换 Code 节点、依赖边与 60 秒节点超时均保留。

## 发布与恢复边界

`Apply` 先检查所有已有工作流均为已发布、未修改的草稿（`vcs_data.type=3`），核对银行空间归属与最新版本，然后注册模型。存在用户未发布的修改时返回 `COZE_UNPUBLISHED_DRAFT`，不会覆盖。

新版本递增实际 `vx.y.z`，旧版本逐项读回。模型配置相同时复用已注册模型 ID；工作流已经绑定对应模型和回复上限时跳过发布。每个成功步骤及时写入私有状态，原 Canvas 备份位于忽略的 `output/deepseek-migration/<run-id>/`。

Coze 的注册、保存与发布跨多次 HTTP 请求，整个迁移没有数据库级全局事务。中途失败时已发布角色保留新版本，其余角色保留旧版本；如果保存成功但发布失败，脚本拒绝自动覆盖残留草稿。需要依据备份、当前 Canvas 内容、原生 CommitID 与发布版本核实后恢复。本次首次联调遇到 CommitID 不匹配，已按照完整内容匹配恢复，记录于 `output/deepseek-migration-recovery-result.json`，没有删除旧版本。

## 密钥与原生平台边界

原生 `config_service.go:CreateModel` 在 DEBUG 会打印连接配置。脚本在注册前通过 Docker 读取实际 Go backend 进程的 `/proc/<pid>/environ`，要求其真实日志级别关闭 DEBUG/TRACE；不会只把 `docker inspect Config.Env` 当作证据，因为本机 launcher 会在容器启动后 source 配置。无法定位唯一进程时返回 `COZE_LOG_LEVEL_UNVERIFIED`，DEBUG/TRACE 返回 `COZE_UNSAFE_DEBUG_LOGGING`。本次实际新进程核实为 INFO。

当前原生 `modelmgr/model_save.go:encryptConn` 直接返回连接信息，因此模型密钥会存在 Coze 的模型配置数据库中，原生加密尚未实现。需控制 Coze 管理员与数据库访问；本次没有改变这一平台实现。Python P5 运行环境、Go Banking 业务环境和任务 checkpoint 均不需要 DeepSeek key。

脚本日志仅含固定错误码、HTTP 状态、模型/工作流 ID 和版本，不回显供应商错误正文、Cookie、PAT、用户密码或 API key。模型凭据变化需重新执行 `Apply`，旧历史发布版本继续引用旧模型记录，历史不会被改写。

## 本次实际结果

| 角色 | 保留的 Workflow ID | 新发布版本 |
| --- | --- | --- |
| Supervisor | 7694865787609153536 | v0.0.4 |
| Billing | 7694865791367249920 | v0.0.4 |
| Subscription | 7694865792856227840 | v0.0.4 |
| Savings | 7694865794450063360 | v0.0.4 |
| Lifestyle | 7696050871041261568 | v0.0.3 |
| Goal v2 | 7696050872391827456 | v0.0.4 |
| 原 Goal Compiler | 7694865796199088128 | v0.0.8 |

本次实际执行了配置准备、官方 `/models` 验证、模型注册、七角色发布与旧版本读回。随后实际执行 P5 的 `verify_p5_live.py --models`，退出码 0：四个专业 Agent 均取得真实 Coze 执行 ID，自然语言 Supervisor 规划成功，银行余额与账户版本保持一致，`financial_execution_enabled=false`。证据为 `services/banking-agent-service/output/p5/live-models.json` 与 `models-exit.json`。安全结果文件 `output/deepseek-config-result.json` 的 `live_agent_validation_passed=true` 依据该业务验收写入；今后重新 `Apply` 将先恢复为 false，要求对新绑定重新验收。

自动化测试命令：

```powershell
node --test deploy/banking/configure-coze-deepseek.test.mjs
node --check deploy/banking/configure-coze-deepseek.mjs
node --check deploy/banking/bootstrap-coze-local.mjs
```

覆盖密钥缺失、伪造服务地址、整数回复上限、原生运行日志、实际数组响应契约、原生生成 CommitID、发布幂等、未发布草稿拒绝、历史版本保留和错误输出脱敏。13 项离线测试通过，并且实际 `Verify`/`Apply` 与 P5 五角色完整模型业务验收已执行通过。

## P6 调用超时调整（2026-10-15）

本轮真实调用曾出现工作流 60 秒超时。新增可选 `DEEPSEEK_WORKFLOW_TIMEOUT_MS`，只接受 30000–180000 整数毫秒，不设置时保留现有节点配置。实际设置 120000 并重新发布七角色；模型仍是原生 ID 100003 / `deepseek-flash`，未新增模型，也未声称已关闭原生未暴露的 thinking 控制。

随后原生执行历史确认：4096 token 上限的一次调用耗尽输出额度却返回空字符串。通过现有有界配置将 `DEEPSEEK_MAX_TOKENS` 调整为 8192；原生接口没有返回 reasoning 内容，不能把“全部额度由推理消耗”作为已证实事实。再次实际调用在 11.351 秒后返回合法非空 JSON，输入 1195 / 输出 2054 token；周期 3、少花 10000 分、储蓄 20000 分，保留正常饮食与必要生活开支的原文约束。证据位于 `output/p6-goal-v2-diagnostic-8192.json` 与 `output/p6-goal-v2-process-8192.json`，不包含认证材料。

最新发布 Supervisor / Billing / Subscription / Savings / Goal v2 为 v0.0.6，Lifestyle 为 v0.0.5，原 Goal 为 v0.0.10。Go 模型客户端独立配置 150 秒，健康检查上限仍为 1 分钟。新增有界超时测试后，离线注册/配置测试实际通过 14 项。

重新发布会将 `live_agent_validation_passed` 恢复为 false。历史 P5 结果不能替代新版本验收；P6 完整模型浏览器报告以 `frontend/ai-banking/test-results/p6/live.json` 和交付说明为准。P6 过程中已实际观察到外部模型 503 服务繁忙及严格目标证据拒绝，均未当成成功或转账授权。
