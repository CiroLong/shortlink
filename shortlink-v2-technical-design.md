# ShortLink v2 技术设计方案

> 基于 Go、Gin、GORM、PostgreSQL 与 Redis 的多租户高可用短链接及访问分析平台

| 项目 | 内容 |
| --- | --- |
| 文档版本 | v1.1 |
| 状态 | Proposed |
| 基线仓库 | `github.com/CiroLong/shortlink` |
| 基线提交 | `414a354` |
| 当前技术栈 | Go 1.23、Gin、GORM、MySQL、Redis |
| 目标主库 | PostgreSQL 17（固定大版本） |
| 目标 Redis | Redis 7.2（固定大版本），开启 AOF，`maxmemory-policy noeviction` |
| ORM 策略 | 当前阶段继续使用 GORM |
| 核心演进方向 | 控制面、重定向数据面、异步访问事件 Worker |

---

## 1. 文档目的

当前项目已经具备短链生成、跳转、Redis 缓存、访问量异步回写、过期清理、布隆过滤器和优雅停机等能力，但整体仍然更接近单机 Demo：业务模型较薄，数据库和缓存之间缺少可靠的一致性设计，后台任务无法安全地随实例数扩展，测试覆盖、指标和压测数据也不足以证明“高性能”。

本方案的目标不是简单增加功能数量，而是将项目升级为一个边界清楚、可以真实运行、能够支撑 Go 后端面试深入讨论的小型分布式系统：

1. 根据现有数据价值选择直接建立 v2 Schema，或先完成 MySQL → PostgreSQL 等价迁移，继续使用 GORM。
2. 修复现有代码中的正确性、部署和可测试性问题。
3. 增加租户、API Key、自定义域名、幂等创建等产品能力。
4. 将低延迟重定向与管理接口分离。
5. 使用 Redis Streams 构建可恢复、可重试、幂等的访问事件处理链路。
6. 使用指标、追踪、集成测试、压测和故障测试验证系统能力。

文档中的性能数字均为“目标值”，不是当前项目已经达到的结果。最终 README 和简历只能填写实际测得的数据。

---

## 2. 项目定位

### 2.1 一句话定义

ShortLink v2 是一个面向营销活动、二维码、消息通知和内部链接治理场景的多租户短链接与实时访问分析平台。

### 2.2 核心问题

系统主要解决三类问题：

- **低延迟重定向**：热点链接在突发流量下仍能快速跳转，缓存故障时可以降级。
- **可靠事件采集**：访问统计与跳转链路解耦，Worker 宕机、重复消费不能破坏统计结果。
- **多租户治理**：提供租户、API Key、自定义域名、配额、审计和链接生命周期管理。

### 2.3 非目标

第一阶段明确不做以下事项：

- 不为了“微服务”而拆成多个独立仓库。
- 不在没有容量证据时引入 Kafka、ClickHouse、TiKV 或 Kubernetes。
- 不实现复杂前端管理平台，只提供 OpenAPI 和最小演示页面。
- 不承诺全球多活、强一致访问统计或零事件丢失。
- 不自行实现 URL 安全信誉库，只预留风控接口。

---

## 3. 当前系统评估

### 3.1 已有能力

- Gin HTTP 服务。
- 长链接到 8 字符短码的生成逻辑。
- Redis Cache Aside 查询。
- Redis 访问计数和定时批量回写。
- 链接过期和周期清理。
- RedisBloom 拦截不存在的短码。
- Docker 多阶段构建与 Compose。
- HTTP Server 优雅关闭。
- 已有短码生成、Handler 错误分支和 Redis 访问计数并发回归测试。

### 3.2 主要问题

| 类别 | 状态 | 问题 | 影响 |
| --- | --- | --- | --- |
| HTTP 正确性 | 已修复（`a0dd106`） | Handler 错误分支已经立即 `return`，并有回归测试 | v2 需要继续保持统一错误映射 |
| 中间件顺序 | 已修复（`4d53dd5`） | Bloom 检查已经先于跳转 Handler 执行 | Bloom 本身仍处于正确性路径 |
| 计数一致性 | 部分修复（`414a354`） | `GETDEL` 已解决并发增量被尾部 `DEL` 删除；进程在提取后崩溃仍可能丢失计数 | v2 使用可恢复事件流水线替代临时计数 |
| 短码唯一性 | 待解决 | 先查再写，且使用 `Save` | 并发创建存在 TOCTOU 竞争和覆盖风险 |
| Bloom 正确性 | 待解决 | Bloom negative 直接返回 404 | Bloom 更新失败时有效链接不可访问 |
| Bloom 重建 | 待解决 | 先删旧 filter，再全量重建 | 重建期间存在假 404 窗口 |
| 故障降级 | 待解决 | Redis 错误返回 500 或触发 `panic` | 缓存故障升级为重定向故障 |
| 并发控制 | 待解决 | 每次访问启动额外 goroutine | 高流量时可能堆积无界 goroutine |
| 多实例运行 | 待解决 | 每个实例都执行同步、清理和 Bloom 重建 | 扩容后后台任务冲突 |
| 配置 | 待解决 | Compose 环境变量没有被 Viper 正确绑定 | 容器配置与程序读取方式不一致 |
| Docker | 待解决 | 工作目录和配置复制目录不一致 | 容器可能找不到 `app.yaml` |
| Redis 模块 | 待解决 | 普通 Redis 镜像不提供 `BF.*` 命令 | 默认 Compose 无法运行 Bloom 逻辑 |
| 数据模型 | 待解决 | `user_id` 只参与哈希，没有持久化 | 不具备真实租户隔离能力 |
| 工程质量 | 部分具备 | 已有基础单元测试，仍缺 migration、集成测试、指标和压测 | 无法证明完整正确性与性能声明 |

### 3.3 迁移原则

数据库迁移与业务重构不能无验证地一次完成。整个升级遵循以下顺序：

1. **先建立测试护栏。**
2. **根据数据迁移决策建立 PostgreSQL Schema。**
3. **修复剩余正确性问题。**
4. **再演进业务模型和进程边界。**
5. **基础观测贯穿各阶段，核心链路稳定后再增加事件分析、Tracing 和性能优化。**

---

## 4. 总体架构

### 4.1 逻辑架构

```mermaid
flowchart TD
    Visitor["访问者"] --> Redirect["Redirect API"]
    Client["管理端 / Open API"] --> Control["Control API"]

    Redirect --> Cache["本地缓存 + Redis"]
    Cache --> PG["PostgreSQL"]
    Control --> PG
    Control --> Cache

    Redirect --> Stream["Redis Streams"]
    Stream --> Worker["Event Worker"]
    Worker --> PG
```

### 4.2 部署形态

项目保持一个仓库、一个 Go module，生成三个常驻进程二进制，并提供 migration/maintenance 工具命令：

