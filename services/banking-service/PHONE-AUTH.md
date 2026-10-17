# 手机号与密码登录

后续已增加 013 手机号验证与密码找回（本地开发收件箱，不发真实短信）；当前启动与验收使用 [SMS-AUTH.md](SMS-AUTH.md)，下文保留此前 012 交付记录。

账户入口改为「登录 / 注册」。注册创建独立的持久化用户，登录后可刷新页面恢复会话，退出会立即撤销该浏览器会话。原有模拟账户、交易、账本与金融授权流程保持原有归属。

手机号目前作为登录标识，支持中国大陆 11 位号码及 `+86` 前缀；未接短信验证、短信登录、密码找回和手机号变更。本地只有模拟银行业务，不能据此宣称具备生产身份核验能力。

## 数据与权限

- 迁移 012 在 `banking_users` 增加 `phone_number`、`password_hash`、`auth_version`、`refresh_hash`、`refresh_expires_at`，业务表仍为 46 张。完整字段和双数据库迁移说明见 [phone_auth_012.md](internal/migration/phone_auth_012.md)。历史 001–011 字节及校验和未修改。
- 密码使用 Argon2id 独立随机盐，19 MiB、2 次迭代、1 并行度；只保存编码散列。密码为 8–128 个 Unicode 字符，最多 512 UTF-8 字节，不裁剪首尾空白。参数依据 [OWASP Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)。
- 公开 `User` 不含凭据。手机号规范化为 `+86`，网页仅显示脱敏号码；注册使用随机正 int64 用户 ID。新用户不会领取历史账户、自动开户或增加余额。
- Access JWT 15 分钟，仅驻留网页内存；刷新凭据为随机 256 位令牌，数据库只存 SHA-256，24 小时固定期限。HttpOnly、SameSite=Strict、host-only Cookie 只发送至 `/api/v1/auth`；正常金融 API/SSE 继续使用 Bearer。
- 每个用户保留一个活跃网页会话。重新登录替换会话，退出撤销版本化 JWT；旧 Cookie 的退出请求不撤销更新的登录。无浏览器会话版本的操作员 JWT 仍按原 API/CLI 契约校验，不再提供粘贴令牌的默认网页入口。
- 绑定历史用户必须提交该用户已验证的用户 Bearer，subject 必须等于 user_id。Agent、匿名请求、指定别人的 user_id、覆盖已绑定密码均被拒绝。没有默认密码。
- Redis Lua 在密码散列前原子限制同一号码 15 分钟 5 次尝试、IP 每分钟 100 次；号码/IP 在 key 中散列。依赖故障拒绝认证。当前反向代理连接视为 IP，生产多实例代理应明确部署真实来源地址策略。
- Cookie 接口严格校验 Origin 的协议、host 和端口；带 Cookie 的请求，以及 refresh/logout，必须有 Origin。仅这些同源认证接口接收 Cookie。所有认证响应 no-store，日志不包含密码、完整号码、JWT、Cookie、散列和请求正文。
- 登录不授予 Agent 金融执行权；金融操作仍须 prepare → 具体意向摘要/nonce 确认 → 风控 → authorize → execute。

## API 契约

完整契约：[api/openapi.yaml](api/openapi.yaml)，版本 `0.15.0`。这些身份接口不进入 Coze Plugin。

| 方法与路径 | 输入 | 成功结果 |
| --- | --- | --- |
| `POST /api/v1/auth/register` | `phone,password,display_name?` | 201，会话及 HttpOnly Cookie |
| `POST /api/v1/auth/login` | `phone,password` | 200，会话及 HttpOnly Cookie |
| `POST /api/v1/auth/refresh` | `{}`、同源 Origin、Cookie | 200，新 Access JWT，不延长固定会话期限 |
| `POST /api/v1/auth/logout` | `{}`、同源 Origin、Cookie | 200，`data:null`，撤销会话并清 Cookie |
| `POST /api/v1/auth/bind` | `phone,password`、原用户 Bearer | 200，绑定该历史用户并登录 |

