# 模块8完整代码审查与实际验收

日期：2026-10-09。范围：Trusted Banking Execution Gateway 及其必要的 Banking Core、Subscription、Orchestrator、身份和 HTTP 对接。结论：本地模拟金融网关修复后验收通过；真实 Coze 模型调用与漏洞数据库扫描尚未完成，不能把本报告视为真实银行上线许可。

本次采用 security-best-practices 的 Go 检查规范，检查入口、配置、权限、持久模型、API、资金计算、事务、日志和依赖。没有修改 Coze 原生 Agent/Workflow 引擎，没有前端改动，没有重构无关模块。

## 发现的问题和实际修复

| 编号 / 严重程度 | 原代码证据与影响 | 修复位置与验证 |
|---|---|---|
| BANK-RESERVE-001 / 高 | evaluate 在消费入账前用 `balance-amount < protected(...)` 拒绝支出。余额全部预留为餐饮时，即便用户确认正常餐饮消费，也无法支付。原逻辑回归确实返回 NECESSARY_RESERVE。 | execution/policy.go 的 postExpenseReserve 改用保存点内的真实入账结果重新计算必要预留，失败回滚；execute.go 在业务保存点失败后记录风险拒绝。NormalFood、ShoppingCannotSpendFoodReserve、PastFoodCannotReleaseCurrentReserve 已在真实 MySQL 和 Docker API 验证；余额、交易数量及拒绝风险记录均断言。 |
| BANK-AUTHZ-001 / 高 | agentPlanAllowed 使用 `HasPrefix("/api/v1/financial-plans")`，把用户选择确认 `/decisions` 也放行；账单前缀还会自动放行未来新增写入路由。Agent 可冒充用户的方案选择，虽不能因此直接转账。 | httpapi/execution.go 改为具体已注册的分析、创建规划、重新规划路由白名单；用户确认路径与未知写入默认拒绝。TestAgentMutationAllowlistUsesRegisteredRoutes 原逻辑复现失败，修复后通过。 |
| BANK-AUDIT-001 / 中 | HTTP human 拒绝、错误 JSON 和部分服务前置校验调用通用 Denied/fail，没有关联已有操作意向 ID，难以完整追踪某一意向的越权尝试。 | repository.go 新增 DeniedOperation，HTTP 和 Authorize/Execute 前置失败复用所有权限定的 failOperation。TestExecutionReviewDeniedAuditBinding 验证错误确认和 Agent 拒绝均关联自己的意向；现有外部账户/意向越权测试继续通过。未知或他人意向仅记录通用拒绝，避免泄露。 |
| BANK-CONTRACT-001 / 中 | Prepare 的预算年份、类别长度、目标名称/周期及导入交易必填字段校验弱于既有 Core，允许创建必然在执行阶段失败的意向。 | execution/prepare.go 对齐 Core 的 2000..2100 年份、名称/类别/备注长度、十年周期和交易必填限制。TestExecutionReviewPrepareValidation 五组非法请求均 HTTP400，不持久化意向；11 种金融操作正常路径继续通过。 |
| BANK-TRACE-001 / 中 | Agent JWT 绑定 task_id，但 HTTP 元数据原先采用 `X-Task-ID`，可把审计关联伪装为其他任务。此问题影响追踪可靠性，不等于获得他人账户权限。 | executionGuard 使用签名 Actor.TaskID 覆盖请求头。TestExecutionReviewDeniedAuditBinding 在测试客户端默认发送其他 task_id 时仍验证实际审计采用签名任务 ID，Docker API 同样通过。 |

另外在 execute.go 为所有业务执行建立嵌套事务保存点。领域错误可以持久为网关失败状态，同时回滚该次业务写入；数据库错误或超时回滚外层事务。已有并发转账、审计 SQL 触发器故障、取消超时、双分录平衡测试通过。正常取消失败的既有业务失败状态仍按其状态机持久化。

原逻辑的失败复现见 output/module8-review/regression-before.log；修复过程中一次跨编译版本的任务关联断言失败保留在 integration-intermediate.jsonl。最终固定版本的完整回归见 integration-tests.jsonl，零失败。没有把中间失败日志改写为通过记录。

## 逐项功能核对

