# P1/P2 修复与验收记录（2026-09-30）

对应[执行计划](superpowers/plans/2026-09-30-p1-p2-remediation.md)。Task 0–8 的代码、回归、分组独立审查和最终跨模块审查已完成，以下记录实际验证结果。

## 基线与工作区

- 基线：`d9d208afdfcf8986d0bfeee64b178c5bd721ed7e`。
- 隔离分支：`codex/p1-p2-remediation`。
- 工作区：`C:/Users/Admin/.codex/worktrees/p1-p2-remediation/personalAIProject`。
- 原工作区 `E:/personalAIProject` 的用户修改、调试工具、配置和未跟踪资料均保留；只复制了已批准的计划。
- 隔离工作区修复前 `go test ./... -count=1 -timeout 120s`、`go vet ./...`、`go build ./cmd/agentd` 均退出 0。此前审查基线是 189 个顶层 Go Test、55.9% 语句覆盖率、9 个 Python fixture；这些数字不代表修复结果。

## 缺陷闭环

| 缺陷 | 失败证据 | 修复与通过证据 |
|---|---|---|
| R1：HA 无过滤历史请求 | 166 实体未形成过滤批次；空实体请求仍发 HTTP | `TestCollectorBatchesAllEntities` 四批覆盖；空过滤、长 URL、失败不推进、重启、checkpoint 写失败重放、终点补采通过 |
| R2：损坏元数据绕过隐私过滤 | 坏 tags/created、重复键、未闭合 YAML 被接受；内部内容进入候选 | `TestInvalidFrontmatterIsRejected`、`TestInvalidMetadataNeverReachesModel` 通过；末尾 YAML 绕过也已补例拒绝；独立审查批准 |
| R3：建议不能确认 | 分析器生成的建议缺结构化可执行配置 | `TestGeneratedSuggestionHasStructuredIntent`、`TestBindingCreatesImmutableSuggestion` 贯通真实分析、绑定新版本、确认、模拟 HA 和归档；管理员负向验证通过 |
| R4a：错误家庭时间 | 缺少家庭时区分析接口 | `TestPatternsUseHomeTimezone` 验证 UTC 10:00→上海 18:00；完整自然日、DST、HA 时区异常/变化覆盖 |
| R4b：初态被当成操作 | 小时级常开初态生成 24 个时间模式 | `TestInitialStatesDoNotCreateTimePatterns` 通过；初态保留时长，unknown/legacy/同刻冲突不造操作 |
| R4c：关联方向不确定 | 同一数据仅输出一个方向 | `TestCorrelationsAreBidirectionalAndDeterministic`、不同日期最低命中数通过 |
| R5：静态 personal 触发词 | 动态按 Vault provider 不存在 | 同 router 实测新增、改名、删除、内部、损坏、修复及同 mtime/长度改动；完整 chain 断言另一 Vault 事实不进入模型；独立审查批准 |
| R6：失效 SID 循环失败 | Node 行为用例 2 项失败，后端缺错误码 | 5 项前端行为通过；后台失效绑定同步清除；存储读取/解析失败已由错误 404 改为 500/session_unavailable，修复文件后原 SID 可恢复 |
| R7：同用户会话交错 | B 在 A 完成前处理，取消后排队 B 仍启动 | FIFO、共享租约、取消回收、长生成/空闲扫描、失败释放和轮次旋转通过；多 WS 问答顺序从全新 store 读取磁盘验证 |
| 普通 Markdown 标题 | EOF 分隔符及文件名回退断言失败 | `TestFilenameTitle` 与 frontmatter 兼容测试通过；显示标题回退不改变触发规则 |
| RAG 冒烟误报通过 | 新增 5 项 fixture 在原脚本失败 | 要求明确问题/唯一事实/来源，JSON 编码请求；14 项离线 fixture 全通过 |

HTTP 集成测试 `TestHTTPRAGChatAndWikiQueryIncludeFactAndSource` 从实际路由进入完整链：chat 使用实体触发，wiki-query 使用不含实体名的问题强制检索；两者都检查模型 system 消息的唯一事实和响应来源路径，排除坏页及其他 Vault 内容。

独立复审发现并补修两个边界：存储故障误报过期 SID；旧版完整结构化时间/日落规则也必须通过当前 HA 时区检查。后者保留原执行 payload，不猜旧规则的生成时区。最终复审和全套验证结果见下文。

## 独立审查

