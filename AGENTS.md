# AGENTS.md — 拾句 · English Reading Annotation

给接手这个仓库的 agent / 开发者的工作手册。**动手前请通读第 4 节「数据层铁律」**，
那里每一条都是实际踩过的坑，不是理论。

最后核实：2026-09-21（含线上部署实测）。

---

## 1. 这是什么

英文阅读批注网站：按段落拆句渲染英文原文，用户可点词查词典、选词性词义、写句子/段落批注、
保存句子/整段翻译。Vue 3 单页应用 + Go HTTP API + 关系型数据库。

**一套模型同时支持两个引擎**，由环境变量切换，业务代码零改动：

| 引擎 | 用途 | 驱动 |
| --- | --- | --- |
| SQLite | 本地开发（默认，零依赖） | `github.com/glebarez/sqlite`（纯 Go，**无 CGO**） |
| MySQL 5.7 | 线上部署 | `gorm.io/driver/mysql` |

---

## 2. 架构与请求流

线上单域名同源，nginx 既发前端又反代 `/api/`：

```
浏览器
  │  http://reading.metanoia.internal/
  ▼
nginx (:80, /www/wwwroot/reading.metanoia.internal)
  ├─ /            → Vue SPA 静态文件（history 路由需 SPA fallback）
  └─ /api/        → proxy_pass http://127.0.0.1:8080   （不带 URI，原样透传）
                        │
                        ▼
                   Go 服务 (:8080, 用户 www)
                        │  GORM
                        ▼
                   MySQL 5.7  english_reading 库
```

前端全部用**相对路径** `/api/...` 调用，所以线上同源、无需 CORS。
后端保留了 CORS 中间件，仅为本地 `vite dev`（5173 → 8080）服务。

**句子锚点设计**：正文句子**不落库**，由后端 `internal/sentence` 按标点统一切句，
前端按 `sentence_index` 顺序渲染。批注/标注的锚点是
`paragraph_id + sentence_index + word_index`。**改断句规则会让已有批注错位** —— 这是设计上的
取舍，不是 bug，但改动时要知道后果。

---

## 3. 仓库地图

```
backend/
  main.go                        # 入口：读配置 → store.Open → seed.Run → server.New
  internal/
    store/                       # ★ 数据层唯一入口，改表结构只改这里
      models.go                  #   GORM 模型 = schema 唯一来源（AutoMigrate）
      open.go                    #   引擎选择、DSN 补全、连接池、日志级别
      config.go                  #   DB_DRIVER / DB_DSN / DB_PATH / DB_LOG
      migrate.go                 #   AutoMigrate + 清理过期会话
      errors.go                  #   IsDuplicate / IsNotFound（跨引擎）
      upsert.go                  #   UpsertReturningID（跨引擎拿主键）
      store_test.go              #   8 用例 × 双引擎
    server/
      server.go                  #   路由、CORS、SPA 静态托管、鉴权中间件、upsertOn 助手
      auth.go                    #   注册/验证/登录/登出/me
      content.go                 #   数据集、文章、段落拆句、词典查询
      annotations.go             #   单词标注、批注、翻译
    seed/                        # 内嵌 JSON 种子（词典 89501 条 + 3 数据集 10 文章）
    sentence/                    # 英文断句
    translate/                   # MyMemory 翻译封装
frontend/
  src/views/                     # HomeView / DatasetView / ReaderView / Login / Register
  src/components/                # 句子渲染、词典弹窗、批注弹窗
  vite.config.*                  # dev 代理 /api → 8080
docs/
  gorm-migration-plan.md         # GORM 改造规划 + 实施结果（含验证数据）
  mysql-migration-assessment.md  # SQLite→MySQL 可行性评估 + 服务器实测结论
```

---

## 4. 数据层铁律

> 每条都对应一个真实故障或实测结论。违反其中任何一条都会产生**编译通过、测试若覆盖不到就静默出错**的问题。