| 功能/要求 | 核对结果及证据 |
|---|---|
| READ / PLAN / PREPARE / EXECUTE | 身份和作用域由服务端定义，Agent JWT 无授权/执行权限，插件仅包含 prepare_operation/get_operation。Agent 不能确认金融操作或用户规划选择。 |
| 用户明确确认 | UserAuthorization 持久绑定 owner、operation、完整摘要和随机确认值；摘要覆盖金额、目标账户、类型、快照、到期时间。confirmed=true 被拒绝；修改摘要/金额/目标或外部身份不能执行。 |
| 11 类本地模拟敏感操作 | 转账、储蓄存入/取出、取消订阅、预算规则、账户状态、开户、导入交易、创建预算、创建目标金及订阅管理均复用真实业务服务和 MySQL，TestExecutionFinancialDomains 验证正常流程。 |
| 旧敏感 API 隔离 | 生产 main 注入 TrustedGateway；旧金融写入返回 GATEWAY_REQUIRED，直接 Core 绕过也被持久授权校验拒绝。SimulationGateway 仅用于明确的操作者种子及测试夹具。 |
| 风控 | 六条持久必要规则，模拟限制、归属、绑定授权、整数金额上限、必要预留、资源版本；缺失/停用/版本不兼容拒绝执行。风险级别采用固定的保守策略与规则分级，不宣称实现机器学习风险评分。 |
| 正常生活约束 | 本月真实消费释放相应预算类别的剩余必要预留，其他类别与上月交易不能释放。FinancialPlan 明确设置的剩余生活义务不自动下调，需要用户更新实际剩余义务。未来收入和预计节约不计为可用余额。 |
| 一致性 / 并发 / 防重放 | 活跃用户行锁、账户锁、数据库事务、唯一幂等键；授权消耗、资金账本、结果和审计原子提交。真实并发转账不能超额扣款，重复执行不重复记账。 |
| 失败 / 过期 / 超时 | rejected/expired/failed 投影到旧意向，context 超时和 SQL 故障回滚，失败状态与错误码可查询。executing 不单独提交，不遗留已扣钱但未持久终态。 |
| 提示注入边界 | 备注和商户文本作为数据，不能更改确定性规则；执行仅使用持久、摘要绑定的参数。实际 Docker 转账使用含恶意指令的备注，未改变金额或收款账户。 |
| 审计与日志 | 用户、Agent、意向、授权摘要、风险级别、状态、时间、trace/task 持久记录；拒绝记录核实资源归属。访问日志不输出请求体、JWT、工具密钥、备注；GORM SQL 日志关闭。 |
| 数据库 / API / 其他模块 | 五个执行实体与 007_execution.sql 字段匹配；账户 ID 采用 JSON 字符串、意向 ID 采用 UUID、金额 int64 分。迁移重复执行及旧迁移校验通过；复用模块2/5/6/7，没有新增迁移或改变 REST 路径。HTTP 集成验证 OpenAPI 请求/响应；kind 参数由服务端严格解码，命名 Schema 提供操作契约。 |
| Coze 保留和工程完整性 | 20 个模块1基准文件 SHA256 全部不变。网关及相关风控/身份/Handler 扫描未发现 TODO 空方法、未实现接口或硬编码金融结果。固定规则常量、模拟器规则和测试夹具不是伪造业务数据；本地取消实际更新订阅状态及历史。 |

## 实际执行结果

| 验证 | 本次结果 | 原始证据 |
|---|---|---|
| go build / go vet / go mod verify | 全部退出码0 | build.log / vet.log / mod-verify.log / compile-results.json |
| go test -count=1 ./... | 146 条测试通过记录，15 个包，零失败 | unit-tests.jsonl |
| go test -count=1 -p 1 -tags integration ./... | 263 条通过记录，16 个包，6 条条件跳过，零失败 | integration-tests.jsonl |
| Go -race | Linux 缓存 golang:1.24-bookworm，执行网关/风控/身份三个包通过；Windows 最初因 CGO 关闭未执行 | race-linux.log；race.log 仅为失败尝试 |
| 实际 Docker HTTP | 7 条通过记录：真实转账、防重放、必要消费边界、审计关联、非法准备参数 | docker-tests.jsonl |
| 实际 docker restart 后 | 转账、授权消耗和余额持久，原请求重放没有重复入账，1 条通过 | restart-tests.jsonl / restart.log |
| 健康和契约 | healthz/readyz HTTP200；服务/MySQL/Redis healthy；运行二进制及两份 OpenAPI SHA256 与编译/源码一致 | banking-module8-review-health.json / binary-check.json |
| 依赖漏洞数据库扫描 | 未完成：govulncheck 工具下载遭 proxy.golang.org 网络超时 | govulncheck.log |

测试计数包含父测试/子测试；integration 重复执行单元测试，不能将各套件相加为独立业务场景数。6 条跳过为模块6/7/8 的运行实例/重启用例；模块8 两个用例随后已实际执行通过，模块6/7 四个 Docker 条件用例本次未执行，不冒充通过。所有 MySQL 集成测试实际连接已有模拟 MySQL 和 Redis；运行测试时暂停金融服务后台 worker，未删除数据卷。

## 实际修改文件