| 进程 | 职责 | 扩容特征 |
| --- | --- | --- |
| `control-api` | 链接管理、租户、域名、API Key、统计查询 | 普通无状态扩容 |
| `redirect-api` | 根据 host/code 查找目标并返回跳转 | 独立高并发扩容 |
| `event-worker` | 消费访问事件、幂等落库、聚合 | Consumer Group 横向扩容 |

早期可以使用一个 `api` 二进制同时承载 Control 和 Redirect 路由，但代码层必须提前分离模块。第二阶段再拆启动入口，不拆仓库。

### 4.3 推荐目录

```text
shortlink/
├── cmd/
│   ├── control-api/main.go
│   ├── redirect-api/main.go
│   ├── event-worker/main.go
│   ├── migrate/main.go
│   └── maintenance/main.go
├── internal/
│   ├── app/                    # 依赖组装与生命周期
│   ├── config/                 # 环境变量和配置校验
│   ├── domain/
│   │   ├── link.go
│   │   ├── tenant.go
│   │   ├── domain.go
│   │   └── click_event.go
│   ├── repository/
│   │   └── postgres/           # GORM Repository 实现
│   ├── cache/
│   │   └── redis/              # 映射缓存、负缓存、失效通知
│   ├── event/
│   │   └── redisstream/        # Producer / Consumer
│   ├── service/                # 用例和事务边界
│   ├── transport/http/         # Gin Handler、Middleware、DTO
│   ├── observability/          # 日志、指标、Tracing
│   └── security/               # API Key、限流、URL 校验
├── migrations/                 # PostgreSQL SQL 迁移
├── deploy/
│   ├── docker-compose.yml
│   ├── prometheus.yml
│   └── grafana/
├── tests/
│   ├── integration/
│   └── e2e/
├── Dockerfile
├── Makefile
├── go.mod
└── README.md
```

---

## 5. PostgreSQL 迁移设计

### 5.1 为什么选择 PostgreSQL

PostgreSQL 作为主库承担业务事实源，原因包括：

- 唯一约束与 `ON CONFLICT` 可以原子解决短码冲突。
- 适合租户、域名、链接、API Key 等关系模型。
- 支持事务、部分索引、JSONB、声明式分区。
- 后续可以使用 Outbox、按时间分区的访问事件表。
- GORM 官方 PostgreSQL driver 基于 pgx，迁移成本有限。

Redis 仍然是缓存和临时事件层，不能成为链接正确性的唯一来源。

### 5.2 迁移范围

迁移路径由现有数据价值决定，不能为了形式固定成一种方式：

- **当前仓库只有可丢弃的开发/测试数据：** 推荐直接创建 PostgreSQL v2 Schema，通过导入脚本迁移需要保留的数据，避免先建立兼容表、随后立即二次改表。
- **已经存在需要保留的真实数据：** 第一次迁移只替换主库，不同时改变 API 和核心业务语义：

```text
MySQL + GORM + Redis
          ↓
PostgreSQL + GORM + Redis
```

真实数据场景下，等价迁移完成并通过回归测试后，再通过后续 migration 增加多租户字段和新表。实施前必须在 ADR 中记录所选路径、数据量、停机窗口和回滚边界。

### 5.3 依赖变更

删除：

```text
gorm.io/driver/mysql
github.com/go-sql-driver/mysql
```

增加：

```text
gorm.io/driver/postgres
```

GORM PostgreSQL driver 内部使用 pgx 作为 `database/sql` driver。实现时应通过 `go get` 选择兼容当前 GORM 版本的稳定版本，并提交 `go.mod` 与 `go.sum`。

### 5.4 配置模型

停止使用只包含 MySQL DSN 的配置对象，改成数据库无关名称：

```go
type Config struct {
    Server   ServerConfig
    Database DatabaseConfig
    Redis    RedisConfig
}

type DatabaseConfig struct {
    URL             string
    MaxOpenConns    int
    MaxIdleConns    int
    ConnMaxLifetime time.Duration
    ConnMaxIdleTime time.Duration
}

type ServerConfig struct {
    HTTPAddr       string
    PublicBaseURL string
    ReadTimeout    time.Duration
    WriteTimeout   time.Duration
}
```

环境变量：

```dotenv
DATABASE_URL=postgres://shortlink:shortlink@postgres:5432/shortlink?sslmode=disable
DATABASE_MAX_OPEN_CONNS=50
DATABASE_MAX_IDLE_CONNS=10
DATABASE_CONN_MAX_LIFETIME=30m
DATABASE_CONN_MAX_IDLE_TIME=5m

REDIS_ADDR=redis:6379
REDIS_PASSWORD=
REDIS_DB=0

HTTP_ADDR=:8080
PUBLIC_BASE_URL=http://localhost:8080
```

配置优先级：

```text
环境变量 > 配置文件 > 默认值
```

启动时必须校验必填项，不能在数据库第一次请求时才暴露错误。日志不得打印密码或完整 DSN。

### 5.5 GORM 初始化

```go
func OpenPostgres(ctx context.Context, cfg DatabaseConfig) (*gorm.DB, error) {
    db, err := gorm.Open(postgres.Open(cfg.URL), &gorm.Config{
        TranslateError: true,
    })
    if err != nil {
        return nil, fmt.Errorf("open postgres: %w", err)
    }

    sqlDB, err := db.DB()
    if err != nil {
        return nil, fmt.Errorf("get sql db: %w", err)
    }

    sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
    sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
    sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
    sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

    pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
    defer cancel()
    if err := sqlDB.PingContext(pingCtx); err != nil {
        return nil, fmt.Errorf("ping postgres: %w", err)
    }

    return db, nil
}
```

要求：

- 所有查询使用 `db.WithContext(ctx)`。
- HTTP 请求传播自己的超时和取消信号。
- 不再把 `context.Background()` 固定保存在全局 DB 对象中。
- 初始化返回错误，由 `main` 统一处理；库代码不得 `panic`。
- 关停时停止接收请求、停止 Worker，再关闭 Redis 和 SQL 连接池。

### 5.6 真实数据迁移路径的兼容模型

仅当迁移 ADR 选择“先等价迁移”时，为了降低风险建立与现有表语义接近的模型；直接建立 v2 Schema 时跳过本节：

```go
type Link struct {
    ShortURL   string     `gorm:"column:short_url;type:varchar(16);primaryKey"`
    OriginalURL string    `gorm:"column:original_url;type:text;not null"`
    VisitCount int64      `gorm:"column:visit_count;not null;default:0"`
    ExpiresAt  *time.Time `gorm:"column:expires_at;index"`
    CreatedAt  time.Time  `gorm:"column:created_at;not null"`
    UpdatedAt  time.Time  `gorm:"column:updated_at;not null"`
}

func (Link) TableName() string { return "links" }
```

注意事项：

