# 轻融金 · AI Banking Web

默认单人管理 AA（0.22.0）：一个发起人账号记录整单，朋友微信转账无需注册；逐项核对后由发起人确认收款，未获平台核验。保留多人协作，旧单不自动切换。

微信 AA 增量：可信执行页支持微信线下账单、消费明细、付款前核对、付款人自报与发起人独立确认收款。见 [WECHAT-AA.md](../../services/banking-service/WECHAT-AA.md)；新增浏览器契约测试为 `node tests/wechat-aa-browser.mjs`，不代表微信官方支付验证。

产品升级沿用本应用，连接独立 FastAPI `/product/api/v1` 与 LangGraph 五角色任务，同时保留 Go `/api/v1`、`/api/v2` 金融网关。产品部署见 [P6.md](P6.md)，金融 PostgreSQL 部署见 [P7.md](../../services/banking-service/P7.md)，本次手机号登录、迁移与真实验收说明见 [PHONE-AUTH.md](../../services/banking-service/PHONE-AUTH.md)。以下历史验收记录不代表本轮新增能力通过。

独立 Vue 3 应用，位于 `frontend/ai-banking`，保留现有 Coze Studio 前端。P6 只对 Go 模型超时配置及生活目标证据校验作必要兼容修复，未重写 Coze 或金融核心。

## 启动

```powershell
cd D:/智慧农业/frontend/ai-banking
npm ci
npm run dev
```

访问 http://127.0.0.1:5178。当前开发代理默认连接 `http://127.0.0.1:8090`。可将 `.env.example` 复制为 `.env.local`，修改服务端变量 `BANKING_PROXY_TARGET`。这不是浏览器环境变量；不要添加 JWT 签名密钥、工具密钥或 Coze PAT。

已有 Docker 部署的网页入口为 `http://127.0.0.1:8088`；5178 是独立的 Vite 开发入口，不会覆盖部署页面。当前后端需完成 013 短信验证元数据迁移及开发收件箱配置，启动和实际验收见 [SMS-AUTH.md](../../services/banking-service/SMS-AUTH.md)。原密码登录设计记录见 [PHONE-AUTH.md](../../services/banking-service/PHONE-AUTH.md)。Vite `/api` 代理保留原始 Host 和端口，使服务端能核对浏览器 Origin。

点击右上角「登录 / 注册」，输入手机号、密码；首次使用切换「立即注册」并重复确认密码。页面先匿名查询 `/api/v1/auth/capabilities` 决定短信能力；不会把配置请求当成登录会话。启用短信时，注册需要先发送验证码并填写本次 6 位码；60 秒后允许重发，5 分钟后过期，修改手机号或切换流程会清除旧验证码请求。短信禁用时保留原密码注册，并明确禁用「忘记密码」；配置无法读取时仍可登录，但不会假设注册无需验证。

本轮只接**本地开发短信收件箱**，暂不接腾讯云或其他真实短信服务。开发短信不会发到手机，HTTP 响应只返回 challenge_id、有效期和重发间隔，验证码必须在本机使用操作员命令读取。页面将此状态标为「开发验证（非真实手机号认证）」，不能据此认定用户拥有真实手机号。已登录但未验证的用户，可在「账户资料」使用当前密码和本次验证码完成开发验证。

「忘记密码」需要手机号、本次验证码、新密码及重复确认。成功后返回登录页，原会话失效，不自动登录。新注册身份不会自动认领已有用户的银行账户，也不会自动创建资金。历史身份如需绑定手机号，必须由本人持有效用户凭据调用受保护 `/api/v1/auth/bind`，不能仅凭手机号认领历史账户。

开发收件箱在 `services/banking-service` 目录运行；先按后端交付配置所需环境文件，再显式选择用途（注册 `register`、本人验证 `verify_phone`、找回密码 `password_reset`）：

```powershell
go run -buildvcs=false ./cmd/sms-inbox -env-file ../../deploy/banking-postgres/finance.app.env.local -env-file ../../deploy/banking/sms.env.local -phone <本次开发测试手机号> -purpose register -allow-development-code
```

该命令会在本机显示开发验证码，只在自己的终端读取，不复制到日志或聊天；网页没有短信收件箱查询接口。端到端真实执行证据以后端短信交付说明为准，前端契约测试的测试码不是实际短信发送证明。

