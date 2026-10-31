# 模块2 Banking Core 交付与实际验证

日期：2026-10-08。独立 Banking Service 金融核心已实现并在 Docker Compose 启动。账户、交易、双分录、预算、目标金和模拟转账使用真实 MySQL 持久化；没有自定义前端或 AI 功能。

使用说明、目录依赖、完整 API 表、错误码、curl、启动及模块8对接见 [MODULE2.md](../services/banking-service/MODULE2.md)。完整契约见 [OpenAPI](../services/banking-service/api/openapi.yaml)。总体设计继续遵守模块0和模块1。

## 实际实现

| 范围 | 代码与行为 |
|---|---|
| Entity | User、BankAccount、只读 AccountBalance、Transaction、LedgerEntry、Budget/Category/Rule、SavingsGoal/Ledger/Plan、TransferIntent、Approval、Audit、Idempotency |
| Repository | 独立 GORM/MySQL，事务范围读写、账户顺序行锁、意向与授权当前读取、数据库幂等和唯一约束、受限余额投影、持久查询 |
| Service | 初始化记账、record_only/apply_simulated_effect 收支、授权转账、月度分类预算及预留、目标金存取意向与进度 |
| Handler | 31个HTTP方法，含6个原基础方法；用户JWT、严格JSON、浮点金额拒绝、字符串ID、分页筛选、关联ID、脱敏统一错误 |
| Risk | Gateway.Check 验证持久转账授权；Gateway.CheckFunding 控制开户初始资金和应用模拟收支；拒绝或未配置时不能绕过 |
| Migration | 001_core.sql 创建14个业务表，另建schema版本表；checksum、连接级named lock、DDL重试；拒绝不兼容旧账户数据 |
| 工程 | migrate/seed CLI、初始化与镜像脚本、Compose迁移依赖、OpenAPI生成与验证、健康检查 |

旧公共 Banking 的金额/币种、分页和租户账户查询已复用；旧通用更新方法可以直接写余额，因此未调用或对外暴露。兼容账户/交易字段保存在独立schema内，新资金写入通过双分录路径。Coze引擎和原backend代码未修改。

## 已执行的验证

| 验证 | 实际结果 |
|---|---|
| `go test -json ./... -count=1 -timeout 60s` | 退出0，47个测试/子测试记录，9个测试包通过，0失败 |
| `go test -tags integration ./tests/integration -count=1 -v -timeout 120s` | 退出0；12个Core子场景与真实依赖测试通过；使用 MySQL8.4.5、Redis8.0.2 和真实 HTTP Server |
| 并发回归 | 修复授权旧快照问题后，重复确认/并发超扣/相反转账连续3轮通过；最终完整测试也通过 |
| `go vet ./...` / `go mod verify` | 退出0，all modules verified |
| 原 Banking `go test ./domain/banking/... -count=1` | 退出0，5个测试包通过；公共仓储包无单独测试，原Coze全仓库测试未运行 |
| Windows `go build -buildvcs=false ... ./cmd/server` | 退出0，output/banking-service-module2.exe |
| Linux 编译与运行镜像 | server/migrate/seed均以linux/amd64、CGO=0编译成功；distroless nonroot镜像实际构建成功 |
| SQL迁移与重复迁移 | 本机CLI、集成测试、Compose迁移容器均成功；重复执行未重复建业务数据；迁移容器退出0 |
| 测试数据脚本 | Initialize-TestData.ps1 实际执行成功；容器seed连续两次，相同ID和576000分余额，reconciled=true |
| Compose | MySQL、Redis、Banking Service healthy，端口只绑定127.0.0.1；迁移完成后服务启动 |
| `/healthz` / `/readyz` | 实际容器HTTP均200；mysql、redis、schema全部ready |
| 容器资金API | 新模拟用户46开户10000分、目标0分；创建意向并显式approve 1000分；结果succeeded，源9000分、目标1000分，双方账本对账一致 |
| 服务重启持久化 | Docker重启banking-service后源仍9000分、reconciled=true；数据库新连接持久化也经集成验证 |
| OpenAPI | 28个路径/31个方法与实际路由逐一匹配；成功集成请求和全部集成响应通过契约校验；插件仍无金融写入/授权端点 |
| 原源码复核 | 模块1记录的20个Coze源文件SHA256均未改变 |

