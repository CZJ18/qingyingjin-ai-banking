# 手机号验证与密码找回：本地开发短信

本轮依照用户最新要求完成验证码和找回密码功能，不实际接入腾讯云、不发送真实短信。未来真实通道选腾讯云；当前没有云 SDK、云密钥或空的腾讯云接口实现。

默认 `BANKING_SMS_PROVIDER=disabled`。本地显式 `development` 时，发送验证码只写入 AES-GCM 加密的 Redis 开发收件箱，5 分钟删除；验证码不出现在 HTTP、服务日志、浏览器持久化存储或 Agent Tool。需要持有本机私有配置，并显式运行 CLI 才能读取开发码。界面和用户记录都明确显示“开发验证（非真实手机号认证）”。

## 完整业务流程

- 注册：输入手机号和密码 → 获取注册验证码 → 读取开发收件箱 → 输入验证码 → 创建独立用户。启用验证后，缺码、错码、旧码或已用码均拒绝注册。
- 已有账号：仍可用原手机号/密码登录 → 账户资料 → 当前密码 + 本人号码验证码 → 记录当前验证方式。不能填别人的 user_id、换号码或由 Agent 代办。
- 忘记密码：手机号 → 找回用途验证码 → 新密码及确认 → SQL 事务更新散列并撤销旧 Access/Refresh → 返回登录页，用新密码重新登录。不会自动登录或执行金融操作。
- 历史未验证号码：仅凭短信找回不能认领旧账号。必须先使用原密码登录并补验证。未知、未验证、已验证号码的发码返回统一结构，只有绑定有效用户/版本/验证方式的 challenge 可实际重置。

密码重置也使重置前签发的无版本用户 JWT 失效；JWT 的 iat 为秒，保守拒绝重置同一秒内签发的此类旧格式令牌。下一秒签发的可信操作员令牌可用。Agent 的任务身份仍按原 READ/PLAN/PREPARE 权限校验，不增加金融执行权。

## 验证码与一致性

6 位密码学随机数字、随机 256 位 challenge ID、5 分钟有效、60 秒手机号发送间隔。Redis 中验证码仅存独立密钥的 HMAC 摘要，绑定手机号、用途与 challenge；已有用户绑定 user ID 和 auth_version。三个用途 `register`、`verify_phone`、`password_reset` 不能串用。

发码限额：手机号每小时 5 次、每天 10 次；IP 每小时 20 次、每天 100 次；安装级每天 1000 次。校验限额：每 challenge 最多 5 次、手机号 15 分钟 20 次、IP 15 分钟 40 次。Redis Lua 原子预留限额及创建 pending，发送器成功才 active；发送异常不假装成功。当前 Nginx 后端禁用可信代理，所以全部网页用户共享代理 IP 限额；这是本地开发限制，生产应明确可信代理边界，不能直接信任任意 X-Forwarded-For。

Lua 单次消费后，SQL 事务锁行再次检查手机号、账号状态、版本及验证方式。更新密码、版本、刷新会话和变更时间在同一事务内完成。Redis 与 SQL 不是跨系统原子事务：SQL 失败时密码回滚，验证码仍保持已消费，需重新申请，避免提交状态不确定时重放。

013 在 `banking_users` 增加 `phone_verified_at BIGINT`、`password_changed_at BIGINT`、`phone_verification_method VARCHAR(16)`，NOT NULL，默认 0/0/空字符串，历史账号不会自动变成已验证。仍为 46 张业务表，001–012 的历史校验和未修改。字段和迁移说明见 [sms_auth_013.md](internal/migration/sms_auth_013.md)。生产真实 provider 不得接受 `development` 的历史验证结果。

## API

完整 [OpenAPI 0.16.0](api/openapi.yaml)。这些身份接口不进入 Coze Plugin，Agent 没有验证码收件箱或密码重置工具。