短期访问令牌只保存在页面内存，刷新后通过 HttpOnly、SameSite=Strict cookie 恢复登录；认证请求与银行操作都由服务端核验。退出先撤销服务端会话，再清理页面状态。普通业务和 SSE 继续用内存 Bearer，不在 localStorage 保存手机号、密码或令牌。页面不再提供粘贴 JWT 的入口，运维 `cmd/token` 仍仅供已有 API/测试使用。

## 已实现

- 总览：账户余额、目标金、账单收支、预算、真实趋势、交易、分析发现、最近任务。
- AI 工作台：会话任务引用、自然语言 Supervisor 任务提交、真实事件流、结构化账单/规划/订阅输出、账户上下文。
- 协同中心：节点状态、依赖关系、工具输入输出、执行耗时、来源、时间线、暂停/恢复/取消、任务确认。
- 账单：月份、账户、分类与商户搜索、服务器分页、交易详情、分类环图、预算对比、异常证据。
- 订阅：服务端月/年费用预测、重复线索、扣费预估、详情、取消意向、授权与执行。
- 健康目标：生活目标保存/修改/Coze 解析、目标金创建、资金流水、存入/取出、储蓄计划修改。
- 动态规划：约束表单、三种真实方案、版本趋势、新旧储蓄建议对比、触发原因、每五秒同步规划变化。
- 可信执行：模拟开户、账户冻结/解冻、转账意向、核验快照、风险规则、待授权与历史、操作审计。
- 登录：手机号密码注册、登录、自动恢复、到期前续期、账户资料掩码展示、退出及身份隔离；能力驱动的开发短信注册、本人验证和密码找回。

## API 与边界

统一客户端：`src/api/client.ts`。类型：`src/api/schema.d.ts`，直接根据仓库完整 OpenAPI 生成，运行 `npm run api:generate` 可更新。订阅 GET 返回 `SubscriptionReport`，客户端明确适配其中的订阅实体；金融意向等待状态为 `pending_authorization`。所有金额请求使用整数分，元输入用十进制字符串转换，拒绝超范围或多于两位小数的输入。

敏感写操作只调用 execution 网关，顺序为 prepare → 用户核对 → authorize → execute。执行带幂等键与最新服务器版本。超时或网络错误后查询状态，页面只根据服务器 operation.status 判断是否执行。开户期初资金为明确确认的模拟资金。订阅取消仅表示本地模拟状态，不能代表外部商户停止扣费。

SSE 使用带 Bearer 的 fetch stream，避免把令牌放到 URL；断开后先 GET 任务恢复快照，再携带 Last-Event-ID 续接。提供取消和退避重连，同时周期查询保障状态恢复。任务输出不是伪造聊天或定时打字效果。

浏览器仅持久化经过验证的用户命名空间下的任务、目标和规划 ID；实际内容每次从服务端获取。通知来自服务器待授权操作。未登录时显示空状态，不展示伪造余额、交易或模型输出。自然语言规划和目标解析需要实际发布的 Coze 工作流；未配置时呈现后端错误。金融应用运行不需要在浏览器中提供 Coze 凭据。

当前账户与目标金读取上限 100 条；规划版本上限 100 条，完整历史仍由服务端保存。大规模数据场景需扩展相应列表翻页。规划接口以整数而非字符串接受 ID，客户端拒绝不能安全表示的整数，避免精度损失。

部署将 `dist` 作为静态资源，配置 SPA history fallback，并将 `/api/v1`、`/api/v2`、`/product` 同源反向代理到对应服务。Vite 的开发代理不会包含在 `dist` 中。认证反向代理必须保留包含端口的 Host。生产 HTTPS 使用 Secure cookie；本地 HTTP 必须在后端显式选择本地配置。当前生产 TLS/HA/灾备覆盖范围见 P7 交付说明，不由页面构建成功推定。

## 验证

```powershell
npm run api:generate
npm test
npm run build
# 本地启动开发服务后，使用已安装的 Chromium（或 npx playwright install chromium）：
npm run test:browser
node tests/planning-lifecycle.mjs
node tests/workbench-contract.mjs
node tests/sms-auth-browser.mjs
```

如果需要指定浏览器路径，设置 `BANKING_TEST_BROWSER`。`inspect.mjs` 检查八个路由、手机号登录失败、390px 手机溢出及导航；`verify-business.mjs` 在浏览器拦截中提供认证及业务契约数据，验证转账授权、重复提交、取消订阅和服务端失败状态。附加契约覆盖规划页面延迟响应隔离、任务准备确认状态，以及短信注册/倒计时/改号清理/找回密码/本人验证/禁用配置和敏感信息不持久化。所有 mock 只属于测试，没有进入应用源代码；契约通过不代表真实数据库、短信或 Coze 已完成联调。

