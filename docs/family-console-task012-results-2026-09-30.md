# 家庭控制台 Task 0–2 实施记录

对应 [实施计划](superpowers/plans/2026-09-30-family-console-implementation.md) 与 [设计](superpowers/specs/2026-09-30-family-console-design.md)。本次授权仅覆盖 Task 0、1、2；页面、共享设备/知识投影和后续部署验收仍属于后续任务。

## 基线与范围

- 工作区：`C:/Users/Admin/.codex/worktrees/p1-p2-remediation/personalAIProject`，分支 `codex/p1-p2-remediation`，开始 HEAD `3618ab2`。
- 既有 P1/P2 改动已保存基线清单和文件快照；不覆盖、不重置、不混入新任务提交。
- 开始前执行 `go test ./... -count=1 -timeout 180s`，全包通过，退出 0。
- 测试只用临时目录、模拟推理和 HTTP/WS 服务；不调用真实 HA、不改变家庭设备、不启用公网服务。

## 逐项状态

| 任务 | 实现 | 测试与独立审查 |
|---|---|---|
| Task 0：账号、登录、共享存储 | 提交 `18ca700`、`d7deaf9` | 包测试、vet、全套 Go 回归通过；独立复审批准 |
| Task 1：HTTP 鉴权与成员管理 | 已完成，未提交 | 全 Go 回归、旧鉴权及目录符号链接检查通过；修复 2 项 Important 后独立复审批准 |
| Task 2：会话隔离与可靠保存 | 已完成，未提交 | 2026-10-03 Go 全套、Linux race 和双平台构建通过；独立审查批准，无遗留问题 |

每项完成后记录八荣八耻的八项检查：接口依据、需求范围、业务规则、存量复用、失败到通过测例、架构边界、未验证限制、独立审查与分步改动。

## Task 0 检查

| 原则 | 实际检查证据 |
|---|---|
| 查档求证 | 核对锁定版本 x/crypto v0.48.0 的 Argon2id 源码签名与 KiB 单位；核对现有存储写法及 Go Rename 平台限制 |
| 对产需求 | 实现计划列出的账号、登录、撤销、共享 revision 和绑定取消接口；登录绝对有效期 7 天 |
| 请示规则 | 保持单管理员、成员强制改密、默认不共享，不推测设备权限 |
| 复用存量 | 使用现有 Go 依赖、文件存储和取消机制，不引入新数据库或服务 |
| 完备测例 | 首批缺实现 RED→GREEN；另发现并修复 Unicode 用户名混淆和失效状态链接；覆盖重启、写失败、撤销、容量及切片隔离 |
| 格守规范 | 业务代码仅新增独立 console 包，HTTP 权限留在 Task 1，不反向依赖 gateway/core |
| 坦诚存疑 | 仅支持单进程写入；Windows/其他文件系统断电恢复尚未验证；不把普通替换失败测试等同于断电保证 |
| 分步迭代 | 独立审查发现 3 项 Important，逐项修复后复审全部关闭，无遗留阻断项 |

审查修复：替换后同步失败使 Store 不可用并取消绑定；共享返回值改为副本；读写统一 16 MiB 上限且清理过期会话。新增 `CheckAvailable()` 供后续网关正确返回 503。

当时修复后的验证：`go test ./internal/console -count=1`（2.411s）、`go vet ./internal/console`、`go test ./...` 均退出 0。整组新鲜验证结果见下文。

## Task 1 检查

| 原则 | 实际检查证据 |
|---|---|
| 查档求证 | 核对 Task 0 Store、现有中间件和配置构造器；用 Node WHATWG URL 实测浏览器 Origin 序列化 |
| 对产需求 | 仅交付登录、改密、退出、成员管理、运维初始化/恢复与配置，不提前宣称页面或设备能力 |
| 请示规则 | 管理员初始化/恢复仍需旧管理 Bearer；家庭账号不取得旧全局权限；成员首次登录必须改密 |
| 复用存量 | 复用账号存储、哈希、撤销和错误类型；HTTP 只承担 DTO、权限与请求边界 |
| 完备测例 | 缺接口 RED→GREEN；滚动限流边界、损坏 UTF-8、Origin 序列化与缺失账号目录均有实际失败再修复证据 |
| 格守规范 | console 认证域独立，旧 Bearer/浏览器身份测试通过；账号目录与 Vault/static 分离，3 项符号链接检查实际执行 |
| 坦诚存疑 | 仅支持已列出的规范 ASCII 域名/IP Origin；未配置可信代理，反代用户共用来源 IP 总额度；无真实部署验收 |
| 分步迭代 | 独立审查和父代理核对共发现 2 项 Important，修复后复审全部关闭；保留旧 P1/P2 未提交改动 |

接口包括 `/api/console/v1/auth/{status,login,me,logout,password}`、`/admin/members` 及成员修改/重置；运维接口为 `/internal/console/bootstrap`、`/reset-admin`。登录令牌仅写入 HttpOnly cookie；写操作验证 Origin 和 CSRF。错误返回安全 DTO 与服务端 request_id。

最终修复前全套 `go test ./... -count=1` 通过；两项审查修复后 `go test ./internal/gateway ./internal/core -run Console -count=1` 通过（gateway 9.196s、core 5.571s），`git diff --check` 通过。初始化步骤与 Origin 限制见 [本地账号初始化](../go-agent/docs/console-local-setup.md)。

## Task 2 检查