| 方法与路径 | JSON 输入 | 成功结果 |
| --- | --- | --- |
| `GET /api/v1/auth/capabilities` | 无 | `sms_enabled,sms_mode,registration_verification_required,code_expires_in,resend_after` |
| `POST /api/v1/auth/sms/registration` | `phone` | `challenge_id,expires_in,retry_after` |
| `POST /api/v1/auth/sms/password-reset` | `phone` | 同上，无论账号是否存在、是否验证 |
| `POST /api/v1/auth/sms/phone-verification` | `{}` + 本人 Bearer | 同上，仅本人绑定号码 |
| `POST /api/v1/auth/phone/verify` | `challenge_id,code,current_password` + 本人 Bearer | `data:null`，不切换账号 |
| `POST /api/v1/auth/password/reset` | `phone,challenge_id,code,new_password` | `data:null`，清刷新 Cookie，要求重新登录 |
| 原 `POST /api/v1/auth/register` | 原输入另加 `challenge_id,code` | 会话增加 `phone_verified,phone_verification_method` |

认证 POST 保留精确 Origin（协议/host/端口）、JSON-only、4096 字节限制及 no-store。错误使用既有 Envelope：`AUTH_CODE_INVALID` 401、`AUTH_RATE_LIMITED` 429、`SMS_UNAVAILABLE`/`DEPENDENCY_UNAVAILABLE`/`STORAGE_FROZEN` 503；密码输入沿用 `INVALID_ARGUMENT` 400。错误不输出供应商/Redis/数据库原始内容或任何秘密。

```shell
curl http://127.0.0.1:8088/api/v1/auth/capabilities
curl http://127.0.0.1:8088/api/v1/auth/sms/registration \
  -H 'Origin: http://127.0.0.1:8088' -H 'Content-Type: application/json' \
  -d '{"phone":"<自己的开发测试手机号>"}'
# 用本机收件箱取得开发验证码后，提交对应 challenge。
curl http://127.0.0.1:8088/api/v1/auth/password/reset \
  -H 'Origin: http://127.0.0.1:8088' -H 'Content-Type: application/json' \
  -d '{"phone":"<手机号>","challenge_id":"<找回challenge>","code":"<一次性验证码>","new_password":"<自行设置的新密码>"}'
```

## 本地配置与运行

在仓库根执行。私有 `sms.env.local` 仅含开发模式、独立随机密钥及 loopback HTTP 标记，已忽略并限制当前用户/SYSTEM 访问。重复 Setup 不替换已有密钥，不修改云配置。

```powershell
powershell -NoProfile -File deploy/banking/Setup-SMSDevelopment.ps1
powershell -NoProfile -File deploy/banking/Build-Image.ps1 -LocalBinaries -ImageTag qingyingjin-banking-service:sms-auth -RuntimeBaseImage qingyingjin-banking-service:phone-auth
powershell -NoProfile -File deploy/banking-web/Build-Image.ps1 -LocalDist -ImageTag qingrongjin-banking-web:sms-auth -NginxRuntimeImage qingrongjin-banking-web:phone-auth

# 显式维护窗口，仅升级当前金融 PostgreSQL，不动冻结历史 MySQL。
docker stop qingyingjin-banking-service-1
Push-Location services/banking-service
go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking-postgres/finance.migrate.env.local -expected-database qingrongjin_finance
Pop-Location
docker compose --env-file deploy/banking-postgres/finance.compose.env.local -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml -f deploy/banking/compose.coze-local.yaml -f deploy/banking-postgres/compose.yaml -f deploy/banking/compose.phone-auth.yaml -f deploy/banking/compose.sms-development.yaml up -d --no-deps --no-build banking-service
$env:BANKING_WEB_IMAGE='qingrongjin-banking-web:sms-auth'
docker compose -f deploy/banking-web/compose.yaml up -d --no-deps --no-build banking-web
```

网页点击「发送验证码」后，在自己的终端读取：

```powershell
# 仓库根目录的一条命令；用途：注册 register，资料验证 verify_phone，找回 password_reset。
powershell -NoProfile -File deploy/banking/Read-SMSDevelopment.ps1 -Phone '<测试手机号>' -Purpose register

# 或直接运行 Go CLI：
Set-Location -LiteralPath 'D:/智慧农业/services/banking-service'
go run ./cmd/sms-inbox -env-file ../../deploy/banking-postgres/finance.app.env.local -env-file ../../deploy/banking/sms.env.local -phone '<测试手机号>' -purpose register -allow-development-code
# 找回密码用 -purpose password_reset；账户资料补验证用 -purpose verify_phone。
```

