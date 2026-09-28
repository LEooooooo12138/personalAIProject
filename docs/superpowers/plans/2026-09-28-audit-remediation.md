# 审计问题修复实施计划

> For agentic workers: 使用 Superpowers 测试驱动修复、分模块执行与独立代码审查。用户已明确授权修复上一轮审计确认的问题。

**Goal:** 修复审计 F01–F23，保持现有接口及工作区修改，并查证、补齐本地 LLM→HA→局域网 Tuya 的规划。

**Architecture:** 复用现有 Go Agent、Chain、Vault、Channel 和 HA 客户端。外部请求认证后仍仅可读取 agent-vault；管理/设备写操作认证并显式确认。文件访问限制在配置根目录；所有设备请求测试使用 httptest，不操作真实设备。

**Tech Stack:** Go 1.26.4、Gin、Gorilla WebSocket、Ollama HTTP、HA REST、原生 JS、Bash。

**Spec:** docs/code-review-and-spec-status-2026-09-28.md、docs/superpowers/specs/CONSTITUTION.md、Phase 2/3/4 现有设计。

## 约束与审查重点

- 当前目录包含用户未提交实现，直接在现状上修复；不创建丢失未提交内容的新基线，不自动提交。
- 先复现、再最小修复、再回归；明确区分缺失的新功能与已存在功能的错误。
- 会话关闭/重连/并发确认、路径分隔符与符号链接、缓存新增/修改/删除、慢流消费者、无凭据访问是必测边界。
- 本次不写入真实 HA 配置，不执行 tools/ha*.py，不读取或输出实际密钥。
- 保留人工确认要求。Tuya 设备类型和离线范围未明确前，只补可验证的接入规划，不臆造设备 ID、local_key 或 DP 映射。

## Task 1：接入权限、会话、Agent 与生命周期

**Files:** internal/core/{agent,app,session,session_store,config}.go、internal/gateway/*、static/chat-widget.js 及对应测试。

- [x] 为匿名管理访问、外部私库、过滤前泄漏、构造依赖、关闭后容量、重连与路径穿越补失败用例。
- [x] 修复认证并配套浏览器认证流程；管理 Bearer 与浏览器身份权限分离，历史/恢复校验所有权。
- [x] Agent 保存链依赖并按可信 Channel 类型选 vault；接通 SmartHome Start/Stop。
- [x] 保留完整 REST 消息、model、metadata、stream/采样语义；传播 result.Error；流式内容先过滤再发送。
- [x] 运行 core/gateway 测试，并回传跨模块接口变化。

## Task 2：Vault、RAG、Chain 与记忆一致性

**Files:** internal/vault/*、internal/chain/*、internal/memory/* 及对应测试。

- [x] 为文件越界、缓存重载、增删改、跨库 dense、internal 页面、index 写回、记忆重复/覆盖补失败用例。
- [x] 统一根目录访问校验；限制生成 category；为 index 添加真实更新操作。
- [x] 检索域与 state.Vault 一致；缓存完整校验与重建，保存完整来源；保留配置注入接口。
- [x] 记忆重复跳过或合并到真实来源，落实最少消息数和唯一写入。
- [x] 兼容上游完整请求/历史/流输出，并修复链失败语义；运行对应包测试。

## Task 3：HA 客户端、规则与状态

**Files:** internal/smarthome/* 及对应测试。gateway HA handler 由 Task 1 所有者集成。

- [x] 用 httptest 固化创建规则路径/正文；用纯函数测试重现条件丢失和一小时变零。
- [x] 保留 typed trigger/condition/action；不支持的自然语言规则拒绝执行，不能悄悄改成开灯。
- [x] 稳定规则 ID，手动/定时分析统一持久化；确认/忽略合法状态迁移、幂等与失败状态。
- [x] 修正开启时长；提供 Manager 确认接口供 gateway 调用。
- [x] 运行 smarthome 单测，不连接真实设备。

## Task 4：过滤、推理、WeCom、脚本和工具链

**Files:** internal/filter/*、internal/inference/*、internal/channel/wecom/*、tests/integration/*、go-agent/tests/integration/*、.github/workflows/check.yml、配置示例。

- [x] 复现多类 secret 过滤、短回调与慢消费者/中断流；修复所有规则遍历、敏感日志和带取消背压。
- [x] 修复 Bash 计数、顺序、路径与失败退出；使用临时 fixture 验证 runner。
- [x] 去除阻断 coverage 的 BOM；CI 从 go.mod 读取版本；增加无秘密配置模板及支持说明。
- [x] 查证 HA Ollama、Tuya 官方集成与 Tuya Local/LocalTuya 的本地控制边界，更新 Phase 4 规划及设备接入验收步骤。

## Task 5：整体验证与复查

- [x] go test ./...、go vet ./...、go build、coverage、前端语法、脚本 fixture 回归。
- [x] 独立复查关键修复及跨模块调用，修复实际发现的问题。
- [x] 记录 F01–F23 修复映射、验证结果、未完成的新功能与真实硬件验收限制。
