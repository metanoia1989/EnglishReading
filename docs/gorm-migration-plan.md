# GORM 改造规划

> 目标：把数据层从 `database/sql` + 手写 SQL 迁移到 **GORM**，一套模型同时支持
> **SQLite（本地零依赖开发）** 与 **MySQL 5.7（服务器部署）**。
> HTTP API 与前端**完全不变**，行为保持等价。
>
> 前置事实见 `docs/mysql-migration-assessment.md`（含已实测结清的结论）。

---

## 0. 范围与前提

| 项 | 决定 |
| --- | --- |
| 引擎 | 双引擎保留：`DB_DRIVER=sqlite`（默认）/ `mysql` |
| Schema 来源 | **GORM `AutoMigrate` + 模型标签**（单一来源，这正是选 ORM 的主要收益） |
| HTTP API | 不变，前端零改动 |
| 本地数据 | 允许重建（SQLite 内仅 1 测试用户 / 2 标注 / 4 批注，词典与文章自动重灌） |
| 服务器数据 | `english_reading` 库为空，无数据迁移 |
| 不在本次范围 | 不改前端；不引入 `context` 传递（见 §8）；不做生产部署 |

---

## 1. 依赖选型

| 依赖 | 版本 | 理由 |
| --- | --- | --- |
| `gorm.io/gorm` | v1.31.2 | 主体 |
| `gorm.io/driver/mysql` | v1.6.0 | 内部走 `go-sql-driver/mysql` |
| **`github.com/glebarez/sqlite`** | v1.11.0 | **纯 Go**（底层 `modernc.org/sqlite`），保住项目「无 CGO」特性 |

⚠️ **不用 `gorm.io/driver/sqlite`**：它依赖 `mattn/go-sqlite3`，**需要 CGO**，
等于放弃项目当前 `modernc.org/sqlite` 纯 Go 的可交叉编译优势。

已验证：`CGO_ENABLED=0` 下 `glebarez/sqlite` 可正常打开、AutoMigrate、upsert、错误归一。

---

## 2. 目录结构

```
backend/internal/store/
  config.go    # 环境变量 → Config{Driver, DSN, LogLevel}
  open.go      # 按 driver 打开 *gorm.DB；连接池、TranslateError、静默日志
  models.go    # 全部 GORM 模型（schema 唯一来源）+ TableName 覆盖
  migrate.go   # AutoMigrate
  errors.go    # IsDuplicate(err) —— 跨引擎重复键判断
  upsert.go    # UpsertReturningID(...) —— 可移植的「upsert 并拿 id」
```

server 层**直接用 `*gorm.DB`**（GORM 本身就是抽象层），不再单独包 repository。
这样后续加查询最省事 —— 符合「以后要改代码，有 ORM 写起来方便」的诉求。

`Server` 结构体字段：`db *sql.DB` → **`db *gorm.DB`**。

---

## 3. 模型清单

### 3.1 表名复数化陷阱（已实测）

GORM 用 `jinzhu/inflection` 复数化表名。实测 `Dictionary` → **`dictionaries`**（错）。
必须显式覆盖的 3 张：

| 模型 | GORM 默认推导 | 实际表名 | 处理 |
| --- | --- | --- | --- |
| `Dictionary` | `dictionaries` ❌ | `dictionary` | `TableName()` |
| `TranslationCache` | `translation_caches` ❌ | `translation_cache` | `TableName()` |
| `Meta` | `metas` ❌ | `meta` | `TableName()` |

其余推导正确：`User→users`、`Session→sessions`、`Dataset→datasets`、`Article→articles`、
`Paragraph→paragraphs`、`WordAnnotation→word_annotations`、`Note→notes`、
`PendingRegistration→pending_registrations`、`UserTranslation→user_translations`。

### 3.2 字段类型映射要点

