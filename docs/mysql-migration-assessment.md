# SQLite → MySQL 改造评估

> **结论：能换，是个小工程（45 处 SQL 调用点，其中仅 6 条 DML 带方言）。**
> **ORM（GORM）的方言抽象是真实有效的**（`clause.OnConflict` 与错误归一实测可用），
> 但它恰好解决不了本项目最关键的一处（upsert 后拿 id，MySQL 上 `RETURNING` 被静默丢弃），
> 且引入跨引擎返回值不一致的新分歧面。详见 §6 —— 其中包含对本文早期结论的修正。
>
> 本文档为纯评估，**未改动任何代码**。
> 目标环境：宝塔服务器 MySQL **5.7.44**，库 `english_reading`（已建好、当前为空）。

---

## 1. 现状盘点

### 1.1 代码侧

| 项 | 值 |
| --- | --- |
| 后端规模 | 1866 行 Go（`main.go` + 6 个 internal 包） |
| 驱动 | `modernc.org/sqlite`（纯 Go，无 CGO） |
| SQL 调用点 | 45 处（server 层 31 · `seed` 11 · `store` 3） |
| 入口 | 全项目**只有一处** `sql.Open` → `store.Open()`（`internal/store/store.go:22`） |

**关键事实：所有 SQL 都走 `database/sql` + `?` 占位符。** `go-sql-driver/mysql` 同样使用 `?`，
因此 **server 层的调用点一行都不用改**。方言只泄漏在少数几条语句里（见 §3 改造清单）。

### 1.2 数据侧

本地 SQLite（`backend/data/app.db`，11 MB）实际行数：

| 表 | 行数 | 是否需迁移 |
| --- | ---: | --- |
| `dictionary` | 89,501 | ❌ 启动时从 `dict_seed.json` 自动重灌 |
| `datasets` / `articles` / `paragraphs` | 3 / 10 / 113 | ❌ 同上（`articles_seed.json`） |
| `users` | 1 | ⚠️ 仅测试账号 |
| `sessions` | 3 | ❌ 可丢弃 |
| `word_annotations` / `notes` / `user_translations` | 2 / 4 / 4 | ⚠️ 仅测试数据 |
| `translation_cache` | 4 | ❌ 可丢弃（会自动重建） |

→ **零 ETL。** 换库后 `go run .` 会自动建表 + 重灌词典与文章。
（若想保留那 1 个账号，手写 20 行 INSERT 即可，不值得写迁移工具。）

### 1.3 一个附带发现

`verify_codes` 表（`store.go:73`）**建了但全项目没有任何引用**，是死表。迁移时可顺手删掉。

---

## 2. 目标环境实测事实

以下均为在宝塔服务器上实际查询得到，不是推断：

| 项 | 实测值 | 影响 |
| --- | --- | --- |
| 版本 | `5.7.44-log` | **不是 8.0**。无 CTE / 窗口函数 / `utf8mb4_0900_ai_ci` / 函数索引 / CHECK 约束 / `RETURNING` |
| `sql_mode` | `STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION` | 好事：超长/非法值会报错而非静默截断 |
| 字符集 | `utf8mb4` / `utf8mb4_general_ci` | ✅ 中文与 emoji 安全 |
| 行格式 | `dynamic`，`innodb_large_prefix=1` | 索引键上限 3072 B，`VARCHAR(191)` 的 utf8mb4 唯一键安全 |
| 时区 | `system_time_zone = UTC` | ✅ 与 Go 侧全程 UTC 一致，无时基错配 |
| `max_allowed_packet` | 1 GB | 词典批量灌入无压力 |
| 库 `english_reading` | 已存在，`SHOW TABLES` 返回 0 行 | 空的，等代码来建表 |

### 2.1 时间字符串实测（重要 ⚠️ 本节结论已修正）

先看 `CAST()` 的行为（宝塔 MCP 只读查询）：

```
SELECT CAST('2025-09-21T11:00:00Z' AS DATETIME)      → 2025-09-21 11:00:00   (IS NULL = 0)
SELECT CAST('2025-09-21T11:00:00+08:00' AS DATETIME) → 2025-09-21 11:00:00
```

**但 `CAST()` 宽容 ≠ 能写进列。** 拿到服务器写权限后用 go-sql-driver 实测 INSERT：

```
INSERT INTO _probe_time (expires_at) VALUES ('2026-09-21T09:56:35Z')
→ ❌ Error 1292 (22007): Incorrect datetime value: '2026-09-21T09:56:35Z' for column 'expires_at'
```

