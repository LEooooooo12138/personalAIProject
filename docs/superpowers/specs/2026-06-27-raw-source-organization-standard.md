# Raw Source 文件组织标准 v1.0

> **适用对象**: AI Agent (Codex / Claude Code) — 本文件是 obsidian-wiki AGENTS.md 的补充标准。
> **生效范围**: `OBSIDIAN_SOURCES_DIR` 和 `_raw/` 暂存目录。
> **目标**: AI 读此文件后能自动分类、命名、组织源文件，优化 ingest 管道效率。

---

## 1. 设计原理

### 1.1 为什么不直接 dump 所有文件进 raw/？

obsidian-wiki 的 ingest 管道通过 `.manifest.json` 的内容哈希去重，乱放**不会损坏数据**。但会带来：

| 问题 | 根因 | 本标准的解法 |
|------|------|-------------|
| LLM ingest 时需逐个读取目录，无结构则 token 浪费 | 无分类导致 LLM 需扫描全部文件才能理解 | **按 content-type 分目录**，LLM 一次只关注相关目录 |
| `wiki-status` delta 列表毫无意义 | 全是随机文件名 | **语义化命名**，看文件名就知内容 |
| 同一主题多次摄入时无法批量处理 | 散落各处 | **domain 子目录**，同主题源文件聚合 |
| 人工审计困难 | 找不到源 | **来源可追溯**，文件名自带元信息 |

### 1.2 obsidian-wiki 已有的保障

| 机制 | 作用 | 本标准的角色 |
|------|------|-------------|
| `.manifest.json` content_hash | 去重 | 无需操心 — manifest 已保证同内容不重复 ingest |
| `source_type` 字段 | 分类追踪 | 利用它做二次校验 |
| `pages_created` / `pages_updated` | 溯源 | 命名清晰更方便 LLM 填写这两个字段 |
| QMD 语义搜索 | 检索 | 命名不影响搜索质量 |

---

## 2. 目录结构

```
OBSIDIAN_SOURCES_DIR/                    # 不可变原始文档
│
├── papers/                              # 学术论文 (PDF)
│   └── <YYYY-MM>_<firstauthor>-<topic-slug>.pdf
│
├── articles/                            # 网页文章 / 博客
│   └── <YYYY-MM-DD>_<source>-<topic-slug>.md
│
├── books/                               # 书籍章节 / 整书笔记
│   └── <author>-<book-title>/
│       ├── <chapter-nn>-<slug>.md
│       └── _overview.md                 # 书的总览
│
├── transcripts/                         # 视频 / 播客转录
│   └── <YYYY-MM-DD>_<speaker>-<topic-slug>.md
│
├── docs/                                # 技术文档 / 规范 / README 存档
│   └── <tool-name>-<section>.md
│
├── images/                              # 截图 / 白板照片 / 图表
│   └── <YYYY-MM-DD>_<context>-<description>.png
│
├── chat-exports/                        # AI 对话记录导出
│   └── <YYYY-MM-DD>_<agent>-<topic>.jsonl
│
└── personal/                            # 个人笔记 / 日记草稿
    └── <YYYY-MM-DD>_<topic-slug>.md
```

### 2.1 分类决策树（AI 自动分类用）

```
源文件进来了 →
│
├─ 是 PDF 且含摘要/引用/实验数据？
│  └─ papers/<...>.pdf
│
├─ 是网页文章/博客（Obsidian Web Clipper 导出）？
│  └─ articles/<...>.md
│
├─ 是书籍内容（多章节）？
│  └─ books/<author>-<title>/<chapter>.md
│
├─ 是视频/播客的转录文本？
│  └─ transcripts/<...>.md
│
├─ 是技术文档、API 规范、README？
│  └─ docs/<...>.md
│
├─ 是图片（截图、图表、白板照片）？
│  └─ images/<...>.png
│
├─ 是 AI 对话记录（JSONL）？
│  └─ chat-exports/<...>.jsonl
│
├─ 是个人碎片、随手记？
│  └─ personal/<...>.md
│
└─ 无法判断？
   └─ _inbox/<original-filename>    ← 标记待人工分类
```

---

## 3. 命名约定

### 3.1 通用格式

```
<YYYY-MM-DD>_<source-identifier>_<topic-slug>.<ext>
  │             │                    │
  │             │                    └─ kebab-case, ≤50 chars, 英文/拼音
  │             └─ 来源标识：作者、网站名、演讲者、工具名
  └─ ISO 日期：内容创建/获取日期
```

### 3.2 各类型示例

