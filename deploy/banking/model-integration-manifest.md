# Model integration manifest 契约

Stage12 增加显式、仅限隔离测试环境的 `integration_mode=direct_provider` 与精确请求摘要绑定，详见 [DIRECT-PROVIDER.md](DIRECT-PROVIDER.md)。以下工作流、Canvas 和受保护 scope 规则针对默认 `coze` 路径；直连路径不能携带这些权限，也不能声称已经通过 Coze 验收。

`model-integration-manifest.json.example` 仍为 schema_version=1 的非敏感配置模板。没有新增字段。所有工作流默认禁用是未配置状态；真实接入必须主动填写并启用 Supervisor。空模板应拒绝，不能作为可运行示例或审批。

普通预检及启动真实模式时必须显式提供 `--readiness-manifest-file`、`--gateway-origin` 和 `--workflow-policy-file`。显式 simulation/test/loopback 的已有隔离回归可不提供 manifest；若提供也必须校验。真实模式不继承该例外。

顺序为：新审批与独立审批摘要 → 精确计划/独立执行确认 → 非敏感 Policy/manifest/目标数据库/工作流集合校验 → 显式受保护配置 → 现有价格审核、身份、SQL 账本、权限和 Coze scope 门禁。缺少计划确认仍在配置读取前阻断。manifest 不能创建任何授权、预占、账本记录或金融意向。

离线与普通入口共用本地校验逻辑。离线结果至多 PARTIAL；普通预检继续执行既有独立审核，READY_SNAPSHOT_ONLY 也不授予执行权。CANDIDATE_NO_EXECUTION 仅供已独立确认的普通预检使用，不能启动调度；新 READY 文件须另行确认精确摘要。

`--prepare-execution-plan` 使用同一组本地校验器及显式新审批、Policy、manifest、验收说明输出未授权候选；不生成确认、不读取凭据、不连 SQL 或外部服务。它不改变 `--offline-preflight` 的行为。完整操作及精确字节保存要求见 [REAL-MODEL-RUNBOOK.md](REAL-MODEL-RUNBOOK.md)。

| 字段 | 契约与真实作用 |
|---|---|
| supplier / model_version | 必填描述元数据；不证明供应商身份或不可变后台修订。实际身份绑定 model、supplier_origin、返回模型及既有审批；真实 origin 白名单不变 |
| mode / supplier_origin / model | 与 Approval.simulation/provider_origin/model 一致；真实模式只支持既有固定供应商 origin，模拟仅显式测试环境及 loopback |
| workflows.* | enabled 必须显式布尔值；Supervisor 必须启用并对应 Approval 主 pin；专业角色可禁用，但禁用时不得带 ID/version/hash；启用必须绑定已发布版本声明与精确 Canvas SHA256 |
| 启用角色集合 | distinct workflow ID，避免角色别名使禁用含义不明确；集合和哈希必须与主工作流+additional_workflows、实际 workflow-policy-file 完全一致，不允许超集或子集；运行实例冻结已校验集合，不热加载 manifest 来增加授权 |
| price_version / price_source / price_evidence_sha256 | 匹配 Policy；Approval.policy_sha256 已覆盖完整 Policy（包含输入、缓存命中、输出价格、上下文和有效期），不新增重复价格 ID/摘要字段；摘要与文件存在不证明官方真实性 |
| budget_amount / currency | 正十进制字符串，精确到 1e-8，不接受数值类型、指数、负数或隐式币种；与审批一致；Policy 同币种，审批额度不超过累计策略额度且足以覆盖保守单次预占 |
| max_requests / max_concurrent / user_id | 正 int64，拒绝缺失、0、负数、小数、错误类型及溢出；并发不超过请求数。不另引入任意历史不兼容上限，实际金额与持久计数共同限制次数，0 从不表示无限 |
| context_tokens / max_output_tokens | 正整数，与 Approval/Policy 精确一致；Policy 上下文≤10^9，输出≤上下文；Coze 路径复用 bankingscope.MaxConfiguredOutputTokens=4096。网关继续限制实际 max_tokens、usage 和原子预占 |
| user_id / task_request_sha256 / database / data_scope | 与审批一致；data_scope 映射 Approval.scope；账户所有权、任务租约和数据权限仍由 Go、持久任务与隔离 SQL 确定 |
| valid_from / valid_until | 带时区 RFC3339，起止有序，有效区间 [from, until)。声明与审批精确相等，Policy 自身也必须有效；不从默认值补齐 |
| external_evidence.* | kind/reference 仅为引用；publication/permission/egress/price/usage/billing 不因字段或哈希存在升级 PASS。核验人、时间、独立证据放材料清单，不扩展本 schema |

## Origin 与出口

`supplier_origin` 是现有供应商的 `/v1` API base；`coze_origin` 是发布/运行 Coze 的服务 origin；`gateway_origin` 是受保护 scope 宣告的费用网关 origin，须与 CLI 一致。真实模式这些地址必须为 HTTPS；主机采用小写规范拼写，端口须有效，不接受空端口、用户信息、查询、片段（包括空片段）、编码路径、尾斜线、通配符或不支持协议。supplier 仅可有 `/v1` 路径，其余无路径。不同端口拼写视为不同精确声明，不默认为等价并扩大匹配。

`allowed_origins` 是这三段模型调用链的**精确去重 origin 集合**（supplier 去掉 `/v1`），不是 CORS，也不是 HTTP 例外配置。只允许上述声明的地址，不允许额外目标。manifest.coze_origin 在实际 scope 请求前与受保护配置中的 Coze origin 比对；gateway_origin 和模型/输出 token/pins 继续与实际 scope 比对。工作流 ID/哈希仍在 Coze 按原始发布 Canvas 核验。

应用传输仍使用既有 outbound.Policy、固定 supplier、禁代理与禁重定向，Coze transport 固定网关及路径；身份/任务服务有独立既有配置，不由这个三段集合授予权限。防火墙、DNS/固定出口和容器网络隔离由部署层强制；manifest 无法证明部署出口真实有效，也不能修改其权限。

## 未核验状态及兼容性

离线真实模式不显示未审核价格计算的真实单次风险；相关字段省略，余额/剩余请求/并发为 null。隔离 simulation 可显示明确标记的声明价格计算值，不是官方费用。既有账本 estimated/reserved/reported/unknown 与 settled 区分不变。

schema 及 Go/Python 业务 DTO、OpenAPI、Vue 生成类型未变。兼容性收紧包括：真实入口必须显式提供 manifest；专业 enabled 不允许缺省/null；无效 origin、额外 origin、角色共享 ID、与实际授权 pin 集合不一致均拒绝。旧材料若不符合应重新审核绑定，不能扩充审批或自动生成确认来迁就。

实际金融工具白名单与资金 prepare→authorize→execute 不变。禁用专业 Coze 解释工作流不会取消确定性只读金融工具；确定性工具权限继续由既有业务契约控制。

第五/六阶段的交接模板副本是历史快照，不能据此推断当前模板默认值或当前制品身份。当前运行容器本轮未替换，工作区修复尚未部署。真实配置、发布、权限、价格及官方账单仍缺失；无真实调用或正式对账。
