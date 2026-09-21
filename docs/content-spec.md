# 语料规范与清洗指南

给「要把一批文档变成这个网站能读的内容」的人（或 agent）。

**先读这一句**：正文的唯一真源是 `<CONTENT_ROOT>` 下的 JSON 文件树，数据库只是它的索引。
所以这份文档的顺序是 **目标格式（§2）→ 存储范式（§3）→ 怎么把任意文档变过去（§4–§5）→ 怎么验收（§6）**。
数据层的坑另见 `AGENTS.md` 第 4 节，本文不重复。

---

## 1. 三层结构

```
原始文档 (.txt/.epub/.pdf/.docx/.html/…)
        │  ① 转成纯文本（§5：按格式选工具）
        ▼
   纯文本 (UTF-8)
        │  ② 清洗 + 结构化（§4：tools/txt2articles.py）
        ▼
corpus.json  ──③ 铺成文件树（articlesync -materialize）──▶  <CONTENT_ROOT>/<数据集>/*.json
                                                                      │  ④ 扫目录建索引（articlesync）
                                                                      ▼
                                                             数据库（只是索引）
```

三条铁律，违反任何一条都会**不报错但结果错**：

| # | 铁律 | 违反后果 |
| --- | --- | --- |
| 1 | **一个段落 = 一个 JSON 字符串，段内不能有 `\n` / `\t`** | 切句器遇到 `\n` 直接断句，句子被切成碎片，而用户标注锚在这些碎片上 |
| 2 | **一篇 = 一章/一篇，不要整本书一篇** | 阅读页一次性渲染整篇（无虚拟滚动），整本书会卡死 |
| 3 | **文件一旦发布就不要再改**（改了就认了：受影响段落的标注会标记 stale） | 锚点是段落文本的哈希，改文本 = 换身份 |

---

## 2. 内容树规范（目标格式）

### 2.1 目录布局

```
<CONTENT_ROOT>/                     # 由环境变量 CONTENT_ROOT 指定，默认 data/content
  aesop/                            # 一级子目录 = 一个数据集，目录名即 dataset.dir
    dataset.json                    # 数据集元数据（可省，省了就用目录名兜底）
    the-fox-and-the-grapes.json     # 一篇文章一个文件
  news/
    2026-09-21/                     # 允许再套一层（articlesync -materialize -by-date 生成）
      000123.json
```

- **数据集目录名 = `dir`**，在库里是 UNIQUE，是同步时认领数据集的第一依据（其次才按 `slug`）。
- 目录/文件名以 **`.` 或 `_` 开头会被整棵跳过** → `.git`、编辑器备份、草稿放这里天然不入库。
- 非 `.json` 文件一律忽略。
- 同名不同篇：**文章在数据集内按 `title` 唯一**，两篇同名会被拒收（否则它们会塌成一行）。

### 2.2 `dataset.json`（可选）

```json
{
  "slug": "aesop",
  "title": "伊索寓言",
  "description": "短小精悍的经典寓言，适合入门阅读与逐词标注。",
  "emoji": "🦊",
  "color": "#f97316"
}
```

| 字段 | 必填 | 上限 | 说明 |
| --- | --- | --- | --- |
| `slug` | 否 | 96 | 缺省用目录名；库内 UNIQUE |
| `title` | 否 | 191 | 缺省用目录名；UI 显示的名字 |
| `description` | 否 | 512 | 数据集卡片副标题 |
| `emoji` | 否 | — | 建议 1 个字符（存的是 `size:16`，但要 `utf8mb4` 才装得下） |
| `color` | 否 | 32 | 卡片主色，`#rrggbb` |

整份文件缺失时：`slug = title = 目录名`，`emoji = 📚`，`color = #6366f1`。

### 2.3 文章文件（核心）

```json
{
  "title": "The Fox and the Grapes",
  "subtitle": "Aesop's Fables · 伊索寓言",
  "level": "入门",
  "author": "Aesop",
  "origin": "https://www.gutenberg.org/ebooks/19994",
  "published_at": "1867-01-01",
  "paragraphs": [
    "第一段，必须是单一逻辑段，段内没有换行。",
    "第二段。"
  ],
  "paragraph_count": 2,
  "sentence_counts": [2, 1]
}
```