### 4.1 不要给「应用总会显式赋值」的字段加 `default:` 标签

GORM 会把**带 `default` 标签且值为零**的字段**从 INSERT 中省略**，让数据库默认值生效。

真实事故：`Note.SentenceIndex` / `UserTranslation.SentenceIndex` 曾带 `default:-1`，
于是 `sentence_index = 0`（**每个段落的第一句**，最常见的情况）被静默写成 `-1`（整段锚点）。
表现是翻译接口 500 `upsert: row missing after write`，句子批注挂到整段上。

已修（`models.go` 有注释），由 `TestZeroValuedSentenceIndexIsPersisted` 守住。
其余字符串列零值 `""` 恰好等于其默认值，暂时安全，**但新增字段必须逐个确认**。

### 4.2 upsert 后要主键，用 `store.UpsertReturningID`

**不要**写 `db.Clauses(clause.OnConflict{...}).Create(&m)` 然后读 `m.ID`。

GORM 在 SQLite 上会自动补 `RETURNING id`；MySQL 没有 `RETURNING`，会退回 `LastInsertId()`。
实测 MySQL 的返回值：

| 场景 | affected | `LastInsertId()` |
| --- | ---: | ---: |
| 新插入 | 1 | ✅ 正确 |
| 命中唯一键、值有变化 | 2 | ✅ 正确 |
| **命中唯一键、值完全相同（真 no-op）** | 0 | ❌ **返回 0** |

而本项目的 upsert 带 `updated_at = CURRENT_TIMESTAMP`，**同一秒内重复保存同一标注即命中 no-op**，
前端拿 `id=0` 去删除就会失效。`UpsertReturningID` 在事务内 upsert 后按唯一键回读，两引擎行为一致、
无方言分支。

### 4.3 判断错误用 `store.IsDuplicate` / `store.IsNotFound`

**绝不要匹配错误文本。** MySQL 报 `Error 1062: Duplicate entry ...`（**不含 "UNIQUE" 一词**），
SQLite 报 `UNIQUE constraint failed`。旧代码的 `strings.Contains(err, "UNIQUE")` 在 MySQL 上
会让「重复注册 409」静默退化成 500。

靠 `gorm.Config{TranslateError: true}` 归一成 `gorm.ErrDuplicatedKey`。

### 4.4 表名：GORM 会复数化，三张表必须显式 `TableName()`

`Dictionary`→`dictionaries`❌、`TranslationCache`→`translation_caches`❌、`Meta`→`metas`❌。
已在 `models.go` 覆盖为 `dictionary` / `translation_cache` / `meta`。**新增模型先确认推导出的表名。**

### 4.5 时间一律 `time.Time`，绝不写 RFC3339 字符串

MySQL 在 `STRICT_TRANS_TABLES` 下会**直接拒绝** RFC3339 字符串：
`Error 1292: Incorrect datetime value: '2026-09-21T09:56:35Z'`。
（注意 `SELECT CAST('...Z' AS DATETIME)` 在表达式层面是容忍的 —— 别被它骗了，列赋值更严格。）

`open.go` 会自动给 MySQL DSN 补 `parseTime=true&loc=UTC&charset=utf8mb4`，不必手写。
`charset=utf8mb4` 是必须的：连成 3 字节 `utf8` 会写不进 `datasets.emoji` 的 📚。

### 4.6 词典大小写不敏感靠列排序规则

`dictionary.word` 用自定义类型 `WordKey` + `GormDBDataType`：
SQLite 返回 `TEXT COLLATE NOCASE`，MySQL 返回 `VARCHAR(64)`（靠库默认 `utf8mb4_general_ci`）。
存 `Paris` 查 `paris` 两引擎都命中（已实测）。

### 4.7 `meta.key` 是 MySQL 保留字

GORM 生成的 SQL 会自动加反引号，所以模型层无需处理。
**但你自己写 raw SQL 时必须写成 `` `key` ``**，否则 `Error 1064`。

