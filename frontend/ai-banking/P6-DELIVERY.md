# P6 实际交付记录

日期：2026-10-15。P6 已实现并完成本机部署与实际验收；最终完整生产浏览器验收于北京时间 16:26:06 通过。入口：`http://127.0.0.1:8088`。以下结果只覆盖明确列出的本阶段检查，不宣称整个项目全部功能均已完成。

## 实现与边界

- 沿用 Vue 产品入口和八个业务页面，连接 Go `/api/v1`、`/api/v2` 与 FastAPI `/product/api/v1`。模型调用留在 Coze，任务运行由 P5 LangGraph 负责。
- 新增资金安全画像、个人建议、生活目标 v2 原文解析/核对/保存/版本历史、完整历史月账单证据、三方案与接受/拒绝反馈、个人投影同步。
- 新协同任务使用真实 LangGraph ID，原 Go 历史任务单独查询；支持事件流、依赖、运行/中断/恢复/取消及准备确认。
- PREPARE_ONLY 用户确认与最终金融授权分离；可检查绑定金额、账户、nonce、digest、版本和失效时间。拒绝、过期、未知结果显示真实状态。模拟资金执行经过现有网关。
- 令牌只存内存，按服务端已验证用户保存任务/目标/计划引用。切换身份清理响应、缓存与页面，丢弃旧会话响应。
- 新增非 root 静态 Docker 部署及同源代理；实际容器运行于 `127.0.0.1:8088`。原生 Coze 管理界面保留。
- 必要 Go 修复：模型调用超时与健康检查独立限额；生活目标证据识别“可调整”，拒绝“不可以/不愿意/不能”等否定表达；将“购物预算”等明确类别匹配已有标准分类。正常饮食和必要开支保护不变，未知或含糊类别不能用于自动调整。新规则标记为 `lifestyle-2`，历史方案保留原值。回归测试先实际失败，再修复通过。

本阶段没有数据库迁移，继续使用 Go schema 010 和产品 PostgreSQL schema 002；没有新增或替换金融执行 API。工具与金额结果使用实际 OpenAPI 契约，前端没有金融金额推理算法。

## 本阶段修改文件

当前目录没有可用 Git 元数据，下列为本阶段实际新增/修改文件清单，不代表 Git diff。

|范围|文件（相对仓库根目录）|
|---|---|
|客户端契约|`frontend/ai-banking/src/api/client.ts`、`agent-client.ts`、`schema.d.ts`、`agent-schema.d.ts`|
|会话与任务|`frontend/ai-banking/src/lib/store.ts`、`queries.ts`、`agent-job.ts`、`job.ts`、`product-actions.ts`|
|产品入口|`frontend/ai-banking/src/App.vue`、`index.html`、`vite.config.ts`、`package.json`|
|产品组件|`frontend/ai-banking/src/components/overview/ProductProfile.vue`、`LifestyleGoals.vue`、`ExecutionDialog.vue`|
|协同组件|`frontend/ai-banking/src/components/agents/DependencyFlow.vue`、`PreparationConfirmation.vue`、`TransferPreparation.vue`、`task-view.ts`、`p6-workbench.css`|
|助手输出|`frontend/ai-banking/src/components/assistant/TaskDetails.vue`、`TaskResult.vue`|
|页面|`frontend/ai-banking/src/pages/Overview.vue`、`Assistant.vue`、`Agents.vue`、`Billing.vue`、`Goals.vue`、`Planning.vue`|
|单元测试|`frontend/ai-banking/tests/agent-client.test.ts`、`agent-job.test.ts`、`client.test.ts`、`product-actions.test.ts`、`session.test.ts`、`task-view.test.ts`|
|浏览器验收|`frontend/ai-banking/tests/workbench-contract.mjs`、`planning-lifecycle.mjs`、`scripts/run-p6-live.py`、`scripts/verify-p6-live.mjs`|
|部署|`deploy/banking-web/compose.yaml`、`Dockerfile`、`Dockerfile.runtime`、`Dockerfile.dockerignore`、`.dockerignore`、`.env.example`、`nginx.conf`、`Setup-Local.ps1`、`Build-Image.ps1`、`Verify-Web.ps1`、`README.md`|
|必要后端修复|`services/banking-service/internal/config/config.go`、`coze_timeout_test.go`、`internal/lifestyle/schema.go`、`schema_test.go`、`engine.go`、`engine_test.go`|
|模型配置|`deploy/banking/configure-coze-deepseek.mjs`、`configure-coze-deepseek.test.mjs`、`.env.deepseek.example`、`DEEPSEEK.md`|
|说明|`frontend/ai-banking/README.md`、`P6.md`、`P6-DELIVERY.md`、`docs/superpowers/plans/2026-10-14-qingrongjin-product-p6.md`、总体升级路线|