当前登录真实验收入口为 `scripts/verify-phone-auth-live.mjs`，运行条件与证据见 [PHONE-AUTH.md](../../services/banking-service/PHONE-AUTH.md)。P6 完整业务验收由 `python scripts/run-p6-live.py` 准备两个固定隔离用户并调用 `verify-p6-live.mjs`，通过真实手机号密码表单登录；它需要真实模型依赖，不应在离线时宣称完整验收通过。夹具凭据只写仓库已忽略的 `output/phone-auth/p6-browser-credentials.local.json`，Unix 权限 0600，Windows 仅当前用户 ACL；不会覆盖未知的既有绑定。

## 历史金融模块联调入口与记录

以下是早期 MySQL 金融模块验收方法和记录，保留用于回归溯源。已经切换 PostgreSQL 的实例应使用 P7 和 PHONE-AUTH 部署步骤，不能把这组旧命令用于恢复旧 MySQL 写入权限。早期开发链路为 `http://127.0.0.1:5178` → Vite 同源代理 → Banking Service `http://127.0.0.1:8090` → 独立 MySQL / Redis：

```powershell
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d banking-mysql banking-redis
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml run --rm --no-deps banking-migrate
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d banking-service
Invoke-RestMethod http://127.0.0.1:5178/readyz
```

`npm run test:live` 仍指向早期 `verify-live.mjs`，本次已将认证入口改为手机号密码，并保留原业务断言；本轮没有重跑其完整历史业务/模型流程。该脚本会持久化模拟账户、转账、目标、规划和订阅记录，因此必须使用全新的隔离模拟用户，不能使用日常用户。除运维 JWT 和用户 ID 外，现需设置 `BANKING_LIVE_PHONE`、`BANKING_LIVE_PASSWORD`；手机号必须先由该隔离用户本人的凭据绑定。缺少密码配置会明确退出，不会寻找已删除的 JWT 输入框。以下在 Banking Service 目录初始化全新测试用户并首次绑定，再到前端目录执行：

```powershell
$testUserId = '4201100' # 每轮更换为未使用的模拟用户 ID
go run ./cmd/seed -env-file ../../deploy/banking/.env.local -user-id $testUserId -confirm-simulated-data
$env:BANKING_LIVE_JWT = go run ./cmd/token -env-file ../../deploy/banking/.env.local -user-id $testUserId -ttl 2h
$env:BANKING_LIVE_USER_ID = $testUserId
$env:BANKING_LIVE_ALLOW_WRITES = 'simulated-test-user'
$env:BANKING_LIVE_PHONE = Read-Host '隔离测试用户手机号'
$env:BANKING_LIVE_PASSWORD = [System.Net.NetworkCredential]::new('', (Read-Host '隔离测试用户密码' -AsSecureString)).Password
$bindBody = @{phone=$env:BANKING_LIVE_PHONE; password=$env:BANKING_LIVE_PASSWORD} | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8090/api/v1/auth/bind -ContentType 'application/json' -Headers @{Authorization="Bearer $env:BANKING_LIVE_JWT"} -Body $bindBody | Out-Null
Set-Location ../../frontend/ai-banking
npm run test:live
Remove-Item Env:BANKING_LIVE_JWT,Env:BANKING_LIVE_PHONE,Env:BANKING_LIVE_PASSWORD
$bindBody = $null
```

这份历史脚本默认检查未配置 Coze 时的 `COZE_UNAVAILABLE`；它原来的三 Agent 金融工具调度不代表大模型联通。已有工作流时通过 `BANKING_LIVE_EXPECT_COZE=1` 选择对应成功链路；P6 五角色产品闭环使用上方 P6 runner。Coze 地址、PAT 及工作流 ID 只配置在后端环境中。

2026-10-10 实测：数据库迁移 001—007、MySQL / Redis 就绪；真实浏览器开户、0.29 元授权转账、重复提交保护、账单搜索与详情、动态规划自动更新、订阅取消、任务工具结果与 SSE 回放通过。修复成功状态 `succeeded` 的执行结果显示，并补齐规划必须关联目标金的校验。截图及无凭据的检查报告保存于被忽略的 `test-results/live/`，不向应用注入测试数据。

依赖包括 Vue 3、TypeScript、Vite、Tailwind CSS 4、shadcn-vue 配置与本地 Button、Reka 无障碍 Dialog/Popover、Vue Router、Pinia、TanStack Vue Query、按需 ECharts、Lucide。