| 字段 | 必填 | 类型 / 上限 | 语义与注意事项 |
| --- | --- | --- | --- |
| `title` | **是** | string ≤ 512 | 文章标题。库内 `(dataset_id, title)` 是认领老行的第二依据 |
| `paragraphs` | **是** | string[] ，非空 | **正文**。每项是一个完整逻辑段；每项 ≤ 60000 字节；不得含 `\n` `\r` `\t` |
| `subtitle` | 否 | string ≤ 512 | 副标题，UI 显示在标题下 |
| `level` | 否 | string ≤ 64 | 难度标签，如 `入门` / `中级` / `进阶` |
| `author` | 否 | string ≤ 191 | 作者 |
| `origin` | 否 | string ≤ 512 | 出处 URL / 版本说明 |
| `published_at` | 否 | `YYYY-MM-DD` \| `YYYY-MM` \| `YYYY` \| RFC3339 | 可空。写进库是**可空 datetime**，没有就不要写这个键 |
| `paragraph_count` | 否 | int | **声明值**，见 §2.4 |
| `sentence_counts` | 否 | int[] | **声明值**，见 §2.4 |

> 上限不是随便定的：MySQL 跑在 `STRICT_TRANS_TABLES` 下，超长会**直接报错**而不是截断；
> `paragraphs[i]` 的 60000 字节是给 MySQL `TEXT`（65535 **字节**，不是字符）留的余量。

### 2.4 声明字段：`paragraph_count` / `sentence_counts`

这两个值**唯一权威是 Go 的切句器**（`internal/sentence`），扫描时一律重算：

| 情况 | 行为 |
| --- | --- |
| 没写 | 正常，扫描时算出来存库 |
| 写了且一致 | 正常 |
| 写了但不一致 | **拒收该文件**，报错指出第几段对不上 |

**所以不要在 Python 里生成它们。** 在清洗器里算句数等于把断句算法实现两遍（缩写 `Mr.`、
省略号、小数 `3.14`、结尾引号……），两种语言必然漂移，而漂移了没人会发现。
`articlesync -materialize` 会用 Go 算好并写进文件。

声明字段的用处是**体检**：手改正文后忘了更新，扫描会立刻报错，而不是静默让锚点错位。

### 2.5 路径安全

`relative_path`（= 数据集目录 + 可选日期目录 + 文件名）是**数据**，会存进库再拿来读文件。
读取侧强制：

- 拒绝绝对路径
- 拒绝 `..` 逃逸
- 拒绝解析后落在内容树之外的符号链接

写文件时用 `Slugify(title)` 生成文件名（小写、非字母数字转 `-`、截断 80 字符）。
**文件名建议视为不可变**：改文件名 = 改 `relative_path`。改名后重新扫描会被「标题相同」认领回原行
（标注还在），但不要在标题与文件名之间反复横跳。

---

## 3. 数据库存储范式

### 3.1 关系图

```
users ──┬── sessions
        ├── pending_registrations (独立，注册用)
        └── word_annotations / notes / user_translations   ← 都带 user_id

datasets ──< articles ──< word_annotations / notes / user_translations
                              ↑ 一律通过 article_id 关联

dictionary              独立（ECDICT 词库，只读）
translation_cache       独立（按源文本 sha256 缓存翻译，全局共享）
meta                    独立（种子版本等键值对）
```

**注意：没有 `paragraphs` 表了。** 正文在文件里；段落身份是 `paragraph_hash`（内容哈希），
只存在于三张锚点表里，不占一行数据。

### 3.2 表清单

