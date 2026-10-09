# 本地部署与后续开发实施计划

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task. 本文是待实施计划；勾选只代表有实测证据，不代表文件已存在即完成。

**Goal:** 从一台 Tuya Wi-Fi 设备开始，验证断网可控，再实现本地模型提出动作、用户确认、HA 执行及回读的完整流程。

**Architecture:** 复用当前 Go Agent、Ollama、HA client 与确认状态机。HA 负责 Tuya 协议接入，Go 不重复实现 DP 协议；模型只产生意图，实际动作由服务端权限检查与人工确认决定。HA 原生 Ollama/Assist 先用于只读能力验证，不能作为绕过确认的执行旁路。

**Tech Stack:** Go 1.26.4、Ollama、Home Assistant、Tuya Local/LocalTuya、PowerShell、现有 WebChat。

**Spec:** [本地 LLM / HA / Tuya LAN](../specs/2026-09-28-local-llm-ha-tuya-lan.md)、[CONSTITUTION](../specs/CONSTITUTION.md)。

## 约束与验收重点

- 允许首次配网/取密钥联网，日常控制通过 LAN；不默默回退云服务。
- 所有智能家居写操作保留人工确认；普通访客 Cookie 不获得家庭控制权限。
- 不重置已配网设备；local_key、HA token 仅存本地秘密配置，不发到聊天或 Git。
- 同名设备必须澄清；离线设备明确失败；HTTP 成功不等于设备状态已改变。
- 超时后的结果先标记未知并回读；不直接重放 toggle；确认绑定不可变动作及用户身份。
- 先解决实体与权限，再扩展模型/渠道；Compose、NAS、k3s 放在单设备验收之后。

## 1. 本地先完成的工作

### 1.1 核对现有环境（2026-09-28 已通过终端补查）

以下结果来自本机 CIM、nvidia-smi、Ollama CLI 和监听端口检查；仅记录本次检查时状态。

| 项目 | 实测结果 |
|---|---|
| CPU | Intel Core Ultra 7 265K，20 核 / 20 线程 |
| 系统内存 | Windows 可见物理内存 47.3 GiB；不是当前剩余空闲内存 |
| GPU | NVIDIA GeForce RTX 5080，显存 16,303 MiB（约 16 GiB） |
| GPU 驱动 | 610.47；检查时显存占用 1,132 MiB |
| Ollama | 0.34.4，监听 `127.0.0.1:11434`；`ollama ps` 当时没有已加载模型 |
| Ollama 程序 | `C:\Users\Admin\AppData\Local\Programs\Ollama\ollama.exe`；当前工具终端 PATH 未包含它 |
| gemma4:12b | 7.6 GB，11.9B，Q4_K_M；本机 `ollama show` 明确列出 tools、vision、audio、thinking、completion |
| llava:7b | 4.7 GB，Q4_0；能力为 completion、vision，未列出 tools |
| bge-m3:latest | 1.2 GB，F16；能力为 embedding |
| HA | 用户确认已安装；检查时本机 8123 未监听、HTTP 连接拒绝，尚不能查询其版本/实体 |
| Docker / WSL | Docker Desktop Linux Engine 管道不存在；Ubuntu 与 docker-desktop 均 Stopped。不能据此认定 HA 未安装 |

**据此调整：** 不再要求用户抄硬件和模型清单，不重新安装 HA，不先下载新模型。优先评测已有 gemma4:12b 的受约束意图输出；从小实体集合和约 8K 上下文起步，实际 GPU 占用、延迟与工具准确率仍需运行测试。模型文件大小不等于运行显存占用，262K 标称上下文不作为当前机器的默认设置。bge-m3 继续用于检索；llava 暂保留，后续实测再决定是否合并视觉模型。

**2026-09-29 复查：** 用户已启动 Docker，现已只读确认现有 HA，无需重新安装。

