import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { errorText, type AssistantSettings, type MiMoEndpoint, type PersonalityMetadata, type ServiceMode, type TTSModel, type TTSVoice, type VoiceReferenceMetadata } from '../api'
import { useAuth } from '../auth'
import { useConsoleAction } from '../consoleActions'
import { useFamilyQuery } from '../familyQuery'
import { ActionGroup, Alert, Button, Card, Checkbox, Disclosure, Field, FileInput, Grid, Input, KeyValueList, LoadingState, PageFrame, PageHeader, Section, Select, Stack, fieldDescriptionIds } from '../ui'
import '../styles/assistant.css'

function ProviderSettings() {
  const query = useFamilyQuery<AssistantSettings>('/admin/assistant')
  const action = useConsoleAction()
  const [value, setValue] = useState<AssistantSettings | null>(null)
  const [key, setKey] = useState('')
  const [remove, setRemove] = useState(false)
  const [saved, setSaved] = useState(false)
  const advanced = useRef<HTMLDetailsElement>(null)
  const localASR = useRef<HTMLInputElement>(null)
  const localTTS = useRef<HTMLInputElement>(null)
  const asrError = action.errorCode === 'local_asr_url_invalid' ? action.error : ''
  const ttsError = action.errorCode === 'local_tts_url_invalid' ? action.error : ''
  useEffect(() => {
    if (action.pending || (!asrError && !ttsError)) return
    if (advanced.current) advanced.current.open = true
    ;(asrError ? localASR.current : localTTS.current)?.focus()
  }, [action.pending, asrError, ttsError])
  const rejectedSnapshot = useRef<AssistantSettings | null>(null)
  // Only initialize once: periodic reads must not overwrite unsaved edits.
  useEffect(() => { if (query.data && !value && query.data !== rejectedSnapshot.current) setValue(query.data) }, [query.data, value])
  async function save(event: FormEvent) {
    event.preventDefault()
    if (!value) return
    setSaved(false)
    const result = await action.run<AssistantSettings>('/admin/assistant', {
      expected_revision: value.revision, llm_mode: value.llm_mode, asr_mode: value.asr_mode, tts_mode: value.tts_mode,
      local_asr_url: value.local_asr_url, local_tts_url: value.local_tts_url, mimo_endpoint: value.mimo_endpoint ?? 'auto',
      tts_model: value.tts_model ?? 'mimo-v2.5-tts', tts_voice: value.tts_voice ?? '冰糖',
      ...(remove ? { remove_key: true } : key ? { api_key: key } : {}),
    }, 'PUT')
    setKey('')
    if (result) { setValue(result); setRemove(false); setSaved(true) }
  }
  return <Section title="模型与语音服务" description="配置与实际服务可用性分别判断。LLM 配置变更从新对话生效。"><Stack gap={24}>
    <Alert tone="info">本地模式仅使用本地服务；云端模式会发送内容到 MiMo；混合模式优先本地，仅在本地服务不可用时回退云端。云端或混合对话不附带本地知识库及设备记录；你输入的文字可能发送到云端进行意图判断。设备目录、状态和执行保留在本地。</Alert>
    {query.error && <Alert tone="danger" live="assertive">{errorText(query.error)}</Alert>}
    {!value ? <LoadingState label="正在读取服务配置…"/> : <form onSubmit={save}><Stack gap={24}>
      <Grid columns={3}>{(['llm', 'asr', 'tts'] as const).map((kind) => <div className="jx-assistant-mode" key={kind}><h3>{{llm:'文字回答',asr:'语音识别',tts:'语音朗读'}[kind]}</h3><Field id={`assistant-${kind}`} label={`${kind.toUpperCase()} 模式`} hint={{local:'仅使用本地服务。',cloud:'内容发送到 MiMo 云端。',hybrid:'本地优先，不可用时回退云端。'}[value[`${kind}_mode`]]}><Select id={`assistant-${kind}`} value={value[`${kind}_mode`]} disabled={action.pending} aria-describedby={fieldDescriptionIds(`assistant-${kind}`,true,false)} onChange={(event) => setValue({ ...value, [`${kind}_mode`]: event.target.value as ServiceMode })}><option value="local">本地</option><option value="cloud">云端 MiMo</option><option value="hybrid">混合：本地优先，云端回退</option></Select></Field></div>)}</Grid>
      <Card heading="MiMo 朗读音色"><Stack>
        <Field id="tts-model" label="MiMo 朗读模型" hint="仅用于云端朗读及混合模式的云端回退。本地服务地址和配置会保留。"><Select id="tts-model" value={value.tts_model ?? 'mimo-v2.5-tts'} disabled={action.pending} aria-describedby={fieldDescriptionIds('tts-model',true,false)} onChange={(event) => setValue({ ...value, tts_model: event.target.value as TTSModel })}>
          <option value="mimo-v2.5-tts">MiMo TTS · 内置音色</option><option value="mimo-v2.5-tts-voiceclone">MiMo VoiceClone · 参考音频克隆</option>
        </Select></Field>
        <Field id="tts-voice" label="MiMo 内置音色" hint={value.tts_model === 'mimo-v2.5-tts-voiceclone' ? '克隆模型使用参考音频，内置音色选择会保留。' : '内置音色无需上传参考音频。'}><Select id="tts-voice" value={value.tts_voice ?? '冰糖'} disabled={action.pending || value.tts_model === 'mimo-v2.5-tts-voiceclone'} aria-describedby={fieldDescriptionIds('tts-voice',true,false)} onChange={(event) => setValue({ ...value, tts_voice: event.target.value as TTSVoice })}>
          {(['mimo_default', '冰糖', '茉莉', '苏打', '白桦', 'Mia', 'Chloe', 'Milo', 'Dean'] as const).map(voice => <option key={voice} value={voice}>{voice === 'mimo_default' ? 'MiMo 默认音色' : voice}</option>)}
        </Select></Field>
      </Stack></Card>
      <Card heading="云端密钥与区域"><Stack>
        <p>云端密钥：{value.key_configured ? '已配置' : '未配置'}</p>
        <Field id="mimo-endpoint" label="MiMo 服务入口" hint="套餐密钥与按量付费密钥不可混用。区域请与 MiMo 控制台显示的 Base URL 一致。"><Select id="mimo-endpoint" value={value.mimo_endpoint ?? 'auto'} disabled={action.pending} aria-describedby={fieldDescriptionIds('mimo-endpoint',true,false)} onChange={(event) => setValue({ ...value, mimo_endpoint: event.target.value as MiMoEndpoint })}>
          <option value="auto">自动识别密钥（Token Plan 默认中国区）</option><option value="payg">按量付费 API</option><option value="token_cn">Token Plan · 中国</option><option value="token_sgp">Token Plan · 新加坡</option><option value="token_ams">Token Plan · 欧洲</option>
        </Select></Field>
        <p className="jx-assistant-endpoint">{({ auto: '自动：tp- / ttp- 密钥使用中国区 Token Plan；其他密钥使用按量付费 API。', payg: 'https://api.xiaomimimo.com/v1', token_cn: 'https://token-plan-cn.xiaomimimo.com/v1', token_sgp: 'https://token-plan-sgp.xiaomimimo.com/v1', token_ams: 'https://token-plan-ams.xiaomimimo.com/v1' })[value.mimo_endpoint ?? 'auto']}</p>
        <p className="jx-muted">此入口同时用于云端 LLM、ASR 和 TTS。<a href="https://mimo.mi.com/docs/zh-CN/tokenplan/Token%20Plan/quick-access" target="_blank" rel="noreferrer">查看官方接入说明</a>。Token Plan 使用范围以官方订阅说明为准。</p>
        <Field id="assistant-key" label="MiMo API Key" hint="留空保留当前密钥。保存不会发起付费测试。"><Input id="assistant-key" type="password" autoComplete="off" value={key} disabled={action.pending || remove} aria-describedby={fieldDescriptionIds('assistant-key',true,false)} onChange={(event) => setKey(event.target.value)}/></Field>
        <Checkbox label="移除已保存的密钥" checked={remove} disabled={action.pending} onChange={(event) => { setRemove(event.target.checked); setKey('') }}/>
      </Stack></Card>
      <Disclosure title="本地语音服务部署详情" ref={advanced}><Stack>
        <Field id="local-asr" label="本地 ASR 服务地址" error={asrError}><Input ref={localASR} id="local-asr" type="url" value={value.local_asr_url} disabled={action.pending} invalid={Boolean(asrError)} aria-describedby={fieldDescriptionIds('local-asr',false,Boolean(asrError))} placeholder="http://127.0.0.1:8000/v1/audio/transcriptions" onChange={(event) => { setValue({ ...value, local_asr_url: event.target.value }); if (asrError) action.clearError() }}/></Field>
        <Field id="local-tts" label="本地 TTS 服务地址" error={ttsError}><Input ref={localTTS} id="local-tts" type="url" value={value.local_tts_url} disabled={action.pending} invalid={Boolean(ttsError)} aria-describedby={fieldDescriptionIds('local-tts',false,Boolean(ttsError))} placeholder="http://127.0.0.1:8000/v1/audio/speech" onChange={(event) => { setValue({ ...value, local_tts_url: event.target.value }); if (ttsError) action.clearError() }}/></Field>
        <Alert tone="warning">本地语音需要已运行的兼容服务，仅支持本机回环地址。填写地址不代表服务已就绪；未配置时本地语音不可用。</Alert>
      </Stack></Disclosure>
      <div><p className="jx-muted">保存配置不会测试收费服务。云端或混合模式会按所选模式发送相关内容；文字回答模式从新对话生效。</p><Button type="submit" busy={action.pending}>{action.pending ? '正在保存…' : '保存服务配置'}</Button></div>
    </Stack></form>}
    <ActionGroup><Button variant="secondary" disabled={action.pending || query.loading} onClick={() => { rejectedSnapshot.current = query.data; setValue(null); setKey(''); setRemove(false); setSaved(false); action.clearError(); query.reload() }}>重新读取配置</Button></ActionGroup>
    {action.error && <Alert tone="danger" live="assertive">{action.error}</Alert>}{saved && <Alert tone="info" live="polite">服务配置已保存。</Alert>}
    <VoiceReferenceUpload cloneSelected={value?.tts_model === 'mimo-v2.5-tts-voiceclone'}/>
  </Stack></Section>
}

