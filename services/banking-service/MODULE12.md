# P4：独立产品 API 集成

完整代码、数据库迁移、API/curl、启动及实际验收见 [banking-agent-service/README.md](../banking-agent-service/README.md)。

新增 `services/banking-agent-service` 与 `deploy/banking-agent`，复用已实现的 Go 身份与只读接口。Go 核心源码及 MySQL 迁移没有修改，数据库仍为 010；独立 PostgreSQL 投影数据库为 001。FastAPI 不持有银行签名秘密，不执行金融操作，不替换 Coze 或已有调度器。

真实联调与重启记录：`../banking-agent-service/output/p4/`。P5–P7 尚未实施。
