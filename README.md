# 拾句 · English Reading Annotation

英文阅读批注网站：正文以 JSON 文件存放，数据库只做索引 —— GORM + SQLite / MySQL + Go + Vue 3。

## 功能

- 用户注册 / 登录（注册使用邮箱验证，前期模拟：验证码直接弹出）
- 首页文章数据集 → 数据集文章列表 → 三栏阅读器
- 阅读器左侧：可折叠的固定文章列表
- 阅读器右侧：标题目录（段落定位 + 当前高亮）
- 正文以 JSON 文件存储（一篇文章一个文件），可用 git 管理、rsync 部署
- 点击单词弹出英汉释义，选择词性（n. / v. / adj. …）和中文词义
- 选中的词性词义以浅色样式显示在原文句子下方
- 句子 / 段落批注，分别显示在对应原文下方
- 句子 / 整段翻译（免费 MyMemory 翻译 API），按用户保存
- 段落内拆句渲染，段落卡片式分割
- 正文被改动时**只提示不删数据**：受影响的标注标记为 stale
- 移动端适配：手机/平板为抽屉式左右栏，词典为底部卡片，批注为底部弹层

## 技术栈

| 层 | 技术 |
| --- | --- |
| 前端 | Vue 3 + Vue Router + Vite |
| 后端 | Go 1.22+，标准库 `net/http` |
| ORM | [GORM](https://gorm.io/)（模型即 schema，`AutoMigrate` 建表） |
| 数据库 | **SQLite**（本地默认，`glebarez/sqlite` 纯 Go 驱动，无需 CGO）<br>**MySQL 5.7+**（服务器部署，同一套模型） |
| 正文 | **JSON 文件**（`CONTENT_ROOT`，默认 `data/content`），一篇文章一个文件 —— 唯一真源；数据库仅索引 |
| 词典 | ECDICT 常用词 + 词形变化（约 8.9 万词条，存后端数据库） |
| 翻译 | [MyMemory](https://mymemory.translated.net/) 免费匿名 API |

数据库引擎由环境变量切换，业务代码无需改动：

```bash
DB_DRIVER=sqlite DB_PATH=data/app.db          # 默认
DB_DRIVER=mysql  DB_DSN='user:pass@tcp(127.0.0.1:3306)/english_reading'
```

`DB_DSN` 里的 `parseTime`、`loc`、`charset` 会被自动补全（`parseTime=true&loc=UTC&charset=utf8mb4`），
不必手写。`DB_LOG=info|warn|error|silent` 控制 SQL 日志级别，默认 `warn`。

正文目录由 `CONTENT_ROOT` 指定，默认 `data/content`（相对于进程工作目录，开发时即
`backend/data/content`）。

## 目录结构

```
backend/
  main.go                      # 启动：建表 → 灌词典 → 内容树为空时铺示例语料 → 必要时建索引
  cmd/articlesync/             # 内容树 ↔ 数据库索引：扫描、重建索引、写入语料
  internal/
    anchor/                    # 锚点定义：ParagraphHash / ArticleHash（全仓库唯一实现）
    content/                   # 正文文件侧：读写、扫描、校验、与数据库同步
      content.go               #   路径安全、解析校验、哈希、Scan
      write.go                 #   写文章文件与 dataset.json
      corpus.go                #   语料 JSON → 文件树（materialize）
      sync.go                  #   文件树 → 数据库索引
    server/                    # HTTP API 与静态文件服务
    store/                     # GORM 模型（schema 唯一来源）、连接、AutoMigrate、跨引擎助手
    seed/                      # 内嵌词典种子 + 内嵌示例语料（仅当内容树为空时铺开）
    sentence/                  # 英文断句
    translate/                 # MyMemory 翻译封装
frontend/
  src/
    views/                     # 首页 / 文章列表 / 登录注册 / 阅读器
    components/                # 句子渲染、词典弹窗、就地批注编辑器
tools/
  txt2articles.py              # raw txt → 合规语料 JSON 的清洗器
data/content/                  # 正文内容树（开发时实际是 backend/data/content）
```

`internal/store` 是整个数据层唯一的入口：

| 文件 | 职责 |
| --- | --- |
| `models.go` | 全部 GORM 模型 —— 改表结构只改这里，`AutoMigrate` 自动同步两个引擎 |
| `open.go` | 按 `DB_DRIVER` 选驱动、补全 DSN、连接池参数 |
| `config.go` | 环境变量 → 配置 |
| `migrate.go` | 建表 + 清理过期会话 |
| `legacy.go` | 一次性迁移：`paragraph_id` → `paragraph_hash` |
| `errors.go` | `IsDuplicate` / `IsNotFound`，跨引擎判断，不依赖驱动报错文本 |
| `upsert.go` | `UpsertReturningID`，insert-or-update 并可靠拿回主键 |

## 快速启动

### 方式一：开发模式（前后端分离）

```bash
# 终端 1：后端（首启自动建表、导入词典；内容树为空时铺入示例语料并建索引）
cd backend
go run .

# 终端 2：前端（Vite 已配置 /api 代理到 8080）
cd frontend
npm install
npm run dev
```

打开 http://localhost:5173

### 方式二：生产模式（后端同时托管前端）

```bash
cd frontend && npm install && npm run build
cd ../backend && go run .
```

打开 http://localhost:8080

### 方式三：用 MySQL 跑

```bash
cd backend
DB_DRIVER=mysql \
DB_DSN='english_reading:密码@tcp(127.0.0.1:3306)/english_reading' \
go run .
```

首次启动会自动建表并灌入词典（MySQL 约 6 秒），之后启动只做增量迁移。
库需要预先建好并授权，其余无需手工建表。

## 正文文件与内容树（唯一真源）

正文存放在 `CONTENT_ROOT` 下的文件树里，一篇文章一个 JSON 文件：

```
<CONTENT_ROOT>/
  aesop/                        # 一个数据集 = 一个目录
    dataset.json                # 数据集元数据（可省，省略时用目录名兜底）
    the-fox-and-the-grapes.json
  news/
    2026-09-21/                 # 允许额外层级的日期目录
      000123.json
```

单个文章文件：

```json
{
 "title": "The Fox and the Grapes",
 "subtitle": "Aesop's Fables · 伊索寓言",
 "level": "入门",
 "author": "Aesop",
 "origin": "https://example.org/source",
 "published_at": "2026-09-21",
 "paragraphs": [
  "A Fox one day spied a beautiful bunch of ripe grapes hanging from a vine trained along the branches of a tree. The grapes seemed ready to burst with juice, and the Fox's mouth watered as he gazed longingly at them."
 ],
 "paragraph_count": 1,
 "sentence_counts": [2]
}
```

上面的 `sentence_counts` 是真的对得上的（那一段确实是两句）—— 声明写错了扫描器会拒收，
所以这两个字段要么别写，要么保证准确。

- 除 `title` / `paragraphs` 必填外，其余字段都可省略。
- `paragraph_count` / `sentence_counts` 是**声明**，不是数据来源：扫描时后端会用
  `internal/sentence` 重算，对不上就直接报错并跳过该文件（避免手改后陈旧的声明
  静默造成锚点漂移）。
- 段落必须是一个逻辑段、**段内不能有换行或制表符**：切句器遇到 `\n` 会断句，
  硬折行会切出假句子，而用户标注正是锚在这些句子上。
- 以 `.` 或 `_` 开头的文件和目录会被跳过（`.git`、编辑器备份、草稿）。
- 单个文件校验失败只跳过它并打印原因，不影响同一棵树里其他文章入库。

**方向只有一个：文件 → 数据库。** 数据库里的文章字段全是扫描时派生出来的，
删掉索引重新扫描即可恢复；反过来不成立。

改了文件之后要跑一次 `go run ./cmd/articlesync` 让索引跟上。服务启动时**只在索引从未建立时**
自动扫描一遍（首次运行，或刚从旧版本升级上来），否则不会每次开机都去读整棵树 ——
所以「文件改了但页面没变」是预期行为，不是 bug。

## 数据模型（与批注渲染对应的核心设计）

```
users
sessions / pending_registrations     # 会话与模拟邮箱验证

datasets -> articles                 # 数据集 -> 文章（正文在文件里，不落库）
articles.rel_path                    # 指向 <CONTENT_ROOT> 下的 JSON 文件（UNIQUE）
articles.content_hash                # 由各段落哈希派生，正文一改就变
articles.paragraph_count             # 冗余列，列表页因此不必开文件、也不必 COUNT
articles.sentence_count
articles.missing                     # 文件不在了：列表隐藏，标注保留

dictionary                           # 后端英汉词典（ECDICT 子集）

word_annotations                     # 用户选的单词词性 + 词义
notes                                # 句子批注 / 段落批注
user_translations                    # 用户自己的翻译
  sentence_index = -1 表示整段批注 / 整段翻译；translation_cache 做全局缓存
```

正文句子**不落库**，由后端统一断句，前端按 `sentence_index` 顺序渲染。标注锚点是：

```
article_id + paragraph_hash + sentence_index + word_index
```

`paragraph_hash` 是**段落文本的 sha256**，而不是行号或行 id —— 这是全套设计里最关键的一条：

| 场景 | 按段落位置锚定 | 按 `paragraph_hash` 锚定 |
| --- | --- | --- |
| 中间插入 / 删除一段 | 后面全部错位 | 其余段落标注**完好** |
| 段落重排 | 全部错位 | 完好 |
| 改写某段文字 | 错位且无从发现 | 只有这一段失效，且**可检测** |

正文改动**绝不自动删标注**：`/api/articles/{id}/state` 给每行打 `stale: true`，
文章接口返回 `staleAnchors` 计数，阅读页显示提示条。删不删由人决定。

> 注意：改 `internal/sentence` 的断句规则仍会让已有批注错位 ——
> `sentence_index` / `word_index` 是纯位置量，锚点只能保证「段落没变」，
> 保证不了「句子没变」。

前端渲染一个句子的四层内容：

1. **原文**（衬线体，单词可点击）
2. **选中的词性词义**（浅紫、弱化字号）
3. **翻译**（浅蓝底，左侧蓝边）
4. **批注**（浅黄底，左侧黄边）

段落级翻译使用浅蓝描边卡片，段落级批注使用浅绿描边卡片。

## 导入自己的语料

```bash
# 1) 清洗：raw txt → 合规语料 JSON（不碰数据库，可反复跑）
python3 tools/txt2articles.py corpus/ -o corpus.json \
    --dataset-slug essays --dataset-title 随笔选 --emoji ✍️ --level 中级

# 2) 写入内容树并建索引（语料变成文件，此后文件才是真源）
cd backend
go run ./cmd/articlesync -materialize ../corpus.json -dry-run   # 先看会做什么
go run ./cmd/articlesync -materialize ../corpus.json

# 3) 之后改了文件，重新建索引即可
go run ./cmd/articlesync -dry-run    # 只报告差异
go run ./cmd/articlesync             # 落地
```

`articlesync` 的常用开关：

| 命令 | 作用 |
| --- | --- |
| `go run ./cmd/articlesync -list` | 只打印扫描到的树，不碰数据库 |
| `go run ./cmd/articlesync -dry-run` | 报告会有什么变化，不写任何东西 |
| `go run ./cmd/articlesync` | 扫描内容树并更新索引 |
| `go run ./cmd/articlesync -prune` | 额外删除「文件已消失」的索引行（其标注一并级联删除） |
| `go run ./cmd/articlesync -materialize <corpus.json>` | 把清洗后的语料写进内容树再建索引（`-by-date` 按日期分目录、`-overwrite` 覆盖已有文件） |

同步的几条安全约定：

- **文章身份会保持**：先按 `rel_path` 匹配，再按 `(数据集, 标题)` 匹配，
  所以改文件名或改标题是**更新**原行而不是新建 —— 标注挂在 `article_id` 上，因此不会丢。
- **正文改动只报告、不惩罚**：内容哈希变了就计数并打印，标注一条都不删。
- **文件消失不删行**：默认只标记 `missing`（列表隐藏、标注保留），文件放回来即恢复；
  要真删得显式加 `-prune`。

## 主要 API

```
POST /api/auth/register         # 返回 devCode，前端弹出模拟验证码
POST /api/auth/verify           # 验证码校验并创建用户
POST /api/auth/login
GET  /api/auth/me
GET  /api/datasets
GET  /api/datasets/{id}/articles
GET  /api/articles/{id}         # 从文件读正文并拆句；含 staleAnchors
GET  /api/articles/{id}/state   # 当前用户的标注/批注/翻译（每行带 stale）
GET  /api/dict/lookup?word=xxx
POST /api/articles/{id}/word-annotations   # 请求体用 paragraph_hash
POST /api/articles/{id}/notes
POST /api/articles/{id}/translations
```

正文相关接口都用 `paragraph_hash` 而不是段落 id：`GET /api/articles/{id}` 的每个段落带
`hash` 与 `index`，三个写接口的请求体也传 `paragraph_hash`。

## 说明

- 邮箱验证为**前期模拟**：验证码保存在服务端 `pending_registrations` 表，并随接口返回给前端弹出。接入真实邮件服务时只需隐藏 `devCode` 字段。
- MyMemory 匿名额度约每天 5000 字符，仅适合学习演示；替换其他翻译服务只需修改 `backend/internal/translate`。
- 词典为 ECDICT 按词频筛选的单词常用子集；查询含 `'` 或 `-` 的词会自动回退到词干。
- 种子词典数据来自 [ECDICT](https://github.com/skywind3000/ECDICT)；示例文章取自 Project Gutenberg 公版文本。
- 全新安装（内容树为空）时，服务会把内嵌的示例语料铺进 `CONTENT_ROOT`，保证打开就有内容；树里一旦有文章就再也不会被覆盖。
- 内容树是数据不是源码（已在 `.gitignore` 中），部署时要连同数据库一起同步/备份。

## 数据层注意事项（改代码前请读）

跨两个引擎的差异已经封装好，但改数据层时仍需注意：

1. **不要给 GORM 字段加 `default:` 标签**，除非该字段的零值确实等于默认值。
   GORM 会把「带 `default` 标签且值为零」的字段从 INSERT 中省略，让数据库默认值生效 ——
   `sentence_index` 曾因此把真实的 `0`（每段第一句）悄悄写成 `-1`（整段锚点）。
   已由 `TestZeroValuedSentenceIndexIsPersisted` 守住。

2. **加在已有表上的 NOT NULL 新列必须带 `default:`**。SQLite 直接拒绝
   `ALTER TABLE … ADD COLUMN x NOT NULL`（无默认值）。这与上一条不矛盾：
   上一条禁的是**默认值 ≠ 零值**，这里每个默认值都恰好等于字段零值。

3. **新增 UNIQUE 列要先填值、再让 AutoMigrate 建索引**，否则已有行全是 `''`，
   索引根本建不起来。`legacy.go` 里的 `preMigrateLegacy` 就是干这个的，
   回填时要用 `UpdateColumn`（此刻 `updated_at` 还不存在）。

4. **upsert 后要拿主键，请用 `store.UpsertReturningID`**，不要直接
   `Clauses(clause.OnConflict{...}).Create(...)` 再读模型的 ID。
   GORM 在 SQLite 上自动加 `RETURNING id`，MySQL 没有 `RETURNING` 会退回 `LastInsertId()`，
   而当更新是**空操作**（值完全相同）时它返回 `0` —— 前端拿 0 去删除就会失效。

5. **判断重复键用 `store.IsDuplicate(err)`**，不要匹配错误文本。
   MySQL 报 `Error 1062`（不含 "UNIQUE"），SQLite 报 `UNIQUE constraint failed`。

6. 模型表名依赖 GORM 的复数化推导，`Dictionary` / `TranslationCache` / `Meta` 已显式
   `TableName()` 覆盖。新增模型若推导出的表名不对，记得覆盖。

### 从旧版本升级

老库（正文还在 `paragraphs` 表里）在**首次启动时自动迁移**，无需手工操作：
加新列 → 读取旧段落文本把 `paragraph_id` 换算成 `paragraph_hash` →
把三张锚点表的数据先备份到 `legacy-anchor-backup-<时间戳>.json` → 重建这三张表 → 删除 `paragraphs`。
标注、批注、翻译都会保留，并仍指向原来的那段文字。

跑测试时可用 `TEST_MYSQL_DSN` 让同一套测试同时验证 MySQL：

```bash
cd backend
go test ./...                                                  # 只跑 SQLite
TEST_MYSQL_DSN='user:pass@tcp(host:3306)/db' go test -p 1 ./...  # SQLite + MySQL
```

> **MySQL 测试必须加 `-p 1`**：`internal/seed` 与 `internal/store` 两个测试包共用同一个库，
> 而 Go 默认并行跑不同包；seed 包会重建 schema，与 store 包并发时会互相踩
> （表现为 `Table 'xxx' doesn't exist`）。`-p 1` 让包串行，几秒钟的代价换确定性。