CLI 只连接 Redis，不修改 SQL；开发码输出属于短期秘密，不应复制到聊天、提交或放入普通日志。普通网页无法读取收件箱。禁用开发模式时删除本地 overlay，明确设置 provider disabled；旧 development 结果不能当作真实腾讯云验证。腾讯云正式接入及手机送达验收留待未来提供真实通道配置后完成。

## 验收记录

2026-10-16 最终汇总：[acceptance.json](../../output/sms-auth/acceptance.json)。测试数以叶子用例计，父测试不重复计入。Go 与 Web `:sms-auth` 镜像已实际部署，均 healthy；Go/产品 readyz 均 HTTP 200，运行中的 OpenAPI 已核验为最终 0.16.0 空响应契约。

| 范围 | 实际执行结果与证据 |
| --- | --- |
| 全量 Go 单元 | 331 叶子通过，0 失败、0 跳过；[unit-final.jsonl](../../output/sms-auth/unit-final.jsonl) |
| SMS 服务与真实 HTTP | MySQL、PostgreSQL 各服务 9 叶子+HTTP 3 项，共 24 通过，0 失败、0 跳过；`output/sms-auth/{service,http}-{mysql,postgres}.jsonl` |
| HTTP 契约 | 所有 SMS 实际请求/响应验证 OpenAPI；验证、重置、退出成功均强制检查 `data` 键存在且为 null；校验 Origin、Agent 拒绝、未知/未验证号码响应一致及响应/日志无验证码 |
| 验证码 Redis | 3 项真实集成通过，另 2 项单元通过；[verifier-live.jsonl](../../output/sms-auth/verifier-live.jsonl)。覆盖并发单次消费、目的隔离、旧码/过期/重发拒绝、五次上限、发送失败、Redis 故障 |
| 加密开发收件箱 | 3 项真实 Redis 集成通过；[provider-integration.txt](../../output/sms-auth/provider-integration.txt)。覆盖重建适配器后持久化、用途/密文绑定、篡改/过期拒绝及取消请求；没有外发短信 |
| 013 双数据库迁移 | 6 个叶子全部通过，0 跳过；[migration-live.txt](../../output/sms-auth/migration-live.txt)。并发、历史用户保留、历史 checksum、46 表、PG 回滚、MySQL 断点重入和字段不兼容拒绝 |
| 既有金融核心 | MySQL / PG 各 107 叶子通过，0 失败；每套 6 项历史 Docker 专项跳过，未计为通过；`core-{mysql,postgres}.jsonl` |
| 前端 | 最新 68 项单元通过；OpenAPI 类型生成、TypeScript/Vite 正式构建通过；保留既有 ECharts 大块提示。日志 `frontend-{api-generate,unit,build}.txt` |
| 测试夹具浏览器 | 原八页面与金融确认契约、规划 3 项、协同 7 项、新短信 5 组通过；明确使用测试拦截，不作为实际业务发送证明；`frontend-*-fixtures.txt` |
| 真实 8088 浏览器 | 5 组全部通过、无页面错误、无响应拦截：[live/acceptance.json](../../output/sms-auth/live/acceptance.json)。通过真实 CLI 读取加密开发码，实际尊重 60 秒发送间隔；注册、旧用户当前密码补验证、找回、旧 Access/无版本用户 JWT 撤销、旧密码拒绝、新密码登录/刷新、localStorage 无秘密及 390px 无溢出均已执行 |
| 部署与检查 | 最终 Web 25 项通过；[web-verification-final.json](../../output/sms-auth/web-verification-final.json)。相关 Go 包及 CLI `go vet` exit 0；两个 PowerShell 脚本语法解析、合并 Compose 验证通过 |
| 原金融数据 | 013 升级前后 1228 用户、1273 账户、3352 交易、3982 账本分录、余额总计 48240000 分及 active 状态完全保留；[storage-before.json](../../output/sms-auth/storage-before.json)、[storage-after.json](../../output/sms-auth/storage-after.json)。随后真实浏览器仅新增独立模拟用户，不开户或产生资金 |