- Go 字段统一使用 `URL`、`ID`，不要混用 `Url`、`Id`。
- `ExpiresAt` 使用指针表达永不过期，避免零值时间的歧义。
- `VisitCount` 使用 `int64`，避免平台相关的 `uint` 映射。
- 查询主键时显式写 `Where("short_url = ?", code)`，不要依赖 GORM 对字符串主键参数的隐式推断。
- 禁止使用 `Save` 创建映射；短码写入必须使用 `Create + ON CONFLICT`。

### 5.7 SQL 迁移脚本

生产和演示环境使用显式 SQL migration。`AutoMigrate` 只能用于临时开发环境，不允许应用进程在生产启动时自动改表。

等价迁移路径的第一份迁移如下；直接建立 v2 Schema 时，首份 migration 应使用第 6 节领域模型，不先创建该兼容表：

```sql
-- 000001_create_links.up.sql
CREATE TABLE links (
    short_url    VARCHAR(16) PRIMARY KEY,
    original_url TEXT NOT NULL,
    visit_count  BIGINT NOT NULL DEFAULT 0 CHECK (visit_count >= 0),
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_links_expires_at
    ON links (expires_at)
    WHERE expires_at IS NOT NULL;
```

回滚：

```sql
-- 000001_create_links.down.sql
DROP TABLE IF EXISTS links;
```

迁移工具可以选择 `goose` 或 `golang-migrate`。项目只选择一种，并通过单独的 migration command 或 CI/CD job 执行。

### 5.8 原子处理短码冲突

```go
func (r *LinkRepository) Create(ctx context.Context, link *Link) error {
    result := r.db.WithContext(ctx).
        Clauses(clause.OnConflict{
            Columns:   []clause.Column{{Name: "domain_id"}, {Name: "code"}},
            DoNothing: true,
        }).
        Create(link)

    if result.Error != nil {
        return fmt.Errorf("create link: %w", result.Error)
    }
    if result.RowsAffected == 0 {
        return ErrShortCodeConflict
    }
    return nil
}
```

随机 code 在收到 `ErrShortCodeConflict` 后生成新 code 并重试；自定义 code 则直接向客户端返回冲突。唯一约束是最终裁决者，任何应用层预检查都只能作为优化。等价迁移表使用 `short_url` 主键时，同样通过对应约束执行 `ON CONFLICT DO NOTHING`。

### 5.9 现有数据迁移

若当前仓库只有测试数据，建议直接建立 v2 Schema 后停机导入需要保留的记录，并为旧记录分配默认 tenant/domain：

1. 停止写入。
2. 导出 MySQL `links` 表。
3. 转换时间、NULL 和字段名称。
4. 导入 PostgreSQL。
5. 比较总行数、主键集合、过期链接数量和访问量总和。
6. 启动 PostgreSQL 版本并执行抽样跳转验证。
7. 保留 MySQL 只读备份，观察稳定后再下线。

校验 SQL 示例：

```sql
SELECT count(*) FROM links;
SELECT coalesce(sum(legacy_visit_count), 0) FROM links; -- v2 直接迁移路径
SELECT count(*) FROM links WHERE expires_at < now();
SELECT min(created_at), max(created_at) FROM links;
```

等价迁移路径在兼容表上使用 `visit_count` 执行相同校验。

如果已经存在不可中断的真实流量，则需要双写、增量校验和灰度切流；这会显著增加复杂度，不作为当前求职项目的默认方案。

### 5.10 回滚策略

- 切换前保留 MySQL 全量备份。
- 数据迁移期间停止写请求，避免双库漂移。
- 新版本通过配置切换 PostgreSQL；回滚应用时同时回滚到只读窗口内的 MySQL 数据。
- 切换后若已经产生新写入，不允许直接切回旧 MySQL，必须先回灌差量。
- migration 的 down 脚本不能被当作数据恢复工具。

---

## 6. v2 领域模型

若选择直接建立 v2 Schema，本节就是 PostgreSQL 的首个业务 migration；若选择等价迁移，则在兼容版本稳定后通过后续 migrations 演进到本模型。

### 6.1 核心实体

```mermaid
erDiagram
    TENANT ||--o{ API_KEY : owns
    TENANT ||--o{ DOMAIN : owns
    TENANT ||--o{ LINK : owns
    DOMAIN ||--o{ LINK : hosts
    LINK ||--o{ CLICK_EVENT : receives
```

### 6.2 表设计

#### tenants

```sql
CREATE TABLE tenants (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        VARCHAR(128) NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

#### api_keys

```sql
CREATE TABLE api_keys (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    BIGINT NOT NULL REFERENCES tenants(id),
    key_prefix   VARCHAR(16) NOT NULL,
    key_hash     BYTEA NOT NULL,
    hash_version SMALLINT NOT NULL DEFAULT 1,
    name         VARCHAR(128) NOT NULL,
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (key_prefix)
);
```

API Key 使用 CSPRNG 生成的高熵随机值。服务端通过 prefix 定位候选记录，只保存 prefix 和 `HMAC-SHA256(server_pepper, api_key)`，使用常量时间比较验证，不保存明文。pepper 通过密钥管理或部署环境注入，并保留 hash 版本以支持轮换。

#### domains

```sql
CREATE TABLE domains (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   BIGINT NOT NULL REFERENCES tenants(id),
    host        VARCHAR(253) NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 0,
    verified_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (host),
    UNIQUE (tenant_id, id)
);
```

`host` 写入前必须规范化为小写 ASCII/Punycode，移除尾部点和默认端口。数据库中的 `UNIQUE (host)` 约束只作用于规范化后的值，避免 `Example.com` 与 `example.com` 被视为不同域名。

#### links

```sql
CREATE TABLE links (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     BIGINT NOT NULL REFERENCES tenants(id),
    domain_id     BIGINT NOT NULL,
    code          VARCHAR(16) NOT NULL,
    original_url  TEXT NOT NULL,
    redirect_type SMALLINT NOT NULL DEFAULT 302,
    status        SMALLINT NOT NULL DEFAULT 1,
    legacy_visit_count BIGINT NOT NULL DEFAULT 0 CHECK (legacy_visit_count >= 0),
    expires_at    TIMESTAMPTZ,
    deleted_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    version       BIGINT NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, id),
    UNIQUE (domain_id, code),
    FOREIGN KEY (tenant_id, domain_id) REFERENCES domains(tenant_id, id),
    CHECK (redirect_type IN (301, 302, 307, 308))
);

CREATE INDEX idx_links_tenant_created
    ON links (tenant_id, created_at DESC, id DESC);

CREATE INDEX idx_links_expire_at
    ON links (expires_at)
    WHERE expires_at IS NOT NULL;