- Vault/Chain/Memory（Task 1/5）：独立审查批准。核查了旧 dense cache 是否绕过当前元数据，结论是每次重建仍先重新读取/验证页面，再复用向量。
- 会话（Task 6/7）：首次发现存储错误误分类；修复后复审批准。真实坏 JSON、文件路径变目录两类测试验证 WS 返回 `session_unavailable`、历史返回 500；修复存储后，同 cookie/SID 继续读取原历史。真正缺失和越权仍返回 not-found。
- HA（Task 2/3/4）：首次发现 legacy 时间规则绕过时区校验；修复后复审批准。time/sun 两种规则 × 五种时区异常共 10 个子场景从错误写入转为零 HA 写入，建议仍为 pending；合法旧 payload 和纯状态规则兼容性测试通过。
- CI、离线脚本与 RAG 文档：独立检查无功能性阻塞；已修正文档中缓存过滤时机和短页面分块的描述。
- 最终整分支审查：批准集成，未发现阻塞的 P1/P2 问题。另修正文档，明确当前 WS 发送经过过滤的完整回复，不发送 source/context 流帧。

## 保留的边界

所有测试使用临时 Vault、模拟推理或 HTTP 服务。没有创建真实 HA 自动化、调用设备服务、修改 HA/Tuya 配置或重启已有服务。

历史请求从 checkpoint 前 60 秒开始，保留重叠真实变化并去重。HA 2026.7.2 排除请求终点；边界事件由下一次重叠请求补入。该窗口不是任意长 recorder 延迟的零丢失承诺。

HA `/api/config.time_zone` 是家庭时间的权威来源；可选 `smarthome.time_zone` 仅校验一致性。条件绑定与最终确认是两个独立管理员操作；绑定不允许任意自动化 JSON，不创建 HA 规则。

新端点 `POST /internal/smarthome/suggestions/:id/bindings` 只接受 `presence.entity_id` / `presence.state`。管理员明确声明家庭聚合 occupancy 语义；允许 group 的 on/home、binary_sensor 或 input_boolean 的 on，并检查实体存在且可用。系统无法从通用 HA 状态独立证明实际家庭成员是否完整聚合，也不会根据实体名称猜测。原 ID 标为 superseded；返回的新 ID 需单独确认。已有 confirmed/applying/failed/ignored 版本不能重新绑定。

浏览器只针对明确 `session_not_found` 或对应历史 404 清除旧 SID，并核对请求 SID。服务器故障、生成失败、容量不足不清除；迟到响应不清除新会话；用户消息不自动重放。

真实设备动作、断 WAN、Tuya 本地控制、现场恢复仍需现场验收，不能依据模拟测试标记完成。

## 最终验证

下列检查均在最后的会话存储和 legacy 时区修正后完成或对应代码保持不变；未使用覆盖原文件的 Go overlay。

| 检查 | 实际命令 / 环境 | 结果 |
|---|---|---|
| Go 全套与覆盖率 | `go test ./... -count=1 -timeout 120s -coverprofile=E:/personalAIProject/go-agent/.gocache/p1-p2-coverage.out` | 退出 0；总语句覆盖率 **64.3%** |
| Go 静态检查 | `go vet ./...` | 退出 0 |
| Windows 构建 | `go build -o E:/personalAIProject/go-agent/.gocache/p1-p2-agentd.exe ./cmd/agentd` | 退出 0 |
| Linux race | Go 1.26.4，`CGO_ENABLED=1`，`go test -mod=readonly -race ./... -count=1 -timeout 120s` | 全包通过，退出 0 |
| Linux 构建 | 同容器 `go build -mod=readonly -o /tmp/agentd ./cmd/agentd` | 退出 0 |
| 前端语法 | `node --check go-agent/static/chat-widget.js` | 退出 0 |
| 前端行为 | `node --test tests/frontend/chat-widget.test.cjs` | **5/5** 通过 |
| Python 离线 fixture | bundled Python，`-m unittest discover -s tests/integration -p 'test_*.py' -v` | **14/14** 通过 |
| 差异格式 | `git -c core.safecrlf=false diff --check`（仅关闭本命令的换行转换提示） | 退出 0，无 whitespace error |

源码现有 **246 个顶层 Go Test 声明**（基线 189）；包含的子测试不计入该数字。覆盖率仅用于观察，缺陷验收依据仍是上表逐项失败→通过证据。

Linux 验证使用独立 `golang:1.26.4` 容器，`--network none`，项目和已有 Go module cache 只读挂载；只把 Go build cache 写入忽略目录。没有访问 HA 或修改运行中的服务。CI 已保留 Go race/coverage、vet、Linux build、Python fixtures，并加入 Node 语法与行为检查；这里报告的是本机实际执行结果，不冒充远程 CI 运行记录。

代码与文档保留在隔离工作区，尚未提交、合并或推送。原 `E:/personalAIProject` 的已跟踪修改仍只有实施前的 `.gitignore` 和 9/28 部署计划。
