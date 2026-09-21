# AGENTS.md — 拾句 · English Reading Annotation

给接手这个仓库的 agent / 开发者的工作手册。**动手前请通读第 4 节「数据层铁律」**，
那里每一条都是实际踩过的坑，不是理论。

最后核实：2026-09-21（含线上部署实测）。

---

## 1. 这是什么

英文阅读批注网站：按段落拆句渲染英文原文，用户可点词查词典、选词性词义、写句子/段落批注、
保存句子/整段翻译。Vue 3 单页应用 + Go HTTP API + 关系型数据库。

**正文存在文件里，数据库只是它的索引**（见 §2）。

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
                        │                        │
                   GORM │                        │ 读文件
                        ▼                        ▼
                   MySQL 5.7  english_reading 库   <CONTENT_ROOT>/  （正文 JSON）
                     （只有索引：元数据 + 高亮锚点）
```

前端全部用**相对路径** `/api/...` 调用，所以线上同源、无需 CORS。
后端保留了 CORS 中间件，仅为本地 `vite dev`（5173 → 8080）服务。

### 2.1 正文与索引分离

| | 位置 | 内容 |
| --- | --- | --- |
| **正文**（唯一真源） | `<CONTENT_ROOT>/<数据集目录>/**/*.json` | 段落文本、标题、作者、level |
| **索引**（可重建） | 数据库 | 数据集/文章元数据、段落数、内容哈希、用户标注 |

方向只有一个：**文件 → 数据库**。改文件后跑 `go run ./cmd/articlesync` 重建索引；
数据库里的正文相关字段全是扫描时派生出来的，删库重扫即可恢复。

### 2.2 锚点：段落按内容哈希识别

句子**不落库**，由 `internal/sentence` 按标点统一切句。标注锚点是：

```
article_id + paragraph_hash + sentence_index + word_index
```

`paragraph_hash` = 段落文本的 sha256（`internal/anchor`）。**这是全文最关键的设计决定**：

| 场景 | 用段落序号 | 用 paragraph_hash |
| --- | --- | --- |
| 在中间插入/删除一段 | 后面所有标注全部错位 | 其余段落标注**完好** |
| 段落重排 | 全部错位 | 完好 |
| 改写某段文字 | 错位且无从发现 | 只有这一段失效，且**可检测** |

正文改动**绝不自动删标注**：`/api/articles/{id}/state` 给每行打 `stale: true`，
阅读页显示提示条，同步命令只统计并打印。删不删由人决定。

**改断句规则仍会让已有批注错位**（`sentence_index` / `word_index` 是纯位置量）——
锚点只能保证"段落没变"，保证不了"句子没变"。改 `internal/sentence` 前要想清楚。

---

## 3. 仓库地图

```
backend/
  main.go                        # 入口：store.Open → seed.Run → 建/索引内容树 → server.New
  cmd/articlesync/               # ★ 内容树 ↔ 数据库索引同步器（见 §10）
  internal/
    anchor/                      # ★ 锚点定义：ParagraphHash / ArticleHash（全仓库唯一实现）
    content/                     # ★ 文件侧真源：读写文章 JSON、扫描目录、与库同步
      content.go                 #   路径安全、解析、校验、哈希、Scan
      write.go                   #   写文章文件 / dataset.json、便宜的存在性探测
      corpus.go                  #   语料 JSON → 文件树（materialize）
      sync.go                    #   文件树 → 数据库索引（identity 保持、stale 检测）
    store/                       # ★ 数据层唯一入口，改表结构只改这里
      models.go                  #   GORM 模型 = schema 唯一来源（AutoMigrate）
      open.go                    #   引擎选择、DSN 补全、连接池、日志级别
      config.go                  #   DB_DRIVER / DB_DSN / DB_PATH / DB_LOG
      migrate.go                 #   AutoMigrate + 清理过期会话
      legacy.go                  #   ★ 一次性迁移 paragraph_id → paragraph_hash（见 §11）
      errors.go                  #   IsDuplicate / IsNotFound（跨引擎）
      upsert.go                  #   UpsertReturningID（跨引擎拿主键）
      store_test.go              #   8 用例 × 双引擎
      dicttext.go                #   ECDICT 字面 `\n` → 真换行（见 §4.9）
      dicttext_test.go           #   该转换的幂等性单测
    server/
      server.go                  #   路由、CORS、SPA 静态托管、鉴权中间件、upsertOn 助手
      auth.go                    #   注册/验证/登录/登出/me
      content.go                 #   数据集/文章列表、正文（**从文件读**）、词典查询
      annotations.go             #   单词标注、批注、翻译
    seed/                        # 内嵌词典种子 + 内嵌示例语料（仅当内容树为空时铺开）
    sentence/                    # 英文断句
    translate/                   # MyMemory 翻译封装
