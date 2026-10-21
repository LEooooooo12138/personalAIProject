import { errorText } from '../api'
import type { useVoice } from './useVoice'
import { Alert, Button, Checkbox, Icon, StatusBadge } from '../ui'

export default function VoiceControls({ voice, disabled }: { voice: ReturnType<typeof useVoice>; disabled: boolean }) {
 const labels = { local: '本地', cloud: '云端', hybrid: '混合（本地优先）' }
 const status = voice.status.data
 const unavailable = !voice.available
 const pendingText = voice.status.loading ? '正在读取配置' : '配置不可用'
 const staleText = voice.status.error && status ? ' · 上次配置，尚未重新确认' : ''
 const captureText = voice.phase === 'permission' ? '正在请求麦克风权限…' : voice.phase === 'recording' ? '正在录音；最多 60 秒、8 MiB。' : voice.phase === 'ready' ? '录音已结束，请点击发送录音。' : voice.phase === 'transcribing' ? '正在识别录音，请勿重复发送…' : '点击录音，结束后发送。60 秒时自动停止，等待你确认发送。'
 return <section className="voice-controls" aria-label="语音对话">
  {voice.status.error && <Alert tone="danger" live="assertive">
   {status ? '语音配置刷新失败；下方显示上次配置，暂时暂停新录音、发送录音和朗读。' : '语音配置读取失败，暂时暂停录音和朗读。'} {errorText(voice.status.error)}
   <Button variant="secondary" disabled={voice.status.loading} onClick={voice.status.reload}>重新读取</Button>
  </Alert>}
  {unavailable && <p>尚未确认当前配置，录音和回答文字可能发送至云端；重新读取成功后才能开始语音任务。</p>}
  <div className="voice-service-status">
   <div><strong>语音识别</strong><p>{status ? labels[status.asr_mode] + staleText : pendingText}{status && !status.local_asr_configured && status.asr_mode !== 'cloud' ? ' · 本地识别未配置' : ''}{status && !status.cloud_configured && status.asr_mode !== 'local' ? ' · 云端密钥未配置' : ''}</p>
    {status && status.asr_mode !== 'local' && <p>此模式可能将录音发送至云端识别服务。</p>}
   </div>
   <div><strong>回答朗读</strong><p>{status ? labels[status.tts_mode] + staleText : pendingText}{status && !status.local_tts_configured && status.tts_mode !== 'cloud' ? ' · 本地朗读未配置' : ''}{status && !status.cloud_configured && status.tts_mode !== 'local' ? ' · 云端密钥未配置' : ''}</p>
    {status && status.tts_mode !== 'local' && <>
     <p>设备、知识及部分历史回复仍需本地朗读。{!status.local_tts_configured ? '当前未配置本地朗读服务，这些回复暂时无法朗读。' : ''}</p>
     <p>{status.tts_model === 'mimo-v2.5-tts-voiceclone' ? `MiMo 克隆音色 · ${status.voice_reference_configured ? '参考音频已配置' : '参考音频未配置'}` : `MiMo 内置音色 · ${status.tts_voice === 'mimo_default' ? 'MiMo 默认音色' : status.tts_voice ?? '冰糖'}`}</p>
     <p>{status.tts_model === 'mimo-v2.5-tts-voiceclone' ? '使用云端克隆朗读时，参考音频和本次朗读文字会发送到 MiMo。' : '此模式可能将回答文字发送至云端朗读服务。'}</p>
    </>}
   </div>
  </div>
  <div className="voice-buttons">
   {voice.phase === 'idle' && <Button variant="secondary" disabled={disabled || unavailable} onClick={() => void voice.start()}><Icon name="microphone"/>开始录音</Button>}
   {voice.phase === 'recording' && <Button disabled={disabled || unavailable} onClick={voice.endAndSend}><Icon name="stop"/>结束并发送</Button>}
   {voice.phase === 'ready' && <Button disabled={disabled || unavailable} onClick={voice.sendReady}><Icon name="send"/>发送录音</Button>}
   {voice.phase !== 'idle' && <Button variant="secondary" onClick={voice.cancel}>{voice.phase === 'transcribing' ? '取消识别' : '取消录音'}</Button>}
   {['recording', 'ready'].includes(voice.phase) && <span className="voice-counter" aria-label="录音计时">{voice.recordedSeconds} / {voice.maxRecordingSeconds} 秒</span>}
  </div>
  <p className="voice-hint" role="status">{captureText}</p>
  <div className="voice-playback">
   <Checkbox label="自动朗读新回答" checked={voice.auto} disabled={unavailable && !voice.auto} onChange={event => voice.setAuto(event.target.checked)}/>
   {voice.playback !== 'idle' && <Button variant="secondary" onClick={voice.cancel}>停止朗读</Button>}
   {voice.playback === 'ready' && <Button disabled={unavailable} onClick={() => void voice.resumePlayback()}><Icon name="play"/>播放已生成语音</Button>}
   {voice.playback === 'loading' && <StatusBadge tone="info" label="正在生成语音…" live/>}
  </div>
  {voice.error && <Alert tone="danger" live="assertive">{voice.error}</Alert>}
 </section>
}
