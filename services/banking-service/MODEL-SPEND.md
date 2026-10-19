# 项目模型金额预算与账单核对

2026-10-18，schema020。功能与模拟银行账户、金融授权、用户调用数/Token配额分开。**本轮仅在隔离环境用合成价格验证，未部署、未调用付费模型、未完成真实供应商账单对账。**

## 控制范围

`banking-model-gateway`是独立进程，Coze保留编排，供应商Key仅由出口持有。每次真正出站前，SQL事务锁定项目累计池，检查`used_units + held_units + 本次最坏预占 <= limit_units`，写attempt及追加审计并提交，成功后才发HTTP。多个用户、进程、循环及SDK重试共用一个项目池；累计额度不自动按日/月清零。

没有稳定键时每个HTTP请求独立占用。提供`X-Model-Request-ID`时，同键同原始请求字节可缓存重放，变参拒绝，未决请求不重新付费发出。未知、超时、断流及重启仍保留占用。业务成功只记录分项用量与运行估算，**完整金额预占保留到独立账单结算**。显式供应商零Token不同于缺失Token。

正式账单核对在同一事务释放预占、增加实际费用和写审计；跨操作员重复同账单不重复计费，新审核请求键可追加零金额绑定审计。供应商响应ID不能分配给两次调用。超出预占的真实债务完整记录并冻结池，不截断金额。

## 策略与证据

[策略空模板](../../deploy/banking/model-spend-policy.json.example)故意不能启动，没有默认币种、价格、上下文上界或额度。

|字段|定义|
|---|---|
|project_id / currency / model|固定项目池、单币种、唯一供应商模型；来自受保护配置，不从提示词推断|
|price_version / price_source / price_evidence_sha256|独立核对的价格/上界版本、证据引用、证据文件SHA256|
|valid_from / valid_until|RFC3339生效区间；未来/过期价格不能出站，历史核对可使用原策略|
|lifetime_limit|不自动重置的项目累计金额上限|
|input_price_per_million / cache_hit_price_per_million / output_price_per_million|币种主单位/每百万Token；输入输出价须大于零，缓存价不能高于输入最贵价|
|provider_context_tokens / max_output_tokens|核实的供应商输入/总上下文界、强制输出界|

金额按`1e-8`币种主单位的64位整数保存，输入为非负普通十进制、最多8位小数，拒绝指数/NaN/Infinity/空白/溢出。乘法使用精确大整数，预占/估算向上取整，不使用float64或隐含汇率。预占把整个上下文界按最贵输入价计算，再加输出上界费用，较为保守；字符数和字节数不能冒充准确Token上界。

除显式test环境的字面量回环模拟供应商外，命令启动须有`--price-evidence-file`及`--confirm-price-review`，文件摘要匹配，`synthetic:`来源不能授权付费出站。这是可追踪的**操作员核对门槛**，不是程序自动证明供应商价格/合同真实。独立核对必须包括思考Token、上下文/输出限制和所有收费项。

策略hash绑定持久化池。直接改价格、额度或有效期会拒绝匹配，不重置余额。**初版没有自动调价、增额或解冻接口**；后续需要受审计策略变更流程，不得用换project_id规避已有累计消费。

## 迁移、权限及启动

1. 备份目标库，由迁移身份执行当前migrate到020，再部署；本轮未迁移业务库。001–019 SQL/checksum兼容。新增`banking_model_spend_pools/attempts/events`，离线存储清单为52表。
2. PostgreSQL采用事务DDL；MySQL若020中断留下未登记新表，下次会拒绝自动接纳，须保留现场人工核对，不自动DROP/清空。
3. 普通Banking/Agent角色不授予金额表权限。PG迁移撤销新表默认继承给非owner的权限，再给专用gateway/操作员角色显式授予pool/attempt必要SELECT、INSERT、UPDATE及events SELECT、INSERT；gateway/操作员还需banking_schema_migrations只读，gateway另需banking_storage_control只读（不授UPDATE）。事件禁止UPDATE/DELETE，PG也禁止TRUNCATE。MySQL必须收敛schema ALL且无DROP/ALTER/TRIGGER，TRUNCATE不触发DML触发器。DBA/owner仍是可信管理边界。
4. 使用[网关私有环境模板](../../deploy/banking/model-gateway.env.example)，不加入操作员管理凭据。Coze只保存独立出口Token，不保存供应商Key；部署人限制文件ACL。
5. 生产入口要求TLS，数据库连接要求TLS（PG verify-full/MySQL true）。上游固定官方HTTPS，验证证书、拒绝重定向、不使用环境代理或不安全降级；只有test允许回环假供应商。