桌面与手机截图已实际查看：[注册弹窗](../../output/sms-auth/live/registration-desktop.png)、[手机账户资料](../../output/sms-auth/live/profile-mobile.png)。临时 Vite 5178 已停止；8088、8090、8091 保持运行。

实际验收发现并修复了两个问题：`Envelope.data,omitempty` 省略了空成功字段，导致后端退出已成功但网页误报失败；现仅三个认证成功接口明确返回 `data:null`。OpenAPI 的 `allOf` 继承了不可空的旧通用 `data:{}`，现认证空响应使用独立公共 Envelope 加必需 null 字段，并有先 RED 后 GREEN 的 API 单测。未全局更改 Envelope 或金融业务。

首次 Docker 未运行、测试 EXPIRE 毫秒误用、HTTP 夹具 Host/手机号冲突，以及真实浏览器发现 null 响应的失败证据均保留，未混入最终通过。Redis 过期夹具已改用毫秒 PEXPIRE，HTTP 夹具修复后保持原权限和限流规则。

尚未覆盖：按用户要求暂不连接腾讯云或验收手机送达；未做短信快捷登录、手机号更换、生产 TLS/HA/灾备、旧完整 Coze/DeepSeek 模型全链路复验。现有共享代理 IP 限流和本机 CGO 未启用而不能执行 `-race` 的限制如实保留，不能据此声称生产身份系统已验收。

## 本次实际文件清单

| 目录 | 新增或修改文件 |
| --- | --- |
| `services/banking-service` | `SMS-AUTH.md`、`PHONE-AUTH.md`；`api/openapi.yaml`、`api/auth_null_test.go`；`scripts/generate_sms_openapi.py`；`cmd/server/main.go`、`cmd/sms-inbox/main.go` |
| `internal/config` | `config.go`、`sms.go`、`sms_test.go` |
| `internal/smsprovider` | `development.go`、`development_test.go`、`development_integration_test.go` |
| `internal/smsverify` | `verify.go`、`verify_test.go`、`verify_integration_test.go` |
| `internal/userauth` | `service.go`、`verification.go`、`verification_test.go`、`verification_integration_test.go` |
| `internal/identity` | `identity.go`、`legacy_reset_test.go` |
| `internal/httpapi` | `userauth.go`、`userauth_test.go`、`auth_null_test.go`、`sms_integration_test.go` |
| `internal/migration` | `dialect.go`、`phone_auth_test.go`（版本测试兼容后续升级）、`sms_auth.go`、`sms_auth_013_mysql.sql`、`sms_auth_013_postgres.sql`、`sms_auth_013.md`、`sms_auth_test.go`、`sms_auth_integration_test.go` |
| `frontend/ai-banking` | `src/App.vue`、`src/api/auth.ts`、`src/lib/store.ts`；生成 `src/api/schema.d.ts` / `agent-schema.d.ts`；`README.md` |
| 前端测试/脚本 | `tests/sms-auth.test.ts`、`tests/sms-auth-browser.mjs`、`tests/planning-lifecycle.mjs`、`tests/workbench-contract.mjs`、`scripts/inspect.mjs`、`scripts/verify-business.mjs`、`scripts/verify-sms-auth-live.mjs`；旧夹具仅补 capabilities/Session 字段 |
| `deploy/banking` | `compose.sms-development.yaml`、`Setup-SMSDevelopment.ps1`、`Read-SMSDevelopment.ps1`、`README.md`；新私有 `sms.env.local` 被忽略、受 ACL 限制 |
| 其他 | `.gitignore`、`deploy/banking-web/README.md`、`deploy/banking-postgres/README.md`、`docs/superpowers/specs/2026-10-16-sms-password-recovery-design.md`、`docs/superpowers/plans/2026-10-16-sms-password-recovery.md` |

`internal/*` 路径相对 Go 服务根目录。没有新增 Go/Node 依赖、没有云 SDK、没有改 Coze 核心或新增金融业务表。工作区无可用 Git 元数据，未声称创建提交。
