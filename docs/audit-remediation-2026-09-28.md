# 审计修复结果与运行说明

本次基于当前未提交工作区修复，没有重置既有修改、提交 Git 或执行真实 HA 设备操作。原始清单见 [审计报告](code-review-and-spec-status-2026-09-28.md)，新接入方案见 [本地 LLM / HA / Tuya LAN 规划](superpowers/specs/2026-09-28-local-llm-ha-tuya-lan.md)。

## 修复映射

| 审计编号 | 本轮处理 | 实现位置 |
|---|---|---|
| F01 / F04 | 管理接口统一 Bearer 认证；浏览器签名身份只可访问自己的会话；会话文件使用哈希路径 | gateway/browser_auth.go、core/session_owner.go、session_store.go |
| F02 | 外部渠道固定 agent Vault；dense store 按 Vault 隔离；过滤 internal 页面 | core/app.go、agent.go、chain/go_steps.go、vault/page_filter.go |
| F03 / F14 | 生成后过滤再发送，替换前端最终文本；全部敏感字段脱敏，引用替换忽略大小写 | gateway/websocket.go、static/chat-widget.js、filter/filters.go |
| F05 | 根目录内读写，拒绝目录穿越、符号链接越界；生成分类允许列表 | vault/filesystem.go、reader.go、chain/vault_steps.go |
| F06 / F08 | 接入 Chain 依赖，启动 HA 采集并清理生命周期 | core/agent.go、app.go、smarthome/manager.go |
| F07 / F15 | 删除密钥、回调密文/正文日志；限制请求体；F15 的短体必 panic 结论已更正 | channel/wecom/crypto.go、callback.go |
| F09 / F10 | 正确 HA config_key 路径；完整保留结构化触发/条件/动作，不支持的语义拒绝执行 | smarthome/client.go、automation.go |
| F11 / F22 | 保留 REST messages/model/metadata/支持的采样参数；失败传播；不支持的请求字段明确拒绝 | gateway/chat_contract.go、http.go、chain/llm_steps.go |
| F12 | 关闭会话释放容量；恢复校验 owner；断线重连保留历史；轮数上限保留最后一答 | core/session.go、session_owner.go、gateway/websocket.go |
| F13 | 流式带取消背压；中断、坏 JSON、扫描器错误不再伪装完成；最终正文完整 | inference/client.go、chain/llm_steps.go |
| F16 / F17 | 缓存保存来源、模型和 Vault；完整清单与内容 hash 检测增删改；失效重建 | vault/embedding.go、bm25.go、inverted.go |
| F18 / F19 | 真实更新 index；最少消息数、返回已有摘要路径、避免同分钟覆盖 | vault/reader.go、memory/sedimentation.go |
| F20 / F21 | 稳定规则 ID；手动/定时分析统一持久化；并发确认与重启幂等；正确累计开启时长 | smarthome/store.go、manager_actions.go、analyzer.go |
| F23 | 有序执行脚本，修复退出状态、Phase 4 路径与鉴权，增加离线 fixture | tests/integration、go-agent/tests/integration |

额外复查发现并处理：WeCom 32 字节 PKCS7 协议、接收方校验和长度溢出；短图像请求路由；后台任务和 WS 的退出；中文检索摘要的 UTF-8 完整性；RRF chunk 合并与 chunk ID；未设置的环境变量不能成为字面凭据；Go 起始 BOM 阻断 coverage。

## 运行行为变化

1. 管理、内部检索、HA 确认、REST 聊天均需 `Authorization: Bearer <AGENT_INTERNAL_KEY>`。未配置管理密钥时拒绝管理请求。
2. WebChat 通过 `POST /auth/browser` 获得 HttpOnly / SameSite=Strict 身份 cookie。它是访客会话身份，不是管理员登录，也不赋予 HA 控制权。会话历史只对当前 owner 可见；不要把此浏览器入口当作私有管理控制台。
3. 为避免过滤前泄漏，外部回答先完整生成并脱敏，再发送。REST `stream=true` 返回 SSE 格式，但当前首个正文需等生成结束；这不是实时逐 token 推送。
4. 旧会话文件仍兼容读取，但没有 owner 信息的历史不会向访客开放。管理密钥轮换会使旧浏览器 cookie 失效。
5. 缺少实体映射的“有人在家”“日落至午夜”等规则确认返回 422。保留条件，不能悄悄转换成无条件开灯。