function VoiceReferenceUpload({ cloneSelected }: { cloneSelected: boolean }) {
  const auth = useAuth()
  const action = useConsoleAction()
  const [reading, setReading] = useState(false)
  const busy = reading || action.pending
  const query = useFamilyQuery<VoiceReferenceMetadata>('/admin/tts-reference', !busy)
  const [metadata, setMetadata] = useState<VoiceReferenceMetadata | null>(null)
  const [file, setFile] = useState<File | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const generation = useRef(0)
  const mounted = useRef(true)
  const mutation = useRef(false)
  const ignoredSnapshot = useRef<VoiceReferenceMetadata | null>(null)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; generation.current++ } }, [])
  // A pre-write query snapshot must never replace the returned write revision.
  useEffect(() => {
    if (!busy && !query.loading && query.data && query.data !== ignoredSnapshot.current) {
      ignoredSnapshot.current = query.data
      setMetadata(query.data)
    }
  }, [busy, query.data, query.loading])
  function clearFile() { setFile(null); if (input.current) input.current.value = '' }
  async function upload(event: FormEvent) {
    event.preventDefault()
    if (!file || !metadata || query.loading || busy || mutation.current) return
    setError(''); setNotice(''); action.clearError()
    if (!/\.(wav|mp3)$/i.test(file.name)) { setError('请选择 WAV 或 MP3 参考音频。'); return }
    if (!file.size) { setError('参考音频不能为空。'); return }
    if (file.size > 6 * 1024 * 1024) { setError('参考音频不能超过 6 MiB。'); return }
    const token = ++generation.current
    const epoch = auth.epoch
    const active = () => mounted.current && auth.isCurrent(epoch) && !auth.signal.aborted && generation.current === token
    mutation.current = true
    ignoredSnapshot.current = query.data
    setReading(true)
    try {
      const bytes = new Uint8Array(await file.arrayBuffer())
      if (!active()) return
      let binary = ''
      for (let offset = 0; offset < bytes.length; offset += 8192) binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192))
      const result = await action.run<VoiceReferenceMetadata>('/admin/tts-reference', { filename: file.name, audio_base64: btoa(binary), expected_revision: metadata.revision }, 'PUT', active)
      if (result && active()) { setMetadata(result); setNotice('参考音频已保存。上传未调用 MiMo。'); clearFile() }
    } catch { if (active()) setError('无法读取参考音频，请重新选择文件。') }
    finally { if (active()) { mutation.current = false; setReading(false) } }
  }
  async function remove() {
    if (!metadata?.exists || query.loading || busy || mutation.current) return
    const token = ++generation.current
    const epoch = auth.epoch
    const active = () => mounted.current && auth.isCurrent(epoch) && !auth.signal.aborted && generation.current === token
    mutation.current = true
    ignoredSnapshot.current = query.data
    setError(''); setNotice('')
    const result = await action.run<VoiceReferenceMetadata>('/admin/tts-reference/remove', { expected_revision: metadata.revision }, 'POST', active)
    if (active()) {
      mutation.current = false
      if (result) { setMetadata(result); setNotice('参考音频已移除。'); clearFile() }
    }
  }
  return <Card heading="克隆声音样本"><Stack>
    <p>参考音频：{metadata ? metadata.exists ? `已配置 · ${metadata.size_bytes} 字节` : '尚未上传' : '正在读取状态…'}</p>
    {metadata?.updated_at && <p>更新时间：{new Date(metadata.updated_at).toLocaleString()}</p>}
    <p>上传只保存在本机，不会调用 MiMo。上传和模型选择分别保存；尚无样本时可以先使用内置音色。</p>
    <Alert tone="warning">使用 MiMo 克隆朗读时，参考音频和本次朗读文字会发送到 MiMo。请只上传你有权使用的声音。</Alert>
    {cloneSelected && metadata && !metadata.exists && <Alert tone="warning">使用 MiMo 克隆朗读前，请先上传参考音频。缺少样本时不会自动改用内置音色。</Alert>}
    <form onSubmit={upload}><Stack>
      <Field id="tts-reference-file" label="克隆参考音频" hint="WAV 或 MP3，最多 6 MiB、60 秒；格式和时长由服务器校验。MP3 需要服务器安装 FFmpeg，可优先使用 PCM16 WAV。"><FileInput id="tts-reference-file" ref={input} accept=".wav,.mp3,audio/wav,audio/mpeg" disabled={busy} aria-describedby={fieldDescriptionIds('tts-reference-file',true,false)} onChange={(event) => { generation.current++; setFile(event.target.files?.[0] ?? null); setError(''); setNotice(''); action.clearError() }}/></Field>
      <ActionGroup>
        <Button type="submit" busy={busy} disabled={!file || !metadata || query.loading}>{busy ? '正在保存参考音频…' : metadata?.exists ? '替换参考音频' : '上传参考音频'}</Button>
        {reading && !action.pending && <Button variant="secondary" onClick={() => { generation.current++; mutation.current = false; setReading(false); clearFile() }}>取消读取</Button>}
        {metadata?.exists && <Button variant="secondary" disabled={busy || query.loading} onClick={() => void remove()}>移除参考音频</Button>}
      </ActionGroup>
    </Stack></form>
    <ActionGroup><Button variant="secondary" disabled={busy || query.loading} onClick={() => { ignoredSnapshot.current = query.data; setMetadata(null); setError(''); setNotice(''); action.clearError(); query.reload() }}>重新读取参考音频状态</Button></ActionGroup>
    {(error || action.error || query.error) && <Alert tone="danger" live="assertive">{error || action.error || errorText(query.error)}</Alert>}
    {notice && <Alert tone="info" live="polite">{notice}</Alert>}
  </Stack></Card>
}