| 项目 | 本次证据 |
|---|---|
| HA 安装方式 | Docker Container，容器名 `ha`，镜像 `homeassistant/home-assistant:latest` |
| 实际版本 | 容器使用的镜像标签元数据与 `/config/.HA_VERSION` 均为 `2026.7.2`，amd64 |
| 持久化配置 | Docker 命名卷 `ha_config` 挂载 `/config` |
| 端口映射 | 宿主机 `8123` → 容器 `8123/tcp`，启动后预期本机地址 `http://localhost:8123` |
| 容器状态 | `exited`，上次结束时间 2026-07-18；退出码 137，但 `OOMKilled=false`，不据此推断内存不足 |
| 重启策略 | `no`，启动 Docker 不会自动拉起此容器 |
| 配置条目 | sun、go2rtc、analytics、backup、shopping_list、google_translate、radio_browser；未发现 Ollama、Tuya、Tuya Local 或 LocalTuya 配置条目 |
| 实体登记 | 21 个；平台为 sun、backup、person、shopping_list、google_translate；Tuya 平台实体 0 个 |

本次通过 Docker 将配置流入内存解析，只输出版本、集成域和数量，没有写出秘密文件、读取认证库、启动容器或调用设备服务。配置登记结果不等于实时实体状态。

**当前下一步：** 启动已有容器 `docker start ha`，等待 HA 就绪并验证网页；然后备份现有配置，接入一台 Tuya 设备。不重建容器、不删除 `ha_config` 卷。需要自动启动时再按部署需求设置重启策略，本轮没有修改。

仍需要补充的非秘密信息：

| 信息 | 用途 |
|---|---|
| 一台灯或插座的品牌、完整型号、Smart Life 中显示的连接类型 | 判断 Tuya 本地集成兼容性 |
| 企业微信是员工自建应用、客户联系还是微信客服 | 后续核定发送 API；不影响 HA 第一阶段 |

可以运行以下只读命令；输出不需要包含账号、token 或 local_key：

```powershell
go version
$taskOllama = 'C:\Users\Admin\AppData\Local\Programs\Ollama\ollama.exe'
& $taskOllama --version
& $taskOllama list
Invoke-RestMethod http://localhost:11434/api/tags | Select-Object -ExpandProperty models | Select-Object name
```

