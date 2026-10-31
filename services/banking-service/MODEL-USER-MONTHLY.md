# 用户月度模型金额额度（schema021）

2026-10-20 增量：已实现受限任务能力、Coze 输入/Canvas 校验、实时任务租约验证和 Python 后台调度。隔离网关实际调用供应商一次，原预占保留，实际账单金额仍为空。完整 Coze 成功链路及部署出口隔离尚未验收，未部署新生产镜像。证据见 [本轮记录](../../docs/ai-closure-20261020.md)；[上一轮记录](../../docs/core-remediation-20261019.md)保留历史范围。

## 身份和持久化

`POST /v1/chat/completions` 同时要求独立网关 Bearer 和 `X-Banking-Authorization: Bearer <Banking用户或受限Agent令牌>`。网关向固定 `BANKING_MODEL_IDENTITY_BASE_URL` 的 `/api/v1/context`、`/api/v1/users/me` 重新验证令牌、归属和 active 状态，不持有银行 JWT 签名密钥。不接受 body/user_id、OpenAI user 字段或 X-User-ID 作为身份。用户停用后缓存重放也拒绝。银行凭据不转发供应商。

生产/开发网关未配置身份校验器时拒绝启动。协议测试例外只允许显式 test 环境及字面量回环供应商，不能用于官方付费出口。

新增表：`banking_model_user_limits` 保存每用户独立金额限额和币种；`banking_model_user_windows` 保存月份、限额快照、已独立结算金额、未决预占、冻结状态；`banking_model_attempt_owners` 将调用不可修改地绑定本人及发起月份。限额和归属表禁止修改/删除，普通用户没有增额入口。

金额为 1e-8 币种主单位的 int64，与银行整数分及 Token 数量分开。沿用固定项目、单币种、单模型价格策略，不隐含汇率。每次按审核价格、上下文和输出上界预占最坏金额，事务先锁项目池，读取不可修改的用户额度，再锁月窗口；两级额度均满足才提交两个预占、调用、归属和审计。提交之后才发供应商请求，比较使用减法防溢出。

请求键按用户隔离。同用户同键同原始请求只重放保存结果；变参冲突；未知结果不重复出站。成功响应的 Token 和内部费用估算不释放金额预占，不冒充供应商账单；失败、取消、超时、缺用量和进程退出也保留。独立账单导入在同一事务释放两级原预占并增加实际费用；重复账单不重复计费，超界债务完整保留并冻结。审计保存策略/模型/价格版本、网关实际供应商 origin、项目及用户前后余额；结算保存证据摘要。

月份使用 `Asia/Shanghai`，不依赖主机时区。新月新建窗口，不删除旧窗口和未知费用；迟到结算仍归发起月份。月度重置不解除项目池冻结。额度暂不提供增额、自动调价或解冻接口；不能更换 project_id 规避累计消费。

`GET /v1/budget` 通过相同认证返回本人当前月份的 limit/settled/reserved 十进制金额、币种、时区、冻结状态。未配置额度则拒绝，不自动发放默认额度。网关 token 不得进入浏览器。

## 配置与操作

先备份，由迁移角色执行至021；历史001–020 checksum不变。本轮仅迁移新建隔离测试库。网关专用角色需 limits SELECT、windows SELECT/INSERT/UPDATE、owners SELECT/INSERT 及原项目账本权限；普通 Banking/Agent 角色不应获得金额表权限。额度操作员另需 limits INSERT，不授 UPDATE/DELETE/TRUNCATE/DDL。PostgreSQL迁移撤销新增表PUBLIC/默认授权，并安装冻结与不可修改触发器；owner/DBA仍是可信管理边界。MySQL须额外收敛DDL权限，不能依赖DML触发器禁止TRUNCATE。

受保护网关环境文件新增：

```text
BANKING_MODEL_IDENTITY_BASE_URL=https://<banking-private-origin>
```

生产需HTTPS和数据库verify-full；开发HTTP必须显式允许origin。沿用 `MODEL-SPEND.md` 的真实价格/上界证据、供应商密钥、独立网关token及启动命令。操作员通过进程环境 `BANKING_MODEL_SPEND_TOKEN` 提供管理员令牌，不放命令行：

```text
banking-model-user-limit --env-file <protected-operator-env> --policy-file <reviewed-policy> --expected-database <exact-db> --user <operator-verified-user-id> --monthly-limit <ordinary-decimal>
```

相同额度重复配置不重置消费，变更额度拒绝。

## 尚未闭环

可信桥接已实现：Go 验证用户及会话，Python SQL 返回真实任务版本/请求摘要/租约指纹，网关签发短期不透明能力；Coze 校验实际输入和已发布 Canvas，只允许单模型图。模型请求前仍需事务额度预占。未配置桥接、缺任务证明、旧 Coze runtime、未审核工作流、异步工作流均继续拒绝。完整 Coze 实例尚未实际部署验收，不能仅设置 URL 就宣布已接通。

部署脚本拒绝 `ApplyDirectUnbudgeted`，并要求网关元数据表明启用用户月度身份验证。模型列表不能证明Coze传播了逐调用身份。旧实例、直连密钥、插件/fallback、其他模型出口仍需现场审计、移除/轮换；本轮没有现场部署无旁路证据。

已保留真实调用时审核的官方价格快照，该短期策略已经过期，不能用于继续付费。正式逐请求账单仍缺失，真实对账保持 BLOCKED。Python 新增默认关闭的持久后台调度，独立库已验证自动领取、重试、租约恢复及真实进程重启；执行结果未知时中断，不自动重放。详见本轮记录，schema021跨库导入仍未验证。