- **短文本用 `size:` 标签** → MySQL 生成 `VARCHAR(n)`；不写 `size` 会变 `longtext`
- **长文本用 `type:text`**，且**绝不能加 `default:`**（实测 MySQL 报 1101）
- 主键统一 `int64` + `gorm:"primaryKey"` → 两引擎各自生成 `INTEGER PRIMARY KEY AUTOINCREMENT` / `BIGINT AUTO_INCREMENT`
- 外键用 `constraint:OnDelete:CASCADE`
- 时间字段一律 `time.Time`（MySQL 侧实测 RFC3339 字符串会被 1292 拒绝，必须走 time.Time）

### 3.3 词典大小写不敏感（唯一需要按方言分叉的类型）

SQLite 需要 `COLLATE NOCASE`，MySQL 靠库默认 `utf8mb4_general_ci`。用自定义类型解决：

```go
type WordKey string

func (WordKey) GormDBDataType(db *gorm.DB, field *schema.Field) string {
    if db.Dialector.Name() == "sqlite" {
        return "TEXT COLLATE NOCASE"
    }
    return "VARCHAR(64)"
}
```

已实测：存 `Paris`、查 `paris` 命中。
（数据侧也确认过：89,501 词最长 24 字符、全 ASCII、大小写冲突 0 条。）

### 3.4 `meta.key` 保留字

SQLite 下裸用 `key` 没问题，MySQL 下会 **1064**。GORM 在 MySQL 上自动用反引号，
所以**保留列名 `key` 即可**，无需改名。已实测确认（裸用报错、反引号正常、GORM 建表成功）。

---

## 4. 五个必须专门处理的技术点

### 4.1 ⚠️ upsert 后拿 id（最关键，实测踩过）

GORM 的 `clause.OnConflict` 在 SQLite 上会自动加 `RETURNING id`，MySQL 上**被静默丢弃**，
退回 `LastInsertId()`。实测 MySQL 行为：

| 场景 | affected | LastInsertId | |
| --- | ---: | ---: | --- |
| 新插入 | 1 | 正确 | ✅ |
| 更新且值有变化 | 2 | 正确 | ✅ |
| **真 no-op（值完全相同）** | 0 | **0** | ❌ |

本项目 upsert 带 `updated_at = CURRENT_TIMESTAMP`，**同一秒内重复保存同一词同一位置即命中 no-op**
→ 返回 id=0 → 前端拿 0 去删除会失败。

**方案**：`store.UpsertReturningID` —— 事务内 upsert 后按唯一键 `SELECT id`。
两引擎行为完全一致，**不含任何方言分支**：

```go
func UpsertReturningID(db *gorm.DB, model any, conflict clause.OnConflict,
    uniqueWhere string, uniqueArgs []any, dest *int64) error {
    return db.Transaction(func(tx *gorm.DB) error {
        if err := tx.Clauses(conflict).Create(model).Error; err != nil {
            return err
        }
        return tx.Model(model).Select("id").Where(uniqueWhere, uniqueArgs...).Scan(dest).Error
    })
}
```

代价是多一次走唯一索引的 SELECT，仅 2 处调用（`annotations.go:165` / `:342`），可忽略。
好处是**不依赖任何引擎的返回语义**。

> 备选：MySQL 侧加 `id = LAST_INSERT_ID(id)`（实测有效）+ SQLite 侧靠 `RETURNING`。
> 但那要引入方言分支，与「换引擎不用改代码」的目标相悖，故不采用。

### 4.2 时间字段

全部改 `time.Time` + DSN `parseTime=true&loc=UTC`。这会让评估文档 §4 的坑**自动消失**。
`auth.go` 里手工 `time.Parse(time.RFC3339, ...)` 删掉，直接比较 `time.Time`。

### 4.3 重复键判断

`strings.Contains(err.Error(), "UNIQUE")` 在 MySQL 上永不成立（报 1062）。
改用 `gorm.Config{TranslateError: true}` + `errors.Is(err, gorm.ErrDuplicatedKey)`。
已实测两引擎均生效。

### 4.4 连接配置

| 项 | SQLite | MySQL |
| --- | --- | --- |
| 连接池 | `SetMaxOpenConns(1)`（保持原样） | 放开（25 / 25 / 5m） |
| DSN 附加 | — | `charset=utf8mb4&parseTime=true&loc=UTC` |
| 注意 | — | **不要设过短 `readTimeout`**：服务器 `skip_name_resolve=OFF` 导致新建连接有 ~10s 反向 DNS 卡顿，握手读也受 `readTimeout` 约束 |

