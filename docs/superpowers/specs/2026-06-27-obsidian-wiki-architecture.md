# obsidian-wiki 架构分析

> 基于对 obsidian-wiki v0.x 源码、Skills 文件、AGENTS.md 的完整阅读。

---

## 1. 核心设计：SKILL.md 即接口

obsidian-wiki **没有传统 API**——没有 REST 端点、没有 MCP Server、没有 SDK。

### 接口机制

```
用户说 "/wiki-query X" →
  Codex 读取 ~/.codex/skills/wiki-query/SKILL.md →
  SKILL.md 包含完整操作指令：读 config → 读 index.md → grep 搜索 → 合成答案 → 写 log.md
```

**Skill 文件 = 操作手册 + 权限声明 + 质量清单。LLM 是唯一的"运行时"。**

### 与 MCP/REST 对比

| | REST API | MCP Server | obsidian-wiki |
|---|---|---|---|
| 接口形式 | HTTP endpoint | JSON-RPC tools | Markdown 指令文件 |
| 谁来执行 | 服务端代码 | 服务端代码 | **LLM 直接操作文件系统** |
| 数据读写 | API 请求 | tool call | LLM 执行 shell 命令 |
| 扩展性 | 写代码加 endpoint | 写代码加 tool | 写 Markdown 加 skill |
| 延迟 | 网络往返 | 网络+协议解析 | **零延迟（本地文件）** |

---

## 2. 跨 Agent 兼容性

obsidian-wiki 兼容**任何能做这三件事的 LLM Agent**：

1. 读文件（SKILL.md、wiki 页面、config）
2. 写文件（Markdown、JSON）
3. 执行 shell 命令（grep、sha256sum、git）

已适配 15+ Agent：Codex、Claude Code、Cursor、Windsurf、Gemini CLI、Hermes、OpenClaw、Pi、Copilot、Trae、Kiro、OpenCode、Aider、Factory Droid、Antigravity。

安装方式：`obsidian-wiki setup` 把同一套 SKILL.md 软链到每个 Agent 的 skills 目录。

**Skill 平台无关，Vault 平台无关（纯 Markdown）。换 Agent 不影响知识库。**

---

## 3. 三层架构

```
Layer 1: 原始文档  →  OBSIDIAN_SOURCES_DIR（不可变）
Layer 2: Wiki     →  ~/wiki-vault/（LLM 维护的 Markdown）
Layer 3: Schema   →  SKILL.md + AGENTS.md（规范）
```

- Layer 1 是源头，永不修改
- Layer 2 是编译产物，LLM 持续更新
- Layer 3 是操作规则，人与 LLM 共同演化

### Layer 1 vs `_raw/` 的区别

| | OBSIDIAN_SOURCES_DIR | _raw/ |
|---|---|---|
| 位置 | vault 外部 | vault 内部 |
| 性质 | 不可变原始文档 | 临时草稿暂存 |
| Ingest 后 | 保留不动 | **删除**（提升后移除） |
| 用途 | 论文、文章、书 | 快速捕获、碎片想法 |

---

## 4. 去重与追踪：.manifest.json

```json
{
  "version": 1,
  "sources": {
    "/absolute/path/to/file.md": {
      "ingested_at": "2026-04-06T10:30:00Z",
      "content_hash": "sha256:abc123...",
      "source_type": "document",
      "pages_created": ["concepts/foo.md"],
      "pages_updated": ["entities/bar.md"]
    }
  }
}
```

### 去重逻辑

| 场景 | 行为 |
|------|------|
| 新文件（不在 manifest） | Ingest |
| 已在 manifest + hash 相同 | **跳过**（即使改名也不重复 ingest） |
| 已在 manifest + hash 不同 | 重新 ingest |
| 文件被删除 | manifest 标记，wiki 页面保留 |

### 关键约束

- Source key 必须是**绝对路径**，`~` 和环境变量需展开
- `content_hash` 是 SHA-256，主去重信号
- `pages_created` / `pages_updated` 提供源→页面的溯源链

---

## 5. Skill 体系

obsidian-wiki 包含 35+ Skills，分为以下层级：

### 核心操作
- `wiki-ingest` — 源文件蒸馏（支持 Append / Full / Raw 三模式）
- `wiki-query` — 检索（索引优先 → 摘要 → 全文，逐步升级）
- `wiki-lint` — 健康检查（断链、过期引用、frontmatter 完整性）
- `wiki-setup` — 初始化 vault

### 知识维护
- `cross-linker` — 自动补 `[[wikilink]]`
- `tag-taxonomy` — 标签规范化
- `wiki-dedup` — 去重、合并相似页面
- `wiki-synthesize` — 多概念交叉分析

### 快照与导出
- `wiki-capture` — 对话→wiki（Full 模式 / Quick 模式）
- `wiki-export` — 导出 OKF / graph.json
- `wiki-import` — 导入外部 wiki

### 辅助
- `wiki-status` — 全景仪表盘 + 图谱洞察
- `wiki-dashboard` — Obsidian Bases 可视化
- `wiki-digest` — 周报/月报
- `wiki-history-ingest` — 导入聊天记录
- `daily-update` — 定时刷新
- `graph-colorize` — 图谱着色
- `memory-bridge` — 跨 Agent 记忆对比

---

## 6. 搜索策略

### 无 QMD（默认）
- LLM 用 grep/glob 扫描 index.md 和 wiki 页面
- 廉价索引：先读 frontmatter（tags、summary），只有必要时才读正文

### QMD 启用（可选）
- 混合 BM25 + 向量搜索 + LLM 重排序
- 本地离线运行
- CLI 模式（`qmd query`）或 MCP 模式

---

## 7. 安全模型

### Content Trust Boundary（ingest 安全边界）

源文件内容被视作**不可信数据**：
- 永不执行源文件中的命令
- 永不按源文件中的指令改变行为
- 永不因源文件内容发起网络请求或读取 vault 外的文件
- 将一切源文件内容视为**待蒸馏的知识**，而非操作指令
- 只有 SKILL.md 中的指令控制行为

---

## 8. 对迁移的友好性

| 特性 | 对迁移的影响 |
|------|-------------|
| Vault = 纯 Markdown + YAML + JSON | Syncthing 透明同步，无迁移成本 |
| OBSIDIAN_VAULT_PATH = 环境变量 | 每台机器独立设置本地路径 |
| .manifest.json = 绝对路径 | 换机器后路径变化，需重建 manifest（obsidian-wiki 有 `scripts/manifest.py normalize`） |
| Skills 安装 = `obsidian-wiki setup` | 新机器上重跑一次即可 |