私有 `.env` 与模型配置仅在本机更新，不随静态构建交付；其中的密钥不列出。`dist`、镜像和忽略目录中的测试结果属于构建/验收产物。

## 已执行的验收

|检查|实际结果|边界|
|---|---|---|
|Vitest|42 / 42 通过|会话隔离、旧响应、幂等重试、任务结果与 SSE|
|TypeScript / Vite|通过|存在 ECharts 557 kB 分块警告|
|工作台浏览器契约|7 项通过|拦截响应，不是真实模型或资金验证|
|规划页生命周期回归|3 项通过|拦截响应；修复前 3 项均实际失败，修复后通过|
|DeepSeek 配置测试|14 / 14 通过|离线配置/原生接口契约测试|
|Go config/coze/httpapi|3 包通过|不是 Go 全项目回归|
|Go lifestyle|包测试通过|含肯定/否定证据、预算后缀分类、未知类别与保护类别回归|
|生产部署只读检查|25 项通过|实际 nginx / Go / FastAPI 健康、鉴权和容器约束|
|生产非模型业务|7 组通过|真实 MySQL / PG / HTTP / Edge / SSE；无响应拦截|
|最终生产完整闭环|11 组通过，136 次 API 请求|真实 DeepSeek / Coze / Go / PG / MySQL / SSE / Edge；无响应拦截|
|重新发布后原生 Goal v2|实际成功|8192 上限，执行 ID 7696802942103322624；不替代完整浏览器闭环|

真实验收验证：变更资金偏好并核对持久化 revision、三个确定性资金方案、目标原文证据与减少支出/储蓄区分、缺预算澄清、三档预计减少 5000/7500/10000 分、必要支出原值保护、建议仍待确认时推进浏览器时钟后的自动禁用、接受/拒绝持久化与个人投影、准备确认不动资金、独立授权后双击仅一笔 29 分转账及双边余额、审计、两种拒绝后余额不变、跨用户任务/账户拒绝、取消任务、刷新重新连接和八个移动页面无溢出/运行错误。当前余额中不包含预计减少的开支。

本轮重新发布后实际模型闭环覆盖 Goal v2、Supervisor 和 Billing；其他专业模型的既有 P5 验收未在本轮重复执行，不把历史结果充作本轮测试。`live.json` 中 `fixture_only=false`、`response_interception=false`、`model_checks_skipped=false`，失败历史另外保留。Go 的 `config/coze/httpapi/lifestyle` 四包实际退出码均为 0。

最终镜像：Go `qingyingjin-banking-service:p6`（`sha256:ca825eba5de6bb16f4d5508ce7fb777770c5849f2f3eeb5395bfb23f3e26a038`）；Web `qingrongjin-banking-web:p6`（`sha256:eb867abf11216d0ec3d41dad6b61b797e6bd5e39d52335ef791a552ff9a7566b`）。已查看最终桌面生活方案与移动助手截图；两端功能检查均在实际 Edge 中完成。

## 运行与未满足项

启动和 curl 见 [P6.md](P6.md)，部署脚本见 [部署说明](../../deploy/banking-web/README.md)。原始报告在忽略目录 `test-results/p6` 与 `test-results/p6-workbench-contract.json`，区分真实调用和 fixture。

Docker Hub 认证超时、容器内 npm 下载 ECONNRESET，因此实际使用宿主机按锁文件安装后的 fresh build 加静态 runtime 镜像；完整多阶段源构建尚未成功验收。Browser plugin 不可用，实际使用本机 Edge / Playwright。P6 验收过程中遇到 Docker 退出、Coze 数据库退出、外部模型 503 和空输出，均保留失败记录。

生产多用户登录/SSO、独立原生 Coze Agent / Plugin Wrapper 正式发布、密钥数据库加密、财务健康评分与订阅长期使用证据仍属后续能力；不会由页面补造。P7 金融数据库迁移是可选后续阶段，尚未实施，不启用双写。

生活目标与任务历史目前由当前用户本机引用和按 ID 查询恢复，尚无跨设备自动发现完整列表。既有资金计划创建接口没有服务端幂等契约，页面不会自动重试创建；响应丢失后的未知计划 ID 可能需要按审计记录恢复，未将单纯添加请求头宣称为服务端去重。

P7 已具备开始迁移设计和演练的基础；切换资金数据库仍须独立完成逐笔对账、并发/回滚/幂等测试和切换回退验收。

汇总原始报告、实际镜像 ID 和未重复执行项保存于 `test-results/p6/acceptance.json`。模型配置结果另记录 P6 产品验收范围，不把 Goal v2 / Supervisor / Billing 的成功扩大为本轮已重跑全部专业模型。