```

`legacy_visit_count` 用于承接 MySQL 历史访问量，并在 Phase 0～2 继续由现有 Redis 计数同步逻辑维护。Phase 3 切换到事件流水线时记录明确 cutover 时间：历史总量保留在该字段，cutover 后的统计来自 `link_stats_hourly`，查询总量为两者之和；新 Worker 不再更新该字段，避免热门 link 行成为额外写热点。

#### idempotency_keys

```sql
CREATE TABLE idempotency_keys (
    tenant_id     BIGINT NOT NULL REFERENCES tenants(id),
    key            VARCHAR(128) NOT NULL,
    request_hash   BYTEA NOT NULL,
    resource_type  VARCHAR(32) NOT NULL,
    resource_id    BIGINT NOT NULL,
    response_status SMALLINT NOT NULL,
    response_body  JSONB NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, key)
);
```

请求体先按固定规则规范化，再计算 `request_hash`。同一幂等键和相同请求返回持久化的原始状态码与响应快照；同一幂等键对应不同请求体时返回 `409 Conflict`，不能静默复用旧结果。链接创建与幂等记录必须位于同一事务中，并为过期幂等记录提供分批清理任务。

---

## 7. 短码生成

### 7.1 推荐算法

默认短码使用 `crypto/rand` 生成 8～10 位 Base62 字符：

```text
0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz
```

流程：

1. 生成随机 code。
2. 尝试插入 PostgreSQL。
3. 唯一约束冲突则重新生成。
4. 最多重试 5 次，仍失败则返回内部错误并上报指标。

相比“URL + user_id 哈希截断”，随机 code 的优势是：

- 不泄漏相同输入之间的确定性关系。
- 不需要把用户 ID 混入哈希。
- 并发冲突由数据库唯一约束可靠裁决。
- 自定义短码可以复用相同约束逻辑。

### 7.2 自定义短码

- 长度 4～32，仅允许 ASCII 字母、数字、`-`、`_`。
- 保留 `/api`、`/health`、`/metrics` 等系统前缀。
- `(domain_id, code)` 范围内唯一。
- 规范化规则必须固定，避免大小写歧义；默认大小写敏感。

---

## 8. API 设计

### 8.1 认证

Control API 使用：

```http
Authorization: Bearer sl_live_xxxxxxxxx
```

Redirect API 不需要认证。

### 8.2 创建短链

```http
POST /api/v1/links
Idempotency-Key: 01J...
Content-Type: application/json
```

```json
{
  "domain": "s.example.com",
  "original_url": "https://example.com/campaign?a=1",
  "custom_code": null,
  "redirect_type": 302,
  "expires_at": "2026-12-31T23:59:59Z"
}
```

```json
{
  "id": "12345",
  "code": "aZ8kP2xQ",
  "short_url": "https://s.example.com/aZ8kP2xQ",
  "original_url": "https://example.com/campaign?a=1",
  "status": "active",
  "expires_at": "2026-12-31T23:59:59Z",
  "created_at": "2026-08-05T12:00:00Z"
}
```

### 8.3 管理接口

```text
POST   /api/v1/links
GET    /api/v1/links/:id
GET    /api/v1/links?cursor=...&limit=20
PATCH  /api/v1/links/:id
DELETE /api/v1/links/:id
GET    /api/v1/links/:id/stats

POST   /api/v1/domains
GET    /api/v1/domains
POST   /api/v1/api-keys
DELETE /api/v1/api-keys/:id
```

列表接口使用基于 `(created_at, id)` 的游标分页，不使用深 OFFSET。

### 8.4 重定向

```http
GET /:code
Host: s.example.com
```

默认返回 `302 Found`。只有用户明确选择永久跳转时才允许 301/308，因为永久跳转可能被浏览器和 CDN 长期缓存，后续无法快速修改目标。

### 8.5 错误格式

```json
{
  "error": {
    "code": "LINK_NOT_FOUND",
    "message": "short link not found",
    "request_id": "01J..."
  }
}
```

所有 Handler 在写入错误响应后必须立即 `return`。领域错误统一映射为 HTTP 状态码，数据库和 Redis 原始错误不得直接返回客户端。

---

## 9. 重定向数据面与缓存

### 9.1 查询路径

```text
请求
  → 校验 host/code
  → 本地缓存
  → Redis
  → PostgreSQL
  → 回填 Redis 和本地缓存
  → 异步写入访问事件
  → 返回 302/307