**结论修正**：在 `STRICT_TRANS_TABLES` 下，RFC3339 字符串**根本写不进 DATETIME 列，会直接报 1292**——
不是本文早期版本说的「存得进去、`Z` 被静默丢弃」。`CAST()` 是表达式层面的宽容转换，
列赋值走的是更严格的校验，两者行为不一致。

这其实是**好消息**：失败是响亮的（1292），不是静默写坏数据。但它意味着
**现有 Go 代码写时间的每一处都必须改成绑定 `time.Time`**（`auth.go:86,250`、`store.go:177`），
否则注册第一步就 500。详见 §4。

### 2.2 词典数据实测

```
条目数 89501 ｜ 最长词 24 字符 ｜ 全 ASCII ｜ 大小写不敏感冲突 0 条
phonetic 最长 43 字符 ｜ senses_json 最长 242 字符 / 480 字节
```

→ `VARCHAR(64)` 主键 + `utf8mb4_general_ci`（默认即大小写不敏感）与 SQLite 的 `COLLATE NOCASE` 语义等价，
`INSERT IGNORE` 会恰好灌入 89,501 行，无冲突、无截断。

---

## 3. 改造清单

### 3.1 DML 方言（6 条语句）

| # | 位置 | SQLite | MySQL 5.7 替换 |
| --- | --- | --- | --- |
| 1 | `annotations.go:165-174` | `ON CONFLICT(...) DO UPDATE ... RETURNING id` | 无 `RETURNING`（5.7/8.0 都没有，那是 MariaDB）。用 `ON DUPLICATE KEY UPDATE ..., id = LAST_INSERT_ID(id)`，再取 `res.LastInsertId()` —— 原子且等效 |
| 2 | `annotations.go:342-351` | 同上（`user_translations`） | 同上 |
| 3 | `auth.go:87-95` | `ON CONFLICT(email) DO UPDATE SET ... excluded.x` | `ON DUPLICATE KEY UPDATE col = VALUES(col)` |
| 4 | `seed.go:75-77` | 同上（`meta` 表） | 同上 |
| 5 | `seed.go:109-110` | `INSERT OR IGNORE INTO dictionary` | `INSERT IGNORE INTO dictionary` |
| 6 | `annotations.go:377-378` | `INSERT OR IGNORE INTO translation_cache` | `INSERT IGNORE` |

> ⚠️ MySQL 8.0.20+ 起 `VALUES(col)` 被标记废弃（仍可用）。目标机是 5.7.44，**必须用 `VALUES()`**；
> 若将来升 8.x，再改成行别名 `AS new ... new.col`。

### 3.2 DDL 方言（17 条：13 建表 + 4 建索引）