| 表 | 模型 | 作用 |
| --- | --- | --- |
| `users` | `User` | 账号（邮箱 + bcrypt 口令） |
| `sessions` | `Session` | Bearer token，`expires_at` 过期 |
| `pending_registrations` | `PendingRegistration` | 模拟邮箱验证的验证码 |
| `datasets` | `Dataset` | 数据集元数据 + `dir`（内容树目录名） |
| `articles` | `Article` | **文章的索引行**：元数据 + `rel_path` + 内容哈希 + 冗余计数 |
| `dictionary` | `Dictionary` | ECDICT 词条（`senses_json` 存义项数组） |
| `word_annotations` | `WordAnnotation` | 单词标注（选了哪个词性 + 哪条词义） |
| `notes` | `Note` | 句子批注 / 段落批注 |
| `user_translations` | `UserTranslation` | 用户自己的句子/段落翻译 |
| `translation_cache` | `TranslationCache` | 全局翻译缓存（按源文本 sha256） |
| `meta` | `Meta` | 键值对（`dict_version` 等） |

表名注意：`Dictionary` / `TranslationCache` / `Meta` 三张表**显式覆盖了 `TableName()`**，
否则 GORM 会推成 `dictionaries` / `translation_caches` / `metas`。

### 3.3 `datasets`

| 列 | 类型 | 约束 | 语义 |
| --- | --- | --- | --- |
| `id` | bigint | PK | 自增。**不要硬编码**（线上是 5,6,7 起） |
| `slug` | varchar(96) | NOT NULL, UNIQUE | 稳定标识 |
| `dir` | varchar(191) | NOT NULL, UNIQUE, default `''` | 内容树里的目录名；同步时第一认领依据 |
| `title` | varchar(191) | NOT NULL | 显示名 |
| `description` | varchar(512) | NOT NULL, default `''` | |
| `emoji` | varchar(16) | NOT NULL, default `📚` | 需要 `utf8mb4` |
| `color` | varchar(32) | NOT NULL, default `#6366f1` | |

**没有 `created_at`**：数据集是从目录扫出来的，没有独立的创建时刻。

### 3.4 `articles` —— 全库最需要理解的一张表

| 列 | 类型 | 约束 | 语义 |
| --- | --- | --- | --- |
| `id` | bigint | PK | **标注锚点的根**。同步时尽最大努力保持不变（见 §3.6） |
| `dataset_id` | bigint | NOT NULL, INDEX, FK→datasets CASCADE | |
| `title` | varchar(512) | NOT NULL | 认领老行的第二依据（`dataset_id + title`） |
| `subtitle` / `level` | varchar(512)/(64) | NOT NULL, default `''` | UI 用 |
| `author` | varchar(191) | NOT NULL, default `''` | |
| `origin` | varchar(512) | NOT NULL, default `''` | |
| `published_at` | datetime | NULL, INDEX | **可空**；没有日期就是 NULL |
| `rel_path` | varchar(512) | NOT NULL, **UNIQUE** | 相对 `<CONTENT_ROOT>` 的斜杠路径，**读正文的唯一入口** |
| `content_hash` | varchar(64) | NOT NULL, default `''`, INDEX | 由各段落哈希派生（sha256），**元数据改动不会变**，正文改动必变 |
| `paragraph_count` | int | NOT NULL, default 0 | 冗余列 |
| `sentence_count` | int | NOT NULL, default 0 | 冗余列 |
| `missing` | bool | NOT NULL, default false, INDEX | 文件不在了：列表隐藏、标注保留 |
| `created_at` | datetime | NOT NULL | 首次建索引的时间 |

**`paragraph_count` / `sentence_count` 为什么冗余**：文章列表页要显示「N 段 · M 句」。
不冗余就得为每篇开一次文件（或跑 `COUNT(*)` 子查询）——几十万篇时列表接口会直接崩。
它们是**派生数据**：删掉整库重扫即可恢复，永远不要手工改。

**`content_hash` 的边界**：它只覆盖段落哈希，**不含标题/作者/level**。
所以改标题不会让标注失效，改正文会。

### 3.5 三张锚点表（标注/批注/翻译）

```go
// 共同的锚点
ArticleID     int64  // → articles.id, CASCADE
ParagraphHash string // 段落文本的 sha256（hex，64 字符）
SentenceIndex int    // 段内句子序号；-1 = 整段
```

| 表 | 唯一键（upsert 依据） | 独有列 |
| --- | --- | --- |
| `word_annotations` | `(user_id, article_id, paragraph_hash, sentence_index, word_index)` | `word`, `pos`, `sense`, `word_index` |
| `notes` | 无唯一键（可多条） | `content` |
| `user_translations` | `(user_id, article_id, paragraph_hash, sentence_index)` | `source_text`, `translated_text` |