若已有 HA，先做其配置备份。如果没有，Windows 上优先评估官方 HA OS 虚拟机方案；安装前核对虚拟化支持与网络适配器，不在已有 HA 旁随意再建第二套。参考 [HA Windows 安装](https://www.home-assistant.io/installation/windows/)。

### 1.2 启动本项目（用户操作，可先不启用 HA/企业微信）

代码验证与启动从 `E:\personalAIProject\go-agent` 执行：

```powershell
Set-Location E:\personalAIProject\go-agent
$env:GOCACHE = 'E:\personalAIProject\go-agent\.gocache'
go test ./... -count=1 -timeout 90s
go build -o agentd.exe ./cmd/agentd
# 已有配置不要覆盖；首次配置时才复制模板。
if (-not (Test-Path config/agent.yaml)) {
    Copy-Item config/agent.example.yaml config/agent.yaml
}
```

编辑本地 `config/agent.yaml`，核对两个 Vault 目录和模型名。初次验收先设 `channels.wecom.enabled: false`、`smarthome.enabled: false`；待各模块条件齐备后再开启。

模板要求进程环境中的 `PERSONAL_VAULT_PATH`、`AGENT_VAULT_PATH` 和 `AGENT_INTERNAL_KEY`。变量与配置值必须一致；应用不会自动加载 `.env`。建议将配置里的密钥写成 `${AGENT_INTERNAL_KEY}`，不要在共享终端输出真实值。PowerShell 7 可用隐藏输入：

```powershell
$env:PERSONAL_VAULT_PATH = Read-Host '个人 Vault 实际完整路径'
$env:AGENT_VAULT_PATH = Read-Host '对外 Vault 实际完整路径'
$env:AGENT_INTERNAL_KEY = Read-Host 'Agent 管理密钥' -MaskInput
.\agentd.exe -config config/agent.yaml
```

本机访问 `http://localhost:8080/chat`。另一个终端可检查 `Invoke-RestMethod http://localhost:8080/health`；预期 `status=ok`。浏览器会话是受限访客身份，不能确认 HA 操作。

### 1.3 单设备接入与断网验收（用户操作，Agent 协助排查）

- [ ] 从灯或插座中选一台正常工作的设备，记录型号、IP、固件与 HA entity_id。
- [ ] 根据 [Tuya Local 设备配置说明](https://github.com/make-all/tuya-local) 检查支持情况。缺少配置时再评估 LocalTuya，不同时用两个本地集成连接同一设备。
- [ ] 在本地配置界面填写合法取得的 device_id/local_key；不要重配网来“试一下”，以免密钥变化。
- [ ] 由用户在 HA UI 手动开/关，并确认真实设备与 HA 状态同步。
- [ ] 保持 LAN，暂断 WAN，重复开/关；随后重启 HA、重启测试设备再重复。安排在不影响其他家庭设备的时段。
- [ ] 记录成功、失败和恢复情况到 `docs/local-acceptance.md`（后续创建，不含密钥）。

成功标准：至少 10 次明确开/关和状态回读成功；断 WAN 后重启仍可操作；离线时明确不可用，不出现假成功。上述次数是本项目验收标准，不是厂商保证。

### 1.4 本地模型与 HA 连通（用户操作）

- [ ] 先确认现有模型的工具调用能力，不凭模型名字推断。HA 官方只允许支持 Tools 的模型控制实体，且控制功能仍标为实验性。[HA Ollama 文档](https://www.home-assistant.io/integrations/ollama/)
- [ ] HA 添加 Ollama 集成时先不启用“Control Home Assistant”；先确认本地对话、中文理解和延迟。
- [ ] HA 若在虚拟机/容器，填写 HA 能访问的 Windows LAN 地址；不要把 HA 内的 localhost 当成 Windows 主机。
- [ ] 需要跨主机访问时，按 [Ollama 官方网络配置](https://docs.ollama.com/faq) 配置监听地址并重启；防火墙只允许 HA 所在主机/可信 LAN。设置 `OLLAMA_NO_CLOUD=1` 可关闭 Ollama 云功能。
- [ ] 自动化控制接入下一节的确认流程后，再开放写操作。模型评估先使用模拟设备和只读状态。

## 2. 后续开发顺序（尚未实施）

### Task A：实体目录与最小权限

**Files:** 新增 `go-agent/internal/smarthome/catalog.go`、`catalog_test.go`；修改 `internal/core/config.go`、`config/agent.example.yaml`。

**Interfaces:** 复用 `HomeAssistantClient.GetStates(ctx)`；新增 `Catalog.Resolve(name, room string) ([]EntityState, error)`。配置明确允许的 entity_id 和管理员身份，查询目录仅包含允许实体；模糊匹配返回候选集合，不自动执行。

- [ ] 先写 `TestCatalogRejectsUnknownEntity`、`TestCatalogRequiresDisambiguation`、`TestCatalogExcludesUnauthorizedAndUnavailable`，断言未知/重名/不可用/无权限均不产生执行目标。
- [ ] `go test ./internal/smarthome -run TestCatalog -count=1`，观察预期失败。
- [ ] 实现目录及配置校验，复用 HA 返回的实体域、名称与状态。
- [ ] 同一命令通过，提交本任务。

### Task B：即时动作的建议、确认与回读

**Files:** 新增 `internal/smarthome/control.go`、`control_test.go`、`internal/gateway/control.go`、`control_test.go`；修改现有 gateway 路由与配置模板。

**Interfaces:** 新增 `ControlService.Propose(ctx context.Context, owner string, intent ControlIntent) (*ControlProposal, error)`、`Confirm(ctx context.Context, owner, proposalID string) (*ControlResult, error)`。`ControlIntent` 仅包含 entity_id、明确 turn_on/turn_off；`ControlProposal` 包含随机 ID、owner、不可变动作、截止时间和状态；`ControlResult` 包含执行状态、回读状态及脱敏错误。复用 HA `CallService`、`GetState`；复用现有原子存储做法，不把即时动作伪装成 HA 自动化规则。

- [ ] 编写 `TestProposalDoesNotCallHA`、`TestConfirmRequiresOwnerAndUnexpiredProposal`、`TestConcurrentConfirmExecutesOnce`、`TestTimeoutRequiresStateReconciliation`、`TestHTTP200WithoutExpectedStateIsNotSuccess`。
- [ ] 运行 `go test ./internal/smarthome ./internal/gateway -run 'Proposal|Confirm|Reconciliation|ExpectedState' -count=1`，先确认失败。
- [ ] 实现提议/确认端点、持久化状态和回读；初期仅管理员 Bearer 可用，不向匿名 Cookie 放权。确认以服务端存档动作为准，禁止客户端临时改参数。
- [ ] 测试通过并复查没有确认前的 `CallService`，提交本任务。

### Task C：本地模型输出受约束意图，WebChat 展示确认

**Files:** 新增 `internal/chain/control_steps.go`、`control_steps_test.go`；修改 `internal/chain/chains.go`、gateway 控制路由、`static/chat-widget.js`；补 gateway 集成测试。

**Interfaces:** 模型只返回 Task B 的 `ControlIntent` 或澄清问题，经 Task A 解析验证后调用 `Propose`。前端展示具体设备、动作和有效期，确认调用 Task B；聊天模型不能调用 `Confirm`。浏览器管理员会话需独立认证实现与测试，不能把 Bearer 密钥写到 JS、URL 或 localStorage。

- [ ] 先补 `TestModelCannotConfirmAction`、`TestInjectedEntityCannotBypassCatalog`、`TestAmbiguousRoomAsksQuestion`、`TestExpiredConfirmationCannotExecute`、`TestVisitorCannotControlHome`。
- [ ] 运行相关 chain/gateway 测试确认失败。
- [ ] 接入本地模型与确认卡片；任何解析失败/越权/未知字段均返回澄清或拒绝，不尝试猜测调用。
- [ ] Mock 全套通过后，用一台已验收设备执行人工确认测试；断 WAN 重复测试，提交本任务。

### Task D：企业微信、部署与长期运行（独立后续迭代）

1. 明确企业微信业务类型后，查官方发送接口与权限；为请求正文/错误码/回执补契约测试，再复用 Task B 确认流程。当前发送接口未完成真实业务验收。
2. 编写实际 Go + Ollama + HA 的部署方案，避免恢复已淘汰 Python/Sidecar 架构。Compose 与 Windows/HA 网络验证通过后，再考虑 NAS；k3s/Helm 最后处理。
3. 做备份与恢复、凭据轮换、7 天持续运行验证；需要时再增加 HA 事件订阅。确认当前小时轮询的可用性后再优化时效性。
4. 性能优化按实测排序：模型冷启动、首答延迟、检索缓存、HA 回读延迟。外部输出仍以脱敏完整性优先，不能为逐 token 展示恢复过滤前泄漏。

## 3. 各阶段完成门槛

| 阶段 | 必须有的证据 | 尚缺时的下一步 |
|---|---|---|
| 代码基线 | 本机 tests/vet/build、脚本 fixture；远端 CI 状态 | 先修确定失败，不开始真实设备写入 |
| HA/Tuya LAN | 单设备 WAN 断开及重启验收记录 | 核对固件/协议/DP/密钥/网络 |
| 本地模型 | 本地请求、能力、中文歧义及延迟记录 | 评测现有模型，必要时再选择模型 |
| Go 控制闭环 | 未确认不执行、一次确认、结果回读、断网测试 | 修正权限、幂等或状态一致性 |
| 长期部署 | 备份恢复、重启自启、连续运行和监控记录 | 再讨论 NAS/容器/k3s |

本次只交付提交与规划，不自动安装 HA、改变防火墙、配网设备或启动家居动作。需要用户参与的最早事项是 1.1 的信息核对与 HA/Smart Life 登录。