- services/banking-service/internal/execution/authorization.go：前置拒绝关联意向审计。
- services/banking-service/internal/execution/execute.go：业务保存点、入账后必要预留校验、失败风险记录。
- services/banking-service/internal/execution/policy.go：基于真实入账快照校验必要消费预留。
- services/banking-service/internal/execution/prepare.go：对齐 Core 参数约束。
- services/banking-service/internal/execution/repository.go：所有权限定的拒绝审计入口。
- services/banking-service/internal/httpapi/execution.go：明确 Agent 路由白名单、签名任务关联、拒绝审计对接。
- services/banking-service/internal/httpapi/execution_test.go：规划确认与未来写入默认拒绝回归。
- services/banking-service/tests/integration/execution_review_test.go：新增必要消费、回滚、审计绑定及非法意向回归，同时支持实际运行实例。
- services/banking-service/MODULE8.md：修复后 API 行为说明。
- docs/banking-module8-delivery.md：关联本次新验收，保留原交付结果历史。
- docs/banking-module8-review.md：本审查报告。

验收元数据另生成 banking-module8-review-tests.json、banking-module8-review-health.json、banking-module8-review-files.json；原始日志和中间失败记录位于 services/banking-service/output/module8-review/。修改清单按审查开始时的 SHA256 基线比对，仓库没有可用 Git HEAD，不能宣称已完成 Git diff/提交。没有数据库迁移、前端或 Coze 核心修改。

## 启动与复验

在 D:\智慧农业 执行：

```powershell
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d --no-build banking-service
curl.exe http://127.0.0.1:8090/healthz
curl.exe http://127.0.0.1:8090/readyz
```

正常联网重新构建仍使用 ./deploy/banking/Build-Image.ps1 -LocalBinaries。本次最终构建遭 gcr.io 超时，因此在脚本已生成的临时构建目录，把 FROM 替换为已缓存的 qingyingjin-banking-service:module8，再用 docker build --pull=false 覆盖三个最终二进制；保留相同 distroless 非 root 运行基础。网络失败日志保留为 docker-network-failure.log，实际成功日志为 docker-build.log；容器与最终二进制哈希核对通过，没有拿旧镜像冒充修复部署。

在 services/banking-service 下复验：

```powershell
go build -buildvcs=false ./...
go test -buildvcs=false -count=1 ./...
$env:BANKING_TEST_ENV_FILE='D:/智慧农业/deploy/banking/.env.local'
# 全量数据库回归前停止服务后台 worker，MySQL/Redis继续运行
go test -buildvcs=false -count=1 -p 1 -tags integration ./...
# 实际运行服务恢复后
$env:BANKING_TEST_BASE_URL='http://127.0.0.1:8090'
$env:BANKING_EXECUTION_STATE_FILE='D:/智慧农业/services/banking-service/output/module8-review/docker-state.json'
go test -buildvcs=false -count=1 -tags integration ./tests/integration -run '^(TestExecutionDocker|TestExecutionReviewEssentialExpense|TestExecutionReviewDeniedAuditBinding|TestExecutionReviewPrepareValidation)$'
# 必须实际重启 banking-service 后再执行
 go test -buildvcs=false -count=1 -tags integration ./tests/integration -run '^TestExecutionDockerRestart$'
```

## 已知限制、未满足项和下一模块条件

1. Coze agent-config.yaml 明确 published:false，model_id/workflow_id 未配置；Prompt/原生配置参考、工作流对接代码和插件契约不等于已经部署可用的 Agent。尚缺真实 Coze 模型、PAT、原生工作流发布与整链验收。无法从当前仓库生成真实凭证或虚构已发布 ID，需要实际运行环境配置后再验收；此次没有自动重写 Coze 引擎代替它。
2. govulncheck 下载失败，不能声称依赖不存在已知漏洞；网络恢复后必须补做漏洞数据库扫描。本次 go mod verify 仅证明依赖校验完整性。
3. 多账户共同规划按扣款账户保留完整安全底线，可能偏保守；未提供预算/规划时只保护已知支出，不声称了解未知生活需求。人工剩余生活义务须用户更新，不能让 Agent 仅以交易分类自行降低。
4. 身份仍为可信操作者签发的模拟用户 JWT，没有接入真实银行身份或商户。所有资金/取消效果在本地 MySQL，接入真实银行之前需要外部执行状态、结果核实、幂等与补偿；这些不在当前模拟模块授权范围内。

下一模块可继续使用持久网关 API 开发模拟业务和自动化联调：READ/PLAN/PREPARE 与人类 AUTHORIZE/EXECUTE 已分离，现有模块接口和数据库回归通过。若下一模块目标是真实 Coze 多智能体演示或完整比赛验收，当前尚缺上述原生配置发布与真实模型调用验证，应先补齐这些环境条件。