frontend/
  src/views/                     # HomeView / DatasetView / ReaderView / Login / Register
  src/components/                # 句子渲染、词典弹窗、就地批注编辑器
  vite.config.*                  # dev 代理 /api → 8080
docs/
  gorm-migration-plan.md         # GORM 改造规划 + 实施结果（含验证数据）
  mysql-migration-assessment.md  # SQLite→MySQL 可行性评估 + 服务器实测结论
tools/
  txt2articles.py                # ★ raw txt → 合规语料 JSON 的清洗器（见 §10）
```

数据流全貌：

```
raw .txt ──txt2articles.py──> corpus.json ──articlesync -materialize──> <CONTENT_ROOT>/*.json
                                                                              │
                                                        articlesync / 首启扫描 │
                                                                              ▼
                                                                       数据库索引
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

`seedArticles` 及它那条「先 DELETE 再重灌」的破坏性路径**已经删除**（正文改为文件真源后，
它既没必要又危险）。但线上历史数据集的 id 仍是 5,6,7、文章从 4 开始，是当年重灌留下的。
→ **不要硬编码 id**（`/api/articles/1` 在线上是 404）；写测试要用真实查询到的 id。
词典是 `INSERT IGNORE` 语义，幂等，重复灌不会变多。

### 4.9 ECDICT 释义里的换行是**字面**的 `\n`（反斜杠 + n）

`dict_seed.json` 里 `def` 的换行**不是真换行**，而是两个字符 `\` `n`
（如 `绝对的, 专制的, 完全的, 独立的\nn. 绝对事物`，89k 条里 38436 条如此；另有 1 条 `\r\n`）。
这是 ECDICT 的原文约定，种子文件原样带进来了。

后果：前端 `.def` 用 `white-space: pre-line` 渲染，只认真换行，所以用户看到的是字面 `\n`。
**不要用 `v-html` 拼 `<br>` 修**（词典文本进 `v-html` 等于开 XSS 口子）。

正解是 `store.NormalizeDictText`（`internal/store/dicttext.go`，幂等），两个位置都调用：

| 位置 | 作用 |
| --- | --- |
| `seed.seedDictionary` | 新库落库即为真换行 |
| `server.handleDictLookup` | **老库（含线上 MySQL）不必重灌**，读时清洗 |
| `server.handleArticleState` / `handleUpsertWordAnnotation` | 存量的 `word_annotations.sense` 同样清洗，且与词典的「已选」比对保持一致 |

词典已 `INSERT IGNORE` 灌过时**改种子文件不会生效**（`OnConflict{DoNothing}`），
所以别指望「改数据 + 升 `dictVersion`」能修线上 —— 那条路只会白跑一遍 89k 行。

### 4.10 新增 NOT NULL 列必须带 `default:`

这不是风格问题，是**一次性迁移能不能跑起来**的问题：

- SQLite 直接拒绝 `ALTER TABLE ... ADD COLUMN x NOT NULL`（无默认值）：
  `Cannot add a NOT NULL column with default value NULL`
- MySQL 宽松些，但 DATETIME 会填零值日期

`Dataset.Dir` / `Article.RelPath` / `Article.ContentHash` / 三个锚点表的 `ParagraphHash`
**全都是加在已有表上的新列**，所以每个都带 `default:`。这与 §4.1 不矛盾：§4.1 禁的是
**默认值 ≠ 零值**的标签（`default:-1` 会把 `sentence_index = 0` 静默改写）；
这里每个默认值都恰好等于字段零值，语义不变。新增列时先问两句：
**它加在已有表上吗？默认值等于零值吗？**

### 4.11 新增 UNIQUE 列要先填值、后建索引

`datasets.dir` / `articles.rel_path` 是 UNIQUE，而 AutoMigrate 会连索引一起建；
已有行若全是 `''`，索引根本建不起来。`preMigrateLegacy` 因此分两步：
先 `AddColumn`（此刻还没有索引），再用**已有的唯一值**回填 —— `dir` 用 `slug`，
`rel_path` 用 `__legacy__/<id>` 占位（`_` 前缀目录会被扫描跳过，真实文件不会占用该路径），
然后 AutoMigrate 才建索引。回填必须用 `UpdateColumn` 而不是 `Update`：
此刻 `updated_at` 还不存在，GORM 的 `Update` 会试图写它。

---

## 5. 命令速查

```bash
# ---- 本地开发（SQLite，零依赖）----
cd backend && go run .                 # http://127.0.0.1:8080，首启自动建表 + 灌库（约 0.5s）
cd frontend && npm install && npm run dev   # http://localhost:5173，/api 已代理到 8080

# ---- 生产模式（后端托管前端）----
cd frontend && npm run build           # 产出 frontend/dist
cd ../backend && go run .              # http://127.0.0.1:8080

# ---- 语料：清洗 → 铺成文件 → 建索引（见 §10）----
python3 tools/txt2articles.py <txt目录> -o corpus.json --dataset-slug X --dataset-title Y
cd backend && go run ./cmd/articlesync -materialize ../corpus.json   # 铺进内容树并索引
go run ./cmd/articlesync -list          # 只看内容树里有什么，不碰数据库
go run ./cmd/articlesync -dry-run       # 只看会怎么改索引
go run ./cmd/articlesync                # 改完文件后重建索引（幂等）

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
| `CONTENT_ROOT` | `data/content` | **正文数据集根目录**（项目级配置，见 §10）。相对进程工作目录，开发时即 `backend/data/content` |

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
| `internal/store` | 9 个：表名/外键级联/词典大小写/upsert 稳定性/重复键/时间往返/中文 emoji/**零值 default 回归**/ECDICT 转义（§4.9，纯函数、两引擎无关） |
| `internal/seed` | 4 个：词典全量灌入/幂等/`meta` upsert/**starter 语料只铺进空内容树** |
| `internal/sentence` | 断句单测 |

尚未覆盖：`internal/server` 的 HTTP 层没有自动化测试，靠手工冒烟（见 §9.1）；
`internal/content` 的扫描/同步/迁移也没有单测，靠 §11 的端到端演练（那是目前唯一验证过的路径）。

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
| 索引同步器 | `/www/server/go_project/english_reading/articlesync`（属主 `www`；服务器无 Go，需本地交叉编译后上传） |
| **正文内容树** | `/www/server/go_project/english_reading/content`（属主 `www`；由 env 里的 `CONTENT_ROOT` 指定，**不随二进制走，必须单独同步/备份**） |

反代关键点：`proxy_pass http://127.0.0.1:8080;` **不带 URI 部分**，所以 `/api/xxx`
原样透传给后端（后端路由就是按 `/api/...` 注册的）。若写成 `proxy_pass http://127.0.0.1:8080/;`
会把 `/api` 前缀吃掉，全部 404。

### 7.2 重新部署

```bash
# 1) 前端（**不要直接 rsync 到 webroot**：目录属主是 www，metanoia 建不了临时文件，
#    会以 "mkstemp ... Permission denied" 半途失败，index.html 与 assets 可能不同步）
cd frontend && npm run build
tar czf - -C dist . | ssh metanoia@192.168.10.10 'rm -rf /tmp/dist-new && mkdir -p /tmp/dist-new && tar xzf - -C /tmp/dist-new'
ssh metanoia@192.168.10.10 '
  W=/www/wwwroot/reading.metanoia.internal
  sudo rm -rf $W/assets && sudo cp -r /tmp/dist-new/. $W/ && rm -rf /tmp/dist-new
  sudo chown -R www:www $W 2>/dev/null || true
'
# `chown -R` 会对 .user.ini 报 "Operation not permitted" —— 那是宝塔的防篡改文件
# （lsattr 显示 immutable 属性 i，root 所有），故意留着，`|| true` 忽略即可。
# 判断部署是否成功：index.html 里引用的 assets 哈希必须都在 assets/ 目录里。

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

> **重启必然有约 2 秒的 502 窗口**，这是 nohup+pid 重启方式固有的：旧进程被杀到新进程 bind(:8080)
> 之间没有任何人在监听。实测（120 次探测、间隔 200ms）：kill 后连续 **10 次 `000`
> （connection refused）**，窗口 13:53:52.560 → 13:53:54.675，其余全部 200。
> nginx 把它转成 502。**部署时不必惊慌，也不要去查代码**；要彻底消除得换平滑重启
> （nginx 先摘 upstream、或 systemd socket activation / SO_REUSEPORT 多进程）。
>
> 另外：偶发的、不在重启窗口内的 502 多半出在**本地代理**（`HTTP_PROXY=127.0.0.1:7897`）
> 而不是服务端 —— 判据是 nginx error log 为空且后端访问日志里根本没有这条请求。
> 本次遇到两次，之后 80 次连测全 200，未能复现。

> 宝塔面板的「Go 项目」管理界面等价于上面的 nohup + pid 文件方式，不是 systemd。
> 面板里改端口/环境变量会重写 `english-reading.env` 与启动脚本。

**正文内容树与同步器要单独部署**（这是本次架构改动带进部署流程的新东西）：

```bash
# 同步器（服务器没有 Go，必须本地交叉编译）
cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o articlesync ./cmd/articlesync
scp articlesync metanoia@192.168.10.10:/tmp/articlesync
ssh metanoia@192.168.10.10 'sudo mv /tmp/articlesync /www/server/go_project/english_reading/articlesync && sudo chown www:www /www/server/go_project/english_reading/articlesync && sudo chmod 755 /www/server/go_project/english_reading/articlesync'

# 内容树（与数据库一起备份；只备份 MySQL 已经不够了）
rsync -av content/ metanoia@192.168.10.10:/www/server/go_project/english_reading/content/
ssh metanoia@192.168.10.10 'sudo chown -R www:www /www/server/go_project/english_reading/content'

# 在服务器上建索引（env 文件里有 DB_DSN 与 CONTENT_ROOT）
ssh metanoia@192.168.10.10 'sudo bash -c "set -a; source /www/server/go_project/english_reading/english-reading.env; set +a; \
  cd /www/server/go_project/english_reading && ./articlesync -dry-run"'
```

`english-reading.env` 里加一行 `CONTENT_ROOT=/www/server/go_project/english_reading/content`。
服务器上如果没有这个目录、也没有该变量，服务会自己在 `backend/data/content` 下建一个空树，
表现为「网站能开但一篇文章都没有」—— 遇到这种情况先查这个变量。

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
| **正文/索引改造尚未提交 git** | ⚠️ 改动仍在工作区。接手第一件事建议先提交 |
| 线上已完成正文/索引改造 | ✅ 2026-09-21：`paragraphs` 已删，10 篇文章 id 4-13 全部保留，users/sessions/dictionary 未动；内容树在 `/www/server/go_project/english_reading/content`，`CONTENT_ROOT` 已写入 env |
| 线上多了一个测试数据集 | `sample-cleaned`（清洗样例，2 篇）是验证 `txt2articles.py` 流程留下的，删掉：删目录 + `./articlesync -prune` |
| `internal/content` 无自动化测试 | 靠 §11 的端到端演练验证 |
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

### 9.2 阅读器的三条交互约定（改 UI 时别退化）

1. **词义一行一条**。ECDICT 把一个词的多个义项塞进一个 `def`，行内自带词性/领域标签
   （`vi. …` / `[医] …`）。`WordPopup.vue` 的 `expandSense()` 按行拆成独立可选行，
   **第一行的词性取 `sense.pos`**，其余行从行首提取标签。这样存进
   `word_annotations.sense` 的才是单独一条义项，而不是整块。
2. **选中的词义一条不折行**（`.pick-chip { white-space: nowrap }`），条数多了整条换到下一行
   （`.word-picks { flex-wrap: wrap }`）。单条超宽时省略号截断，`title` 里给全文。
3. **批注不再是弹窗**：`InlineNoteEditor.vue` 在句子/段落内部就地展开（高度从 0 动画到内容高度，
   提交或取消后收起），句子批注落在**词义之后、下一句之前**，段落批注落在段尾按钮之下。
   `NoteModal.vue` 已删除，不要把它加回来。

## 10. 导入自己的语料（大批量 txt）

正文是文件，所以"导入"分两步：**清洗成文件** → **扫描建索引**。没有一步会删用户数据。

```bash
# 1) 清洗：raw txt → 合规 corpus JSON（纯离线，可反复跑，不碰数据库）
python3 tools/txt2articles.py corpus/ -o corpus.json \
    --dataset-slug essays --dataset-title 随笔选 --emoji ✍️ --level 中级
python3 tools/txt2articles.py books/ -o books.json --mode book \
    --dataset-slug novels --dataset-title 小说     # 一本书按章拆成多篇

# 2) 铺进内容树 + 建索引（已有文件默认不覆盖，加 -overwrite 才覆盖）
cd backend
CONTENT_ROOT=/srv/reading/content go run ./cmd/articlesync -materialize ../corpus.json
CONTENT_ROOT=/srv/reading/content go run ./cmd/articlesync -materialize ../corpus.json -by-date

# 3) 之后每次改完文件，重建索引（幂等）
CONTENT_ROOT=/srv/reading/content go run ./cmd/articlesync -dry-run   # 先看会改什么
CONTENT_ROOT=/srv/reading/content go run ./cmd/articlesync
CONTENT_ROOT=/srv/reading/content go run ./cmd/articlesync -list      # 只看树，不碰库

# 线上走 MySQL：同一份 DSN
DB_DRIVER=mysql DB_DSN='user:pass@tcp(127.0.0.1:3306)/english_reading' \
  CONTENT_ROOT=/www/server/go_project/english_reading/content go run ./cmd/articlesync -dry-run
```

### 10.1 内容树长什么样

```
<CONTENT_ROOT>/
  aesop/
    dataset.json                      # slug/title/description/emoji/color，可选
    the-fox-and-the-grapes.json       # 一篇文章一个文件
  news/
    2026-09-21/                       # 允许再套一层（-by-date 生成）
      000123.json
```

文章文件：

```json
{
 "title": "On Reading Slowly",
 "level": "中级",
 "author": "A. Reader",
 "origin": "https://www.gutenberg.org/ebooks/1",
 "published_at": "2005-03-01",
 "paragraphs": ["第一段……", "第二段……"],
 "paragraph_count": 2,
 "sentence_counts": [2, 1]
}
```

- `paragraph_count` / `sentence_counts` **是可选的声明**。扫描器一律用 Go 的切句器重算，
  对不上就**报错拒收**这个文件 —— 手改正文后忘了更新声明会立刻暴露，而不是静默错位。
  在 Python 里算这两个数等于把断句算法实现两遍（必然漂移），所以清洗器不生成它们，
  `-materialize` 由 Go 算好写进去。
- 目录/文件名以 `.` 或 `_` 开头的会被跳过（`.git`、编辑器备份、草稿）。
- 文件名校验：不含标题也可以（标题以文件里的 `title` 为准），但**改文件名等于改 `relative_path`**，
  重扫时靠"标题相同"认领回原行（见 10.3）。

### 10.2 清洗器为什么必须「合并折行」

后端按标点切句（`internal/sentence`），而切句器**遇到 `\n` 直接断句**。所以 txt 里的硬折行
会变成假句子，用户标注就锚在这些碎片上。实测同一段文字：

| 输入 | 切出的句子 |
| --- | --- |
| 带硬折行 | `"The bunch hung from a high branch,"` / `"and the Fox had to jump for it."` / `"The first time he jumped"` / `"he missed it by a yard."` ❌ |
| 合并折行后 | `"The bunch hung from a high branch, and the Fox had to jump for it."` / `"The first time he jumped he missed it by a yard."` ✅ |

因此**段内绝不能有 `\n`/`\t`**（扫描时会拒收并指出是哪个文件第几段）。
清洗器还会：剥 Gutenberg 头尾、**把 `Title:/Author:/Release Date:` 元数据抓出来当文章字段**
（不是丢掉）、丢页码行、接行尾连字符（`demonstra-\ntion` → `demonstration`）、
弯引号/破折号归一、cp1252 回退解码、段长 ≤ 60000 字节（MySQL TEXT 是 65535 **字节**）。
坏文件不会中断整批：扫描把每个坏文件收集起来一起报，其余照常入库。

### 10.3 同步器的安全契约

| 情况 | 行为 |
| --- | --- |
| 数据集 | 先按目录名（`dir`）匹配，再按 `slug` 认领老行 —— **id 不变** |
| 文章 | 先按 `relative_path` 匹配；找不到再按 `(dataset_id, title)` 认领 —— 文件改名/挪目录**不会**产生新行，标注跟着旧 id 走 |
| 文件内容与 `content_hash` 一致 | 整行不动（真 no-op） |
| 文件内容变了 | 更新索引，**只报告**有多少篇正文变了；正文改动**从不删标注**，变了的段落其锚点自然失效（`stale`） |
| 文件消失 | 标记 `missing`（列表隐藏、标注保留），加 `-prune` 才真删 |
| 文件非法 | 跳过并汇总报错，其余继续 |

### 10.4 粒度建议

`ReaderView` 一次性渲染整篇（`v-for="p in paragraphs"`，无虚拟滚动），文章接口也一次返回整篇。
所以**按章/篇切，不要整本书一篇** —— 现有 `alice` 数据集就是一章一篇，照这个粒度走。

---

## 11. 正文迁移到文件（一次性，已实测）

数据库里已经有正文和标注时，首次用新版本启动会自动跑一次迁移（`internal/store/legacy.go`）：

1. `preMigrateLegacy`：给已有表补新列（见 §4.10/§4.11），`datasets.dir` 用 `slug` 回填，
   `articles.rel_path` 先给 `__legacy__/<id>` 占位；
2. AutoMigrate 建索引；
3. `migrateLegacyParagraphs`：读 `paragraphs` 表的正文 → 在 Go 里算 `paragraph_hash` →
   把三张锚点表整表导出到内存 → 写一份 `legacy-anchor-backup-<ts>.json`（0600）→
   **drop 三张表再按新 schema 重建** → 回插（保留主键）→ `DROP TABLE paragraphs`；
4. 首启发现索引未建（`content_hash = ''`）→ 铺开示例语料 + 扫描建索引，老的
   article 行按标题被认领，`rel_path`、`content_hash`、段落数一次补齐。

实测结果（旧库 → 新库）：`paragraphs` 113 行删除；2 条标注 + 1 条批注 + 1 条翻译全部保留；
迁移出来的 `paragraph_hash` 与文件里段落文本算出的哈希**逐字节相等**；接口返回 `stale: false`。

**为什么整表重建而不是 ALTER**：MySQL 不允许 drop 一个参与外键的列，而旧的复合唯一索引里
就含 `paragraph_id`，外科手术式 ALTER 需要按引擎去查约束名。三张锚点表都很小，
drop + 重建是引擎无关的，而且**导出先行**（DDL 在 MySQL 里无法回滚）。

已经在线上跑过旧版的话，升级前先 `mysqldump english_reading > backup.sql`
**并且备份内容树** —— 现在数据库不是唯一的数据了。

---

## 12. 工作约定

1. **改表结构只改 `internal/store/models.go`** —— `AutoMigrate` 负责同步两个引擎，不要手写 DDL。
   唯一例外是 `internal/store/legacy.go`：那次迁移必须 ALTER/DROP 已经有数据的表，
   AutoMigrate 做不到（也不会删列）。新增这类迁移时照它的样子来：先备份、可重入、
   两引擎同一份代码、并在本文件记一条铁律。
2. **改数据层后必须两个引擎都跑测试**（§6），并确认 `CGO_ENABLED=0 go build` 仍能过。
3. **提交前跑** `go vet ./...` 与 `gofmt -l .`（应为空）。
4. **不要提交**：`backend/data/`、`backend/english-reading`、`*-env` 里的口令、任何 dump。
   `.gitignore` 已覆盖前两者。
5. 遇到「同一段代码在两个引擎行为不同」时，**优先在 Go 侧写成引擎无关的形式**，
   而不是加 `if driver == "mysql"` 分支 —— 这是引入 ORM 的初衷。目前全仓库没有方言分支。
6. **正文相关的新字段一律由扫描派生**，不要在 handler 里现算；`internal/content` 是文件侧唯一入口，
   `internal/anchor` 是哈希的唯一实现（不要另写一份 sha256，否则迁移和读取会算出不同的锚点）。
7. 不确定的引擎行为**先去真机实测**（MySQL 在 §8.1），不要凭印象写注释或文档。
   本次改造中，「RFC3339 能否写进 DATETIME」「upsert 返回什么 id」两条最初的判断
   都被真实服务器推翻了。
