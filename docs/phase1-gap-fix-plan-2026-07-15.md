# Phase 1 缺口补全 — 完整修改方案

> 日期：2026-07-15
> 范围：纯 Phase 1 内容，不涉及 Phase 2/3/4
> 状态：方案阶段，待讨论确认后实施

---

## 目录
1. [Gap 1: phase1_1_vault_bootstrap.sh](#gap-1)
2. [Gap 2: phase1_5_end_to_end.md](#gap-2)
3. [Gap 3: run_all.sh](#gap-3)
4. [Gap 4: 单元测试补齐](#gap-4)

---

## <a id="gap-1"></a>Gap 1: `tests/integration/phase1_1_vault_bootstrap.sh`

### 当前状态
- 文件不存在
- 但两个 vault 实际已存在且结构完整：
  - personal: AGENTS.md ✓, .manifest.json ✓, concepts/ (4 个 .md) ✓, 9 子目录 ✓
  - agent: AGENTS.md ✓, .manifest.json ✓, concepts/ (4 个 .md) ✓, 9 子目录 ✓

### 设计文档要求的验证点
```
1. 验证两个 vault 目录结构就绪
2. 验证 AGENTS.md 存在
3. 验证 .manifest.json 可解析
4. 验证 seeds 数量 >= 4
```

### 需要新增的内容
**新建文件**: `E:\personalAIProject\tests\integration\phase1_1_vault_bootstrap.sh`

**脚本逻辑**（参照已有脚本风格，bash + curl + python3 JSON 解析）:
```
1. 定义路径: PERSONAL="C:\Users\Admin\vaults\personal", AGENT="C:\Users\Admin\vaults\agent"
2. 必要子目录列表: concepts entities skills references synthesis journal projects _raw _meta
3. 对每个 vault:
   a. 检查目录存在
   b. 检查每个必要子目录存在
   c. 检查 AGENTS.md 存在且非空
   d. 检查 .manifest.json 存在且可被 python3 json.load 解析
   e. 检查 concepts/*.md 文件数 >= 4
4. 输出 PASS/FAIL，任一步失败 exit 1
5. 最后验证 git 仓库存在（git rev-parse --git-dir）
```

**实现细节**:
- 用 PowerShell 风格写但保持 `.sh` 扩展名（已有脚本都用 bash shebang，实际在 Windows 上通过 Git Bash 或 WSL 运行）
- `.manifest.json` 解析验证：`python3 -c "import json; json.load(open('...'))"` 检查退出码
- 种子内容计数：`ls concepts/*.md | wc -l`

### 影响分析
- 纯新增文件，不影响任何现有代码
- 不依赖 go-agent 运行
- 只读取文件系统

---

## <a id="gap-2"></a>Gap 2: `tests/e2e/phase1_5_end_to_end.md`

### 当前状态
- `tests/e2e/` 目录存在但为空
- Phase 1.5 全链路实际已通过 chain 系统跑通

### 设计文档要求的 E2E 步骤
```
1. 启动 Ollama（确保 gemma4:12b 已加载）
2. 启动 Inference Service: uvicorn main:app --port 8000
3. 启动 Go Agent: .\agentd.exe
4. 设置 Codex 环境变量: OPENAI_BASE_URL + OPENAI_API_KEY
5. 发送: "用中文解释什么是 RAG"
6. 验证: 回复包含 RAG 的核心概念，提及 obsidian-wiki vault 中的种子内容
```

### 需要新增的内容
**新建文件**: `E:\personalAIProject\tests\e2e\phase1_5_end_to_end.md`

**文档结构**:
```markdown
# Phase 1.5 端到端验证

## 前置条件
- [ ] Ollama 已安装，gemma4:12b 已拉取
- [ ] Go Agent 已编译（go build -o agentd.exe ./cmd/agentd）
- [ ] config/agent.yaml 配置正确
- [ ] .env 文件已配置 AGENT_INTERNAL_KEY

## 步骤

### 1. 启动 Ollama
ollama serve
验证：curl http://localhost:11434/api/tags

### 2. 启动 Go Agent
$env:AGENT_INTERNAL_KEY = "your-key"
.\agentd.exe
验证：curl http://localhost:8080/health → {"status":"ok"}

### 3. 发送测试请求
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-key" \
  -d '{"model":"auto","messages":[{"role":"user","content":"用中文解释什么是 RAG"}]}'

### 4. 验证标准
- [ ] 返回 HTTP 200
- [ ] choices[0].message.content 非空
- [ ] 回复包含 RAG 核心概念（检索增强生成）
- [ ] Agent 日志显示完整的请求链
- [ ] 完成后 agent-vault/log.md 有操作记录

## 常见问题
- Ollama 未启动 → 检查端口 11434
- 模型未加载 → ollama pull gemma4:12b
- Agent 启动失败 → 检查 config/agent.yaml 路径
```

**由于 Python Inference Service 已被 Go 原生替代（CONSTITUTION §八 注记），步骤 2 简化为只启动 Go Agent。**

### 影响分析
- 纯文档文件，不影响代码
- 固化手动测试步骤，后续任何人可按此文档复现

---

## <a id="gap-3"></a>Gap 3: `tests/integration/run_all.sh`

### 当前状态
- 文件不存在
- 4 个集成测试脚本存在且可用（phase1_2 到 phase1_5）

### 设计文档要求
> Phase N 开发前必须跑通 Phase 1..N-1 的全量集成测试

### 需要新增的内容
**新建文件**: `E:\personalAIProject\tests\integration\run_all.sh`

**脚本逻辑**:
```
1. 定义脚本列表（按 Phase 顺序）:
   - phase1_1_vault_bootstrap.sh
   - phase1_2_ollama.sh
   - phase1_3_agent_gateway.sh
   - phase1_4_inference_service.sh
   - phase1_5_full_chain.sh

2. 对每个脚本:
   a. 检查文件存在，否则 WARN 并跳过
   b. bash 执行脚本
   c. 记录退出码（零 = PASS，非零 = FAIL）
   d. 收集结果

3. 汇总输出:
   PASS: X/5
   FAIL: Y/5
   SKIP: Z/5

4. 任一失败则 exit 1
```

**注意**: phase1_4 测试 Python Inference Service，但当前架构已无 Python 服务。该脚本需要被标记为 SKIP 或更新为验证 Go 原生 inference 客户端。

### 影响分析
- 纯新增脚本，不影响代码
- 它引用的子脚本必须各自已存在

---

## <a id="gap-4"></a>Gap 4: 单元测试补齐（9 个模块）

### 总览

| 模块 | 可测试性 | 优先测试内容 | 预计用例数 |
|------|:---:|------|:---:|
| `core/router.go` | 高 | Decide() 6 条路由规则 + hasImage() | 8 |
| `core/session.go` | 高 | GetOrCreate, AddMessage, EndSession, scanExpired, CloneSession | 9 |
| `core/session_store.go` | 中 | Initialize(Save/Load), ListSessions, GetMessages | 5 |
| `core/agent.go` | 中 | sessionToConversation, Agent 初始化 | 3 |
| `channel/manager.go` | 高 | Register, Get, StartAll/StopAll | 5 |
| `filter/filters.go` | 高 | 5 个 Filter + Chain.Apply | 10 |
| `memory/sedimentation.go` | 高 | JudgeDecide, tokenize, TF-IDF, cosineSimilarity, slugify, WriteMemory | 10 |
| `memory/summarizer.go` | 中 | extractJSON | 3 |
| `gateway/websocket.go` | 低 | newSessionID, buildHistoryAsMap | 2 |

**总计约 55 条测试**

---

### 4.1 `core/router_test.go`（纯逻辑，无需 mock）

**新建文件**: `E:\personalAIProject\go-agent\internal\core\router_test.go`

**已有基础**: `config_test.go` 中已有 `TestRoute_AlwaysLocal` 覆盖 4 个 legacy Route() 用例

**新增用例**:

| # | 用例名 | 输入 | 预期 |
|---|--------|------|------|
| 1 | `TestDecide_ExplicitModel` | body=nil, hint="gemma4:12b" | TargetModel=gemma4:12b, Backend=local, Reason="explicit model hint" |
| 2 | `TestDecide_CloudHint` | body=nil, hint="cloud" | TargetModel=deepseek-chat, Backend=cloud, Reason 含 stub |
| 3 | `TestDecide_ImageDetection` | body 含 "image_url" | TargetModel=visionLocal, Reason 含 vision |
| 4 | `TestDecide_Base64Image` | body 含 "data:image/" | TargetModel=visionLocal |
| 5 | `TestDecide_SensitiveContent` | metadata["sensitive"]="true" | TargetModel=defaultLocal, Backend=local |
| 6 | `TestDecide_SkillOperation` | metadata["skill"]="wiki-query" | TargetModel=defaultLocal |
| 7 | `TestDecide_AutoDefault` | body=nil, hint="auto" | TargetModel=defaultLocal, Reason 含 auto |
| 8 | `TestDecide_CustomRouter` | NewModelRouter("modelA", "modelB") | defaultLocal=modelA, visionLocal=modelB |
| 9 | `TestHasImage_NoImage` | body 纯文本 <100 bytes | false |
| 10 | `TestHasImage_WithImageUrl` | body 含 "image_url" | true |
| 11 | `TestHasImage_WithBase64` | body 含 "data:image/png;base64,..." | true |

**依赖**: `core/router.go` — 纯函数，零外部依赖

---

### 4.2 `core/session_test.go`（纯逻辑 + 并发安全）

**新建文件**: `E:\personalAIProject\go-agent\internal\core\session_test.go`

| # | 用例名 | 输入/操作 | 预期 |
|---|--------|----------|------|
| 1 | `TestGetOrCreate_New` | GetOrCreate("ch1", "user1") | 返回非 nil Session，State=Active，RoundCount=0 |
| 2 | `TestGetOrCreate_Existing` | 两次同一 key | 返回同一 Session 指针 |
| 3 | `TestGetOrCreate_EndedCreatesNew` | Get→End→Get | 新 Session，不同指针 |
| 4 | `TestAddMessage_User` | Add user message | RoundCount++，返回 true |
| 5 | `TestAddMessage_Assistant` | Add assistant message | RoundCount 不变，返回 true |
| 6 | `TestAddMessage_RoundLimit` | 连续 Add 20 轮 user msg | 最后一次返回 false |
| 7 | `TestEndSession_StateTransition` | Active→EndSession | State=Ending，EndChan 收到通知 |
| 8 | `TestCompleteSession` | Ending→CompleteSession | State=Closed |
| 9 | `TestCloneSession` | CloneSession(s) 后修改原 | clone 不受影响（深拷贝） |
| 10 | `TestActiveCount` | 创建 N 个 active | ActiveCount() = N |
| 11 | `TestSessionLimit` | 超过 MaxSessions 的创建 | 返回 error |
| 12 | `TestScanExpired_IdleTimeout` | 设置 IdleTimeout=1ms，等待 | 过期 session 进入 Ending |

**依赖**: `core/session.go`，`go.uber.org/zap`（传入 `zap.NewNop()`），标准库

**注意**: 测试需要直接调用 SessionManager 的内部方法。Go 同包测试可以访问私有字段和函数。

---

### 4.3 `core/session_store_test.go`（文件系统 + mock inference）

**新建文件**: `E:\personalAIProject\go-agent\internal\core\session_store_test.go`

| # | 用例名 | 操作 | 预期 |
|---|--------|------|------|
| 1 | `TestSaveAndLoadSession` | Save→重新 Initialize | 恢复的 session 消息数一致 |
| 2 | `TestListSessions` | Save 3 sessions | ListSessions() 返回 3 条 |
| 3 | `TestListSessions_ChannelFilter` | 不同 channel 的 sessions | filter 正确过滤 |
| 4 | `TestGetMessages_Memory` | 从活跃 session 读取 | 返回内存中的消息 |
| 5 | `TestGetMessages_DiskFallback` | 非活跃 session | 从磁盘文件读取 |
| 6 | `TestEmbeddingCache_SaveLoad` | Index 后保存缓存 | 重新 load 后 messages 一致 |
| 7 | `TestSearchSessions` | 索引几条消息后搜索 | 返回相关结果 |

**依赖**: `core/session_store.go`，`os.ReadFile/WriteFile`，临时目录

**注意**: Embedder 接口需要 mock：

```go
type mockEmbedder struct {
	vec []float32
}
func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return m.vec, nil
}
```

---

### 4.4 `core/agent_test.go`（轻量）

**新建文件**: `E:\personalAIProject\go-agent\internal\core\agent_test.go`

**只测不需要外部依赖的纯函数**:

| # | 用例名 | 操作 | 预期 |
|---|--------|------|------|
| 1 | `TestSessionToConversation` | 传入 Session 含 2 user + 2 assistant | Conversation.Messages 长度=4，角色顺序正确 |
| 2 | `TestSessionToConversation_Empty` | 空 Session | Messages 长度=0 |
| 3 | `TestNewAgent_Initialization` | 传入 AgentDeps | 所有字段正确赋值 |

**不测**: `Run()` 和 `handleMessage()` — 需要完整的 mock 链（SessionManager + ChannelManager + ChainExecutor + FilterChain），投入产出比低

---

### 4.5 `channel/manager_test.go`（in-memory）

**新建文件**: `E:\personalAIProject\go-agent\internal\channel\manager_test.go`

| # | 用例名 | 操作 | 预期 |
|---|--------|------|------|
| 1 | `TestRegisterAndGet` | Register → Get | 返回同一 channel |
| 2 | `TestGet_NotFound` | Get 未注册的 ID | 返回 error |
| 3 | `TestStartAll_Success` | Register → StartAll | 所有 Start() 被调用 |
| 4 | `TestStartAll_StopsOnError` | 第二个 channel Start 返回 error | error 传播 |
| 5 | `TestStopAll` | StartAll → StopAll | 所有 Stop() 被调用 |
| 6 | `TestRun_FanIn` | 2 channel → Run → 发送消息 | handler 收到两条消息 |
| 7 | `TestRun_ContextCancel` | ctx cancel | Run 退出 |

**依赖**: `channel/manager.go`，需要实现一个 `mockChannel`：

```go
type mockChannel struct {
	id     string
	typ    Type
	msgCh  chan Message
	startErr error
	started  bool
	stopped  bool
}
// 实现 Channel 接口的 5 个方法
```

---

### 4.6 `filter/filters_test.go`（纯逻辑，最易测）

**新建文件**: `E:\personalAIProject\go-agent\internal\filter\filters_test.go`

| # | 用例名 | 输入 | 预期 |
|---|--------|------|------|
| 1 | `TestPII_Phone` | "我的电话是13800138000" | 替换为 "[PHONE]" |
| 2 | `TestPII_Email` | "邮箱test@example.com联系" | 替换为 "[EMAIL]" |
| 3 | `TestPII_IDCard` | "身份证110101199001011234" | 替换为 "[ID_CARD]" |
| 4 | `TestPII_NoMatch` | "正常文本无敏感信息" | text 不变，filtered=false |
| 5 | `TestSensitive_Token` | "token=abc123def" | 替换为 "token=[REDACTED]" |
| 6 | `TestSensitive_ApiKey` | "api_key: sk-xxx" | 替换为 "api_key=[REDACTED]" |
| 7 | `TestPersonalRef_Pattern` | "参考 personal-vault 中的" | 替换为 "[internal-reference]" |
| 8 | `TestPlatform_WeCom` | platform="wecom" + markdown 图片 | 图片替换为 [image] |
| 9 | `TestPlatform_WeCom_HTML` | platform="wechat" + HTML标签 | HTML 移除 |
| 10 | `TestPlatform_Default` | platform="" | text 不变 |
| 11 | `TestLength_Truncate` | 2500 字符文本 | 截断到 maxChars，含 [truncated] |
| 12 | `TestLength_Short` | 100 字符 | 不变 |
| 13 | `TestChain_AllFilters` | 含 PII + 长文本 | 所有 filter 依次执行，返回 records |
| 14 | `TestChain_EmptyText` | "" | 返回空，无 records |

**依赖**: `filter/filters.go` — 零外部依赖，纯 Go 标准库

---

### 4.7 `memory/sedimentation_test.go`（纯算法逻辑）

**新建文件**: `E:\personalAIProject\go-agent\internal\memory\sedimentation_test.go`

| # | 用例名 | 输入 | 预期 |
|---|--------|------|------|
| 1 | `TestJudgeDecide_KnowledgeContent` | "什么是 RAG？怎么实现？" | worthy=true |
| 2 | `TestJudgeDecide_Greeting` | "你好" | worthy=false, reason 含 greeting |
| 3 | `TestJudgeDecide_ShortGreeting` | "hi" | worthy=false |
| 4 | `TestJudgeDecide_EmptyConversation` | 空 Messages | worthy=false |
| 5 | `TestJudgeDecide_TechnicalQuestion` | "配置 docker 时遇到 error" | worthy=true |
| 6 | `TestTokenize_English` | "Hello World" | ["hello", "world"] |
| 7 | `TestTokenize_Chinese` | "你好世界" | ["你","好","世","界"] |
| 8 | `TestTokenize_Mixed` | "RAG是什么" | ["rag","是","什","么"] |
| 9 | `TestComputeTFIDF_Basic` | 2 篇文档 | 相同词在两篇中权重不同 |
| 10 | `TestCosineSimilarity_Identical` | 相同向量 | 接近 1.0 |
| 11 | `TestCosineSimilarity_Orthogonal` | 无重叠词 | 0 |
| 12 | `TestSlugify_Basic` | "Hello World 测试" | "hello-world-测试" |
| 13 | `TestSlugify_ConsecutiveDashes` | "a  b" | "a-b" (无连续短线) |
| 14 | `TestDedupCheck_NoExisting` | 空白 memory 目录 | IsNew=true |
| 15 | `TestWriteMemory_Format` | 完整参数 | 生成正确 YAML frontmatter + 节标题 |

**依赖**: `memory/sedimentation.go`，`vault` 包（IsCJK, IsAlphaNum, IsStopWord, Truncate），临时目录

---

### 4.8 `memory/summarizer_test.go`（轻量）

**新建文件**: `E:\personalAIProject\go-agent\internal\memory\summarizer_test.go`

| # | 用例名 | 输入 | 预期 |
|---|--------|------|------|
| 1 | `TestExtractJSON_Valid` | `{"title":"test","decisions":"x","follow_ups":"y"}` | 正确解析三个字段 |
| 2 | `TestExtractJSON_WithExtraText` | `前缀 {"title":"t","decisions":"d","follow_ups":"f"} 后缀` | 正确提取 JSON |
| 3 | `TestExtractJSON_Invalid` | "not json at all" | 返回 error |

**不测**: `Summarize()` — 需要真实 LLM 调用，属于集成测试范畴

---

### 4.9 `gateway/websocket_test.go`（极轻量）

**新建文件**: `E:\personalAIProject\go-agent\internal\gateway\websocket_test.go`

| # | 用例名 | 操作 | 预期 |
|---|--------|------|------|
| 1 | `TestNewSessionID` | 调用两次 | 两次返回不同值 |
| 2 | `TestNewSessionID_Length` | 调用 | 长度 ≥ 16 |

**不测**: WebSocket 连接管理、消息循环 — 需要真实网络连接，属于集成测试

---

## 五、执行顺序

```
第一组（无依赖，可并行）:
  ├── Gap 1: phase1_1_vault_bootstrap.sh      ← 纯脚本
  ├── Gap 2: phase1_5_end_to_end.md            ← 纯文档
  ├── Gap 3: run_all.sh                        ← 纯脚本
  ├── router_test.go                           ← 零依赖
  ├── filters_test.go                          ← 零依赖
  └── session_test.go                          ← 只依赖 zap（已有）

第二组（依赖第一组的基础）:
  ├── sedimentation_test.go                    ← 依赖 vault 包
  ├── summarizer_test.go                       ← 轻量
  └── manager_test.go                          ← 需要 mock channel

第三组（依赖文件和 mock）:
  ├── session_store_test.go                    ← 需要文件系统 + mock embedder
  ├── agent_test.go                            ← 需要完整 Session 结构
  └── websocket_test.go                        ← 极轻量
```

## 六、风险评估

| 风险 | 等级 | 应对 |
|------|:---:|------|
| Phase 1.4 测试脚本引用了 Python Inference Service（已废弃） | 低 | run_all.sh 中标记该脚本为 SKIP 或添加注释说明 |
| session_store_test 依赖真实文件系统 | 低 | Go `t.TempDir()` 提供隔离临时目录 |
| manager_test 需要 mockChannel | 低 | 在一个文件内定义，约 30 行 |
| gateway wsConn 的私有方法无法从外部测试 | 低 | 同包测试可访问私有方法 |

---

## 七、总计

| 类型 | 新增文件数 | 预计代码行数 |
|------|:---:|:---:|
| Shell 脚本 | 2 | ~80 |
| Markdown 文档 | 1 | ~60 |
| Go 测试文件 | 9 | ~800 |
| **合计** | **12** | **~940** |

预计实施时间：2-3 小时（大部分是纯逻辑测试，不需要启动服务）。