当前运行镜像为 `qingyingjin-banking-service:module2`。本轮采用本地Go1.26.4编译后构建运行镜像；完整Dockerfile多阶段构建在拉取Docker Hub构建镜像时因网络超时失败，不能声称该远程构建路径已经通过。初次在中文路径构建亦遇到BuildKit非ASCII请求头错误，Build-Image脚本提供英文盘符或临时英文运行上下文。镜像已实际运行，不只是交叉编译产物。

Git元数据仍不可用，使用buildvcs=false构建；没有创建Git提交。运行过程中未重置数据库卷、Coze数据或Windows Docker数据。

## 关键失败场景

真实数据库测试覆盖：余额不足、冻结账户、授权过期、拒绝决定、摘要不匹配、工具凭证冒充用户、账户及确认越权；相同幂等重放/冲突、外部交易标识重复、20个并发确认只产生一次入账、16个并发同键意向只产生一个ID、12笔100分转账争用500分仅5笔成功、相反方向转账锁序；第二次余额写入故障与成功审计故障完整回滚；账本每journal两分录和为0、账户投影等于账本、重连持久化；预算真实消费及目标金存取账本；替换拒绝网关能阻止所有资金写入类型。

实际测试曾失败并已修正：基础业务路由不存在；旧模块1测试期望账户404；并发确认在REPEATABLE READ旧快照下未读到新授权（改为FOR UPDATE当前读取）；状态响应ID与字符串OpenAPI契约不符。测试结果不是未经执行的承诺。

## 证据文件

- output/banking-module2-tests.jsonl：最终单元/HTTP/OpenAPI测试JSON记录。
- output/banking-module2-integration.txt：最终真实依赖/金融集成详细输出。
- docs/banking-module2-test-summary.json：测试计数。
- docs/banking-module2-health.json：容器实际健康响应，未保存凭证。
- docs/banking-module2-docker-api.json：容器真实授权转账及重启持久化结果。
- output/banking-module2-seed.json：本机seed持久结果；容器用户44两次结果亦已实际核验，账户118、预算7、目标7，余额576000分。

集成用例使用随机独立模拟用户，不删除已有数据。当前数据库包含测试/演示账户，这些数据明确是模拟输入；查询没有用固定结果替代MySQL。测试故障通过实际GORM回调注入，不将替身数据库宣称为MySQL测试。

## 修改文件清单

新增：

- services/banking-service/internal/entity/{models.go,rules.go,money_test.go,approval.go}
- services/banking-service/internal/repository/core.go
- services/banking-service/internal/service/{core.go,transfer.go,budget.go,savings.go,queries.go,validation_test.go}
- services/banking-service/internal/risk/{gateway.go,gateway_test.go}
- services/banking-service/internal/migration/{migration.go,001_core.sql}
- services/banking-service/internal/httpapi/{core.go,core_test.go}
- services/banking-service/cmd/{migrate,seed}/main.go
- services/banking-service/tests/integration/core_test.go
- services/banking-service/scripts/generate_core_openapi.py
- services/banking-service/Dockerfile.runtime
- services/banking-service/MODULE2.md
- deploy/banking/{Build-Image.ps1,Initialize-TestData.ps1}
- docs/superpowers/specs/2026-10-08-banking-core-design.md
- docs/superpowers/plans/2026-10-08-banking-module2.md
- docs/banking-module2-{delivery.md,health.json,docker-api.json,test-summary.json}

修改：

- services/banking-service/internal/httpapi/{server.go,server_test.go,contract_test.go}
- services/banking-service/internal/identity/identity.go
- services/banking-service/cmd/server/main.go
- services/banking-service/api/openapi.yaml
- services/banking-service/{Dockerfile,README.md}
- deploy/banking/{compose.yaml,compose.test.yaml,README.md}

输出目录中的日志、二进制与临时编译上下文是验证产物，不是业务源码或密钥。go.mod、go.sum、原backend和前端未修改。

## 后续模块与当前边界

本模块完成请求范围的模拟金融核心。模块8完整风控、审批分级、多因子确认、撤销、异步执行、Outbox/事件任务仍未开发；目前用户确认后同步执行。BudgetRule已持久化并影响可规划结余，不锁定资金。SavingsPlan只存月度建议，不自动扣款。身份沿用操作员JWT，用户登录/OAuth与工具金融作用域委托尚未实现。跨用户银行收款、真实银行转账、AI/多Agent、订阅取消、自定义前端均未实现。

模块8需实现Gateway的两个检查方法，使用已经锁定的意向/授权或已保存的交易/确认审计，禁止另开余额修改路径。模块3及后续金融工具只可访问用户授权数据、创建提案；当前插件不提供资金操作，不能用Coze工具密钥确认转账。
