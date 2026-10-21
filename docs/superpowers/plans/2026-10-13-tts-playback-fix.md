# TTS 未就绪与播放失败修复计划

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

日期：2026-10-13。用户报告普通及设备回复均可能失败，要求对照 MiMo 官方技术文档核对播放标准。沿用已授权的修复、真实 MiMo 测试与正式重启范围。

## 已核对的根因

1. MiMo 非流式调用为 assistant 文本、audio.format=wav，取 choices[0].message.audio.data 并进行 Base64 解码；流式 pcm16 是另外一条协议，不能混作 WAV。当前实现使用非流式 WAV，与官方文档一致。
2. 正式页面 CSP 为 default-src 'none' 且没有 media-src。useVoice 用 Blob URL 播放，Chrome 报 media-src blockedURI=blob / MediaError 4。相同真实 WAV 能被 AudioContext 解码，加 media-src blob: 后可播放到结束。
3. 未就绪提示混淆不同路由。设备、知识、未标记旧回复强制本地，而本地地址为空；实际近期会话也存在未标记的普通回复。该限制不能通过把私有文本转发 MiMo 解决。
4. resumePlayback 将所有 play() 拒绝都解释为自动播放限制；非 NotAllowedError 应明确报播放失败并释放资源。

## 实施顺序与所有权

- Frontend agent：先重现真实浏览器失败，最小增加 media-src blob:；修改播放器错误分类与清理，补 Go CSP 合同、useVoice 行为测试和真实 HTMLAudioElement 全链路浏览器回归。
- Backend agent：Gateway 返回 speech_local_required / speech_local_unconfigured 明确本地限制和缺配置；保留归属、正文哈希、设备/知识隐私规则；更新中文提示和状态说明，行为红绿验证。
- Root：独立核对官方文档、正式配置和非敏感元数据；新短句真实 MiMo 生成，验证格式与播放；复审最终变更，运行全量 Go/前端检查，复用既有保护部署脚本发布重启。

本轮不因假设修改 WAV 格式/时长限制，不放开媒体任意外部来源，不改变人格原文或配置，不发送家庭设备信息到云端。

## 验收

- 真实有效 MiMo WAV 在原策略下 RED，在最终实际服务策略下解码、play、时间推进与 ended 成功。
- 真实 fixture 的已保存助手回复→job→useVoice→原生 Audio 成功；单元模拟不替代浏览器播放。
- 本地必需但未配置时给可操作的准确原因，普通云端回复不被误拦；NotAllowedError 保留手动播放，其他播放错误清理。
- 全量回归和构建，独立审查，正式运行二进制/资源/CSP匹配，保护文件保持不变。

官方依据：https://mimo.mi.com/docs/en-US/quick-start/usage-guide/audio/speech-synthesis-v2.5 。原始验证证据仅本地保留。
