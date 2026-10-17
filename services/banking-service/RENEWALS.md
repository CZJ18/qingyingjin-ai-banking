# 主动续费提醒

入口：网页「订阅与代扣」→「续费提醒」，右上角铃铛显示未读通知。Go 服务启动时立即扫描，此后每分钟扫描；网页在前台每 30 秒查询。关闭网页不会停止服务端生成提醒，下次登录仍可看到。服务停机期间没有定时执行，恢复后仅补仍未过期且处于提醒窗口内的订阅。

默认开启、提前 3 天，可设置 0–30 天或关闭。按 Asia/Shanghai 自然日覆盖到目标日结束；0 天只覆盖今天剩余时间。仅对已确认 active、模拟、CNY、金额有效且预计扣费时间未过的订阅生成，不把识别候选当作真实订阅。提醒中的金额单位为整数分，时间为 Unix 毫秒，订阅 ID 为十进制字符串。

这项功能只提醒：不自动续费、扣款、转账、取消订阅，不发送短信、邮件或操作系统通知。取消订阅继续使用现有可信执行网关，用户明确授权后才能执行本地模拟取消。未来预计节约金额不记入余额。

## 持久化、并发与边界

迁移 015 在 banking_users 增加可空 renewal_reminders：PostgreSQL TEXT、MySQL MEDIUMTEXT。保存版本化设置和有 UUID/版本号的通知，旧用户 NULL 读取默认设置。采用有界用户聚合，复用用户行锁、46 表迁移快照和数据库写围栏，不新增第二套通知数据库。

同一订阅与预计扣费时间只生成一次；多进程后台扫描通过数据库用户行锁串行更新。价格或名称变更更新原通知并重新置为未读；旧版本确认返回 VERSION_CONFLICT。已读请求重试幂等。取消、改期、关闭设置、缩小窗口后撤回旧通知，扣费时间过后标记过期。订阅源变化通常在下一次扫描反映；保存设置时立即核对源数据。

每用户保留至多 200 条通知，清理预计扣费时间已过去 30 天的记录；容量不足时明确返回 capacity_limited，不伪装全部生成成功。单用户最多扫描 1000 条订阅，超过返回 STATE_CONFLICT。后台按用户 ID 分批扫描，单用户超时 3 秒、单轮 45 秒；单用户错误不会阻断后续用户，日志只记录 renewal_scan_failed，不打印商户、手机号或通知内容。本实现适合当前本地比赛规模，尚未进行大规模容量和通知时效 SLA 验收。

## API 契约（OpenAPI 0.18.0）

仅本人用户 Bearer 可用，拒绝 Agent；从已验证身份确定 user_id，不接受客户端指定用户。GET 只查询已存通知，不触发生成。偏好设置和标记已读不属于金融执行，不需要额外金融授权。

| 方法 | 路径 | 请求 |
| --- | --- | --- |
| GET | /api/v1/notifications/renewals | 无 |
| PUT | /api/v1/notifications/renewals/settings | enabled、lead_days、expected_version（设置版本） |
| POST | /api/v1/notifications/renewals/{id}/read | expected_version（通知版本） |

返回统一 Envelope，data 包含 settings、items、unread_count、last_checked_at、capacity_limited。通知包含 id、subscription_id、merchant_name、amount_minor、currency、charge_at、status、reason、version、read_at、created_at、updated_at。状态为 unread/read/withdrawn/expired。reason 为 subscription_changed、billing_time_passed、disabled、outside_window 或空串。

常见错误：INVALID_ARGUMENT=400，UNAUTHORIZED=401，FORBIDDEN=403，NOT_FOUND=404，VERSION_CONFLICT/STATE_CONFLICT=409，DEPENDENCY_UNAVAILABLE=503。无金融 Tool 或公开扫描全部用户的 API。

curl 示例（TOKEN 为登录所得的临时用户令牌；NOTICE_ID/版本必须取自查询结果）：

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8088/api/v1/notifications/renewals
curl -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"lead_days":3,"expected_version":1}' \
  http://127.0.0.1:8088/api/v1/notifications/renewals/settings
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"expected_version":1}' \
  "http://127.0.0.1:8088/api/v1/notifications/renewals/$NOTICE_ID/read"
```

## 升级与启动

先完成双数据库测试。当前本机权威金融库为 PostgreSQL；停止 Go 写服务，用已有私有迁移配置运行下列命令，核对数据未变，再加载新镜像。旧历史 MySQL 保持冻结，不对其执行迁移。

```powershell
Set-Location services/banking-service
go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking-postgres/finance.migrate.env.local -expected-database qingrongjin_finance
```

Go 与网页镜像标签均为 renewal-reminders。Go 在原有 Compose 链末尾追加 deploy/banking/compose.renewals.yaml；网页在原链末尾追加 deploy/banking-web/compose.renewals.yaml。两份 overlay 不含凭据；后台扫描复用原数据库配置，没有新密钥或外部发送服务。构建方式复用两个 Build-Image.ps1，运行验收和完整 Compose 命令见交接验收记录。

测试代码：internal/reminders/model_test.go、service_integration_test.go、internal/httpapi/reminders_integration_test.go、internal/migration/renewals_test.go；前端 tests/renewals.test.ts 和 scripts/verify-renewals-live.mjs。后者只允许 localhost，在新建零余额模拟用户下通过操作员 SQL 初始化一条订阅测试夹具，其余提醒、设置、已读及取消使用实际接口，无响应拦截，不可当作真实商户集成。
