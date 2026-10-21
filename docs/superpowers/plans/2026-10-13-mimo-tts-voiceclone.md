# MiMo TTS / VoiceClone 接入与部署计划

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

日期：2026-10-13。用户明确要求修复两种 TTS、重启服务；随后确认当前使用 MiMo，未来可能切换本地，暂无参考音频但必须保留入口。此授权覆盖在现有功能上接入、验证并部署，无需重复设计审批。Superpowers debugging/brainstorming/writing-plans/TDD/verification 用于根因、设计、红绿和验收。

## 根因与范围

现有普通 TTS API/任务/播放器链路存在，但运行设置与真实上游尚未验证；model/voice 硬编码。VoiceClone 完全缺少配置及参考音频通路。新设备人格回复的 event/device_result 标记需要纳入强制本地朗读判断。运行静态目录在 p1-p2-remediation 工作目录，最新源在 component-library-reset，仅重启不发布新前端。

## 设计与固定接口

- assistantconfig.Settings 增加 tts_model、tts_voice；旧记录默认 mimo-v2.5-tts/冰糖。Update 对新字段使用可选指针，旧客户端省略时保留当前值。模型仅允许 mimo-v2.5-tts 和 mimo-v2.5-tts-voiceclone；普通音色允许 mimo_default、冰糖、茉莉、苏打、白桦、Mia、Chloe、Milo、Dean。保留现有 local/hybrid 和 loopback local_tts_url。新字段只配置 MiMo 分支，不能把克隆参考发往未知本地适配器。
- server-only VoiceReferenceContent() ([]byte,error)；VoiceReference() (VoiceReference,error)；SaveVoiceReference(audio []byte, expectedRevision string) 与 RemoveVoiceReference(expectedRevision string) 返回元数据。元数据为 exists/size_bytes/updated_at/revision。存储固定 secrets/tts-reference.dpapi，复用目录限制、原子写、Windows DPAPI；不返回原始样本，revision 控制覆盖/删除。缺失音频是可用的未配置状态。
- GET/PUT /admin/tts-reference；POST /admin/tts-reference/remove。PUT JSON {filename,audio_base64,expected_revision}，限制原始6MiB，WAV/MP3，最大60秒，复用本地受限 FFmpeg 转码（MP3）及 PCM16 WAV 校验。上传只保存，不调用云端。无样本时克隆朗读明确 voice_reference_required，不静默换音色。
- speech.Config 增加 TTSModel、TTSVoice、VoiceReferenceWAV []byte。speech.NormalizeVoiceReference(ctx,b,mimeType,ffmpeg) 仅接受 WAV/MP3、限制大小/时长，不接受URL。cloudTTS 选模型：普通音色传枚举；克隆传 data:audio/wav;base64,...，朗读文字仍在 assistant 消息；不附人格原文或历史。配置/job复制样本，不能让异步读到后续可变数据。local 路径保持现有 tts-1/alloy 协议。
- Gateway 在检查已保存正文和本地隐私限制后，仅真正可能使用 MiMo 克隆时加载样本；缺失报 voice_reference_required。参考变更和设置变更取消既有语音任务；保存/删除需 admin+CSRF+revision，并在解码后重新鉴权。
- UI 在原助手设置增加模型和内置音色选择；新增参考音频状态、上传/替换/移除入口，说明上传不调用MiMo、克隆朗读会发送样本及朗读文字。缺失音频保留入口，明确提示。聊天语音状态显示所选类型，不把配置状态称为连接成功。
- 固定的设备/知识/旧消息本地朗读边界保留；新EventID和DeviceResult即使无附件也强制local且去除云端密钥/参考样本。

## 顺序与所有权

1. Gateway 隐私漏洞红绿修复（audit_tts_integration）：仅守卫和专门tests，已复现。
2. Store + speech（独立agent）：上述存储/配置/模型/音频API，单元红绿；不改gateway/frontend。
3. Frontend（独立agent）：api类型/AssistantPage/VoiceControls +专门tests；不改backend。
4. Root：Gateway参考管理和speech配置接线/权限/状态/回归；小型服务器内工具读取现有配置元数据（不打印密钥），以用户确认MiMo为授权，用不含私人信息的短文本验证真实普通TTS。用户无样本，克隆用fixture验证契约，绝不假装真实音色验收。
5. 全量Go/前端/构建、独立接口安全复审，浏览器入口验证。部署带回滚：保留账户、OAuth、DPAPI、人格、知识和历史；保护性备份、替换binary/static并隐藏重启，核对PID/端口/health/资产哈希。

## 验收

旧加密设置可读、旧更新保持新值、非法模型/音色拒绝；音频缺失/损坏/过大/格式/版本冲突/路径链接拒绝；克隆payload无其他私有资料；取消/权限/归属/hash边界；设备事件不外传；上传/删除/切会话晚响应不污染新身份。真实普通MiMo生成单独记录成功或具体供应商错误；VoiceClone入口与契约验收不等于真实音色成功。运行服务必须实际更新并重启，不能只报告候选构建。

官方接口核对（2026-10-13）：https://mimo.mi.com/docs/en-US/quick-start/usage-guide/audio/speech-synthesis-v2.5 。普通模型voice枚举；clone的audio.voice是WAV/MP3 data URL，base64小于10MB；文字为assistant消息，返回message.audio.data。本产品采用更小的6MiB/60秒上传限制和规范化WAV。
