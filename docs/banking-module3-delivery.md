# 模块3 Goal-to-Banking 交付与验证

2026-10-08。独立 Banking Service 的目标编译后端、版本管理、Schema、安全任务编译、MySQL迁移和REST API已实现，Docker镜像已运行。当前没有可用Coze实例/PAT/已发布解析workflow_id，因此**真实模型端到端联调未完成**；目标解析不会用固定结果或规则解析冒充模型调用。

完整目录、依赖、Coze逐节点配置、启动命令、curl、状态/错误和后续对接规范见 [MODULE3.md](../services/banking-service/MODULE3.md)，总体设计见 [设计](superpowers/specs/2026-10-08-goal-compiler-design.md)，API契约见 [OpenAPI](../services/banking-service/api/openapi.yaml)。

## 实际实现

| 项目 | 实现 |
|---|---|
| 自然语言接口 | 使用本仓库实际Coze同步workflow/run协议，Start→LLM→End；Prompt/节点配置/原生画布参考生成器 |
| 结构化校验 | Draft2020-12 Schema；严格JSON对象/未知字段/整数金额；周期、数值、金融含义与原文证据检查；拒绝代码围栏、模型金融任务、局部数字匹配 |
| Entity | Goal、GoalConstraint、GoalVersion、TaskPlan、TaskDependency；解析事实/上下文/任务/冲突/替代方案DTO |
| 编译 | 六类固定任务与依赖、参数/权限/模式允许清单、查询/分析/建议模式；未解析或有冲突时不执行金融业务 |
| 预算安全 | 复用Core预算只读接口；完整生活预算和必要预留优先；不把余额当月收入；不削减必要开支，不生成体重或限制饮食金融奖惩 |
| 冲突 | 原因、可选调整方案、requires_confirmation；保持原始目标；缺信息/不支持或模糊信息要求澄清 |
| 版本 | 原始目标创建、乐观更新、历史、字段对比；成功版本解析/计划不可重写；解析与更新并发不会覆盖新版本 |
| 持久化 | MySQL5张新业务表、用户复合外键、唯一版本计划、持久化解析claim；计划/约束/依赖/审计原子落库 |
| API | 新增7个方法；合计34个路径/38个方法；仅用户JWT可调用目标接口，工具凭证不能冒充用户 |
| 部署 | 环境变量、Compose、002增量迁移、模块3镜像、原始示例目标初始化脚本、健康与重启验证 |

任务包含task_id、task_type、dependencies、parameters、required_permissions、execution_mode、status。pending_confirmation仍固定financial_execution_enabled=false。propose_financial_operation只写计划节点，没有调用CreateTransfer或其他资金入口，也未创建订阅取消操作。

模型只接收原始目标text。Go根据用户明确提供的financial_context消解已知字段的澄清请求，防止同一信息被重复询问。明确总目标可生成需确认的月度建议（整数分向上取整）；明确月额、周期、总额不一致时返回冲突，不覆盖解析值。

## 已执行验证

