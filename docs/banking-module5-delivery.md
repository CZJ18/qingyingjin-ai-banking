# 模块5交付：Subscription Agent

交付时间：2026-10-09（Asia/Shanghai）。模块5后端、迁移与测试已实现；原生Coze配置、Prompt和Workflow生成器已交付。真实Coze实例发布/模型联调尚未完成，原因是没有可用运行实例和真实发布模型/Plugin/API ID。

## 实际实现

- 基于真实MySQL交易识别周期消费、可能重复扣费/订阅、观测金额变化；至少3次不同扣费事件，公开规则评分和交易证据，不虚构订阅/使用信息。
- Subscription、SubscriptionPayment、SubscriptionAnalysis、SubscriptionActionIntent、SubscriptionApproval五表，使用004迁移，001–003不变。复用Coze公开订阅状态、Core事务/幂等/审计框架、已有金额类型和身份中间件。
- 六个工具、查询/详情/状态/历史、用户确认本地管理、取消意向、明确approve/reject及模拟取消，均有持久状态；Agent不能授权/执行。
- 提案和执行接入SubscriptionGateway。取消摘要绑定当前订阅版本、用户、意向和15分钟有效期；并发确认仅执行一次。业务失败提交failed，SQL/审计错误回滚订阅、意向、授权。越权/拒绝追加审计。
- 预计每月、三个月、年度减少支出均整数分，明确条件及取整；余额增量始终0，不向目标金入账。缺少使用数据为unknown，用户提供旧使用记录只提示复核。
- 权威分析JSON持久化并做Schema验证。完整REST/插件OpenAPI保持运行路由一致，插件没有用户确认、管理、资金写权限。
- 原生Agent Prompt/配置和六个1/4/2节点工具Workflow画布生成器基于实际当前源码，需要实际ID与版本，无虚构发布API。适配Coze只接受HTTP200、空对象不发送body、整数null不能直接转换这三项实际限制。
- 无前端、无Coze核心修改、无真实商户调用、无新Go依赖。

## 执行验证

最终验证：84条Go单元测试记录、58条真实MySQL集成测试记录、5项Workflow生成器测试全部通过，go vet与go mod verify通过。完整结果见 [测试结果](banking-module5-test-results.json)，原始日志位于services/banking-service/output/module5。重点覆盖：无记录/证据不足、重复扣费、价格变化、多账户可能重复、用户明确管理、取消失败、缺失网关、过期、摘要伪造、越权、SQL回滚、十个并发确认、重复确认/决策冲突、旧版本、六工具与空body兼容、工具不能确认/管理、余额/目标金流水未变化。

采用本地Linux二进制构建distroless非root运行镜像，Docker Compose启动并执行迁移004。源代码多阶段构建未重新验证，不将运行镜像构建冒充该路径测试。

Docker停止曾导致一次依赖连接失败；已重新启动并用最后的代码重跑测试和镜像构建，失败及恢复说明保留在测试结果中。

实际Docker API检查记录在 [工具证据](banking-module5-docker-api.json)、[健康与重启持久化](banking-module5-health.json)。初始化脚本通过真实API建立零余额账户和4笔record_only扣费，候选有实际交易ID、金额、评分及价格变化；五个读取工具成功，取消意向在未纳入管理的candidate上被RISK_REJECTED正确拒绝。成功提案/取消完整链路已在真实MySQL集成测试中执行，不伪称此Docker候选已取消。初始化与验证脚本不会纳入管理或授权取消。

## 文件与使用

- [完整修改文件及SHA256](banking-module5-files.json)
- [架构/状态机/后续模块契约](superpowers/specs/2026-10-08-subscription-agent-design.md)
- [模块说明、启动、curl、错误码、测试命令](../services/banking-service/MODULE5.md)
- [004迁移](../services/banking-service/internal/migration/004_subscriptions.sql)
- [REST OpenAPI](../services/banking-service/api/openapi.yaml)
- [Coze工具OpenAPI](../services/banking-service/api/coze-plugin.openapi.yaml)
- [Agent配置](../coze/banking/subscription-agent/agent-config.yaml)
- [Workflow生成器](../coze/banking/subscription-agent/generate_tool_canvas.py)
- [初始化脚本](../deploy/banking/Initialize-Subscriptions.ps1)
- [Docker读取/候选网关验证脚本](../deploy/banking/Verify-Subscriptions.ps1)

Coze底座源码按模块1的20个文件SHA256重新比对，保持一致。仓库没有可用Git HEAD，因此提供明确文件清单，不编造commit/版本。

## 后续模块接入和未实现项

Supervisor/Savings Agent使用同一用户绑定身份读取SubscriptionReport，传递user_id、task_id、trace_id、audit_id与交易证据；其中预测是未来支出变化，不是可用余额，不能作为自动转账/目标金资金来源。模块8替换risk.SubscriptionGateway提案与执行检查，保持服务端持久授权与事务边界。当前用户JWT作为开发模拟身份，运营方签发；现有单用户插件凭据不支持共享跨用户Agent。

未实现：真实Coze实例配置发布与模型端到端验证、真实商户取消/使用遥测、自动周期扣费调度、模块8完整风控策略。数据库/账单观察不能证明合同或闲置；复杂混合周期与缺失账单可能漏检，预测日期已过期时明确提示证据过时。历史窗口付款证据累积保留，费用不称全部银行消费。取消只改本地模拟记录，账单历史保持。