**`paragraph_hash` 是这套设计的核心。** 它替代了旧的 `paragraph_id`：

| 场景 | 用行号/自增 id | 用 `paragraph_hash` |
| --- | --- | --- |
| 在中间插入或删除一段 | 后面所有标注全部错位 | 其余段落标注**完好** |
| 段落重排 | 全部错位 | 完好 |
| 改写某段文字 | 错位且无从发现 | 只有这一段失效，且**可检测** |

读接口会给每行打 `stale: true`（该哈希在文件里已经不存在）。**stale 只报告，从不自动删**。

> 仍有位置量残留：`sentence_index` 和 `word_index` 是段内/句内的位置。
> 所以**改断句规则（`internal/sentence`）或改前端分词正则，仍会让已有标注错位**。
> 锚点只能保证「段落没变」，保证不了「句子没变」。

### 3.6 同步时如何保持 `articles.id`（= 保住标注）

`articlesync` 按这个顺序认领：

```
1. 按 rel_path 精确匹配            → 命中即同一篇（改标题、改正文都走这条）
2. 按 (dataset_id, title) 匹配     → 命中即同一篇，更新 rel_path（文件改名/挪目录走这条）
3. 都不命中                        → 新建行
```

因为标注挂在 `article_id` 上，**1 和 2 都保住标注，只有 3 会得到新 id**。
文件消失时不删行，只置 `missing = true`；要真删得显式 `-prune`。

### 3.7 跨引擎约束（SQLite / MySQL）

| 主题 | 处理 |
| --- | --- |
| 时间 | 一律 Go `time.Time`，**绝不写 RFC3339 字符串**（MySQL 严格模式直接拒） |
| 词典大小写不敏感 | `dictionary.word` 用自定义类型：SQLite `TEXT COLLATE NOCASE` / MySQL `VARCHAR(64)` + `utf8mb4_general_ci` |
| 错误判断 | 用 `store.IsDuplicate` / `store.IsNotFound`，**绝不匹配错误文本** |
| upsert 取主键 | 用 `store.UpsertReturningID`（MySQL 无 `RETURNING`，真 no-op 时 `LastInsertId()` 返回 0） |
| 新增 NOT NULL 列 | **必须带 `default:`**，否则 SQLite 拒绝 `ADD COLUMN`；默认值必须等于零值 |
| 表结构变更 | 只改 `internal/store/models.go`，`AutoMigrate` 负责两个引擎 |

---

## 4. 清洗脚本原理（`tools/txt2articles.py`）

### 4.1 为什么必须「合并折行」——这是全部清洗规则里最要命的一条

后端切句器**遇到 `\n` 直接断句**（`internal/sentence`）。所以纯文本里的硬折行会变成假句子。
实测同一段文字：

| 输入 | 切出的句子 |
| --- | --- |
| 带硬折行 | `"The bunch hung from a high branch,"` / `"and the Fox had to jump for it."` / `"The first time he jumped"` / `"he missed it by a yard."` ❌ |
| 合并折行后 | `"The bunch hung from a high branch, and the Fox had to jump for it."` / `"The first time he jumped he missed it by a yard."` ✅ |

假句子不只是难看：**用户会点这些碎片上的词做标注，锚点就固定在碎片上**，
将来正文重排时全部失效。

### 4.2 处理流水线（按代码实际执行顺序，每步的后果都实测过）