## 配置与验证边界

提供不含实际密钥的模板 [agent.example.yaml](../go-agent/config/agent.example.yaml)。复制到被 Git 忽略的 `agent.yaml` 后，在启动进程的环境中设置 `AGENT_INTERNAL_KEY`、`PERSONAL_VAULT_PATH`、`AGENT_VAULT_PATH`；按需设置模型/HA/企业微信配置。应用不会自动加载 `config/.env`。未修改用户现有 `agent.yaml` 或密钥。

已有日志若包含旧版输出的 token/AES key，应在相应服务管理界面轮换；本次删除日志代码不会撤销历史凭据。

本轮测试使用临时目录、模拟推理服务和模拟 HA。真实硬件断网验收、WeCom 联网投递、HA 已安装版本兼容性仍待环境验证。当前 Windows Go 使用 CGO_ENABLED=0；本机不能据此声称 race 已通过。CI 配置使用 go.mod 的版本，并在 Linux 上执行 race 与 coverage。

自动化创建调用 HA 的配置组件接口，它不同于公开稳定的服务调用 API，应针对目标 HA 版本做契约验收。httptest 证明本程序构造的路径/正文及错误处理，不证明实际 HA 已接受规则。

## 八荣八耻对应实践

| 原则 | 本轮证据 |
|---|---|
| 查档求证 | HA 配置路由、Ollama、Tuya LAN/SDK、企业微信加密参考实现均查原始文档/源码 |
| 对齐需求 | 保留人工确认，按用户选择采用初始化可联网、日常 LAN 的规划 |
| 不脑补业务 | 条件缺实体映射返回错误；设备型号/DP 未知时不宣称兼容 |
| 复用存量 | 复用 Chain、Vault、HA client；优先 HA 原生 Ollama/Assist |
| 完备测例 | 针对已复现错误补红→绿回归，并用其他代理复查跨模块调用 |
| 恪守规范 | 外部读 agent Vault；外部记忆沉淀仍仅进入 personal/_memory |
| 坦诚存疑 | 更正 F15；区分单元/模拟验证与真实设备验证 |
| 分步迭代 | 按模块分工、独立验证，不重置或提交用户工作区 |

## 尚未完成的新功能

本地模型经聊天触发 HA 控制、Tuya 逐设备接入与断网验收、HA 原生 Assist 配置、外部渠道的家庭控制授权、企业微信建议通知/确认交互、部署 Compose/Helm，以及原计划的事件驱动采集仍未完成。本次修复不应标记这些规划任务为已实现。

企业微信还需区分员工自建应用消息与客户联系/微信客服业务。现有发送器沿用旧 spec 的 `externalcontact/message/send`，本轮没有证明它是目标业务可用的官方接口；不能把 Agent 链接通和密码学回归通过当作端到端消息发送已经可用。该项须核定接入类型后使用对应的官方发送与权限流程，不能靠猜接口替换。

## 最终验证

2026-09-28 最终本机验证：

| 命令/检查 | 结果 |
|---|---|
| `go test ./... -coverprofile=.gocache/coverage.out -count=1 -timeout 90s` | 全部通过；189 个顶层 Go Test 定义（含子用例），整体语句覆盖率 55.9% |
| `go vet ./...` | 通过 |
| `go build ./cmd/agentd/` | 通过 |
| `node --check go-agent/static/chat-widget.js` | 通过 |
| `python -m unittest discover -s tests/integration -p 'test_*.py' -v` | 9 个离线脚本 fixture 通过 |
| `git diff --check` | 通过；仅有 Windows LF/CRLF 提示 |

Go 测试从 `go-agent` 目录执行，GOCACHE 放在项目 `.gocache`；PowerShell 中 coverage 参数使用引号。Python 使用可用的独立运行时，不依赖 pytest。fixture 创建临时 Vault 和 curl 替身，不调用 HA confirm 或设备服务。首次 fixture 的 Git Bash PATH 问题曾导致两个请求连接/域名失败，已改为在 Bash 内强制验证替身路径；没有成功调用真实服务。

交叉复查使用不同代理核对 gateway/core、vault/chain/memory 与 smarthome；发现的问题均通过新增失败用例确认后修复。未运行 Linux CI 或本机 race，因此不能将 CI 配置变更描述为 race 已通过。