会话 `data` 为 `{access_token,expires_in,user,phone_masked}`。刷新凭据只出现在 Set-Cookie，不出现在 JSON。统一错误码：`INVALID_ARGUMENT` 400、`AUTH_INVALID_CREDENTIALS` / `UNAUTHENTICATED` 401、`FORBIDDEN` 403、`AUTH_PHONE_UNAVAILABLE` / `STATE_CONFLICT` 409、`AUTH_RATE_LIMITED` 429、`DEPENDENCY_UNAVAILABLE` / `STORAGE_FROZEN` 503。账户不存在与密码错误返回同一认证失败消息。

示例中的尖括号均为自己的值，不使用默认密码。正式使用应由同源网页保管 HttpOnly Cookie；CLI 调试 Cookie 文件也属于秘密，不应提交或公开。

```shell
curl -i http://127.0.0.1:8088/api/v1/auth/register \
  -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8088' \
  -d '{"phone":"<手机号>","password":"<自行设置的密码>","display_name":"我的账户"}'

curl -i http://127.0.0.1:8088/api/v1/auth/login \
  -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8088' \
  -d '{"phone":"<手机号>","password":"<密码>"}'

curl http://127.0.0.1:8088/api/v1/users/me -H 'Authorization: Bearer <access_token>'

# 只绑定自己已验证的历史身份，不会覆盖已绑定号码或密码。
curl -i http://127.0.0.1:8088/api/v1/auth/bind \
  -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8088' \
  -H 'Authorization: Bearer <原用户JWT>' \
  -d '{"phone":"<手机号>","password":"<自行设置的密码>"}'
```

## 本地启动与更新

复用 P7 的 PostgreSQL 金融主库及其运行/迁移角色。冻结的历史 MySQL 主库不能迁移、启用或自动回退。012 之后的快照只能导入同样为 012 的目标，旧 011 接收端会先因列契约不符被拒绝。

配置 `BANKING_AUTH_COOKIE_SECURE` 默认 `true`。只有 loopback HTTP 的本地 overlay 显式设为 `false`；HTTPS 部署必须使用 Secure Cookie 和受信任的同源反代。Nginx/Vite 保留网页 Host 的端口，不通过开放任意 Origin 规避校验。

```powershell
# 仓库根目录。镜像均以新标签构建，不覆盖历史 P7/P6。
powershell -NoProfile -File deploy/banking/Build-Image.ps1 -LocalBinaries -ImageTag qingyingjin-banking-service:phone-auth -RuntimeBaseImage qingyingjin-banking-service:p7
powershell -NoProfile -File deploy/banking-web/Build-Image.ps1 -LocalDist -ImageTag qingrongjin-banking-web:phone-auth -NginxRuntimeImage qingrongjin-banking-web:p6

# 维护窗口：先验证备份/目标库，再停止 Go，运行受显式库名约束的迁移。
docker stop qingyingjin-banking-service-1
Push-Location services/banking-service
go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking-postgres/finance.migrate.env.local -expected-database qingrongjin_finance
Pop-Location

docker compose --env-file deploy/banking-postgres/finance.compose.env.local -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml -f deploy/banking/compose.coze-local.yaml -f deploy/banking-postgres/compose.yaml -f deploy/banking/compose.phone-auth.yaml up -d --no-deps --no-build banking-service
$env:BANKING_WEB_IMAGE = 'qingrongjin-banking-web:phone-auth'
docker compose -f deploy/banking-web/compose.yaml up -d --no-deps --no-build banking-web
Invoke-RestMethod http://127.0.0.1:8088/readyz
```

迁移命令的 `-expected-database` 在打开连接前检查实际配置，可拒绝继承环境变量导致的串库；测试库还应加 `-test-only`，同时要求精确匹配及 `_test` 后缀。不要重新执行历史 P7 镜像启动命令降级新版 schema。