| # | 步骤 | 函数 | 不做的后果 |
| --- | --- | --- | --- |
| 1 | 解码：UTF-8(BOM) → UTF-8 → cp1252 → latin-1 逐级回退，非 UTF-8 时**告警** | `decode` | 老 Gutenberg dump 是 cp1252，硬按 UTF-8 读会乱码或抛异常 |
| 2 | **抓取并移除**开头元数据：`The Project Gutenberg eBook of X` / `Title:` / `Author:` / `Release Date:` / `Language:` … | `extract_metadata` | 这些行会变成正文段落被用户读到；而它们的值恰好就是文章字段，所以是**抓走而非丢弃** |
| 3 | 剥 Gutenberg `*** START/END OF … ***` 头尾；无标记时退而丢掉开头的 `Produced by` 等行 | `strip_gutenberg` | 许可证全文会变成几百个段落 |
| 4 | **接行尾连字符**：`culti-\nvating` → `cultivating`，**仅当后接小写字母** | `fix_hyphenation` | 正文里出现 `culti-` / `vating` 两个假词。限定小写是为了不误伤 `well-\nKnown` 这类真连字符。**必须在合并折行之前做**，否则断词已经被空格拆开、再也接不回去 |
| 5 | 归一版面字符（都在 `clean_lines` 里）：<br>a. 统一换行；**`\f` 分页符 → 单个 `\n`**<br>b. 删零宽字符 / 软连字符；`\t`→空格、`\v`→换行<br>c. 弯引号 / 破折号 → ASCII（`--keep-typography` 可关）<br>d. 丢掉整行只有数字的行（页码）、boilerplate 行 | `clean_lines` | a. **分页符当空行会把跨页段落劈成两段**（实测踩过）<br>b. 不可见字符混进段落<br>c. 跨来源文本不一致<br>d. 页码成为独立段落 |
| 6 | **重排折行**：空行分段，连续非空行合并成一段；缩进行 / CHAPTER 行 / 全大写短行也起新段 | `join_wrapped_lines` | 见 §4.1 —— 假句子会让标注锚在碎片上 |
| 7 | 段内空白折叠为单空格 | `normalize_paragraph` | `pdftotext -layout` 对两端对齐文本会输出连续多空格 |
| 8 | 校验：段内无 `\n`/`\t`、段 ≤ 60000 字节、字段长度上限（超长截断并告警） | `validate_paragraph` / `cap` | 会被扫描器拒收。这是有意的：**早失败好过静默入库** |

### 4.3 两种分段模式（**选错会静默毁掉整篇结构**）

| 模式 | 规则 | 用于 |
| --- | --- | --- |
| 默认（按空行） | 空行 = 段落边界；连续非空行 = 同一段的折行 | 纯文本电子书、`pdftotext` 输出 |
| `--line-per-paragraph` | 每个非空行 = 一个段落 | **提取器本身就是「一段一行、从不折行」**：`textutil -convert txt`（.docx/.rtf）、LibreOffice `--convert-to txt` |

实测（同一份 .docx 导出）：

```
默认模式           → 1 个段落   ❌（整篇被合并，因为 docx 导出里根本没有空行）
--line-per-paragraph → 5 个段落   ✅
```

判断方法：`grep -c '^$' file.txt`，如果空行数是 0 或极少，而每个自然段恰好占一行，
就该用 `--line-per-paragraph`。

### 4.4 它不做什么（有意为之）

- **不生成 `paragraph_count` / `sentence_counts`** —— 见 §2.4，算句数是 Go 的职责。
- **不改写正文用词**，不做拼写纠正、不做语法修正。清洗只处理**版面噪声**，不动内容。
- **不决定文章边界**（一本书要不要拆章由 `--mode book` 或人工决定）。
- **不写数据库**，纯离线、可反复跑，输出一个 corpus JSON。

### 4.5 开关速查

```bash
# 常用
--mode article|book          # 一文件一篇 / 按章拆多篇
--dataset-slug/--dataset-title/--emoji/--color/--level/--subtitle
--title-from T               # 覆盖标题（否则用文档 Title: ，再否则用文件名）
--origin URL                 # 出处
--dry-run                    # 只分析不写文件
--min-paragraph-chars N      # 丢掉过短的碎片段

# 分段与字符
--line-per-paragraph         # 见 §4.3（docx/rtf 必用）
--keep-typography            # 保留弯引号/破折号
--no-page-number-strip       # 保留纯数字行
--split-regex RE             # book 模式自定义章节标题正则
```

---

## 5. 从各类文档转换

### 5.1 总原则

