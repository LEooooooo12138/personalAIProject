import { useEffect, useRef, useState } from 'react'
import { ApiError, errorText, type ManagedRevisionRequest, type ManagedVersion } from './api'
import { useAuth } from './auth'
import { readAutomationEntityDirectory, type AutomationDirectoryEntity } from './automationEntityDirectory'
import { PresenceEntityPicker, isActionCandidate, isSourceCandidate, isPresenceCandidate } from './PresenceEntityPicker'
import { Alert, Button, Card, Field, Input, LoadingState, Select, Stack, ActionGroup } from './ui'

export function AutomationRevisionForm({ version, pending, error, enabled, onSave, onCancel, managed = false }: { version: ManagedVersion; pending: boolean; error: string; enabled: boolean; onSave: (request: ManagedRevisionRequest) => Promise<void>; onCancel: () => void; managed?: boolean }) {
  const auth = useAuth()
  const [entities, setEntities] = useState<AutomationDirectoryEntity[] | null>(null)
  const [directoryError, setDirectoryError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [at, setAt] = useState('')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [presence, setPresence] = useState('')
  const [presenceState, setPresenceState] = useState('')
  const [fieldError, setFieldError] = useState('')
  const form = useRef<HTMLFormElement>(null)
  const intent = version.intent
  const time = intent?.kind === 'time'
  useEffect(() => {
    const controller = new AbortController()
    const signal = AbortSignal.any([auth.signal, controller.signal])
    const epoch = auth.epoch
    setEntities(null); setDirectoryError(''); setSource(''); setTarget(''); setPresence(''); setPresenceState('')
    void readAutomationEntityDirectory(signal).then(directory => {
      if (!signal.aborted && auth.isCurrent(epoch)) setEntities([...directory.values()])
    }).catch(cause => {
      if (signal.aborted || !auth.isCurrent(epoch)) return
      if (cause instanceof ApiError && cause.status === 401) auth.expire()
      else setDirectoryError(errorText(cause))
    })
    return () => controller.abort()
  }, [attempt, version.id, auth.epoch])
  const pickedPresence = entities?.find(entity => entity.entity_id === presence && isPresenceCandidate(entity))
  const blocked = pending || Boolean(error) || !enabled
  function invalid(message: string, selector: string) {
    setFieldError(message)
    form.current?.querySelector<HTMLElement>(selector)?.focus()
  }
  async function save() {
    if (blocked || !intent || !entities || directoryError) return
    if (time && !/^([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?$/.test(at)) return invalid('请填写有效的触发时间，例如 18:00。', '#revision-time')
    if (!time && !entities.some(entity => entity.entity_id === source && isSourceCandidate(entity))) return invalid('请明确选择可用的触发源。', '#revision-source select')
    if (!entities.some(entity => entity.entity_id === target && isActionCandidate(entity))) return invalid('请明确选择可用的动作目标。', '#revision-target select')
    if (!time && source === target) return invalid('触发源与动作目标不能相同。', '#revision-target select')
    if (time && !pickedPresence) return invalid('请明确选择代表全家占用的实体。', '#revision-presence select')
    if (time && !(presenceState === 'on' || pickedPresence?.domain === 'group' && presenceState === 'home')) return invalid('请明确选择已核实的有人在家状态。', '#revision-presence-state')
    setFieldError('')
    const revisedIntent = { ...intent, entity_id: time ? target : source, ...(time ? { at } : { related_id: target }) }
    await onSave({ expected_version: version.id, intent: revisedIntent,
      ...(time ? { presence_binding: { entity_id: presence, state: presenceState } } : {}),
    })
  }
  return <Card heading={managed ? "修订受管规则草稿" : "修订待审核建议"}>
    <Stack gap={16}>
      <p>保存将产生新版本，原版本保留。这里只能调整受支持的时间、触发源和动作目标；动作始终为开启，强制条件与家庭时区保持不变。</p>
      <p>请重新明确选择设备与家庭占用条件。名称和房间归属不能证明实体代表全家是否有人在家。</p>
      {directoryError ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={() => setAttempt(value => value + 1)}>重新读取实体</Button>}>{directoryError}</Alert> : entities === null ? <LoadingState label="正在读取修订可用实体…"/> :
        <form ref={form} noValidate onSubmit={event => { event.preventDefault(); void save() }}><Stack gap={16}>
          {time ? <Field id="revision-time" label="触发时间" hint="使用家庭时区，格式为 HH:mm 或 HH:mm:ss。" required><Input id="revision-time" value={at} onChange={event => setAt(event.target.value)} placeholder="18:00" aria-describedby="revision-time-hint" disabled={blocked}/></Field> :
            <fieldset id="revision-source" disabled={blocked}><legend>触发源</legend><PresenceEntityPicker purpose="source" entities={entities} value={source} onChange={setSource} disabled={blocked}/></fieldset>}
          <fieldset id="revision-target" disabled={blocked}><legend>动作目标</legend><PresenceEntityPicker purpose="target" entities={entities} value={target} onChange={setTarget} disabled={blocked}/></fieldset>
          {time && <><fieldset id="revision-presence" disabled={blocked}><legend>家庭占用条件</legend><PresenceEntityPicker entities={entities} value={presence} onChange={value => { setPresence(value); setPresenceState('') }} disabled={blocked}/></fieldset>
            <Field id="revision-presence-state" label="有人在家时的状态" hint="必须由你核实此状态代表全家有人在家。" required><Select id="revision-presence-state" value={presenceState} onChange={event => setPresenceState(event.target.value)} disabled={blocked || !pickedPresence} aria-describedby="revision-presence-state-hint"><option value="">请选择已核实的状态</option><option value="on">开启</option>{pickedPresence?.domain === 'group' && <option value="home">在家</option>}</Select></Field></>}
          {fieldError && <Alert tone="danger" live="assertive">{fieldError}</Alert>}
          <Button type="submit" disabled={blocked}>保存修订并查看差异</Button>
        </Stack></form>}
      {error && <Alert tone="danger" live="assertive">{error} 请关闭修订并刷新版本后重新审阅；本页不会自动重试。</Alert>}
      <ActionGroup><Button variant="secondary" onClick={onCancel} disabled={pending}>关闭修订</Button></ActionGroup>
      <p>保存修订不会安装自动化。新版需要再次审阅并单独确认；旧版历史证据不代表新版已被验证。</p>
    </Stack>
  </Card>
}