## 自动化验收

Go 单元：`go test ./... -count=1`。前端：`npm test`、`npm run build`。真实 Cookie/认证集成只接受显式 `_test` 数据库，不连接金融主库。

```powershell
Push-Location services/banking-service
go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking-postgres/finance.core-test.env.local -expected-database qingrongjin_finance_core_test -test-only
$env:BANKING_AUTH_TEST_ENV = 'D:/智慧农业/deploy/banking-postgres/finance.core-test.env.local'
go test -tags integration ./internal/userauth ./internal/httpapi -count=1 -v
Pop-Location
Push-Location frontend/ai-banking
node scripts/verify-phone-auth-live.mjs
Pop-Location
```

`verify-phone-auth-live.mjs` 在真实 8088 服务创建独立模拟用户、完成实际手机号注册/登录、零余额开户确认及越权检查，不拦截业务响应；凭据只在进程内存。截图、检查记录位于 `frontend/ai-banking/test-results/phone-auth`。原 P6 浏览器脚本已适配登录入口，但其整套模型验收与本轮手机号验收分别记录，不能混用历史通过结果。

## 本轮实际验收（2026-10-16）

汇总：[acceptance.json](../../output/phone-auth/acceptance.json)。各数字由实际 JSONL/浏览器报告核对，父测试不重复计入叶子数。

| 范围 | 实际结果 |
| --- | --- |
| 全量 Go 单元 | 304 个叶子通过，0 失败、0 跳过；原始 [unit-final.jsonl](../../output/phone-auth/unit-final.jsonl) |
| MySQL / PostgreSQL 认证 | 每库服务 6 项、HTTP 3 项，共 18 项通过，0 失败、0 跳过；`output/phone-auth/{service,http}-{mysql,postgres}.jsonl` |
| 双数据库迁移 | 7 个叶子通过，0 失败、0 跳过；[migration-live.jsonl](../../output/phone-auth/migration-live.jsonl) |
| 金融核心回归 | MySQL、PG 分别 107 个叶子通过、0 失败；每套 6 个历史 Docker 专项跳过，未计为通过；`output/phone-auth/core-{mysql,postgres}.jsonl` |
| 前端单元 / 构建 | 58 项通过，8 个文件；TypeScript/Vite 构建通过，保留既有 ECharts 大包提示；[单元日志](../../output/phone-auth/frontend-unit.txt)、[构建日志](../../output/phone-auth/frontend-build.txt) |
| 前端契约浏览器 | `npm run test:browser`、规划生命周期 3 项、协同工作台 7 项通过；认证拦截仅用于这些显式测试夹具，不能代替真实集成 |
| 8088 实际浏览器 | 8 组全部通过：注册、Cookie/内存保管、Go/FastAPI 同一身份、独立金融确认、刷新恢复、错密/退出/重新登录、账户隔离、390px 手机界面；[真实报告](../../frontend/ai-banking/test-results/phone-auth/live.json)；没有响应拦截 |
| 部署与健康 | 25 项 Web 部署检查通过，Go 和产品服务 readyz 均 HTTP 200；[部署记录](../../output/phone-auth/web-verification.json) |
| 开发代理 | 5178 → 8090 无 Cookie 的同源 refresh 实际返回 401 `AUTH_INVALID_CREDENTIALS`，路由与 Origin 透传正常；[记录](../../output/phone-auth/readiness-go-and-vite.json)。临时 Vite 进程已停止，8088 仍运行 |
| 静态检查 | 相关 Go 包及两个 CLI 的 `go vet` exit 0；P6/旧 live 脚本语法检查通过，整套真实模型流程未重跑 |
| 原有数据保留 | 主库迁移前后均为 1225 用户、1272 账户、3352 交易、3982 账本分录，总余额 48240000 分，存储权威源 active；[迁移前](../../output/phone-auth/storage-before.json)、[迁移后](../../output/phone-auth/storage-after-migration.json)。随后真实浏览器仅新增独立模拟用户及显式确认的零余额账户 |

