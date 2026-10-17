# Qingyingjin Banking Service

默认单人管理 AA（0.22.0）：一个发起人账号记录整单，朋友微信转账无需注册；逐项核对后由发起人确认收款，未获平台核验。保留多人协作，旧单不自动切换。

当前微信 AA 增量：API 0.21.0，见 [WECHAT-AA.md](WECHAT-AA.md)。保留模拟结算，新增明细核对和两阶段人工确认；不接管微信资金、不伪造平台回执。

当前模块8可信金融执行网关见 [MODULE8.md](MODULE8.md)，实际验证、迁移和文件清单见 [模块8交付](../../docs/banking-module8-delivery.md)。生产资金执行必须经过意向、绑定用户授权和网关事务；旧敏感写接口已经关闭。模块2至7文档保留其交付时点的历史行为。

当前模块3目标编译器、Coze配置、API与启动指南见 [MODULE3.md](MODULE3.md)，实际验证见 [模块3交付](../../docs/banking-module3-delivery.md)。模块2资金业务保持独立，目标编译不执行资金。

当前模块2金融核心代码、API、curl与启动指南见 [MODULE2.md](MODULE2.md)，实际验证与文件清单见 [模块2交付](../../docs/banking-module2-delivery.md)。以下保留模块1工程基础记录，关于“尚无业务接口”的描述属于模块1时点。

模块0总体设计：../../docs/superpowers/specs/2026-10-08-qingyingjin-system-architecture.md。
Coze 源码分析：../../docs/coze-module1-analysis.md。
部署及 curl：../../deploy/banking/README.md。

## 目录与依赖

- cmd/server：Gin 进程、环境读取、连接生命周期、优雅退出和 Docker 探针。
- cmd/token：可信操作员本地签发用户 JWT，不存在公开签发端点。
- internal/config：默认值和必填配置校验；拒绝模板凭证，错误不输出密钥值。
- internal/identity：用户 JWT、工具密钥与服务端绑定 Actor。
- internal/platform：GORM MySQL、Redis、连接池、真实 SELECT/PING、连接释放。
- internal/httpapi：HTTP 路由、统一响应、关联 ID、访问日志和异常恢复。
- internal/coze：已核实 /v1/workflow/run 客户端，只有内部调用方法。
- internal/application/reuse_test.go：验证现有 Banking 公开仓储工厂/领域服务可跨 module 编译并保留校验行为；不是已完成的金融应用层。
- api：内嵌 OpenAPI 和 Coze 工具子集，构建后不依赖工作目录寻找 YAML。
- tests/integration：integration build tag 下的真实数据库与 HTTP 集成检查。

Go module 使用 replace 指向 ../../backend；不修改 backend/go.mod，不创建 go.work。Gin 只用于独立服务。现有领域代码复用已通过编译测试；业务服务装配和 ID 生成将在金融模块中实现，不提前接入底层通用账户更新。

## 已实现接口

| 接口 | 鉴权 | 用途 |
|---|---|---|
| GET /healthz | 无 | 进程存活，与依赖可用性无关 |
| GET /readyz | 无 | 实际 MySQL SELECT 1 和 Redis PING；异常 503 |
| GET /api/v1/context | 用户 Bearer JWT | 返回可信用户与关联上下文 |
| GET /tools/v1/integration/context | X-Banking-Tool-Key | 返回工具服务绑定的用户与关联上下文 |
| GET /openapi.yaml | 无 | 完整 OpenAPI 文档 |
| GET /coze-plugin.openapi.yaml | 无 | 只含集成探测的插件文档 |

没有账户、转账、预算或储蓄业务端点。未知路径返回 404；已知路径错误方法返回 405。

## 响应和关联约定

统一 Envelope：code、message、request_id、trace_id、可选 task_id、data。每个请求生成新的 request_id；合法 X-Trace-ID 用于链路关联，不提供时用 request_id。X-Task-ID 只做关联标记。关联值最大 128 字符，仅字母/数字/下划线/点/冒号/短横线。非法值返回 400，日志不保留非法内容。

日志字段包括路由模板、方法、状态、耗时、关联 ID、可信 actor_kind/user_id。不输出 Authorization、工具密钥、SQL 连接串、请求体、query 或上游错误详情。