服务器侧根治（`skip_name_resolve=ON` + 重启 MySQL）属服务端配置变更，**等你确认后再动**。

### 4.5 词典批量灌入性能

现状 SQLite 约 1s（事务内 prepared statement 循环）。改 GORM 后用
`CreateInBatches(&entries, 500)` + `SkipDefaultTransaction`，
**实测计时**；MySQL 侧一并计时。若明显劣化再调 batch size 或对大表保留 `Exec` 批量插入。

---

## 5. 分步实施（每步可独立验证）

| 步骤 | 内容 | 验证 |
| --- | --- | --- |
| **1** | 加依赖；写 `store/config.go` `open.go` `models.go` `migrate.go` `errors.go` `upsert.go` | `go build`；SQLite 下 AutoMigrate 建出 13 张表 + 4 索引 |
| **2** | 迁移 `seed`（`Run(db *gorm.DB)`），批量灌词典与文章 | 词典行数 = **89501**；计时对比 |
| **3** | 迁移 `auth.go`（含时间与 1062） | 注册→验证→登录→me 全通 |
| **4** | 迁移 `content.go`（datasets/articles/dict lookup） | 三个 GET 接口返回与原一致 |
| **5** | 迁移 `annotations.go`（用 §4.1 助手） | upsert 两次同位置返回**同一非零 id** |
| **6** | `main.go` / `server.New` 签名改为 `*gorm.DB`；`DB_DRIVER`/`DB_DSN` | 进程启动，日志显示引擎 |
| **7** | 更新 README（技术栈、环境变量、启动方式） | — |
| **8** | **双引擎验证**：SQLite 全流程 + MySQL 全流程 | §6 清单 |

`verify_codes` 死表（`store.go:73`，全项目无引用）**不迁移**。

---

## 6. 验收清单

对 **SQLite 与 MySQL 各跑一遍**：

- [ ] 注册全流程：register → devCode → verify → token（覆盖评估文档 §4 的时间坑）
- [ ] 重复邮箱注册 → **409**（不是 500）
- [ ] 登录 → `GET /api/auth/me`
- [ ] `GET /api/dict/lookup?word=The` 与 `?word=the` **都命中**（大小写不敏感）
- [ ] `GET /api/datasets` / `/datasets/{id}/articles` / `/articles/{id}`（中文标题正常）
- [ ] **标注 upsert 两次同一位置 → 返回同一个非零 id**（§4.1 的坑）
- [ ] 批注 / 翻译 增删 → `GET /articles/{id}/state` 一致
- [ ] 中文与 emoji（📚 🦊）无乱码
- [ ] 词典 `COUNT(*)` = **89501**
- [ ] 冷启动建表后再启动一次 → 幂等，seed 不重复灌

---

## 7. 风险与回滚

| 风险 | 缓解 |
| --- | --- |
| 行为不等价（接口返回结构变化） | 逐接口对比迁移前后响应；前端零改动是最强的回归信号 |
| AutoMigrate 在 MySQL 5.7 生成非法 DDL | 步骤 1 先在空库跑一遍，人工核对 `SHOW CREATE TABLE` |
| 词典灌入变慢 | 步骤 2 计时，超阈值改批量参数 |
| 服务器 10s 连接卡顿 | 客户端超时先绕过；根治待你确认 |
| 改坏了 | 单 commit 提交，`git revert` 即可；本地 SQLite 删库自动重建 |

---

## 8. 明确不做

- **不引入 `context` 传递**：本次目标是行为等价的迁移，混入 `WithContext` 会扩大 diff 与验证面。
  建议作为迁移完成后的独立小改动（后续想加，每个 handler 一行即可）。
- 不改前端、不改 HTTP 路由与响应结构。
- 不部署、不改服务器 MySQL 配置。

---

## 9. 实施结果（已完成）

8 个步骤全部完成，两个引擎都过了验收。

