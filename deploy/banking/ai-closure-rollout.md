# 本轮隔离验收后的发布与回滚约束（2026-10-20）

本轮没有替换生产容器或迁移现有业务库。本文件是待执行方案，不能作为部署或回滚演练 PASS 的证据。验收记录见 `docs/ai-closure-20261020.md`。

## 发布前必须补齐

1. 本轮隔离环境已有 Go/Python 镜像及真实 Coze Linux 二进制运行记录，见 `output/ai-e2e-20261020-083042/reviewed-image-build.json` 和 `coze-execution-evidence.json`。这不能替代生产完整制品验收：仍须绑定最终源码清单、镜像仓库 digest、依赖锁文件、运行容器 image ID 和 Coze 二进制/挂载配置摘要，并对最终构建复验。
2. 使用独立 PostgreSQL、Redis、用户和 Coze PAT；不得复用生产业务库、测试凭据或已过期价格策略。导出待变更库的备份并在隔离库验证可恢复。供应商 key 只注入网关；操作员结算凭据不得注入网关、Go、Python 或 Coze。
3. Go 费用表保持 schema021；本轮没有新增 Go 迁移。Python migration003 由独立迁移角色执行，核对 checksum、FORCE RLS、`banking_agent_app` 无 super/bypass、`banking_agent_scheduler` 为 NOLOGIN 且无成员授权。运行角色不得持有迁移/角色管理权限。
4. 专用 Banking Coze 只开放 PAT 认证的 `/v1/banking/model-scope` 和同步 `/v1/workflow/run`；禁止 chat、stream、异步、插件执行、代码执行和其他通用入口。配置入口与出站网络白名单，Coze 仅能连接固定私有网关及所需基础设施，不能直达任何供应商、任意 HTTP 服务或任意 DNS 出口。删除或轮换旧直连 key；单凭模型列表和 metadata 响应不能证明网络封闭。
5. 从实际发布仓库读取精确 Canvas UTF-8 原始字节计算 SHA256，人工审核唯一 start→LLM→end 图。Go 网关与 Coze 的 workflowID/pin 必须一致；模型、输出上界和 origin 必须匹配。重新审核官方价格及上下文上界，使用短期有效价格策略并记录证据摘要。不得通过延长旧策略有效期跳过审核。

## 配置清单（只填写受保护文件或 Secret）

|组件|配置|
|---|---|
|Go Banking|现有认证/数据库/Redis配置；`BANKING_LANGGRAPH_TASK_STATE_URL`；`BANKING_LANGGRAPH_SERVICE_TOKEN`；`BANKING_COZE_MODEL_GATEWAY_URL`、`BANKING_COZE_MODEL_GATEWAY_TOKEN`；受控 Coze origin/PAT/工作流配置及每日配额|
|Model Gateway|独立 `BANKING_MODEL_GATEWAY_TOKEN`；`BANKING_MODEL_SUPPLIER_API_KEY`；`BANKING_MODEL_SUPPLIER_BASE_URL`；`BANKING_MODEL_IDENTITY_BASE_URL`；`BANKING_MODEL_TASK_STATE_URL`、`BANKING_MODEL_TASK_SERVICE_KEY`；专用最小数据库角色；审核的 policy、price evidence、workflow-policy-file；生产 TLS|
|Python|`BANKING_AGENT_DATABASE_URL`、`BANKING_AGENT_REDIS_URL`、`BANKING_AGENT_BANKING_URL`、`BANKING_AGENT_SERVICE_KEY`；最后才启用 `BANKING_AGENT_BACKGROUND_WORKER=true`，设置受限 poll 间隔|
|专用 Coze|非空 `BANKING_COZE_WORKFLOW_DIGESTS`；`BANKING_MODEL_GATEWAY_ORIGIN`；`BANKING_MODEL_NAME`；`BANKING_MODEL_MAX_OUTPUT_TOKENS`；PAT；供应商 key 不进入此进程|
|操作员|独立额度/结算受保护配置；用户额度按已经认证确认的实际用户配置；不得在前端开放增额接口|

生产私有服务通信使用 HTTPS，PostgreSQL 使用 verify-full；不要把隔离回环 HTTP 和 SSL disable 配置复制到生产。日志禁止记录 Authorization、Banking capability、PAT、DSN、提示词中的财务明细。

## 隔离验证与灰度顺序

1. 先启动数据库、只读身份/任务服务和网关，检查实际 readiness。Python worker 未启用时应明确显示 disabled。
2. 启动已打补丁的专用 Coze，验证 PAT 权限和 metadata；无 capability、无效 capability、错误 input/pin、无额度、跨用户和撤销用户必须拒绝，且供应商请求数不增加。验证插件/HTTP/code 无法外连，不得只看应用日志。
3. 本轮 `output/ai-e2e-20261020-083042/e2e-browser.json` 已记录 Vue→Go→Python→Coze→gateway 的实际完整浏览器链路，但供应商明确为本地 fixture，不包含真实 LLM。生产灰度前仍须以最终构建、全新受限测试用户、重新审核价格和受限预算验证真实模型完整链路；留存 taskID、providerID、journal、checkpoint、预占和最终 UI，并验证重复、超时、取消、进程中断和未知结果不得二次付费。前轮真实网关单调用不能替代此项。
4. 对同一构建进行独立代码审查、镜像校验、网络封闭检查及回滚演练，签收后另行准备生产灰度；不得直接执行旧部署脚本恢复直连路径。

## 回滚顺序

1. 先从入口停止新付费任务，停网关付费监听/撤销其出站权限；禁用 Coze bridge 和 Python background worker。对已有 in-flight 请求标记待核实，禁止以超时证明免费。
2. 记录全部 reserved/reported/unknown 调用、任务 lease/journal、价格版本和供应商请求 ID；保留预占、账本、加密后台能力和审计。不能删表、清窗口、清幂等键或以估算金额结算来恢复额度。
3. 等待或人工核对 in-flight；模型/工具结果未知的任务保持 interrupted。无正式逐请求账单时真实对账仍 BLOCKED。
4. 恢复上一个已验收的只读业务镜像与配置；Go 身份代码回滚前务必关闭付费入口，因为旧代码可能忽略新父会话/租约声明。不得以旧网关继续接收新能力令牌。
5. Python003 为增量兼容表/列，默认保留，不执行 DROP/反向破坏迁移；如必须恢复数据库，仅在隔离演练和独立授权后按备份恢复，另行核实在途操作和账本不能丢失。业务数据恢复不能替代供应商费用对账。
6. 检查旧能力失效、账户隔离、只读链路和所有未受控付费入口继续关闭。记录回滚镜像和源码 digest，回滚完成后再决定是否恢复已验收功能。