1. **两步走**：先转成纯文本，再清洗。不要让转换工具直接产出 JSON。
2. **转换工具负责「结构」，清洗器负责「版面噪声」**。理想情况下转换器已经用空行分段。
3. **转换完必须用眼睛看**（§6），不同工具/不同 PDF 的结果差别极大，没有万能的自动判断。

### 5.2 各格式的可用通道（本机实测）

| 源格式 | 命令 | 实测结论 |
| --- | --- | --- |
| `.txt` | 直接用 | — |
| `.html` | `textutil -convert txt -stdout in.html` (macOS) | ✅ 好：每个 `<p>` 变一行，标签全清 |
| `.docx` / `.rtf` | `textutil -convert txt in.docx` (macOS) | ⚠️ **一段一行、无空行** → 必须配 `--line-per-paragraph` |
| `.epub` | `ebook-convert in.epub out.txt`（Calibre，需安装）<br>或 `pandoc -f epub -t plain in.epub -o out.txt` | 未在本机验证（两个都没装）。epub 本质是 HTML 打包，也可以直接解压后按 HTML 处理 |
| `.pdf` | `pdftotext -layout in.pdf out.txt`（poppler，已装） | ⚠️ **见下面「PDF 专属坑」**，最容易翻车 |
| `.md` | 需先去掉标记：`pandoc -f markdown -t plain` | 未在本机验证。不处理的话 `**粗体**`、`#`、`>` 会原样进正文 |
| 未安装工具的安装 | `brew install pandoc` / `brew install --cask calibre` | — |

### 5.3 PDF 专属坑（实测数据）

用同一份文档生成 PDF 后抽取，**两种模式差别巨大**：

```
pdftotext  (默认)       → 空行 1 个  → 清洗后 1 个段落    ❌ 标题、署名、正文全糊成一段
pdftotext -layout       → 空行 6 个  → 清洗后 7 个段落    ✅ 结构正确
```

**所以 PDF 一律用 `-layout`。** 但 `-layout` 还有自己的副作用：

| 现象 | 原因 | 现状 |
| --- | --- | --- |
| 单词间出现连续多空格 | 两端对齐文本的视觉间距被保留 | ✅ 清洗器第 10 步折叠掉 |
| **段落中间冒出空行** | `-layout` 保留垂直位置，行距稍大就吐一个空行 | ⚠️ 会把一段劈成两段（实测遇到）。需要人工检查；`--min-paragraph-chars` 可丢碎片 |
| 页眉/页脚/页码混进正文 | PDF 每页都重复 | 页码行会被丢；**页眉（书名、章节名）目前无法自动识别**，需要人工删或先用编辑器批量删 |
| 行尾连字符 | 断词 | ✅ 清洗器第 8 步接回 |
| 分页符 | `\f` | ✅ 清洗器按单换行处理，跨页段落不会被劈开 |

**扫描版 PDF（图片型）不能这样处理** —— `pdftotext` 会输出空文件或乱码，必须先 OCR
（`ocrmypdf` / `tesseract`），而 OCR 结果没有段落结构，需要更多人工整理。

### 5.4 一本书怎么切章

```bash
# 自动按 CHAPTER/PART/LETTER 等标题拆篇
python3 tools/txt2articles.py books/ -o corpus.json --mode book \
    --dataset-slug novels --dataset-title 小说 --level 进阶

# 你的版本章节标题不规整时，自己给正则
python3 tools/txt2articles.py books/ -o corpus.json --mode book \
    --split-regex '^\s*(第[一二三四五六七八九十百]+章|CHAPTER\s+[IVXLC\d]+)'
```

拆完先 `--dry-run` 看输出了几篇、每篇几段。识别不出章节时会**告警并退化成整本一篇**，
不要忽略那条告警。粒度原则见 §1 铁律 2。

### 5.5 单独一个文件 → 一篇

```bash
python3 tools/txt2articles.py one.txt -o one.json \
    --dataset-slug essays --dataset-title 随笔 --title-from "真正的标题" --origin "https://…"
```

---

## 6. 验收流程

### 6.1 入库前（只看文件，不碰数据库）