```

### 9.2 Redis Key

```text
sl:v1:link:{host}:{code}
sl:v1:invalidate                 # Pub/Sub channel，仅用于加速本地缓存失效
sl:v1:ratelimit:{scope}:{identity}
sl:v1:clicks
```

正缓存、负缓存和删除 tombstone 使用同一个 link key，避免正负缓存同时存在。缓存 value 至少包含：

```json
{
  "state": "active",
  "link_id": 12345,
  "tenant_id": 7,
  "original_url": "https://example.com",
  "redirect_type": 302,
  "expires_at": "2026-12-31T23:59:59Z",
  "version": 4
}
```

`state` 可以是 `active`、`not_found`、`disabled` 或 `deleted`。不存在记录使用 `version=0` 的短 TTL negative value；已删除或封禁记录使用数据库事务中递增后的 version 写 tombstone。

### 9.3 TTL

正缓存 TTL：

```text
有过期时间：min(配置最大 TTL × 随机抖动系数, expires_at - now)
永不过期：配置最大 TTL × 随机抖动系数
```

建议 Redis 正缓存默认 1～6 小时，并增加 0.9～1.1 的随机抖动。计算结果不得超过链接剩余有效期；小于等于零时不写正缓存。永不过期链接也必须设置缓存 TTL，以便配置或封禁最终生效。

负缓存和 tombstone TTL 建议 15～60 秒。本地缓存 TTL 建议 5～30 秒，并且每次缓存命中仍检查 `state` 和 `expires_at`，不能只依赖 TTL 自动淘汰。

### 9.4 缓存更新

采用 PostgreSQL 版本号配合 Cache Aside/Write Through：

- 创建、更新、删除和封禁都在 PostgreSQL 事务中递增 `version`，提交后把最新值或 tombstone 写入 Redis。
- Redis 使用 Lua 比较 incoming version 与 cached version，只允许相同或更高版本覆盖；查询 miss 回源得到的旧版本不能覆盖更新后的值。
- 不存在记录只能以 `version=0` 执行 `SET if absent`，防止并发创建成功后又被旧的 negative 结果覆盖。
- Redis Lua 在写入新值后发布 `{host, code, version}` 失效消息，各实例据此删除本地缓存中的旧版本。
- Redis Pub/Sub 是尽力而为的失效加速，不承担正确性；实例断线可能漏消息，因此仍由本地短 TTL、版本比较和命中时的状态/过期检查限制陈旧窗口。
- Redis 写入或发布失败不回滚已经提交的 PostgreSQL 事务，记录指标并依靠短 TTL 回源修复。

缓存协议必须用并发测试覆盖“旧回源晚于更新写回”“创建与 negative cache 并发”“删除与本地缓存命中”三类竞态。

### 9.5 故障降级

| 故障 | 行为 |
| --- | --- |
| 本地缓存 miss | 查询 Redis |
| Redis miss | 查询 PostgreSQL |
| Redis 超时/不可用 | 记录指标，直接查询 PostgreSQL |
| PostgreSQL 不可用、Redis 命中 active 且未过期值 | 允许重定向，接受文档规定的最大陈旧窗口 |
| PostgreSQL 不可用、Redis 命中 negative/tombstone | 按缓存状态返回 404/410，不重定向 |
| PostgreSQL 不可用、Redis miss | 返回 503，不伪装成 404 |
| 事件流写入失败 | 不阻止重定向，记录事件丢失指标和结构化日志 |

缓存是性能优化，不是业务事实源。有效链接不能仅因为 Bloom 或 Redis 异常而返回 404。可用性优先意味着封禁或修改在极端故障下可能有不超过本地缓存 TTL 的传播延迟；若未来存在必须立即生效的安全封禁，需要增加独立的短 TTL denylist 并采用 fail closed 策略。

### 9.6 缓存击穿

- 单实例内使用 `singleflight` 合并相同 host/code 的并发回源。
- 不默认引入分布式锁；数据库能够承受少量跨实例重复回源。
- 对随机不存在 code 使用短 TTL 负缓存和限流。

### 9.7 Bloom Filter 决策

v2 初始版本移除 Bloom Filter 的强依赖，使用负缓存替代。原因是当前实现存在模块依赖、更新顺序和重建空窗问题。

只有压测证明随机 miss 已经成为 PostgreSQL 明显压力来源时才重新引入 RedisBloom，并满足：

- Bloom negative 只能作为优化，Redis 异常必须降级回源。
- 创建成功后异步补写 Bloom，失败可修复。
- 使用 `filter:v1`、`filter:v2` 双缓冲重建后原子切换。
- 监控估算元素数量和误判率。

---

## 10. 访问事件流水线

### 10.1 设计目标

- 跳转不等待统计数据库写入。
- Stream 接受事件后提供至少一次消费。
- Worker 重启后能够恢复 pending 事件。
- 重复投递不能重复增加统计结果。
- 统计故障不影响核心重定向可用性。

初版的“至少一次”限定为：事件已经成功写入 Stream 且 Redis 数据仍然存在时，Worker 进程重启、重复投递或数据库短暂故障不会令事件静默丢失。它不等同于 Redis 灾难故障下的零丢失。

Redis 使用 AOF `appendfsync everysec`、持久化 volume 和 `maxmemory-policy noeviction`。该配置在主机灾难故障时仍可能丢失约一秒事件；若业务要求更强保证，需要评估 `appendfsync always`、副本配合 `WAIT/WAITAOF`、本地 WAL 或更重的消息系统，并重新压测延迟。Redis 不可用或 `XADD` 超时时，初版继续重定向并记录事件丢失指标。

### 10.2 事件结构

```json
{
  "event_id": "01J5...",
  "link_id": 12345,
  "tenant_id": 7,
  "occurred_at": "2026-08-05T12:00:00.123Z",
  "referer": "https://example.org",
  "user_agent": "...",
  "ip_hash": "..."
}
```

不在事件中保存原始 IP。可以按天轮换 HMAC salt，使同一天内可以近似去重，但不能跨长期追踪用户。

### 10.3 Redis Streams

```text
Stream: sl:v1:clicks
Group:  click-aggregators
Consumer: worker-{instance-id}
```

Producer 使用 `XADD`。Worker 使用 Consumer Group：

1. `XREADGROUP` 批量读取。
2. 在 PostgreSQL 事务中幂等插入事件并更新聚合。
3. 数据库事务提交成功后执行 `XACK`。
4. 使用唯一 consumer name，定期通过 `XAUTOCLAIM` 分页认领超过最大处理时间的 pending 事件。
5. 根据 `XPENDING` delivery count 判断毒消息；超过最大重试次数后，通过 Lua 原子执行 DLQ 去重写入与原 Stream `XACK`。

Redirect API 写入 Stream 时使用很短的独立超时；超时或失败只增加丢失指标，不能拖长或中断跳转请求。`referer`、`user_agent` 等字段在写入前必须限制长度，Stream 容量规划同时考虑事件条数和平均字节数。

初版不在 `XADD` 上使用无条件 `MAXLEN`，因为它可能删除仍在 PEL 中的消息正文。维护任务根据 Consumer Group 的 `last-delivered-id` 和最老 pending ID 计算安全水位，只使用 `XTRIM MINID ~ <safe-id>` 删除所有 Group 都已越过且不再 pending 的历史记录。若增加新的 Consumer Group，安全水位必须同时考虑所有 Group。积压接近容量上限时告警并扩容 Worker，不能通过删除未确认事件恢复容量。

### 10.4 幂等消费

```sql
CREATE TABLE click_events (
    event_id      VARCHAR(32) PRIMARY KEY,
    tenant_id     BIGINT NOT NULL REFERENCES tenants(id),
    link_id       BIGINT NOT NULL,
    occurred_at   TIMESTAMPTZ NOT NULL,
    referer_host  VARCHAR(253),
    user_agent    TEXT,
    ip_hash       BYTEA,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, link_id) REFERENCES links(tenant_id, id)
);