后续审计应保存 request_id/trace_id/task_id/operation_id/workflow_run_id，但本模块只有日志，没有审计表。调用者提供的 task_id 不能代替数据库所有权检查。

## 错误码规范

| HTTP | code | 本模块实现 |
|---|---|---|
| 200 | OK | 是 |
| 400 | INVALID_ARGUMENT | 是 |
| 401 | UNAUTHENTICATED | 是 |
| 404 | NOT_FOUND | 是 |
| 405 | METHOD_NOT_ALLOWED | 是 |
| 500 | INTERNAL_ERROR | 是 |
| 503 | DEPENDENCY_UNAVAILABLE | 是 |
| 403 | FORBIDDEN | 后续权限模块 |
| 409 | VERSION_CONFLICT / IDEMPOTENCY_CONFLICT / PLAN_STALE | 后续业务模块 |
| 422 | RISK_REJECTED / INSUFFICIENT_FUNDS / APPROVAL_REQUIRED / APPROVAL_EXPIRED | 后续金融模块 |
| 429 | RATE_LIMITED | 后续限流模块 |

接口不通过“200 + 失败 code”表示业务失败；Coze 客户端单独适配 Coze 自身行为。底层错误映射由未来 Application Service 完成，禁止向客户端返回 DSN、数据库错误文本或上游 msg。

## 用户和工具身份

JWT 必须使用 HS256，包含有效 iss/aud/exp 和正整数 sub；可选 coze_user_id 由操作员验证并签发。签发 TTL 最大 24 小时。当前返回用户 scope=context:read，不能用来声称金融操作已授权。

工具密钥至少 32 字节，与 JWT 签名密钥不同，通过 SHA-256 和常量时间比较校验。工具凭证固定绑定 BANKING_TOOL_USER_ID；不能通过 body/query/header 中的 user_id 覆盖。服务只返回 integration:read；用户 JWT 和工具密钥不能互相替代。

这是单演示用户的工程基础，尚无持久身份映射表、用户登录、OAuth 自动委托或密钥轮换管理 API。真实多用户身份模块应在可信来源验证 Coze 用户，按用户/角色/作用域管理工具凭证。远程部署应通过 TLS 网关使用这些接口；本轮 Compose 仅绑定宿主 loopback，Coze 容器经私有网络通信。

## 后续模块必须遵守

1. Handler/Tool → Application → Domain/RiskPolicy → Repository，不在 Handler/Agent 内访问 GORM。
2. 金额使用 int64 最小货币单位、首版 CNY；ID 对外用十进制字符串；持久时间沿用 Unix 毫秒。
3. task_id 由持久任务产生，workflow_run_id 保存 Coze execute_id；不混用 Coze 会话 ID。
4. 用户归属来自 ActorContext，仓储和操作事务再次校验；user_id 参数不能赋予权限。
5. 工具仅提供金融读取和操作提案；审批/执行 API 不出现在插件 OpenAPI。
6. 资金修改只能通过授权、确定性规则和数据库事务；不能直接调用通用 UpdateBankAccount。
7. 新模块自行提供迁移、OpenAPI/curl、单元/真实集成测试与执行结果。模块1未创建金融表。
8. 自然语言健康目标不得转为节食、体重变化的资金奖惩。

当前模块4：[模块4账单分析](MODULE4.md)。六个只读金融分析工具已接入，Coze真实模型配置/发布仍需实际实例和凭证；当前镜像tag为module4。


模块5 Subscription Agent 已扩展到现有服务；参见 [MODULE5.md](MODULE5.md)。包含识别、授权取消、预测、原生 Coze 配置和真实数据库测试。

模块6 Adaptive Savings：确定性三方案、事务事件总线、版本和建议确认，参见 [MODULE6.md](MODULE6.md)。模块6实现参见该文档。


模块7 Multi-Agent Orchestrator：[MODULE7.md](MODULE7.md)。持久化DAG、只读并行、有限重试、超时、暂停/恢复/取消、动态订阅核实、JWT REST和SSE。当前镜像为module7、schema版本6；敏感意向需模块8，真实Coze模型接入需实际发布配置。