| 类型 | 模板 | 示例 |
|------|------|------|
| paper | `YYYY-MM_firstauthor-topic-slug.pdf` | `2024-06_vaswani-attention-is-all-you-need.pdf` |
| article | `YYYY-MM-DD_source-topic-slug.md` | `2026-06-25_lilianweng-llm-prompt-engineering.md` |
| transcript | `YYYY-MM-DD_speaker-topic-slug.md` | `2026-06-20_kaparthy-llm-wiki-pattern.md` |
| doc | `tool-section.md` | `python-asyncio-cheatsheet.md` |
| image | `YYYY-MM-DD_context-description.png` | `2026-06-25_meeting-whiteboard-arch.png` |
| chat | `YYYY-MM-DD_agent-topic.jsonl` | `2026-06-25_codex-wiki-setup.jsonl` |
| personal | `YYYY-MM-DD_topic-slug.md` | `2026-06-25_weekly-review.md` |

### 3.3 中文内容处理

如果元信息本身是中文（中文书名、中文演讲者名等），使用拼音 slug：

```
《深入理解计算机系统》  →  books/ryant-ohallaron-csapp/
李沐 "动手学深度学习"   →  transcripts/2026-01-15_limu-d2l-lecture1.md
```

### 3.4 同源多文件

同一来源的多个文件（如一本书的多章节），用目录组织：

```
books/ryant-ohallaron-csapp/
├── _overview.md              ← 书名、作者、总概述
├── ch01-intro.md
├── ch02-representing-info.md
└── ch03-machine-level-code.md
```

同一天同一来源的多个网页文章：

```
articles/2026-06-25_openai-platform-docs/
├── _index.md                 ← 列表索引
├── api-reference.md
├── rate-limits.md
└── function-calling.md
```

---

## 4. AI 自动整理流程

当用户把新文件放进 `_inbox/` 或 `OBSIDIAN_SOURCES_DIR` 根目录时，AI 应执行：

### Step 1: 扫描 `_inbox/`
```bash
ls -la $OBSIDIAN_SOURCES_DIR/_inbox/
```

### Step 2: 对每个文件判断类型并移动
- 读文件头部（前 2KB）判断内容类型
- 根据决策树（§2.1）分类
- 根据命名模板（§3.2）生成目标文件名
- 移动到对应子目录

### Step 3: 报告
```
Sorted 5 files:
  _inbox/paper.pdf    → papers/2024-06_vaswani-attention-is-all-you-need.pdf
  _inbox/clip1.md     → articles/2026-06-25_medium-system-design-interview.md
  _inbox/clip2.md     → articles/2026-06-25_devto-react-server-components.md
  _inbox/photo.png    → images/2026-06-25_meeting-architecture-sketch.png
  _inbox/notes.txt    → personal/2026-06-25_random-ideas.md
```

### Step 4: 提供 ingest 选项
```
Run /wiki-ingest to process these now, or leave them for later.
```

---

## 5. 与 obsidian-wiki 的集成

### 5.1 环境变量设置

```bash
# .env
OBSIDIAN_SOURCES_DIR=/path/to/sources   # 指向上面结构的根目录
OBSIDIAN_RAW_DIR=_raw                   # vault 内的草稿暂存（不改）
```

### 5.2 Manifest 兼容性

本标准与 `.manifest.json` 完全兼容：
- source keys 使用绝对路径 → manifest 自动追踪
- content_hash 去重 → 改名不影响去重
- source_type 字段 → `document`, `image` 等与 manifest 一致

### 5.3 ingest 触发方式

```
"ingest the papers directory"     → 只处理 papers/
"ingest the articles directory"   → 只处理 articles/
"ingest everything"               → 全量
"ingest new sources"              → delta（默认 append 模式，仅处理 manifest 中无 hash 或无记录的文件）
```

---

## 6. 维护规则

| 规则 | 说明 |
|------|------|
| **源文件不可变** | 一旦 ingest 完成，源文件不应手动修改 |
| **修改 = 重新 ingest** | 修改源文件后 manifest 检测到 hash 变化，下次 append 会重新 ingest |
| **删除源文件** | 可以删除（manifest 会标记 deleted），但 wiki 页面保留 |
| **_inbox 定期清理** | 建议每周 AI 自动整理一次 inbox |
| **同名冲突** | AI 自动加 `-2` 后缀，并记录到 log.md |

---

## 7. AGENTS.md 精简引用

将此段加入 `~/wiki-vault/AGENTS.md`：

```markdown
## 源文件组织

所有源文件遵循 `docs/superpowers/specs/2026-06-27-raw-source-organization-standard.md`。
新文件先放入 `_inbox/`，AI 在 ingest 前自动分类到对应子目录。
命名格式: `<YYYY-MM-DD>_<source-id>_<topic-slug>.<ext>`
```
