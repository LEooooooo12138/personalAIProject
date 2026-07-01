# 个人 LLM Wiki 知识库系统设计

## 概述

基于 Karpathy LLM Wiki 模式 + obsidian-wiki 框架，构建本地优先的 AI 增强个人知识库。内容域覆盖技术（编程知识、架构笔记、论文、工具文档）+ 个人（日记、目标追踪、读书笔记、兴趣探索），最终形态部署在 NAS 上实现永久存储 + 全家设备访问。

## 架构设计

### 三层角色

| 节点 | 角色 | 在线时段 | 职责 |
|------|------|----------|------|
| **台式机** (U7-265K + 5080 + 48G) | 计算主力 | 白天/工作时 | LLM 直接读写 vault（零延迟）、Ollama embedding、Obsidian 图谱 |
| **Mac Mini** (未来) | 服务网关 | 24/7 | Syncthing hub、Docker(Kryton + qmd)、局域网访问入口、接班后 LLM 读写 |
| **NAS** (未来) | 永久存储 | 24/7 | 权威数据存储、wiki.git 裸仓库、间歇备份 |

### Syncthing 同步网格

```
Phase 1：单节点
  台式机: ~/wiki-vault/ ← Syncthing 管理

Phase 2：双节点
  台式机: ~/wiki-vault/ ←→ NAS: /volume/wiki-vault/

Phase 3：三节点
  台式机: ~/wiki-vault/ ←→ NAS: /volume/wiki-vault/ ←→ Mac Mini: ~/wiki-vault/
  [可选关机]                 [权威数据]                    [主力读写 + 服务]
```

- 同步方向：双向
- 冲突处理：默认（.sync-conflict 副本）
- Mac Mini 读写本地 SSD，Syncthing 秒级同步到 NAS

### 最终形态（Phase 3）

```
NAS (永久数据层)
  /volume/wiki-vault/           ← 权威 Source of Truth
  wiki.git 裸仓库               ← 版本历史

Mac Mini (24/7 计算+服务层)
  ~/wiki-vault/                 ← Syncthing 本地副本，LLM 读写
  Codex/Claude Code             ← LLM 直接读写
  Ollama                        ← 本地 embedding
  Docker
    ├── Kryton:3000             ← Web UI + MCP Server
    └── qmd                     ← 本地语义搜索

任意设备
  浏览器 → http://mac-mini:3000 ← Kryton Web UI (图谱/搜索/浏览)
```

## Vault 目录结构

基于 obsidian-wiki 框架标准：

```
~/wiki-vault/
├── index.md                       # 全局索引（LLM 每次先读）
├── log.md                         # 操作日志（追加式时间线）
├── hot.md                         # 会话热缓存 (~500 词)
├── .manifest.json                 # 来源追踪
│
├── concepts/                      # 抽象概念：架构模式、算法、设计哲学
├── entities/                      # 具体事物：工具、框架、库、公司、人物
├── skills/                        # 方法论：如何搭建、调试、部署
├── references/                    # 备忘参考：API spec、配置模板、命令速查
│
├── synthesis/                     # 交叉分析：多个概念的关联洞察
├── journal/                       # 时间线：每日回顾、总结、想法碎片
│
├── projects/                      # 一个项目一页 (wiki-update 自动同步)
│
└── _raw/                          # 临时中转站，下次 ingest 提升到正式目录
```

每页要求 YAML frontmatter：`title`, `category`, `tags`, `sources`, `created`, `updated`。页面间用 `[[wikilink]]` 互连。

## Skills 启用范围