### 9.1 交付物

| 文件 | 说明 |
| --- | --- |
| `internal/store/models.go` | 12 个 GORM 模型 = schema 唯一来源 |
| `internal/store/open.go` | 引擎选择、DSN 补全、连接池 |
| `internal/store/config.go` | `DB_DRIVER` / `DB_DSN` / `DB_PATH` / `DB_LOG` |
| `internal/store/migrate.go` | `AutoMigrate` + 清理过期会话 |
| `internal/store/errors.go` | `IsDuplicate` / `IsNotFound` |
| `internal/store/upsert.go` | `UpsertReturningID` |
| `internal/store/store_test.go` | 8 个用例 × 双引擎 |
| `internal/seed/seed_test.go` | 3 个用例 × 双引擎 |
| 删除 | `internal/store/store.go`（旧的手写 SQLite 层） |

### 9.2 验证结果

| 项目 | 结果 |
| --- | --- |
| `go build` / `go vet` / `gofmt` | 全部干净 |
| `CGO_ENABLED=0 go build` | ✅ 纯 Go 特性保住（用 `glebarez/sqlite` 而非 `gorm.io/driver/sqlite`） |
| 单元测试 `go test -count=1 -p 1 ./...` | ✅ SQLite + MySQL 全绿（11 个用例 × 双引擎） |
| 接口冒烟 **42 项** | ✅ SQLite 42/42 ｜ MySQL 42/42 |
| MySQL **冷启动**（空库 → 建表 + 灌库） | ✅ 42/42，词典 89501 行 |
| 重启幂等 | ✅ 不重复灌库，`dictionary=89501 datasets=3 articles=10 paragraphs=113` |
| 词典灌库耗时 | SQLite **0.5s** ｜ MySQL **5.9s**（原 SQLite 约 1s，未劣化） |

### 9.3 过程中发现并修掉的真实 bug

**GORM 的 `default:` 标签会吞掉零值** —— 计划里没有预见，是冒烟测试抓出来的。

`Note.SentenceIndex` 与 `UserTranslation.SentenceIndex` 原本带 `default:-1`。
GORM 会把「带 `default` 标签且值为零」的字段**从 INSERT 中省略**，让数据库默认值生效。
于是 `sentence_index = 0` —— **每个段落的第一句**，最常见的情况 —— 被静默写成了 `-1`
（整段锚点）。表现是翻译接口 500 `upsert: row missing after write`，
而句子批注则会挂到整段上。

修复：去掉这两个字段的 `default:` 标签，并加回归测试
`TestZeroValuedSentenceIndexIsPersisted` 守住。`models.go` 里留了注释解释该陷阱。

> 教训：**凡应用总是显式赋值的字段，都不要带 `default:` 标签**。
> 其余字符串列的零值 `""` 恰好等于其默认值，因此暂时安全，但新增字段时要逐个确认。

### 9.4 与计划的偏差

| 计划 | 实际 | 原因 |
| --- | --- | --- |
| §4.1 用 `UpsertReturningID`（事务 + 事后 SELECT） | 照做 | 实测确认必要：MySQL 真 no-op 时 `LastInsertId()=0` |
| §3.3 用 `GormDBDataType` 区分方言 | 照做 | 实测 SQLite `TEXT COLLATE NOCASE` 生效 |
| §3.4 保留 `meta.key` 列名 | 照做 | GORM 自动加反引号，实测通过 |
| 计划外 | 新增 `DB_LOG` 环境变量 | 默认 `warn` 会把 `record not found` 刷屏，改为忽略该错误 |
| 计划外 | MySQL 测试必须 `-p 1` | 两个测试包共用一个库，Go 默认并行跑包会互踩 schema |

### 9.5 仍待你决定

服务器 `skip_name_resolve=OFF` 导致**每次新建连接约 10s**（评估文档 §10）。
客户端已按「不设过短 `readTimeout`」绕过，连接池也会摊薄；
根治需在服务端 `my.cnf` 加 `skip_name_resolve=ON` 并重启 MySQL —— 属于服务器配置变更，未动。
