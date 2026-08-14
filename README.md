# 拾句 · English Reading Annotation

英文阅读批注网站：SQLite + Go + Vue 3。

## 功能

- 用户注册 / 登录（注册使用邮箱验证，前期模拟：验证码直接弹出）
- 首页文章数据集 → 数据集文章列表 → 三栏阅读器
- 阅读器左侧：可折叠的固定文章列表
- 阅读器右侧：标题目录（段落定位 + 当前高亮）
- 点击单词弹出英汉释义，选择词性（n. / v. / adj. …）和中文词义
- 选中的词性词义以浅色样式显示在原文句子下方
- 句子 / 段落批注，分别显示在对应原文下方
- 句子 / 整段翻译（免费 MyMemory 翻译 API），按用户保存
- 段落内拆句渲染，段落卡片式分割
- 移动端适配：手机/平板为抽屉式左右栏，词典为底部卡片，批注为底部弹层

## 技术栈

| 层 | 技术 |
| --- | --- |
| 前端 | Vue 3 + Vue Router + Vite |
| 后端 | Go 1.22+，标准库 `net/http` |
| 数据库 | SQLite（`modernc.org/sqlite` 纯 Go 驱动） |
| 词典 | ECDICT 常用词 + 词形变化（约 8.9 万词条，存 SQLite 后端） |
| 翻译 | [MyMemory](https://mymemory.translated.net/) 免费匿名 API |

## 目录结构

```
backend/
  main.go
  internal/
    server/        # HTTP API 与静态文件服务
    store/         # SQLite 连接与建表
    seed/          # 词典、文章种子数据
    sentence/      # 英文断句
    translate/     # MyMemory 翻译封装
frontend/
  src/
    views/         # 首页 / 文章列表 / 登录注册 / 阅读器
    components/    # 句子渲染、词典弹窗、批注弹窗
```

## 快速启动

### 方式一：开发模式（前后端分离）

```bash
# 终端 1：后端（首次启动自动建库并导入词典与文章，约 1 秒）
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

## 数据模型（与批注渲染对应的核心设计）

```
users
sessions / pending_registrations     # 会话与模拟邮箱验证

datasets -> articles -> paragraphs   # 数据集 -> 文章 -> 段落
paragraphs.kind = heading | text     # 标题段落与正文段落
正文句子不落库，由后端统一断句，前端渲染顺序与
sentence_index 一一对应，保证批注锚点稳定。

dictionary                           # 后端英汉词典（ECDICT 子集）

word_annotations                     # 用户选的单词词性 + 词义
  唯一键：user + article + paragraph + sentence_index + word_index

notes                                # 句子批注 / 段落批注
  sentence_index = -1 表示整段批注

user_translations                    # 用户自己的翻译
  sentence_index = -1 表示整段翻译；translation_cache 做全局缓存
```

前端渲染一个句子的四层内容：

1. **原文**（衬线体，单词可点击）
2. **选中的词性词义**（浅紫、弱化字号）
3. **翻译**（浅蓝底，左侧蓝边）
4. **批注**（浅黄底，左侧黄边）

段落级翻译使用浅蓝描边卡片，段落级批注使用浅绿描边卡片。

## 主要 API

```
POST /api/auth/register         # 返回 devCode，前端弹出模拟验证码
POST /api/auth/verify           # 验证码校验并创建用户
POST /api/auth/login
GET  /api/auth/me
GET  /api/datasets
GET  /api/datasets/{id}/articles
GET  /api/articles/{id}         # 含段落与拆句
GET  /api/articles/{id}/state   # 当前用户的标注/批注/翻译
GET  /api/dict/lookup?word=xxx
POST /api/articles/{id}/word-annotations
POST /api/articles/{id}/notes
POST /api/articles/{id}/translations
```

## 说明

- 邮箱验证为**前期模拟**：验证码保存在服务端 `pending_registrations` 表，并随接口返回给前端弹出。接入真实邮件服务时只需隐藏 `devCode` 字段。
- MyMemory 匿名额度约每天 5000 字符，仅适合学习演示；替换其他翻译服务只需修改 `backend/internal/translate`。
- 词典为 ECDICT 按词频筛选的单词常用子集；查询含 `'` 或 `-` 的词会自动回退到词干。
- 种子词典数据来自 [ECDICT](https://github.com/skywind3000/ECDICT)；示例文章取自 Project Gutenberg 公版文本。