function PersonalityUpload() {
  const auth = useAuth()
  const query = useFamilyQuery<PersonalityMetadata>('/admin/personality')
  const action = useConsoleAction()
  const [file, setFile] = useState<File | null>(null)
  const [error, setError] = useState('')
  const [reading, setReading] = useState(false)
  const [saved, setSaved] = useState(false)
  const [metadata, setMetadata] = useState<PersonalityMetadata | null>(null)
  const generation = useRef(0)
  const mounted = useRef(true)
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; generation.current++ } }, [])
  useEffect(() => { if (query.data) setMetadata(query.data) }, [query.data])
  async function upload(event: FormEvent) {
    event.preventDefault()
    if (!file || !metadata || query.loading || reading || action.pending) return
    const currentGeneration = generation.current
    const epoch = auth.epoch
    const active = () => mounted.current && auth.isCurrent(epoch) && generation.current === currentGeneration
    setError('')
    if (!file.name.toLowerCase().endsWith('.md')) { setError('请选择 .md 文件。'); return }
    if (file.size > 256 * 1024) { setError('人格文件不能超过 256 KiB。'); return }
    setReading(true)
    try {
      const content = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer())
      if (!active()) return
      const result = await action.run<PersonalityMetadata>('/admin/personality', { filename: file.name, content, expected_revision: metadata.revision }, 'PUT', active)
      if (result && active()) { setMetadata(result); setSaved(true); setFile(null); if (input.current) input.current.value = ''; query.reload() }
    } catch { if (active()) setError('文件必须是有效的 UTF-8 文本。') }
    finally { if (active()) setReading(false) }
  }
  return <Section title="助手人格" description="个人管家的基础身份、职责与操作确认规则由系统定义，无需在文档中重复。"><Stack>
    <p>文档可以完整描述人物的性格、关心方式、兴趣和表达习惯，第三人称设定也会自然采用。上传后，助手会自动融合到后续对话和设备反馈，无需另写管家定义或操作话术；已保存的回复保持原样。</p>
    {metadata && <p>当前文件：{metadata.exists ? `已配置 · ${metadata.size_bytes} 字节 · 用于后续对话回复` : '尚未上传'}</p>}
    {metadata?.updated_at && <p>更新时间：{new Date(metadata.updated_at).toLocaleString()}</p>}
    <form onSubmit={upload}><Stack><Field id="personality-file" label="人格 Markdown 文件" hint="有效 UTF-8 的 .md 文件，最多 256 KiB。例如：她叫小序，性格温和，说话简洁，习惯先给结论。"><FileInput ref={input} id="personality-file" accept=".md,text/markdown" disabled={reading || action.pending} aria-describedby={fieldDescriptionIds('personality-file',true,false)} onChange={(event) => { generation.current++; setFile(event.target.files?.[0] ?? null); setError('') }}/></Field>
      <Alert tone="warning">云端或混合模式会将人格内容随对话发送到 MiMo。请在上传前核对文件内容。</Alert>
      <ActionGroup><Button type="submit" busy={reading || action.pending} disabled={!file || !metadata || query.loading}>{reading || action.pending ? '正在上传…' : '上传人格文件'}</Button></ActionGroup>
    </Stack></form>
    <Disclosure title="人格文件存储详情"><KeyValueList items={[{id:'path',label:'服务器固定文件',value:'personality/default.md'}]}/></Disclosure>
    <ActionGroup><Button variant="secondary" disabled={reading || action.pending || query.loading} onClick={() => { setError(''); action.clearError(); setSaved(false); query.reload() }}>重新读取人格状态</Button></ActionGroup>
    {(error || action.error || query.error) && <Alert tone="danger" live="assertive">{error || action.error || errorText(query.error)}</Alert>}
    {saved && <Alert tone="info" live="polite">人格文件已保存。</Alert>}
  </Stack></Section>
}

export default function AssistantPage() {
  const { identity } = useAuth()
  return <PageFrame><Link className="jx-action-link jx-button-ghost" to="/app/account">返回我的</Link><PageHeader title="助手设置" description="管理家庭助手的服务模式、云端密钥与人格文件。"/>
    {identity?.capabilities.includes('providers:manage') && <ProviderSettings/>}
    {identity?.capabilities.includes('personality:manage') && <PersonalityUpload/>}
  </PageFrame>
}