| 原则 | 实际检查证据 |
|---|---|
| 查档求证 | 核对实际 Store、Owned 会话、WS 和结束消费者接口；明确公开 SID 与内部存储 ID 不同 |
| 对产需求 | 新会话列表、历史详情和 WS 固定 console channel、agent Vault 和当前账号 owner；未新增页面或后续共享功能 |
| 请示规则 | 管理员普通聊天也只能读取本人历史；退出不删除历史；撤销前已进入发布的操作先完成，不能撤回已进入网络的字节 |
| 复用存量 | 复用现有聊天循环、跨连接处理锁、chain、输出过滤及会话结束流程；只增加服务端选项和严格错误列表接口 |
| 完备测例 | 实际 RED 发现索引/缓存/沉淀隐私泄漏、直接覆盖历史、启动吞存储错误；修复后追加跨设备、排队取消、握手竞争、最终发布、写入故障与冷读测试 |
| 格守规范 | console 与旧 webchat 分开，旧入口 FIFO、轮次切换、恢复和取消回归通过；console 结束仍释放容量 |
| 坦诚存疑 | 同步文件系统操作不能被 context 强行中止；Windows 未执行目录 fsync，真实掉电及家庭部署尚未验证 |
| 分步迭代 | 从已保存的 RED/GREEN 继续补测，保留旧改动；独立审查批准规格与质量，无 Critical/Important/Minor 遗留 |

新增 `/api/console/v1/sessions`、`/sessions/:id/messages` 和 `/chat/ws`。同账号不同登录可以读取同一会话；读取他人或不存在的 SID 返回 404，管理员也遵循这一规则；历史损坏/读取故障返回 500。登录撤销关闭空闲和生成中的连接，排队消息不再进入模型或会话。最终保存和发布使用传输无关的账号会话守卫，网络写期限最多十秒，登录剩余期限或调用方期限更短时优先采用。

不进入全局索引的限制覆盖即时写入、冷启动重建和旧缓存；不进入 personal/_memory 的限制通过真实结束消费者验证。原子保存覆盖部分写入、短写、替换失败，旧历史可冷读且临时文件清理。关键会话目录错误阻止启动；模型暂时离线仍能读取已保存历史。

## 2026-10-03 恢复执行验证

上一轮因执行代理额度中断，停在 Task 2 补测阶段。恢复时核对持久记录、HEAD 和未提交文件，继续既有实现，没有重做 Task 0、1 或重置旧 P1/P2 修改。

| 检查 | 实际结果 |
|---|---|
| Windows `go test ./... -count=1 -timeout 180s` | 最终修复后 11 个有测试包全部通过，3 个包无测试文件；gateway 12.205s，包含原 P1/P2 回归 |
| `go vet ./...` | 退出 0，无输出 |
| Linux `go test -mod=readonly -race ./... -count=1 -timeout 300s` | 最终修复后全部通过，退出 0，无 race 报告；gateway 27.977s |
| Windows `go build -o E:/personalAIProject/go-agent/.gocache/console-task012-agentd.exe ./cmd/agentd` | 退出 0；产物放在原工作区忽略的 `.gocache` |
| Linux `go build -mod=readonly -o /tmp/agentd ./cmd/agentd` | 退出 0；产物仅在已移除的临时容器内 |
| 旧前端 Node 语法与行为 | 语法退出 0，行为 5/5 通过 |
| Python 离线 fixture | 14/14 通过，无真实 HA 请求 |
| `git -c core.safecrlf=false diff --check` | 退出 0，无输出 |

Linux 使用已有 `golang:1.26.4`、`CGO_ENABLED=1`，禁用容器网络，代码和依赖缓存只读挂载。测试仅把 Go 构建缓存写入忽略目录；没有启动项目服务或家庭设备操作。

## 最终组合审查

三项任务各自通过审查后，又对本轮组合差异作独立审查，发现并完成一轮修复：

| 发现 | 失败到通过证据与结果 |
|---|---|
| Important：单轮生成超时被当作连接撤销，客户端没有终结帧 | 两个 channel 的短期限真实 WS 测试在修复前均读超时；修复后返回安全 error，保留 SID，不追加迟到答案或重放输入，释放处理锁，同连接下一轮成功且可冷读。生产 300 秒期限仍在取得锁后才开始 |
| Minor：JSON 语法错误返回 422，与计划约定 400 不符 | 空文档、空白、语法/截断、尾随文档/垃圾共 6 项原先返回 422；修复后返回 400。合法 JSON 的字段、类型、缺字段和业务验证保持 422，12 项 HTTP 用例全部通过 |

两项修复均有实际 RED/GREEN 日志，旧撤销/发布顺序/排队取消/FIFO/会话恢复回归通过。最终针对性复审确认两项全部解决，无新增问题；本批次 Task 0–2 规格与质量通过，未留阻断项。最终全套、race、vet、构建和差异检查均针对修复后的代码执行；未变动的 Node/Python 在同日通过。

Task 0 已提交为 `18ca700`、`d7deaf9`；Task 1、2 及最终修复保留为工作区改动，未混入旧 P1/P2 提交、未推送、未合并。分支与工作区保留，供后续 Task 3–8 继续使用。

## 完成边界

Task 0–2 交付后端基础，不代表 React 页面或完整家庭共享边界已交付。控制台默认关闭；Task 3 的知识/HA 运行资料过滤、Task 4 的共享设备与建议投影及后续前端仍须按计划完成，才能做完整家庭使用验收。