```bash
# ① 清洗并看报告：每个文件被做了什么，都会逐行打印
python3 tools/txt2articles.py corpus/ -o corpus.json \
    --dataset-slug essays --dataset-title 随笔选 --emoji ✍️ --level 中级
#    报告里重点看：captured metadata / rejoined N hyphens / dropped N page-number lines
#    以及 WARN（非 UTF-8、字段被截断…）

# ② 看抽出来的段落长什么样 —— 这一步不能省
python3 -c "
import json
for a in json.load(open('corpus.json'))[0]['articles']:
    print('==', a['title'], '|', len(a['paragraphs']), '段')
    for p in a['paragraphs'][:3]: print('   ', p[:100])"

# ③ 段落数是否合理（整本一篇 / 全糊成一段，这一步就能发现）
```

### 6.2 入库（幂等，可反复跑）

```bash
cd backend
go run ./cmd/articlesync -materialize ../corpus.json -dry-run   # 先看会做什么
go run ./cmd/articlesync -materialize ../corpus.json            # 铺文件 + 建索引
go run ./cmd/articlesync -list                                  # 复核：树里有什么、每篇几段几句
```

### 6.3 入库后

```bash
B=http://reading.metanoia.internal
curl -s $B/api/datasets | python3 -m json.tool | head -20          # 数据集与篇数
curl -s $B/api/datasets/<id>/articles | python3 -m json.tool       # 每篇的 paragraphCount / sentenceCount
curl -s $B/api/articles/<id> | python3 -c "
import sys,json; d=json.load(sys.stdin)
print(d['article']['title'], d['paragraphCount'], '段')
for p in d['paragraphs'][:3]:
    print(' ', p['kind'], len(p['sentences']), '句:', p['content'][:70])"
```

**最后一定要在浏览器里读一遍**：句子切开得对不对、有没有把页码/页眉读进去，
是 `curl` 看不出来的。

---

## 7. 症状对照表

| 症状 | 多半是 | 怎么办 |
| --- | --- | --- |
| 阅读页一整篇变成**一个巨大段落** | 输入没有空行（docx/rtf 导出、`pdftotext` 默认模式） | 用 `--line-per-paragraph`，或 `pdftotext -layout` |
| 句子被切成**半截**（`"and the Fox had to jump"`） | 段内残留 `\n` | 清洗时没合并折行；扫描器本应拒收，检查是否绕过了清洗器 |
| 段落**从中间断开** | PDF 的 `-layout` 吐了多余空行 | 人工看一遍；或提高 `--min-paragraph-chars` |
| 正文里出现**页码 / 书名页眉** | 提取器保留了页眉页脚 | 页码会自动丢；页眉需人工删 |
| 数字里出现 `culti- vating` 这类**断词** | 行尾连字符没接回 | 确认走了清洗器（它会接回）；检查是否 `-\n` 后接的是大写 |
| 扫描器报 `paragraph_count says N but there are M` | 手改了正文没更新声明 | 删掉 `paragraph_count` / `sentence_counts` 两个键，重跑 `-materialize` |
| 扫描器报 `contains a newline or tab` | 段落里有换行 | 重跑清洗器；不要手写含 `\n` 的段落 |
| 扫描器报 `duplicate article title` | 同一数据集里两篇同名 | 改标题；`(dataset_id, title)` 是认领依据，不能重名 |
| 导入后**篇数为 0** | 目录名以 `.`/`_` 开头，或没有 `.json` | 检查目录名与文件扩展名 |
| 老标注突然大量 `stale` | 正文被改了 | 这是设计行为（不自动删）；确认是否误改了已发布正文 |

---

## 8. 相关文档

| 文档 | 内容 |
| --- | --- |
| `AGENTS.md` §2 | 正文/索引分离与锚点设计总览 |
| `AGENTS.md` §4 | 数据层铁律（跨引擎、迁移、默认值） |
| `AGENTS.md` §10 | 导入流程命令速查 |
| `AGENTS.md` §11 | 从旧库（正文在数据库里）迁移到文件的一次性过程 |
| `AGENTS.md` §9.2 | 阅读器三条交互约定（词义拆分 / 单行 / 就地批注） |
| `docs/批注文章存储方案.md` | 这套存储方案最初的取舍记录 |