| 验证 | 实际结果 |
|---|---|
| `go test -json ./... -count=1 -timeout 90s` | 64个测试/子测试记录通过，10个测试包，0失败 |
| `go test -tags integration ./tests/integration -json -count=1 -timeout 120s` | 29个测试/子测试记录通过，含模块2完整金融回归、真实MySQL/Redis依赖和14个目标模块子场景，0失败 |
| Goal模块测试 | 模糊目标、缺少预算、金额/周期证据、非法任务/权限/参数/依赖、冲突、安全饮食约束、总目标取整、已知上下文消解澄清 |
| 真实数据库/API | 幂等创建、不可重写计划、所有权与工具隔离、预算快照/必要预留、错误解析持久化及显式重试、乐观更新/历史/对比、并发解析/晚到响应、依赖落库故障回滚 |
| Coze协议 | 测试专用HTTP fixture验证PAT、同步模式、字符串parameters、End output和execute_id；异常输出/不可用不会生成金融任务；**不是实际模型联调** |
| 原生canvas生成 | Python unittest 2个测试通过；真实样例字段与节点引用复用，无工具/聊天历史；要求实际模型ID，拒绝无效ID；未声称画布已发布或原生导入成功 |
| `go vet ./...`、`go mod verify` | 退出0，all modules verified；最终Goal代码单独再次vet |
| SQL001/002 | 本机迁移、重复迁移、集成迁移和Compose迁移容器成功；002新增5张表，001内容/checksum保持不变 |
| Linux运行镜像 | Go1.26.4交叉编译server/migrate/seed，distroless nonroot镜像实际构建成功，Compose实际运行module3 |
| `/healthz`、`/readyz` | 容器HTTP200；MySQL、Redis、schema均ready，服务和依赖healthy |
| 容器目标API | 用户42目标38创建持久化；用户46目标39创建、更新到v2、历史和对比均成功；Coze未配置时parse为503 COZE_UNAVAILABLE |
| 资金隔离 | 集成测试检查编译没有新增账户/账本/交易/转账/目标金；容器用户46的既有账户在目标API操作前后完全一致 |
| 重启持久化 | Banking Service重启后目标39仍为v2、保留新文本和上下文；当前存储未使用内存伪结果 |
| OpenAPI | 所有实际注册路由与文档一致；成功集成请求及全部响应经过kin-openapi契约检查 |
| Coze源码 | 模块1快照记录的20个原源码文件SHA256全部未变；未重写原生引擎、未开发前端 |

原始测试日志位于本机 `services/banking-service/output/module3/unit.jsonl`、`integration.jsonl`。可查阅的交付证据：

- [测试结果和原源码核对](banking-module3-test-results.json)
- [容器健康和重启持久化](banking-module3-health.json)
- [实际容器API返回](banking-module3-docker-api.json)
- [修改文件清单及SHA256](banking-module3-files.json)

## 修改清单

新增：`internal/goal` 的8个Go源文件/测试、2份Schema；`internal/httpapi/goals.go`；`internal/migration/002_goals.sql`；`tests/integration/goals_test.go`；`scripts/generate_goals_openapi.py`；`MODULE3.md`；`coze/banking/goal-compiler`的Prompt、节点配置、生成器、测试和忽略文件；`deploy/banking/Initialize-Goal.ps1`；设计、计划、交付和验证证据文件。

修改：`internal/coze/client.go`增加同步执行；`internal/config/config.go`增加workflow_id配置校验；`internal/httpapi/server.go`增加独立目标装配，`core.go`增加统一错误映射；`internal/migration/migration.go`按顺序校验/应用001、002；`cmd/server/main.go`装配目标服务、迁移002就绪检查和合理模型响应超时；`cmd/migrate/main.go`更新实际迁移提示；`go.mod/go.sum`固定Schema依赖；`api/openapi.yaml`及生成器；`deploy/banking/compose.yaml`、Build-Image.ps1、.env.example；三个README。

没有修改已有001 SQL、backend/go.mod、Coze源码、前端或本机真实凭证。Git元数据不可用，未创建提交；使用文件清单记录实际交付。当前镜像为module3，采用本地编译+运行镜像构建；本轮没有把完整Dockerfile远程源码构建当成已验证成果。

## 尚未完成与后续模块

1. **真实Coze配置/发布和模型联调**：需要实际模型配置、可达Coze地址、PAT、已发布workflow_id。已提供Prompt和真实节点参考；本机缺少这些条件，不能伪造发布结果或真实模型测试。
2. Supervisor/Billing/Subscription/Savings Agent及执行器不属于本模块。计划中的读/分析任务暂不执行，不虚构账单或订阅结果。
3. 计划确认不是资金授权；模块8必须检查当前资金/账户状态、明确用户授权和风险策略后才可执行，不能因计划或模型文本直接扣款。
4. 自动重新规划、后台调度、SSE及金融工具扩展由后续模块接入；当前同步解析有持久化claim和失败状态，无内存队列冒充可恢复任务。

所有后续执行应关联服务器保存的goal_id、version、plan_id、task_id、trace_id、audit_id和Coze execute_id；重新校验预算及所有权，禁止使用客户端或模型自造计划直接执行。