### 4.8 种子数据的 id 不稳定

`seedArticles` 是「先 `DELETE FROM articles` 再重灌」，所以**每次重灌（seed 版本号变化时）
文章/数据集的自增 id 都会前移**。线上现在数据集是 5,6,7、文章从 4 开始，就是这个原因。
→ **不要硬编码 id**（`/api/articles/1` 在线上是 404）；写测试要用真实查询到的 id。
词典是 `INSERT IGNORE` 语义，幂等，重复灌不会变多。

---

## 5. 命令速查

```bash
# ---- 本地开发（SQLite，零依赖）----
cd backend && go run .                 # http://127.0.0.1:8080，首启自动建表 + 灌库（约 0.5s）
cd frontend && npm install && npm run dev   # http://localhost:5173，/api 已代理到 8080

# ---- 生产模式（后端托管前端）----
cd frontend && npm run build           # 产出 frontend/dist
cd ../backend && go run .              # http://127.0.0.1:8080

# ---- 用 MySQL 跑 ----
DB_DRIVER=mysql DB_DSN='user:pass@tcp(127.0.0.1:3306)/english_reading' go run .

# ---- 构建 ----
cd backend && go build -o english-reading .
go vet ./... && gofmt -l .             # 提交前跑，应为空

# ---- 交叉编译到服务器（服务器无 Go 工具链，必须本地编）----
cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o english-reading .
```

**必须保持 `CGO_ENABLED=0` 可构建** —— 这是选用 `glebarez/sqlite` 而不是
`gorm.io/driver/sqlite`（依赖 `mattn/go-sqlite3`，需要 CGO）的唯一原因。
服务器是 `x86_64` 且**没装 Go**，只能本地交叉编译后上传。

环境变量：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | 监听端口 |
| `DB_DRIVER` | `sqlite` | `sqlite` \| `mysql` |
| `DB_PATH` | `data/app.db` | SQLite 路径 |
| `DB_DSN` | — | MySQL DSN，`DB_DRIVER=mysql` 时必填 |
| `DB_LOG` | `warn` | `info` \| `warn` \| `error` \| `silent` |
| `FRONTEND_DIST` | 自动探测 | 前端构建产物目录 |

---

## 6. 测试

```bash
cd backend
go test ./...                                  # 只跑 SQLite
TEST_MYSQL_DSN='user:pass@tcp(host:3306)/db' go test -p 1 ./...   # SQLite + MySQL
```

- **改数据层必须两个引擎都跑**。方言差异（upsert 返回主键、列排序规则、保留字）只在 MySQL 暴露。
- **MySQL 测试必须加 `-p 1`**：`internal/seed` 与 `internal/store` 两个测试包共用同一个库，
  而 Go 默认并行跑不同包；seed 包会重建 schema，与 store 包并发时互踩，表现为
  `Table 'xxx' doesn't exist`。这是测试基建约束，不是产品 bug。
- 测试里 `TEST_MYSQL_DSN` 未设置时，MySQL 那一半会打印 skip 提示，属正常。

覆盖现状（`-p 1` 全绿）：

