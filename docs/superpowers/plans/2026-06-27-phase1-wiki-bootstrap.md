# Phase 1: Wiki 知识库初始化实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在台式机上完成 obsidian-wiki 框架安装 + vault 初始化 + 首批种子内容录入，从空白状态到可日常使用的 LLM Wiki 知识库。

**Architecture:** obsidian-wiki pip 包安装 Skills → 创建标准 vault 目录结构 → 撰写 AGENTS.md 引导 LLM → 运行 wiki-setup 初始化 → ingest 种子内容 → git init + Syncthing 预埋。纯 Markdown + YAML，零数据库依赖。

**Tech Stack:** Python 3 + pip, obsidian-wiki, Git, Syncthing

---

## 前置检查

- [ ] **Step 0.1: 确认 Python 3 可用**

```bash
python3 --version
```
预期: Python 3.10 或更高

- [ ] **Step 0.2: 确认 pip 可用**

```bash
pip3 --version
```
预期: 版本号正常输出

- [ ] **Step 0.3: 确认 git 可用**

```bash
git --version
```
预期: git version 2.x

- [ ] **Step 0.4: 确认 Syncthing 可安装**

```bash
which syncthing || brew install syncthing
```

---

### Task 1: 安装 obsidian-wiki 框架

**Files:**
- 安装: pip 全局包
- 配置: `~/.obsidian-wiki/config`

- [ ] **Step 1.1: 安装 obsidian-wiki**

```bash
pip3 install obsidian-wiki
```

- [ ] **Step 1.2: 验证安装**

```bash
obsidian-wiki --version
obsidian-wiki list
```
预期: 显示版本号 + 列出 30+ skills 名称

- [ ] **Step 1.3: 查看当前 agent 安装状态**

```bash
obsidian-wiki info
```
预期: 显示 skills 路径和 Codex skills 安装状态

---

### Task 2: 创建 Vault 目录结构

**Files:**
- 创建: `~/wiki-vault/` 及所有子目录

- [ ] **Step 2.1: 创建 vault 根目录和标准子目录**

```bash
VAULT=~/wiki-vault
mkdir -p "$VAULT"/{concepts,entities,skills,references,synthesis,journal,projects,_raw,_meta}
```

- [ ] **Step 2.2: 验证目录结构**

```bash
ls -la ~/wiki-vault/
```
预期: 看到 9 个子目录

- [ ] **Step 2.3: 创建 .gitkeep 确保空目录进入 git**

```bash
for d in concepts entities skills references synthesis journal projects _raw; do
  touch ~/wiki-vault/$d/.gitkeep
done
```

---

### Task 3: 安装 obsidian-wiki Skills 到 Codex

**Files:**
- 安装: `~/.codex/skills/wiki-*`

- [ ] **Step 3.1: 运行 obsidian-wiki setup 指定 vault 路径**

```bash
obsidian-wiki setup --vault ~/wiki-vault
```

- [ ] **Step 3.2: 验证 Codex skills 安装**

```bash
ls ~/.codex/skills/ | grep wiki
```
预期: 列出 wiki-ingest, wiki-query, wiki-setup 等 skill 目录

- [ ] **Step 3.3: 验证 config 文件**

```bash
cat ~/.obsidian-wiki/config
```
预期: 包含 `OBSIDIAN_VAULT_PATH`

---

### Task 4: 撰写个性化 AGENTS.md

**Files:**
- 创建: `~/wiki-vault/AGENTS.md`

此文件是 obsidian-wiki 框架的本地覆盖配置，包含个人偏好和 LLM 行为指引。

- [ ] **Step 4.1: 创建 AGENTS.md**

在 `~/wiki-vault/AGENTS.md` 中写入以下内容：

```markdown
# Wiki Agent 操作约定

> obsidian-wiki 框架的个人化覆盖。Agent 应在每次会话启动时读取此文件。

## 内容域

| 轨道 | 目录 | 主题范围 |
|------|------|----------|
| **技术** | `concepts/`, `entities/`, `skills/`, `references/` | 编程语言、框架、架构模式、论文笔记、工具链、调试技巧 |
| **个人** | `journal/`, `synthesis/` | 日记、目标追踪、读书笔记、兴趣探索、个人反思 |

## 语言约定

- 技术内容: 中文为主，专有名词保留英文
- 个人内容: 中文
- 代码块和命令: 原样保留

## Ingest 偏好

- 网页文章 → 优先 Obsidian Web Clipper 转 Markdown 再放 `_raw/`
- PDF 论文 → 先放 `_raw/`，ingest 时 LLM 提取关键信息
- 聊天记录 → 使用 `wiki-history-ingest` skill
- 碎片想法 → 直接放 `journal/`，不走 ingest 流程

## 页面风格

- Wiki 页面保持客观、结构化
- Journal 页面可以主观、随意
- 每条声明应注明来源
- [[wikilink]] 宁多勿少

## 工具链

- LLM: Codex（主力）/ Claude Code（备用）
- 可视化: Obsidian（图谱视图）
- 同步: Syncthing
- 版本管理: Git
```