| # | SQLite 写法 | 出现位置 | MySQL 5.7 处理 |
| --- | --- | --- | --- |
| 1 | `INTEGER PRIMARY KEY AUTOINCREMENT` | `store.go` 61, 86, 94, 102, 116, 132, 154（7 处） | `BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY` |
| 2 | `TEXT PRIMARY KEY` | `store.go` 68 (`sessions.token`), 74, 79 (`email`), 143 (`source_hash`), 150 (`meta.key`) | 改 `VARCHAR(n)`。**TEXT 不能直接做主键**，且见 #3 |
| 3 | `TEXT PRIMARY KEY COLLATE NOCASE` | `store.go:110` (`dictionary.word`) | `VARCHAR(64) NOT NULL PRIMARY KEY`（`utf8mb4_general_ci` 天然大小写不敏感） |
| 4 | `TEXT ... DEFAULT '...'` | **11 个列**（见下） | 🚨 **MySQL 不允许 BLOB/TEXT 带 DEFAULT**（Error 1101），全部必须改 `VARCHAR` |
| 5 | `CREATE INDEX IF NOT EXISTS` | `store.go` 108, 129, 141, 165（4 处） | 🚨 **MySQL 不支持 `CREATE INDEX IF NOT EXISTS`**（那是 MariaDB）。改为在 `CREATE TABLE` 里内联 `KEY idx_x (cols)` |
| 6 | `PRAGMA journal_mode=WAL / foreign_keys / busy_timeout / synchronous` | `store.go:45-48` | 删除；改用 DSN 参数，InnoDB 外键默认开启 |
| 7 | `PRAGMA journal_mode=OFF` / `WAL`（灌库加速） | `seed.go:98, 101` | 删除；MySQL 侧靠单事务 + `INSERT IGNORE` 已足够 |
| 8 | （SQLite 无此问题） | — | 建表统一补 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci` |

**#4 受影响的 11 个列**（naive 移植会在 `migrate()` 首跑就失败）：

```
users.nickname                 · datasets.description / .emoji / .color
articles.subtitle / .level     · paragraphs.kind
dictionary.phonetic / .senses_json / .source
word_annotations.pos / .sense  · translation_cache.target
pending_registrations.nickname
```

### 3.3 保留字陷阱

`meta` 表的列名 **`key` 是 MySQL 保留字**（SQLite 下无碍）。
以下语句在 MySQL 会直接 **1064 语法错误**：

- `seed.go:64` — `SELECT value FROM meta WHERE key = ?`
- `seed.go:75` — `INSERT INTO meta(key, value) VALUES(?, ?) ...`

建议**直接把列改名 `name`**（比到处加反引号干净）。其余列名（`value`/`level`/`kind`/`source`/`target`/`seq`/`pos`）在 MySQL 中均非保留字，安全。

### 3.4 连接与配置

| 位置 | SQLite 现状 | MySQL |
| --- | --- | --- |
| `store.go:22` | `sql.Open("sqlite", path)` | `sql.Open("mysql", dsn)` |
| `store.go:28-30` | `SetMaxOpenConns(1)` / `SetMaxIdleConns(1)` / `SetConnMaxLifetime(0)` | **删除**（SQLite 专用调优，MySQL 应放开池，如 25/25/5m） |
| `main.go:15` | `DB_PATH=data/app.db` | 建议改 `DB_DSN=user:pass@tcp(127.0.0.1:3306)/english_reading?...` |
| `main.go:16` | 目录探测（`firstExisting`） | 无关，不动 |

推荐 DSN：

```
english_reading:PASS@tcp(127.0.0.1:3306)/english_reading
  ?charset=utf8mb4
  &collation=utf8mb4_general_ci
  &parseTime=true
  &loc=UTC
  &timeout=5s&readTimeout=30s&writeTimeout=30s