主 PostgreSQL 已实际升级 012，新 Go 和 Web 镜像 `:phone-auth` 已部署并健康，镜像摘要见汇总。原先包含环境清理的测试迁移包装被自动策略拒绝；新增执行前目标库校验并通过 6 项单元后，受限测试库迁移和主库迁移实际成功。worker 阶段的 `migration-acceptance.json` 保留当时待迁移状态，最终部署状态以总汇总和迁移后记录为准。

首次真实浏览器运行已通过前三组认证检查，随后因脚本未展开「模拟账户管理」折叠区而超时。修正选择步骤后完整 8 组通过，原失败报告保留为 `live-first-failed.json`；没有降低金融确认断言。桌面及手机截图已实际查看：[桌面登录](../../frontend/ai-banking/test-results/phone-auth/login-desktop.png)、[手机登录](../../frontend/ai-banking/test-results/phone-auth/login-mobile.png)。

本机 `-race` 因 CGO 未启用不能运行，没有计为通过。短信身份验证、密码找回/更改、手机号更换、多设备会话管理、生产 TLS/HA/灾备，以及旧完整 Coze/DeepSeek 模型流程的本轮重新验收尚未覆盖。

## 本次文件清单

下列路径相对各分组根目录；没有新增依赖或新建金融业务表。

| 根目录 | 文件 |
| --- | --- |
| `services/banking-service` | `PHONE-AUTH.md`；`api/openapi.yaml`；`scripts/generate_auth_openapi.py`；`cmd/server/main.go`；`cmd/migrate/main.go`、`guard.go`、`guard_test.go` |
| `services/banking-service/internal/config` | `config.go`、`userauth_test.go` |
| `services/banking-service/internal/httpapi` | `server.go`、`userauth.go`、`userauth_test.go`、`userauth_integration_test.go` |
| `services/banking-service/internal/identity` | `identity.go`、`session_test.go` |
| `services/banking-service/internal/userauth` | `password.go`、`limit.go`、`service.go`、`password_test.go`、`service_test.go`、`service_integration_test.go` |
| `services/banking-service/internal/migration` | `dialect.go`、`phone_auth.go`、`phone_auth_012_mysql.sql`、`phone_auth_012_postgres.sql`、`phone_auth_012.md`、`phone_auth_test.go`、`phone_auth_integration_test.go` |
| `frontend/ai-banking` | `src/App.vue`、`src/lib/store.ts`、`src/api/auth.ts`、`src/api/client.ts`、`src/api/agent-client.ts`、`src/api/schema.d.ts`；`src/api/agent-schema.d.ts` 再生成；`vite.config.ts`、`README.md`、`P6.md` |
| `frontend/ai-banking/src` | 登录提示文字：`components/ExecutionDialog.vue`、`components/Notifications.vue`、`components/State.vue`、`pages/Assistant.vue`、`pages/Overview.vue` |
| `frontend/ai-banking/tests` | `phone-auth.test.ts`、`token-renewal.test.ts`、`planning-lifecycle.mjs`、`workbench-contract.mjs` |
| `frontend/ai-banking/scripts` | `inspect.mjs`、`verify-business.mjs`、`verify-live.mjs`、`run-p6-live.py`、`verify-p6-live.mjs`、`verify-phone-auth-live.mjs` |
| `deploy` | `banking/compose.yaml`、`banking/compose.phone-auth.yaml`、`banking/README.md`、`banking-web/nginx.conf`、`banking-web/README.md`、`banking-postgres/README.md` |
| `docs/superpowers` | `specs/2026-10-16-phone-password-auth-design.md`、`plans/2026-10-16-phone-password-auth.md` |

日志、截图、构建产物位于忽略目录，不包含真实用户密码或认证 Cookie。工作区没有可用 Git 元数据，因此未声称创建提交。