| 包 | 用例 |
| --- | --- |
| `internal/store` | 8 个：表名/外键级联/词典大小写/upsert 稳定性/重复键/时间往返/中文 emoji/**零值 default 回归** |
| `internal/seed` | 3 个：全量灌入/幂等/`meta` upsert |
| `internal/sentence` | 断句单测 |

尚未覆盖：`internal/server` 的 HTTP 层没有自动化测试，靠手工冒烟（见 §9.1）。

---

## 7. 部署

### 7.1 线上布局（已部署并验证）

| 项 | 路径 / 值 |
| --- | --- |
| 域名 | `reading.metanoia.internal` → `192.168.10.10`（内网 DNS 已解析，仅 HTTP，无 SSL） |
| 前端产物 | `/www/wwwroot/reading.metanoia.internal/`（属主 `www`） |
| 后端二进制 | `/www/server/go_project/english_reading/english-reading`（属主 `www`） |
| 后端环境变量 | `/www/server/go_project/english_reading/english-reading.env`（`0600 www:www`，**含 DB 口令，别提交**） |
| 启动脚本 | `/www/server/go_project/vhost/scripts/english_reading.sh`（`nohup` + pid 写入 `/var/tmp/gopids/english_reading.pid`） |
| 运行日志 | `/www/wwwlogs/go/english_reading.log` |
| nginx 反代 | `/www/server/panel/vhost/nginx/extension/reading.metanoia.internal/english-reading.conf` |
| 站点配置 | `/www/server/panel/vhost/nginx/reading.metanoia.internal.conf` |
| 后端端口 / 用户 | `:8080` / `www` |

反代关键点：`proxy_pass http://127.0.0.1:8080;` **不带 URI 部分**，所以 `/api/xxx`
原样透传给后端（后端路由就是按 `/api/...` 注册的）。若写成 `proxy_pass http://127.0.0.1:8080/;`
会把 `/api` 前缀吃掉，全部 404。

### 7.2 重新部署

```bash
# 1) 前端
cd frontend && npm run build
rsync -av --delete dist/ metanoia@192.168.10.10:/www/wwwroot/reading.metanoia.internal/

# 2) 后端（本地交叉编译 → 上传 → 重启）
cd ../backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o english-reading .
scp english-reading metanoia@192.168.10.10:/tmp/english-reading
ssh metanoia@192.168.10.10 '
  sudo mv /tmp/english-reading /www/server/go_project/english_reading/english-reading
  sudo chown www:www /www/server/go_project/english_reading/english-reading
  sudo chmod 755 /www/server/go_project/english_reading/english-reading
  sudo kill "$(sudo cat /var/tmp/gopids/english_reading.pid)" 2>/dev/null
  sleep 1
  sudo bash /www/server/go_project/vhost/scripts/english_reading.sh
'
```

> 宝塔面板的「Go 项目」管理界面等价于上面的 nohup + pid 文件方式，不是 systemd。
> 面板里改端口/环境变量会重写 `english-reading.env` 与启动脚本。

### 7.3 SSH 访问

`ssh metanoia@192.168.10.10` —— 该账号**有免密 sudo**，足以完成部署与排查。
（`root` 与其它常见用户不允许公钥登录。）

---

## 8. 环境与基础设施

### 8.1 MySQL

| 项 | 值 |
| --- | --- |
| 地址 | `192.168.10.10:3306`（服务器本机可走 `127.0.0.1:3306`，线上 DSN 用的就是这个） |
| 版本 | **5.7.44** —— 不是 8.0，注意别用 8.0 专有语法 |
| 库 | `english_reading`，字符集 `utf8mb4` / `utf8mb4_general_ci`，InnoDB |
| 账号 | `english_reading@192.168.10.%`，`GRANT ALL PRIVILEGES ON english_reading.*` |
| 口令 | 见服务器上的 `english-reading.env`（**不要写进仓库**） |
| `sql_mode` | `STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION`（好事：非法值报错而非静默截断） |

MySQL 5.7 的硬约束（都已实测）：没有 `RETURNING`、不支持 `CREATE INDEX IF NOT EXISTS`、
`TEXT/BLOB` 不能带 `DEFAULT`、`key` 是保留字。**用 GORM `AutoMigrate` 时这些都被自动绕开** ——
这也是不要退回手写 DDL 的原因之一。

### 8.2 ⚠️ 内网连 MySQL 每次新建连接约 10 秒

服务器 `skip_name_resolve=OFF`，其 DNS（systemd-resolved，`nameserver 127.0.0.53`）
解析不了 `192.168.10.x` 的反查区，MySQL 发握手包前会阻塞约 **10.02 秒**。

- 症状：`ERROR 2013 Lost connection ... 'waiting for initial communication packet'`
  —— 这个报错具有误导性，TCP 其实是通的
- mysql CLI：`--connect-timeout=30` 可绕过（默认 5s 太短）
- Go DSN：**不要设过短的 `readTimeout`**（握手读也受它约束），连接池会摊薄后续开销
- `nc -z` / SSH banner 都正常，容易误判为网络问题；用「握手包计时」定位最快
- **根治**需服务端 `my.cnf` 加 `skip_name_resolve=ON` 并重启 MySQL —— 属服务器配置变更，**尚未执行**

### 8.3 其它

- 邮箱验证是**模拟**：验证码存在 `pending_registrations` 表并随接口返回 `devCode`，前端弹窗显示。
  接真实邮件服务时隐藏该字段即可。
- 翻译走 MyMemory 免费匿名 API（约 5000 字符/天），仅适合演示。
  换服务只需改 `backend/internal/translate`。全局缓存在 `translation_cache` 表（按源文本 sha256）。
- 词库来自 [ECDICT](https://github.com/skywind3000/ECDICT) 常用词子集；文章取自 Project Gutenberg 公版文本。
- 宝塔面板有一个 **只读** 的 MCP 工具集（`mcp__baota__*`）可用于查库/看站点/看防火墙，
  但**不能写**。要改服务器请走 SSH。

---

## 9. 已知问题 / 待办

| 项 | 状态 |
| --- | --- |
| **GORM 改造尚未提交 git** | ⚠️ 全部改动还在工作区（`git status` 一堆 M/??）。接手第一件事建议先提交 |
| `skip_name_resolve=OFF` 导致内网连接 10s | 未修，需服务端改配置（见 §8.2） |
| 站点无 SSL | 仅 HTTP |
| `internal/server` 无自动化测试 | HTTP 层靠手工冒烟（§9.1），可考虑补 httptest |
| 未做 `context` 传递 | handler 里没有 `WithContext(r.Context())`；加的话每个 handler 一行 |
| 无请求级日志/追踪 | 只有 `logRequests` 打印方法+路径+耗时 |

### 9.1 手工冒烟清单

线上或本地都可按此顺序验（曾用它抓出 §4.1 的零值 bug）：

```bash
B=http://reading.metanoia.internal
curl -s $B/api/health
curl -s $B/api/datasets                       # 3 个数据集，中文标题 + emoji 正常
curl -s "$B/api/dict/lookup?word=The"         # found=true（大小写不敏感）
# 注册 → 取 devCode → verify → token（覆盖时间字段）
# 同一位置 upsert 标注两次 → 必须返回同一个非零 id（§4.2）
# 句子的翻译/批注：sentenceIndex 必须保持 0，不能变 -1（§4.1）
```

---

## 10. 工作约定

1. **改表结构只改 `internal/store/models.go`**，不要手写 DDL，不要新增迁移脚本 —— `AutoMigrate` 负责同步两个引擎。
2. **改数据层后必须两个引擎都跑测试**（§6），并确认 `CGO_ENABLED=0 go build` 仍能过。
3. **提交前跑** `go vet ./...` 与 `gofmt -l .`（应为空）。
4. **不要提交**：`backend/data/`、`backend/english-reading`、`*-env` 里的口令、任何 dump。
   `.gitignore` 已覆盖前两者。
5. 遇到「同一段代码在两个引擎行为不同」时，**优先在 Go 侧写成引擎无关的形式**，
   而不是加 `if driver == "mysql"` 分支 —— 这是引入 ORM 的初衷。目前全仓库没有方言分支。
6. 不确定的引擎行为**先去真机实测**（MySQL 在 §8.1），不要凭印象写注释或文档。
   本次改造中，「RFC3339 能否写进 DATETIME」「upsert 返回什么 id」两条最初的判断
   都被真实服务器推翻了。