- [ ] **Step 4.2: 验证**

```bash
head -5 ~/wiki-vault/AGENTS.md
```

---

### Task 5: 运行首次 wiki-setup（通过 LLM）

**Files:**
- 创建: `~/wiki-vault/index.md`, `log.md`, `hot.md`, `.manifest.json`, `_meta/taxonomy.md`

> 此任务在 Codex 会话中通过 LLM 执行。

- [ ] **Step 5.1: 在 Codex 中执行 wiki-setup**

对 Codex 发送：

> 用 wiki-setup skill 初始化我的 wiki vault，路径 ~/wiki-vault。
> 内容域: 技术 + 个人双轨。创建 index.md / log.md / hot.md / .manifest.json / taxonomy。

- [ ] **Step 5.2: 验证生成的文件**

```bash
ls -la ~/wiki-vault/index.md ~/wiki-vault/log.md ~/wiki-vault/hot.md
cat ~/wiki-vault/.manifest.json | python3 -m json.tool | head -10
```

---

### Task 6: 录入第一批种子内容

**Files:**
- 创建: 首批 wiki 页面 (concepts/ 下 4 篇)
- 修改: index.md, log.md, .manifest.json

- [ ] **Step 6.1: ingest 种子知识条目**

在 Codex 中执行，口述以下四条录入：

> 帮我 ingest 第一批 wiki 内容：
>
> 1. RAG (Retrieval-Augmented Generation) — 基本原理与工作流程
> 2. LLM Wiki 模式 — Karpathy 理念、三层架构、vs RAG 对比
> 3. obsidian-wiki 框架 — pip 安装、Skills 系统、vault 结构约定
> 4. Syncthing — 去中心化文件同步、多设备 vault 同步原理
>
> 每个生成 concept 页，带完整 frontmatter + [[wikilink]]，更新 index.md / log.md。

- [ ] **Step 6.2: 验证种子页面**

```bash
ls ~/wiki-vault/concepts/
head -10 ~/wiki-vault/concepts/*.md
```

- [ ] **Step 6.3: 验证 frontmatter**

每个 concept/*.md 文件应包含 `title`, `category`, `tags`, `sources`, `created`, `updated` 字段。

---

### Task 7: Git 版本管理

**Files:**
- 创建: `~/wiki-vault/.git/`, `.gitignore`

- [ ] **Step 7.1: 初始化 git 仓库**

```bash
cd ~/wiki-vault
git init
```

- [ ] **Step 7.2: 创建 .gitignore**

```bash
cat > ~/wiki-vault/.gitignore << 'GITIGNORE'
# Obsidian workspace
.obsidian/workspace.json
.obsidian/workspace-mobile.json
.obsidian/cache/

# Syncthing
.sync/
*.sync-conflict*

# macOS
.DS_Store

# Trash
.trash/
GITIGNORE
```

- [ ] **Step 7.3: 首次提交**

```bash
cd ~/wiki-vault
git add -A
git commit -m "init: wiki vault bootstrap with obsidian-wiki framework"
```

- [ ] **Step 7.4: 提交种子内容**

```bash
cd ~/wiki-vault
git add -A
git commit -m "seed: initial concept pages (RAG, LLM Wiki, obsidian-wiki, Syncthing)"
```

---

### Task 8: Syncthing 单节点预埋

**Files:**
- 配置: Syncthing 添加 `~/wiki-vault` 文件夹

- [ ] **Step 8.1: 安装并启动 Syncthing**

```bash
brew install syncthing
syncthing &
```

打开浏览器访问 `http://localhost:8384`

- [ ] **Step 8.2: 在 Syncthing Web UI 中添加 vault 文件夹**

- Label: `wiki-vault`
- Path: `~/wiki-vault`
- 状态: "Unshared"（等待 NAS/Mac Mini 到位后加对端）

- [ ] **Step 8.3: 验证 Syncthing 识别**

在 Web UI 中确认文件夹状态为待共享。

---

### Task 9: 最终验证

- [ ] **Step 9.1: 完整性检查**

```bash
echo "=== Vault 结构 ==="
ls -R ~/wiki-vault/ | head -40

echo ""
echo "=== Git log ==="
cd ~/wiki-vault && git log --oneline

echo ""
echo "=== obsidian-wiki 状态 ==="
obsidian-wiki info
```

- [ ] **Step 9.2: 功能验证**

在 Codex 中执行：

> /wiki-query LLM Wiki 模式的核心思想是什么

> /wiki-status

预期: query 返回种子内容的引用链接；status 显示 vault 状态。

---
## 完成后状态

- ✅ obsidian-wiki 已安装，30+ skills 在 Codex 中可用
- ✅ Vault 目录结构就绪
- ✅ AGENTS.md 个性化配置生效
- ✅ 4 条种子概念页面已录入
- ✅ Git 管理运行中 (2 commits)
- ✅ Syncthing 单节点预埋
- ✅ `/wiki-query` 和 `/wiki-status` 可用