| 频率 | Skills | 用途 |
|------|--------|------|
| 🔥 每天 | `wiki-ingest` `wiki-query` `wiki-update` | 录入源、查知识、项目同步 |
| 📆 每周 | `wiki-lint` `cross-linker` `tag-taxonomy` | 健康检查、补链接、标签整理 |
| 📊 按需 | `wiki-status` `wiki-dashboard` `wiki-synthesis` | 看全景、交叉洞察 |
| 🧹 偶尔 | `wiki-dedup` `wiki-rebuild` `wiki-export` | 去重、重建、导出 |
| 🏗️ 初始化 | `wiki-setup` `wiki-history-ingest` | 首日初始化、导入聊天记录 |

## Docker 服务

```yaml
services:
  kryton:
    image: azrtydxb/kryton:latest
    ports: ["3000:3000"]
    volumes: ["./wiki-vault:/data"]
    restart: unless-stopped
  qmd:
    image: tobi/qmd:latest
    volumes: ["./wiki-vault:/wiki:ro"]
    command: serve --dir /wiki
    restart: unless-stopped
```

Phase 2 部署在 NAS，Phase 3 迁移到 Mac Mini（改挂载路径即可）。

## 三阶段演进

### Phase 1：台式机单机（现在）
- 本地 vault + Obsidian/Warp + Codex
- 零外部依赖，性能最优
- git 本地版本管理

### Phase 2：NAS 接入（NAS 到货后）
- Syncthing 加 NAS 对端 → 数据冗余
- NAS Docker 部署 Kryton + qmd → 全家设备 Web 访问
- 台式机仍主力读写

### Phase 3：Mac Mini 接班（Mac Mini 到位后）
- 台式机关机 / 仅 Obsidian 浏览
- Mac Mini 接管所有计算：LLM 读写 + Ollama + Docker 服务 + Syncthing
- NAS 作为永久 Source of Truth

### Phase 2 → 3 迁移步骤
1. 台式机停写 → 终态同步
2. NAS 目录设为权威源
3. Mac Mini 挂 Syncthing 加对端 → 拉取 vault
4. scp docker-compose.yml → Mac Mini 启动
5. 设置 OBSIDIAN_VAULT_PATH
6. 验证 ingest → 台式机退役

## 迁移可行性

| 机制 | 为什么无痛 |
|------|-----------|
| `OBSIDIAN_VAULT_PATH` | 环境变量，每台机器独立设置本地路径，不影响 Syncthing 同步的 Markdown 数据 |
| `.manifest.json` | 追踪相对路径（`wiki/concepts/xxx.md`），不依赖机器路径 |
| Skills | 每台机器独立 `obsidian-wiki setup`，skill 逻辑一致 |
| Vault 格式 | 纯 Markdown + YAML，无数据库/二进制/锁文件，Syncthing 天然友好 |
| 冲突处理 | Markdown 极少并发冲突；真有 Syncthing 产生 `.sync-conflict` 副本不丢数据 |
| 写入约定 | 始终以台式机/Mac Mini（当前主力节点）为唯一 LLM 写入节点，其他节点只读查询 |

### 风险评估

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| Syncthing 同步延迟 | 低 | 轻微 | 主力节点关机前等 30s；Mac Mini 服务有 stale 容忍 |
| 多节点同时写 index.md | 极低 | 轻微 | 始终单一写入节点约定 |
| NAS 性能不够跑 Docker | 中 | 中等 | Phase 2 提前验证；不通过则 Docker 留在台式机/Mac Mini，NAS 仅文件存储 |
| 千兆 NFS 读写延迟 | 极低 | 可忽略 | Markdown 文件 KB 级别，千兆延迟 <10ms；在 Mac Mini 本地 SSD 做 Syncthing 副本完全避免问题 |

## 核心原则

- **先跑起来再优化** — Phase 1 用最简配置立即开工，不等到 Phase 2/3
- **数据永远不搬家，只加副本** — 每个阶段都是新增 Syncthing 对端或 Docker 节点
- **LLM 零延迟读写** — 读写始终走本地 SSD，NAS 只是备份和 Web 服务源
- **Markdown 即数据库** — 所有内容可读、可 git diff、可手动编辑、可导出