```text
banking-model-gateway --env-file <private-gateway-env> --policy-file <verified-policy> --expected-database <exact-db> --listen <private-interface:port> --tls-cert <cert> --tls-key <key> --price-evidence-file <reviewed-price-and-bounds-evidence> --confirm-price-review
```

启动只读核对实际数据库/schema/列/已有池策略，不自动迁移。`/readyz`检查本地策略、数据库/存储权威和池状态，不联网供应商，也不证明真实账单正确。

## Coze接入

[开发Compose overlay](../../deploy/banking/compose.model-spend.yaml)在既有compose及coze-local overlay后合并；私有配置、策略、价格证据只读挂载，不挂载操作员凭据。它使用显式开发HTTP，production会拒绝，需另外配置TLS。本轮没有build/up或替换容器。

```text
node deploy/banking/configure-coze-deepseek.mjs PrepareGateway
# 编辑忽略提交的.env.deepseek-gateway.local，填独立出口Token、允许origin及模型上限
node deploy/banking/configure-coze-deepseek.mjs VerifyGateway
node deploy/banking/configure-coze-deepseek.mjs Apply
```

`Apply`/`ApplyGateway`认证GET模型元数据，要求金额控制标记、项目/策略hash及输出上界，再更新Coze模型连接。保存到Coze的api_key是出口Token。开发主机侧验证可显式经127.0.0.1发布端口，写入Coze的仍是批准的内部地址；生产禁止这种地址替换。

旧直连必须显式选择`ApplyDirectUnbudgeted`，**未实施金额硬控**。必须停用Coze旧模型直连、fallback及插件中的供应商凭据，按需轮换旧Key，确认没有项目外程序/其他Key消费绕过出口。模型列表并不证明无部署旁路；没有现场证据不能关闭A-01部署风险。

只允许文本及普通函数工具定义/历史；网关不执行工具。不支持的字段、图片/文件、收费检索、思考切换等拒绝，不能静默直连。SSE采用**完整缓冲**（最多2MiB），最终用量/结束验证并持久化后才返回，不是实时逐Token转发，需验证真实工作流延迟及超时。

## 离线账单核对

[操作员配置模板](../../deploy/banking/model-spend-operator.env.example)不得进入普通服务容器。`BANKING_MODEL_SPEND_ADMIN_TOKEN`至少32字节且不得复用其他凭据；呈示值通过进程环境`BANKING_MODEL_SPEND_TOKEN`传入，不放命令行。鉴权和证据核查发生在SQL连接前，没有公开REST/Plugin/Agent核对入口。

```text
banking-model-spend-reconcile --env-file <private-operator-env> --policy-file <original-policy> --expected-database <exact-db> --list-pending
banking-model-spend-reconcile --env-file <private-operator-env> --policy-file <original-policy> --expected-database <exact-db> --bill-file <normalized-reviewed-bill> --evidence-file <independent-supplier-record> --confirm-bill-review
```

格式如下，空值仅作示意，不能作为有效对账：

```json
{
  "schema_version": 1,
  "project_id": "",
  "currency": "",
  "bill_id": "",
  "evidence_sha256": "",
  "rows": [{"line_id": "", "attempt_id": "", "provider_id": "", "actual_cost": "", "confirm_no_active_run": false}]
}
```

证据文件最多16MiB，JSON最多512KiB/100行。拒绝重复键、大小写别名、未知字段、尾随JSON、非法金额及币种/归属冲突。文件hash只证明文件身份，不自动证明金额与请求对应关系正确；操作员必须独立审核，只有周期总额不能编造逐请求账单。

**每行事务独立**，后行失败不回滚前行已提交记录。CLI逐行报告进度并以失败退出，同一审核文件可安全重试。pending最多100行，不返回模型响应/提示词，需运维处理时限，初版无分页。

reserved调用可能仍执行，独立确认停止后才可设`confirm_no_active_run=true`；超时不证明免费。初版只支持每attempt一次完整结算，**拒绝账单更正、供应商同请求分多行结算及重复归属**。后续更正须追加调整流程，禁止改历史或伪造新bill_id绕过。

## 尚未验收或实现的边界

- 专用Key、付费出口无旁路、旧凭据移除/轮换和真实Coze Generate/Stream部署兼容尚未验证。
- 真实价格、计价/思考上限、账单样本及实际对账缺少外部证据；合成账单不能关闭真实对账风险。
- 每用户金额归属未实现，需可信签名执行上下文；现有用户Token/调用数限制继续保护。
- 本版仅有项目累计池，没有自动日/月金额窗、汇率兑换、自动调价/增额/解冻或账单更正。
- 项目外消费或供应商违反已确认上界不受本地承诺覆盖；停止新请求不能撤销已发出的收费。超界会冻结并完整保留债务，不能泛称整个供应商账户绝不超额。