CREATE TABLE link_stats_hourly (
    tenant_id    BIGINT NOT NULL REFERENCES tenants(id),
    link_id      BIGINT NOT NULL,
    bucket_start TIMESTAMPTZ NOT NULL,
    visits       BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, link_id, bucket_start),
    FOREIGN KEY (tenant_id, link_id) REFERENCES links(tenant_id, id)
);
```

处理单条事件时：

1. `INSERT click_events ... ON CONFLICT DO NOTHING RETURNING event_id`。
2. 只有确实插入的新事件才 `UPSERT link_stats_hourly` 增加计数。
3. 两步位于同一 PostgreSQL 事务中。
4. 事务完成后再确认 Stream 消息。

当原始事件量增长到 PostgreSQL 不再经济时，再将 `click_events` 迁移到 ClickHouse；控制面和链接元数据继续保留在 PostgreSQL。

---

## 11. 一致性语义

| 场景 | 语义 | 实现 |
| --- | --- | --- |
| 创建短链 | 强一致唯一性 | PostgreSQL 唯一约束 + `ON CONFLICT` |
| 幂等创建 | 同租户同 key 返回相同结果 | `idempotency_keys` 与链接同事务 |
| 修改目标 URL | PostgreSQL 强一致，缓存有界最终一致 | DB version + Redis CAS 写入 + Pub/Sub 加速失效 + 本地短 TTL |
| 删除/封禁 | PostgreSQL 强一致，缓存有界最终一致 | versioned tombstone + 本地短 TTL；安全封禁可选 fail closed denylist |
| 重定向 | 优先可用 | 缓存命中可在 PostgreSQL 短暂不可用时服务 |
| 访问事件 | Redis 数据仍存在时至少一次 | AOF + Consumer Group + 安全 trimming + 事务后 ACK |
| 统计聚合 | 最终一致、幂等 | event_id 去重 + 聚合 UPSERT |

不宣称“全链路 exactly once”。系统实现的是：在声明的 Redis 持久化和保留边界内至少一次传输，加业务层幂等，最终得到不重复的聚合效果；Redis 不可用、超时或灾难恢复窗口内的事件允许丢失并必须可观测。

---

## 12. 后台任务与多实例

现有版本由每个 API 实例运行同步、清理和 Bloom 重建，扩容后会冲突。v2 采用以下规则：

- API 进程不执行周期性全库任务。
- 访问事件由独立 Worker Consumer Group 处理。
- 管理端删除只设置 `deleted_at/status`、递增 version 并写缓存 tombstone；过期链接在查询时根据 `expires_at` 判断，二者都不立即物理删除。
- `click_events`、小时聚合、幂等记录和链接 tombstone 分别配置保留期，清理顺序必须先删除依赖数据，再删除 link。
- 初始版本可以长期保留体量较小的 link tombstone，避免短码被意外复用；只有数据保留策略明确后才启用 link 物理清理。
- 物理清理由独立 maintenance command 执行，不与事件消费共享阻塞循环。
- 需要全局单实例的任务使用 PostgreSQL advisory lock。
- 清理使用 keyset 分批删除，避免长事务和大范围锁。

链接物理清理必须确认不存在事件和聚合外键引用。例如：

```sql
WITH candidates AS (
    SELECT l.id
    FROM links l
    WHERE coalesce(l.deleted_at, l.expires_at) < now() - interval '365 days'
      AND NOT EXISTS (SELECT 1 FROM click_events e WHERE e.link_id = l.id)
      AND NOT EXISTS (SELECT 1 FROM link_stats_hourly s WHERE s.link_id = l.id)
    ORDER BY l.id
    LIMIT 1000
)
DELETE FROM links l
USING candidates c
WHERE l.id = c.id;
```

具体保留天数必须进入配置和隐私说明，示例中的 365 天不是默认承诺。原始事件量较大后优先按 `occurred_at` 分区并按分区淘汰，不执行大范围逐行删除。

---

## 13. 安全与防滥用

### 13.1 URL 校验

- 只允许 `http` 和 `https` scheme。
- 拒绝空 host、控制字符和超过配置上限的 URL。
- 规范化 host，但不擅自改写 path/query。
- 重定向服务不会主动请求目标 URL，因此不存在普通意义的服务端 SSRF；如果未来增加链接预览或安全扫描，则必须单独做 SSRF 防护。

### 13.2 API Key

- Key 使用 CSPRNG 生成。
- 明文只在创建时展示一次。
- 数据库保存 prefix 和 hash。
- 支持撤销、过期和最后使用时间。
- 日志和 tracing 中必须脱敏 Authorization header。

### 13.3 限流

| 路径 | 维度 | 建议 |
| --- | --- | --- |
| 创建/修改 API | API Key、tenant | Token Bucket / GCRA |
| 随机 miss 重定向 | IP prefix、host | 较严格限制 |
| 正常命中重定向 | IP prefix、host | 较宽松限制 |

Redis Lua 保证一次限流判断的原子性。Redis 不可用时，管理 API 默认 fail closed；重定向 API 可以使用实例内限流降级。

### 13.4 审计与风控

- 记录链接创建、修改、封禁和 API Key 操作。
- 支持 tenant/domain/link 三级封禁。
- 预留安全扫描接口，但异步扫描失败不能阻塞所有创建请求。
- 对原始 Referer、User-Agent 和 IP 派生信息设置明确保留期限。

---

## 14. 可观测性

### 14.1 日志

统一结构化 JSON 日志，至少包含：

```text
timestamp
level
service
request_id
trace_id
tenant_id
link_id
host
code
latency_ms
cache_result
error_code
```

禁止记录 API Key、数据库密码和完整原始 IP。

### 14.2 Prometheus 指标

#### HTTP

```text
shortlink_http_requests_total{service,route,method,status}
shortlink_http_request_duration_seconds{service,route}
shortlink_redirect_total{result,source}
```

#### 缓存

```text
shortlink_cache_requests_total{layer,result}
shortlink_cache_operation_duration_seconds{operation}
shortlink_cache_fallback_total{reason}
```

#### 数据库

```text
shortlink_db_operation_duration_seconds{operation}
shortlink_db_errors_total{operation,kind}
shortlink_db_pool_connections{state}
```

#### 事件 Worker

```text
shortlink_events_produced_total{result}
shortlink_events_consumed_total{result}
shortlink_event_processing_duration_seconds
shortlink_event_pending
shortlink_event_dead_letter_total
```

指标 label 禁止使用 `code`、`link_id`、`tenant_id` 等高基数字段。

### 14.3 Tracing

使用 OpenTelemetry：

- Control API：HTTP → Service → PostgreSQL → Redis invalidation。
- Redirect API：HTTP → local cache/Redis/PostgreSQL → Stream producer。
- Worker：Stream consume → PostgreSQL transaction → ACK。

Tracing 采用采样，不能为每次大流量重定向永久保存完整 trace。

### 14.4 健康检查

```text
GET /health/live   # 进程是否存活，不检查外部依赖
GET /health/ready  # 是否可以接收流量，检查必要依赖
GET /metrics       # Prometheus
```

Redirect API 的 readiness 策略应考虑 Redis 和 PostgreSQL：二者之一可提供已有链接时仍可服务，但应用启动时至少必须成功连接 PostgreSQL并完成配置校验。

---

## 15. 测试策略

### 15.1 单元测试

- Base62 随机短码长度和字符集。
- 自定义短码校验。
- URL scheme、host、长度和控制字符校验。
- 过期边界和时区。
- 缓存 TTL 及 jitter。
- 领域错误到 HTTP 状态映射。
- API Key hash 和验证。

### 15.2 Repository 集成测试

使用真实 PostgreSQL 容器，不使用 SQLite 模拟 PostgreSQL：

- migration up/down。
- 短码唯一约束。
- 100 个 goroutine 同时创建相同自定义 code，只有一个成功。
- `ON CONFLICT` 行为。
- 跨租户的 domain/link、link/event 组合写入必须被数据库约束拒绝。
- 域名规范化后大小写和尾部点变体不能重复注册。
- 幂等键同请求复用和不同请求冲突。
- 过期查询和清理批次。
- link 仍被事件或聚合引用时不能被物理清理。
- 统计事件重复消费不重复增加聚合。

Redis 集成测试覆盖：

- 缓存命中/miss。
- TTL 和负缓存。
- 旧版本回填不能覆盖新版本或 tombstone。
- 创建与 negative cache 并发时最终保留有效链接。
- 漏掉 Pub/Sub 失效消息时，本地缓存陈旧时间不超过配置上限。
- Streams Consumer Group、pending 恢复和 ACK。
- trimming 不删除任何仍 pending 的消息正文。
- DLQ 写入与原消息 ACK 的原子性和重试幂等。
- 限流 Lua 原子性。

### 15.3 E2E

```text
创建租户/API Key
  → 创建短链
  → 第一次跳转（PostgreSQL 回源）
  → 第二次跳转（Redis 命中）
  → Worker 消费事件
  → 查询统计
  → 修改目标并验证缓存失效
  → 封禁链接并验证不再跳转