```

> `charset=utf8mb4` 是**必须**的：连成 3 字节 `utf8` 会让 `datasets.emoji` 的 📚 写不进去。

### 3.5 Go 侧行为差异（3 个静默 bug，最危险的部分）

| # | 位置 | 问题 | 修法 |
| --- | --- | --- | --- |
| 1 | `auth.go:160` | `strings.Contains(err.Error(), "UNIQUE")` 判断重复邮箱。MySQL 报的是 `Error 1062: Duplicate entry '...' for key 'email'`，**不含 "UNIQUE" 一词** → 重复注册会静默从 **409 变成 500** | 判 `*mysql.MySQLError.Number == 1062`，或 `strings.Contains(msg, "1062") \|\| Contains(msg, "Duplicate entry")` |
| 2 | `auth.go:120-141` | 见 §4，**邮箱验证码会永远判定过期，注册流程直接死掉** | 见 §4 |
| 3 | `store.go:176-177` | `DELETE FROM sessions WHERE expires_at <= ?` 绑定 RFC3339 字符串 | 可工作（§2.1 已验证），但建议改绑 `time.Time` |

> #1 和 #2 是「能用但行为悄悄变了」的类型 —— 编译通过、启动成功、主流程正常，
> 只有注册/重复注册这两条路径会坏。移植后**必须手工验证这两条**。

---

## 4. 时间字段专项（最易踩的坑）

现状：`expires_at` 由 Go 写入 RFC3339 字符串，列类型是 `DATETIME`。

**写入侧**（§2.1 实测）：MySQL 上 RFC3339 字符串会被 **Error 1292 直接拒绝** —— 所以第一步就炸，
连读回来的机会都没有。

**读取侧**（若写入绕过）：

| DSN `parseTime` | 驱动返回类型 | 结果 |
| --- | --- | --- |
| `false`（默认） | DATETIME → `[]byte("2025-09-21 11:10:00")` | `auth.go:126` 扫进 `string` 成功，但 `auth.go:137` 的 `time.Parse(time.RFC3339, "2025-09-21 11:10:00")` **解析失败** → `auth.go:138` 判定「验证码已过期」，**注册流程 100% 失败** |
| `true` | DATETIME → `time.Time` | `auth.go:126` 扫进 `string` 直接报 `unsupported Scan, storing driver.Value type time.Time into type *string` → 500 |

**写入读回两条路都坏，所以这不是 DSN 开关问题，Go 代码必须改。** 三个选项：

- **(a) 推荐，也是 GORM 的默认形态**：`parseTime=true&loc=UTC`，时间字段一律 `time.Time`，
  SQL 比较绑 `time.Time`。GORM 模型里 `UpdatedAt time.Time` 天然就是这条路径，
  所以**改用 GORM 后这个问题自动消失**——这是选 GORM 的一个实际收益。
- **(b) 最小改动**：`parseTime=false`，把 `auth.go:137` 改成 `time.Parse("2006-01-02 15:04:05", ...)`。改动最小，但保留了脆弱的字符串时间约定。
- **(c) 让 MySQL 装成 SQLite**：时间列一律用 `VARCHAR(32)` 存 RFC3339。零 Go 改动，但放弃了数据库的时间类型能力，不推荐。

---

## 5. 完整 MySQL DDL 参考

按实测数据长度定标（`§2.2`），可直接用作方言层的 MySQL 分支：

```sql
CREATE TABLE IF NOT EXISTS users (
  id            BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  email         VARCHAR(191) NOT NULL UNIQUE,
  nickname      VARCHAR(64)  NOT NULL DEFAULT '',
  password_hash VARCHAR(100) NOT NULL,              -- bcrypt = 60 字符
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS sessions (
  token      VARCHAR(64) NOT NULL PRIMARY KEY,      -- hex(32B) = 64
  user_id    BIGINT      NOT NULL,
  created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME    NOT NULL,
  KEY idx_sessions_user (user_id),
  KEY idx_sessions_expires (expires_at),
  CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS pending_registrations (
  email         VARCHAR(191) NOT NULL PRIMARY KEY,
  password_hash VARCHAR(100) NOT NULL,
  nickname      VARCHAR(64)  NOT NULL DEFAULT '',
  code          VARCHAR(16)  NOT NULL,
  expires_at    DATETIME     NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS datasets (
  id          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  slug        VARCHAR(96)  NOT NULL UNIQUE,
  title       VARCHAR(191) NOT NULL,
  description VARCHAR(512) NOT NULL DEFAULT '',
  emoji       VARCHAR(16)  NOT NULL DEFAULT '📚',
  color       VARCHAR(32)  NOT NULL DEFAULT '#6366f1'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS articles (
  id         BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  dataset_id BIGINT       NOT NULL,
  title      VARCHAR(512) NOT NULL,
  subtitle   VARCHAR(512) NOT NULL DEFAULT '',
  level      VARCHAR(64)  NOT NULL DEFAULT '',
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_articles_dataset (dataset_id),
  CONSTRAINT fk_articles_dataset FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS paragraphs (
  id         BIGINT     NOT NULL AUTO_INCREMENT PRIMARY KEY,
  article_id BIGINT     NOT NULL,
  seq        INT        NOT NULL,
  kind       VARCHAR(16) NOT NULL DEFAULT 'text',
  content    TEXT       NOT NULL,                   -- 实测最长 1385 字符
  KEY idx_paragraphs_article (article_id, seq),
  CONSTRAINT fk_paragraphs_article FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS dictionary (
  word        VARCHAR(64)  NOT NULL PRIMARY KEY,    -- general_ci = 原 COLLATE NOCASE
  phonetic    VARCHAR(191) NOT NULL DEFAULT '',     -- 实测最长 43
  senses_json TEXT         NOT NULL,                -- 实测最长 242 字符
  source      VARCHAR(32)  NOT NULL DEFAULT 'ECDICT'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS word_annotations (
  id             BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  user_id        BIGINT       NOT NULL,
  article_id     BIGINT       NOT NULL,
  paragraph_id   BIGINT       NOT NULL,
  sentence_index INT          NOT NULL,
  word_index     INT          NOT NULL,
  word           VARCHAR(128) NOT NULL,
  pos            VARCHAR(32)  NOT NULL DEFAULT '',
  sense          VARCHAR(512) NOT NULL DEFAULT '',
  created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_word_annotations (user_id, article_id, paragraph_id, sentence_index, word_index),
  KEY idx_word_annotations_article (user_id, article_id),
  CONSTRAINT fk_wa_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE,
  CONSTRAINT fk_wa_article   FOREIGN KEY (article_id)   REFERENCES articles(id)   ON DELETE CASCADE,
  CONSTRAINT fk_wa_paragraph FOREIGN KEY (paragraph_id) REFERENCES paragraphs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS notes (
  id             BIGINT   NOT NULL AUTO_INCREMENT PRIMARY KEY,
  user_id        BIGINT   NOT NULL,
  article_id     BIGINT   NOT NULL,
  paragraph_id   BIGINT   NOT NULL,
  sentence_index INT      NOT NULL DEFAULT -1,
  content        TEXT     NOT NULL,                 -- 上限 2000 rune（代码已校验）
  created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_notes_article (user_id, article_id),
  CONSTRAINT fk_notes_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE,
  CONSTRAINT fk_notes_article   FOREIGN KEY (article_id)   REFERENCES articles(id)   ON DELETE CASCADE,
  CONSTRAINT fk_notes_paragraph FOREIGN KEY (paragraph_id) REFERENCES paragraphs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS translation_cache (
  source_hash     VARCHAR(64) NOT NULL PRIMARY KEY, -- sha256 hex = 64
  source_text     TEXT        NOT NULL,
  translated_text TEXT        NOT NULL,
  target          VARCHAR(16) NOT NULL DEFAULT 'zh-CN',
  created_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS meta (
  `name` VARCHAR(64)  NOT NULL PRIMARY KEY,         -- 原列名 key 是保留字，建议改名
  value  VARCHAR(191) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS user_translations (
  id              BIGINT   NOT NULL AUTO_INCREMENT PRIMARY KEY,
  user_id         BIGINT   NOT NULL,
  article_id      BIGINT   NOT NULL,
  paragraph_id    BIGINT   NOT NULL,
  sentence_index  INT      NOT NULL DEFAULT -1,
  source_text     TEXT     NOT NULL,
  translated_text TEXT     NOT NULL,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_user_translations (user_id, article_id, paragraph_id, sentence_index),
  KEY idx_user_translations_article (user_id, article_id),
  CONSTRAINT fk_ut_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE,
  CONSTRAINT fk_ut_article   FOREIGN KEY (article_id)   REFERENCES articles(id)   ON DELETE CASCADE,
  CONSTRAINT fk_ut_paragraph FOREIGN KEY (paragraph_id) REFERENCES paragraphs(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
```

`verify_codes` 表（`store.go:73`）无任何引用，建议不迁移。

---

## 6. ORM 路线评估（GORM）

> 本节结论基于**实测**，不是印象：用 `gorm.io/gorm v1.31.2` + `gorm.io/driver/mysql v1.6.0`
> + `gorm.io/driver/sqlite v1.6.0`，以 `db.ToSQL()` 生成两套方言的真实 SQL，并交叉阅读 dialector 源码。

### 6.1 GORM 确实做到了的事（比预想的强）

**① `clause.OnConflict` 是真正的方言感知**，同一段 Go 代码产出两种正确 SQL：

```go
db.Clauses(clause.OnConflict{Columns: [...], DoUpdates: clause.AssignmentColumns([]string{"word","pos","sense"})}).
   Create(&WordAnnotation{...})
```

| 方言 | 实测生成的 SQL |
| --- | --- |
| MySQL | `INSERT INTO ... VALUES (...) ON DUPLICATE KEY UPDATE \`word\`=VALUES(\`word\`),\`pos\`=VALUES(\`pos\`),\`sense\`=VALUES(\`sense\`)` |
| SQLite | `INSERT INTO ... VALUES (...) ON CONFLICT (...) DO UPDATE SET \`word\`=\`excluded\`.\`word\`,... RETURNING \`id\`` |

机制：core 的 `clause.OnConflict` 只产出 PostgreSQL/SQLite 语法，每个 dialector 通过
`ClauseBuilders()` 注册自己的 `ON CONFLICT` 构建器覆盖它（`driver/mysql/mysql.go`）。
**这消除了本项目 6 条方言 DML 中的 4 条** —— 这一点我上一版低估了。

**② 跨驱动错误归一**：`gorm.Config{TranslateError: true}` + MySQL dialector 的
`error_translator.go` 把 `1062 → gorm.ErrDuplicatedKey`。
**这正好自动修掉 §3.5 的 bug #1**（重复邮箱 409 变 500），而且是驱动无关的写法。

### 6.2 GORM 没有解决的问题（同样实测）

**① `clause.Returning` 在 MySQL 上被静默丢弃 —— 这正是本项目最需要它的地方。**

上表可见：SQLite 的 SQL 里有 `RETURNING id`（且不加 `clause.Returning` 也会自动加），
**MySQL 的 SQL 里没有**。回填主键的机制在 `callbacks/create.go`：

```go
supportReturning := utils.Contains(config.CreateClauses, "RETURNING")   // :38
if supportReturning && len(...FieldsWithDefaultDBValue) > 0 { AddClause(clause.Returning{...}) }  // :52
insertID, err := result.LastInsertId()                                  // :142
if !supportReturning { ... }                                            // :146
```

MySQL dialector 只在 **MariaDB ≥ 10.5** 时才注册 `RETURNING`（`withReturning = checkVersion(ServerVersion, "10.5")`），
真 MySQL 走 `LastInsertId()` 分支。

**拿到服务器写权限后的实测修正**（本节早期版本说「MySQL 上一律拿回 0」，不够准确）：

| 场景 | RowsAffected | `LastInsertId()` | 是否正确 |
| --- | ---: | ---: | --- |
| 新行插入 | 1 | 1 | ✅ |
| 命中唯一键，**值有变化** | 2 | 1（既有行 id） | ✅ |
| 命中唯一键，**值完全相同（真 no-op）** | 0 | **0** | ❌ |
| 真 no-op + 语句里加 `id = LAST_INSERT_ID(id)` | 0 | 1 | ✅ |

→ 危险的是**真 no-op** 那一行。而本项目的 upsert 语句带 `updated_at = CURRENT_TIMESTAMP`，
**同一秒内重复保存同一个词的同一个位置 → 命中 no-op → 拿到 id=0**，
前端拿这个 0 去调 `DELETE /api/articles/{id}/word-annotations/0` 就会失败。

所以准确表述是：**同一段 GORM 代码跨引擎行为不一致**——SQLite 上 `RETURNING id` 永远正确，
MySQL 上在 no-op 时给出 0。**值不对、但不报错**，恰恰是「用 ORM 保证跨引擎一致」最想避免的那类差异。
（注：`id = LAST_INSERT_ID(id)` 这个技巧在 GORM 全源码中 grep 不到，GORM 不会替你生成。）

**② `gorm.Open` 默认 `Ping`，要求数据库必须在线**（实测拒绝连接即失败），
而 `database/sql` 的 `sql.Open` 是惰性的。可用 `DisableAutomaticPing: true` 关掉。

**③ 离线出 SQL 也没那么直接**：`Session(&gorm.Session{DryRun: true})` 仍会 `Begin` 事务
（实测 sqlmock 报 `call to database transaction Begin was not expected`），必须用 `db.ToSQL()`。

### 6.3 修正后的判断

我上一版的结论「**ORM 不消除方言，只是搬家**」**说过头了** —— GORM 的方言抽象是真实有效的，
`ON CONFLICT` 与错误归一都是实打实的收益。更准确的表述是：

> **GORM 解决了大部分方言问题，但恰好解决不了本项目最关键的那一处（upsert 后拿 id），
> 并且引入了新的、更隐蔽的分歧面：同一段代码在两个引擎上返回值不同。**

成本端结论不变：45 处调用点重写、89,501 条词典 seed 从「事务内 prepared statement」变成 ORM 写入、
`AutoMigrate` 要复刻 `dictionary.word` 的大小写不敏感主键与 `IF NOT EXISTS` 索引语义。

**什么情况下该选 GORM**（这取决于你的判断，不是技术对错）：

| 更适合 GORM | 更适合薄方言层 |
| --- | --- |
| 认定长期双引擎 + 表会持续增加（schema 单一来源、方言 DML、错误归一的收益随时间增长） | 表结构稳定在十几张，SQL 简单直白，raw SQL 是资产 |
| 愿意接受：upsert 拿 id 仍需 raw SQL 分支，且要自己验一遍跨引擎行为一致性 | 只想改 6 条语句就拿到双引擎，成本约为前者的 1/5 |
| 团队更习惯 ORM 而非手写 SQL | 不想引入反射开销与新依赖 |

**本项目的具体约束**：45 处 SQL 里真正需要「upsert + 拿 id」的有 2 处
（`annotations.go:165`、`annotations.go:342`），而这两处正是 GORM 在 MySQL 上给不出正确结果的地方。
换句话说，**你为了跨引擎而引入 ORM，最后仍然要为这 2 处写方言分支** —— 这就是我仍推荐方案 A 的理由。
但如果你更看重「表会越来越多」这件事，选 GORM 也是站得住的，只要提前接受 6.2 的代价。

---

## 7. 推荐方案

### 方案 A（推荐）：`internal/store` 加一层薄方言层

保持 `database/sql` + 现有 raw SQL 不变，把方言收敛到一处：

```
store.Open(cfg) 按 DB_DRIVER 选路
  ├─ sqlite://  → modernc.org/sqlite  + PRAGMA + SetMaxOpenConns(1) + SQLite DDL
  └─ mysql://   → go-sql-driver/mysql + DSN 参数 + 连接池 + MySQL DDL

Dialect 接口提供：
  DriverName()            DSN()                PoolTuning(db)
  Schema()                -- 两套 DDL
  UpsertReturningID()     -- 或拆成 Upsert*() + LastInsertID()
  InsertIgnorePrefix()    IsDuplicateErr(err)  -- 1062 / UNIQUE 归一
```

- **server 层零改动**（`?` 占位符两边通用）
- 本地仍可 `DB_DRIVER=sqlite` 零依赖开发，服务器 `DB_DRIVER=mysql` 跑
- 成本约为 GORM 路线的 1/5，且不引入新依赖
- ⚠️ 注意：本地目前**没有 MySQL 在跑**（OrbStack/Docker 未启动，3306 关闭）。若改单引擎，
  本地开发就必须先起 MySQL —— 这是保留双引擎的一个实际理由

### 方案 B：直接锁死 MySQL

改动最少最快，删掉全部 SQLite 分支。代价：本地开发必须起 MySQL 实例。

### 方案 C：GORM 全量重构

不是错误选择，但代价明确：45 处调用点重写 + 新依赖 + 反射开销，而 2 处「upsert 拿 id」仍需 raw SQL 分支。
**若你判断这个项目会长期双引擎、表会持续增加**，GORM 的收益（单一 schema 源、方言感知 DML、错误归一）
会随时间增长，值得选。取舍见 §6.3。

### 执行顺序（若走方案 A）

1. `store` 包引入 `Dialect`，抽出两套 DDL + 连接配置（不动 server 层）
2. 改 6 条 DML 方言（§3.1），其中 upsert 用 `LAST_INSERT_ID(id)` 技巧
3. 修 3 个静默 bug（§3.5）：1062 映射、`auth.go` 时间解析、`meta.key` 改名
4. `main.go` 读 `DB_DRIVER` / `DB_DSN`
5. 本地 SQLite 回归（确认没打坏现有功能）→ 服务器 MySQL 首跑建表 + 灌库

---

## 8. 移植后验证清单

必须**手工**跑通（这两条是静默坏掉的高危路径）：

- [ ] 注册全流程：`POST /api/auth/register` → 拿到 `devCode` → `POST /api/auth/verify` → 拿到 token
      （若 §4 的时间问题没修，这里会报「验证码已过期」）
- [ ] 重复邮箱注册 → 必须返回 **409**「该邮箱已注册」，不是 500
- [ ] 登录 → `GET /api/auth/me`
- [ ] 词典：`GET /api/dict/lookup?word=the`，且大小写不敏感（`?word=The` 同样命中）
- [ ] 文章：`GET /api/datasets`、`/api/datasets/{id}/articles`、`/api/articles/{id}`（含中文标题渲染）
- [ ] 标注 upsert 两次同一位置 → 返回**同一个 id**（验证 `LAST_INSERT_ID(id)` 正确）
- [ ] 批注 / 翻译新增 + 删除 → `GET /api/articles/{id}/state` 一致
- [ ] 中文与 emoji（📚 🦊）无乱码 → 确认连接是 `utf8mb4`
- [ ] 词典行数 = **89501**（`SELECT COUNT(*) FROM dictionary`）
- [ ] 冷启动建表后**再启动一次** → `CREATE TABLE IF NOT EXISTS` 幂等，seed 不重复灌

---

## 9. 实测结清与遗留项

### 9.1 已实测结清（拿到服务器写权限后）

用 go-sql-driver/mysql 直连 `192.168.10.10:3306`（`english_reading@192.168.10.%`，
`GRANT ALL PRIVILEGES ON english_reading.*`）建临时表逐条验证，**验证后已全部 DROP，库内剩余表数 = 0**：

| 原条目 | 实测结果 |
| --- | --- |
| ② `TEXT ... DEFAULT ''` | ✅ 确认为 **Error 1101**：`BLOB, TEXT, GEOMETRY or JSON column 'nickname' can't have a default value` |
| ③ `CREATE INDEX IF NOT EXISTS` | ✅ 确认为 **Error 1064** 语法错误 |
| ④ `id = LAST_INSERT_ID(id)` | ✅ 有效：真 no-op（affected=0）时**朴素写法给 0，加技巧给正确 id**（详见 §6.2 ①） |
| 新增：§3.3 保留字 `key` | ✅ 裸用 → **1064**；加反引号 → 正常（GORM 会自动加） |
| 新增：RFC3339 写 DATETIME | ✅ **Error 1292 直接拒绝**（修正了 §2.1 的早期结论） |
| 新增：utf8mb4 中文 + emoji | ✅ 建表/写入/回读全部正常 |
| 新增：GORM `TranslateError` | ✅ SQLite 上 `errors.Is(err, gorm.ErrDuplicatedKey)` = true |
| 新增：`Dictionary` 表名复数化 | ✅ GORM AutoMigrate 建出的是 **`dictionaries`**，必须显式 `TableName()` |

### 9.2 仍未实测

1. `Error 1062` 的确切报文文本（GORM 已代为归一，不影响实现）
2. 89,501 条词典 seed 的**实际耗时**（SQLite 现状约 1s；MySQL 待实现后计时）
3. 服务器 `skip_name_resolve=OFF` 导致**每次新建连接都有 ~10s 反向 DNS 卡顿**
   —— 已诊断清楚（见 §10），但修复需改服务端配置，待用户决定

---

## 10. 连接层实测：每次新建连接约 10 秒

拿到凭据后第一次连接报 `ERROR 2013 Lost connection ... 'waiting for initial communication packet'`。
逐层排查结果：

| 检查 | 结果 |
| --- | --- |
| `nc` / TCP 握手 | ✅ 0.003s 成功 |
| 服务端防火墙 | ✅ 已放行 `3306 tcp accept 192.168.10.0/24`（"MySQL LAN access"） |
| `bind_address` / `port` / `skip_networking` | `*` / 3306 / 0 —— 监听正常 |
| SSH 22 端口 | ✅ 立即返回 banner，证明链路通畅 |
| **MySQL 握手包** | ⚠️ **恰好 10.02s 后才到达** |
| `skip_name_resolve` | **OFF** ← 根因 |
| 服务端 DNS | `nameserver 127.0.0.53`（systemd-resolved），`search .`，解析不了局域网反查区 |

**根因**：`skip_name_resolve=OFF` 时 MySQL 对客户端 IP 做反向解析，服务端 DNS 无法解析
`192.168.10.x` 反查区 → 阻塞约 10s 才发握手包。mysql 客户端默认 `--connect-timeout=5` 比它短，
于是报出具有误导性的 "waiting for initial communication packet"。

**影响与对策**：

- 客户端 / Go 侧可先绕过：`--connect-timeout=30`，Go 的 DSN **不要设过短的 `readTimeout`**（握手读也受它约束）
- 连接池会摊薄这个成本，但每次新建连接仍要 10s
- **根治**：服务端 `my.cnf` 加 `skip_name_resolve=ON` 后重启 MySQL（非动态变量）。
  这是服务器配置变更，需你确认后再动

---

## 附：本文档事实来源

| 事实 | 来源 |
| --- | --- |
| 代码规模、45 处调用点、方言语句位置 | `grep` 扫描 `backend/**/*.go`（server 31 / seed 11 / store 3） |
| 本地表行数 | `sqlite3 backend/data/app.db` |
| 词典/文章种子长度分布 | Python 解析 `internal/seed/data/*.json` |
| MySQL 版本 / sql_mode / 字符集 / 行格式 / 时区 | 宝塔 MCP 查询 `@@variables`（实测） |
| `english_reading` 库为空 | 宝塔 MCP `SHOW TABLES`（0 行） |
| §6 两套方言的真实 SQL | `gorm.io/gorm v1.31.2` + `driver/mysql v1.6.0` + `driver/sqlite v1.6.0`，`db.ToSQL()` 生成的实测输出 |
| `RETURNING` 在 MySQL 被丢弃、回填主键走 `LastInsertId()` | 读 `callbacks/create.go:38,52,80,142,146` 与 `driver/mysql/mysql.go`（`withReturning` 仅 MariaDB ≥10.5） |
| `1062 → ErrDuplicatedKey` | 读 `driver/mysql/error_translator.go` + SQLite 侧实测 |
| `gorm.Open` 默认 Ping、`DryRun` 仍 Begin | 实测（拒绝连接 / sqlmock 报 unexpected Begin） |
| §2.1 / §3.2 / §3.3 / §6.2 / §9.1 全部结论 | go-sql-driver/mysql 直连 `192.168.10.10:3306` 实测（建表→验证→DROP，已复原为空库） |
| 连接 10s 卡顿根因 | 逐层排查 + 握手包计时 + `@@skip_name_resolve` + 服务端 `/etc/resolv.conf` |

> 取证脚手架（临时 Go module + sqlmock + 直连探针）已在取证后删除；临时表已全部 DROP，
> `english_reading` 库恢复为空。工作区只留下本文档。