```

### 15.4 故障测试

| 场景 | 预期结果 |
| --- | --- |
| Redis 停止 | 有效链接回源 PostgreSQL，跳转仍成功 |
| PostgreSQL 停止、Redis 命中 | 缓存内链接仍能跳转 |
| PostgreSQL 停止、Redis miss | 返回 503 |
| Worker 消费后、ACK 前退出 | 消息重新投递但统计不重复 |
| Worker 长时间停止后执行 trimming | 所有 pending 消息正文仍可恢复 |
| Redis 主进程异常退出 | 事件损失不超过声明的 AOF durability 窗口 |
| Redis Streams 积压 | Worker 恢复后继续消费，监控触发告警 |
| 同 code 并发创建 | 只有一个请求成功，其余返回冲突 |

### 15.5 压测

使用 k6 或 Vegeta，固定记录：

- CPU、内存、实例数。
- PostgreSQL、Redis 是否与应用同机。
- 数据量和热点分布。
- 缓存命中率。
- 请求并发数和连接复用方式。
- QPS、p50、p95、p99、错误率。

压测矩阵：

| 场景 | 数据分布 | 目的 |
| --- | --- | --- |
| 100% Redis hit | Zipf 热点 | 测试 Redirect API 上限 |
| 95% hit | 真实混合 | 常规容量基线 |
| 0% hit | 均匀随机 | 测试 PostgreSQL 回源和负缓存 |
| 热 key 突发 | 单 code | 验证 singleflight 与连接池 |
| Redis 故障 | 95% 原应命中 | 验证降级和数据库保护 |
| Stream 积压 | Worker 降速 | 验证背压与恢复能力 |

初始验收目标可以设为：在明确记录的 4C8G 测试环境中、95% 缓存命中条件下，单实例达到 5,000 QPS，p99 小于 20 ms，错误率小于 0.1%。该目标必须根据实测修订，不能直接写入简历作为结果。

---

## 16. Docker 与本地开发

### 16.1 Compose 服务

```text
postgres
redis
migrate
control-api
redirect-api
event-worker
prometheus（可选 profile）
grafana（可选 profile）
```

要求：

- PostgreSQL 和 Redis 镜像固定大版本，不使用 `latest`。
- 本地基线使用 PostgreSQL 17 与 Redis 7.2；Redis 开启 AOF、持久化 volume 和 `maxmemory-policy noeviction`。
- 使用 healthcheck，不仅依赖容器启动顺序。
- `migrate` 成功后 API 才启动。
- 应用从环境变量读取配置，不把密码提交到仓库。
- Dockerfile 使用非 root 用户和最小运行镜像。
- `HTTP_ADDR` 默认 `:8080`，不要求容器内绑定特权端口 80。

### 16.2 启动命令

```text
make dev-up
make migrate-up
make test
make test-integration
make lint
make bench
make dev-down
```

---

## 17. 分阶段实施计划

以下时间是单人全职开发的粗略区间，不是交付承诺。每个 Phase 必须先满足完成标准再进入下一阶段；基础日志、request ID、错误指标和依赖健康指标从 Phase 0 开始建设，不等待最终工程化阶段。

### Phase 0：PostgreSQL 基础与剩余正确性修复

预计 1～2 周。

- 编写迁移 ADR：若只有测试数据则直接建立 v2 Schema；若有真实数据则执行等价迁移和二阶段 schema migration。
- 扩充现有行为回归测试，并保留 `a0dd106`、`4d53dd5`、`414a354` 已修复行为。
- 接入 GORM PostgreSQL driver。
- 重构数据库配置和连接池。
- 增加显式 SQL migration。
- 按 ADR 迁移需要保留的数据并校验。
- 去除业务路径中的 `panic`。
- 使用 PostgreSQL 唯一约束处理短码碰撞。
- 修复 Docker 配置路径和环境变量。
- 移除 Bloom Filter 的强依赖，使用负缓存。
- 增加 liveness/readiness。
- 增加结构化日志、request ID、基础 HTTP/DB/Redis 错误指标。

**完成标准：** 原有创建、跳转、过期和访问计数功能在 PostgreSQL 上通过单元、集成和 E2E 测试，Redis 停止时有效链接仍可回源跳转。

### Phase 1：产品化控制面

预计 2～3 周。

- tenants、api_keys、domains、links v2 schema。
- 使用组合外键和 tenant-scoped Repository 保证跨租户关系不可写入。
- API Key 鉴权。
- 自定义域名和自定义 code。
- 创建幂等。
- 链接查询、修改、删除、封禁。
- 游标分页。
- 租户级限流与审计日志。
- OpenAPI 文档。

**完成标准：** 两个租户之间数据完全隔离；相同域名下 code 唯一；重复创建请求不会产生多条记录。

### Phase 2：重定向数据面

预计 1～2 周。

- 拆出 `redirect-api` 启动入口。
- 规范化 host/code 查询。
- Redis 正缓存和负缓存。
- 实例内缓存和 singleflight。
- versioned Redis CAS、tombstone、本地短 TTL 和 Pub/Sub 尽力失效。
- 分层故障降级。
- 独立压测基线。

**完成标准：** Redis 故障可回源，PostgreSQL 故障时缓存命中仍可服务；缓存更新后不存在长期旧跳转。

### Phase 3：访问事件 Worker

预计 2 周。

- Redis Streams producer。
- Consumer Group Worker。
- pending 恢复、重试和死信 Stream。
- AOF/noeviction 配置、安全 `MINID` trimming 和积压告警。
- click_events 幂等落库。
- 小时级聚合表。
- 记录计数 cutover 时间，停止更新 `legacy_visit_count`，统计查询合并历史总量与新聚合。
- stats 查询接口。

**完成标准：** Worker 在数据库提交后、ACK 前崩溃，重新处理不会重复统计；trimming 不删除 pending 正文；Redis 异常退出的事件损失符合声明的 durability 窗口。

### Phase 4：可观测性与工程化

预计 1～2 周。

- 完善结构化日志、指标字段和脱敏规则。
- Prometheus 指标与 Grafana dashboard。
- OpenTelemetry tracing。
- CI：test、race、lint、build、migration test。
- 压测报告。
- Redis/PostgreSQL/Worker 故障演练报告。

**完成标准：** README 提供可复现环境、架构图、实测数据和故障结果。

---

## 18. 验收标准

### 18.1 功能

- 支持租户、API Key、自定义域名和短码。
- 支持链接创建、查询、更新、封禁和过期。
- 支持幂等创建和游标分页。
- 支持异步访问事件与小时级统计。

### 18.2 正确性

- 短码唯一性由 PostgreSQL 约束保证。
- domain/link、link/event 的跨租户关联由组合外键拒绝，所有管理查询按 tenant scope 执行。
- 所有数据库操作携带 context。
- 不存在请求路径和后台 goroutine `panic`。
- 重复事件不重复增加统计。
- Stream trimming 不删除 pending 消息正文，事件损失不超过明确声明的 Redis durability 窗口。
- 旧缓存回填不能覆盖更高 version 或 tombstone，缓存命中不得越过链接过期时间。
- 所有错误响应结束当前 Handler。

### 18.3 可用性

- Redis 故障不会令所有有效链接失效。
- Worker 故障不会阻止跳转。
- PostgreSQL 与 Redis 的错误可观测且分类明确。
- 缓存失效丢失时，旧跳转的最长存活时间不超过配置的本地缓存 TTL。
- 所有进程支持有超时的优雅关停。

### 18.4 工程质量

- 使用 SQL migration，不在生产启动时 AutoMigrate。
- CI 执行单元测试、集成测试、`go test -race` 和 lint。
- Docker Compose 可以一条命令启动。
- 数据保留策略明确，物理清理不会违反事件或聚合外键。
- 核心模块有接口边界，但不存在无业务价值的过度抽象。
- README 中所有性能声明均可复现。

---

## 19. 关键取舍

### 19.1 PostgreSQL，而不是纯 KV

重定向映射确实适合 KV，但多租户、域名、权限、幂等、管理查询和事务更适合关系数据库。Redis 已经承担高频点查，PostgreSQL 作为事实源能用更低复杂度提供正确性。

未来如需实验 TiKV，可抽象只包含 `Get/Put/Delete` 的 MappingStore，并提供可选实现及 benchmark；不让它成为 v2 的部署前提。

### 19.2 GORM，而不是立即切换 sqlc

当前目标是降低数据库迁移风险，因此继续使用 GORM。通过 Repository、显式 SQL migration、`WithContext` 和 `OnConflict` 约束其使用方式。

如果后续复杂查询增多或希望进一步展示 SQL 能力，可以逐步将热点查询迁移到 `pgx/sqlc`，不在本次迁移中同时更换 ORM。

### 19.3 Redis Streams，而不是 Kafka

现有系统已经依赖 Redis，Streams 能展示 Consumer Group、至少一次投递、pending 恢复和幂等消费，而不显著增加运维成本。当事件吞吐、保留期或多下游需求超过 Redis Streams 能力时，再评估 Kafka/NATS JetStream。

### 19.4 模块化单仓，而不是立即微服务

三个进程解决的是不同扩容和故障边界，但仍共享领域模型和基础设施代码。单仓可以控制开发成本，保留未来独立部署能力。

### 19.5 移除 Bloom 强依赖

在没有容量证据时，负缓存和限流更容易保证正确性。Bloom 可以是优化，但不能成为有效链接能否访问的事实来源。

---

## 20. README 与项目包装

README 首页建议按以下顺序：

1. 一句话定位。
2. 在线演示或 API 示例。
3. 架构图。
4. 核心特性。
5. 一键启动。
6. 一致性和故障降级设计。
7. Grafana 截图。
8. 压测环境与真实结果。
9. 故障演练结果。
10. ADR/设计文档链接。

可以增加以下设计文档：

```text
docs/adr/0001-use-postgresql.md
docs/adr/0002-cache-aside-and-fallback.md
docs/adr/0003-redis-streams-delivery-semantics.md
docs/adr/0004-remove-bloom-from-correctness-path.md
docs/benchmarks/redirect-baseline.md
docs/runbooks/redis-outage.md
docs/runbooks/postgres-outage.md
```

升级完成后的简历描述模板：

> 使用 Go 构建多租户短链接与访问分析平台，将管理控制面、低延迟重定向数据面和异步事件 Worker 解耦；以 PostgreSQL 唯一约束和幂等键保证创建正确性，基于 Redis 多级缓存、故障回源和 singleflight 优化高并发访问，并通过 Redis Streams Consumer Group 与业务幂等实现可恢复的访问事件聚合。在明确记录的测试环境中达到 **X QPS / Y ms p99 / Z% 错误率**。

其中 X、Y、Z 必须替换成实测结果。

---

## 21. 后续可选演进

以下能力只有在 Phase 0～4 完成后再考虑：

- ClickHouse 存储大规模原始点击事件。
- TiKV/CockroachDB/TiDB 存储实验与对比 benchmark。
- NATS JetStream 或 Kafka 替换 Redis Streams。
- 基于地域的重定向规则和 A/B 分流。
- CDN/Edge KV 发布只读映射。
- Kubernetes HPA、PDB 和多可用区部署。
- 自定义域名证书自动化。
- 链接安全扫描与风控管理后台。

这些扩展必须由实际问题或实验目标驱动，不能仅用于增加技术栈数量。

---

## 22. 参考资料

- [ShortLink 当前仓库](https://github.com/CiroLong/shortlink)
- [GORM：连接 PostgreSQL](https://gorm.io/docs/connecting_to_the_database.html)
- [GORM：索引](https://gorm.io/docs/indexes.html)
- [GORM：约束](https://gorm.io/docs/constraints.html)
- [PostgreSQL：INSERT / ON CONFLICT](https://www.postgresql.org/docs/current/sql-insert.html)
- [PostgreSQL：约束与组合外键](https://www.postgresql.org/docs/current/ddl-constraints.html)
- [PostgreSQL：事务隔离](https://www.postgresql.org/docs/current/transaction-iso.html)
- [PostgreSQL：表分区](https://www.postgresql.org/docs/current/ddl-partitioning.html)
- [Redis Streams](https://redis.io/docs/latest/develop/data-types/streams/)
- [Redis：XTRIM](https://redis.io/docs/latest/commands/xtrim/)
- [Redis：持久化](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/)
- [Redis：Pub/Sub 交付语义](https://redis.io/docs/latest/develop/pubsub/)
- [Prometheus：监控 Go 应用](https://prometheus.io/docs/guides/go-application/)
- [OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/)
