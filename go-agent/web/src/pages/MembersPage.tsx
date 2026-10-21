import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ApiError, consoleApi, errorText, type Member } from '../api'
import { useAuth } from '../auth'
import { ActionGroup, Alert, Avatar, Button, Card, Dialog, EmptyState, Field, Icon, Input, ListRow, LoadingState, PageFrame, PageHeader, Stack, StatusBadge } from '../ui'

type DialogState = { kind: 'create' } | { kind: 'rename'; member: Member } | { kind: 'temporary'; username: string; password: string } | { kind: 'reset'; member: Member } | null
type Operation = { generation: number; epoch: number; kind: string; memberId: string | null }

export default function MembersPage() {
  const auth = useAuth()
  const [members, setMembers] = useState<Member[] | null>(null)
  const [loadError, setLoadError] = useState('')
  const [actionError, setActionError] = useState('')
  const [dialog, setDialog] = useState<DialogState>(null)
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [pending, setPending] = useState(false)
  const [reload, setReload] = useState(0)
  const generation = useRef(0)
  const mounted = useRef(false)
  const operation = useRef<Operation | null>(null)
  const currentDialog = useRef(dialog)
  currentDialog.current = dialog
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; generation.current++; operation.current = null } }, [])
  useEffect(() => {
    const clear = () => { generation.current++; operation.current = null; setDialog(null); setUsername(''); setDisplayName(''); setPending(false); setActionError('') }
    auth.signal.addEventListener('abort', clear, { once: true })
    return () => auth.signal.removeEventListener('abort', clear)
  }, [auth.epoch, auth.signal])
  useEffect(() => {
    let active = true
    const expected = auth.epoch
    const controller = new AbortController()
    const onAbort = () => controller.abort()
    auth.signal.addEventListener('abort', onAbort, { once: true })
    setMembers(null); setLoadError('')
    void consoleApi.members(controller.signal).then(({ members }) => {
      if (active && auth.isCurrent(expected)) setMembers(members)
    }).catch((cause) => {
      if (!active || !auth.isCurrent(expected)) return
      if (cause instanceof ApiError && cause.status === 401) auth.expire()
      else if (!(cause instanceof DOMException && cause.name === 'AbortError')) setLoadError(errorText(cause))
    })
    return () => { active = false; controller.abort(); auth.signal.removeEventListener('abort', onAbort) }
  }, [auth.epoch, reload])
  function openDialog(next: DialogState) {
    if (operation.current) return
    generation.current++; setActionError(''); setUsername('')
    setDisplayName(next?.kind === 'rename' ? next.member.display_name : '')
    setDialog(next)
  }
  function closeDialog() { if (!operation.current) openDialog(null) }
  function begin(kind: string, memberId: string | null): Operation | null {
    if (operation.current || !mounted.current || auth.signal.aborted) return null
    const token = { kind, memberId, epoch: auth.epoch, generation: generation.current }
    operation.current = token; setPending(true); setActionError(''); return token
  }
  function isCurrent(token: Operation) {
    if (!mounted.current || !auth.isCurrent(token.epoch) || generation.current !== token.generation || operation.current !== token) return false
    if (token.kind === 'rename' || token.kind === 'reset') {
      const value = currentDialog.current
      return value?.kind === token.kind && 'member' in value && value.member.id === token.memberId
    }
    return token.kind !== 'create' || currentDialog.current?.kind === 'create'
  }
  function finish(token: Operation) {
    if (mounted.current && auth.isCurrent(token.epoch) && operation.current === token) { operation.current = null; setPending(false) }
  }
  function actionFailure(cause: unknown, token: Operation) {
    if (!isCurrent(token)) return
    if (cause instanceof ApiError && cause.status === 401) auth.expire()
    else setActionError(errorText(cause))
  }
  async function create(event: FormEvent) {
    event.preventDefault(); const token = begin('create', null); if (!token) return
    try {
      const result = await consoleApi.createMember({ username: username.trim(), display_name: displayName.trim() }, auth.identity!.csrf_token, auth.signal)
      if (!isCurrent(token)) return
      setMembers((old) => old ? [...old, result.user] : old)
      finish(token); openDialog({ kind: 'temporary', username: result.user.username, password: result.temporary_password })
    } catch (cause) { actionFailure(cause, token) } finally { finish(token) }
  }
  async function rename(event: FormEvent, member: Member) {
    event.preventDefault(); const token = begin('rename', member.id); if (!token) return
    try {
      const { user } = await consoleApi.updateMember(member.id, { display_name: displayName.trim() }, auth.identity!.csrf_token, auth.signal)
      if (!isCurrent(token)) return
      setMembers((old) => old?.map((item) => item.id === member.id ? user : item) ?? null)
      finish(token); closeDialog()
    } catch (cause) { actionFailure(cause, token) } finally { finish(token) }
  }
  async function toggle(member: Member) {
    const token = begin('toggle', member.id); if (!token) return
    try {
      const { user } = await consoleApi.updateMember(member.id, { disabled: !member.disabled }, auth.identity!.csrf_token, auth.signal)
      if (isCurrent(token)) setMembers((old) => old?.map((item) => item.id === member.id ? user : item) ?? null)
    } catch (cause) { actionFailure(cause, token) } finally { finish(token) }
  }
  async function reset(member: Member) {
    const token = begin('reset', member.id); if (!token) return
    try {
      const result = await consoleApi.resetMemberPassword(member.id, auth.identity!.csrf_token, auth.signal)
      if (!isCurrent(token)) return
      finish(token); openDialog({ kind: 'temporary', username: member.username, password: result.temporary_password })
    } catch (cause) { actionFailure(cause, token) } finally { finish(token) }
  }
  const title = dialog?.kind === 'create' ? '创建家庭成员' : dialog?.kind === 'rename' ? '修改显示名称：' + dialog.member.display_name : dialog?.kind === 'reset' ? '重置成员口令：' + dialog.member.display_name : '临时密码'
  return <PageFrame className="members-page">
    <PageHeader title="家庭成员" description="为家人创建独立账号，管理他们的登录状态。" actions={<Button disabled={pending} onClick={() => openDialog({ kind: 'create' })}><Icon name="plus"/>创建成员</Button>}/>
    <Card heading="成员列表" description="每位成员只能查看自己的对话。">
      {loadError ? <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={() => setReload(reload + 1)}>重新加载</Button>}>{loadError}</Alert> : members === null ? <LoadingState label="正在读取成员…"/> : members.length === 0 ? <EmptyState icon="members" title="还没有家庭成员" description="创建账号后，家人可以用临时密码首次登录。"/> : <ul className="jx-plain-list">{members.map((member) => <li key={member.id}><ListRow leading={<Avatar label={member.display_name}/>} title={member.display_name} description={'@' + member.username} meta={<StatusBadge tone={member.disabled ? 'warning' : 'success'} label={member.disabled ? '已禁用' : '可登录'}/>} trailing={<ActionGroup><Button variant="ghost" disabled={pending} aria-label={'改名：' + member.display_name} onClick={() => openDialog({ kind: 'rename', member })}>改名</Button><Button variant="ghost" disabled={pending} aria-label={'重置口令：' + member.display_name} onClick={() => openDialog({ kind: 'reset', member })}>重置口令</Button><Button variant="ghost" aria-label={(member.disabled ? '启用' : '禁用') + '：' + member.display_name} disabled={pending} onClick={() => void toggle(member)}>{member.disabled ? '启用' : '禁用'}</Button></ActionGroup>}/></li>)}</ul>}
      {actionError && !dialog && <Alert tone="danger" live="assertive">{actionError}</Alert>}
    </Card>
    <Dialog open={dialog !== null} title={title} dismissible={!pending} onRequestClose={closeDialog}>
      <Stack gap={16}>
        {pending && <Alert tone="info" live="polite">操作已提交，等待结果。离开页面不会撤销已提交的操作。</Alert>}
        {dialog?.kind === 'create' && <><p>创建后会生成一次性临时密码，请当面交给家人。</p><form onSubmit={(event) => void create(event)}><Stack gap={16}><Field id="member-username" label="登录名" required hint="3–32 位英文字母、数字、下划线或连字符"><Input id="member-username" value={username} onChange={(event) => setUsername(event.target.value)} pattern="[A-Za-z0-9_-]{3,32}" title="3 至 32 位英文字母、数字、下划线或连字符" autoComplete="off" required disabled={pending} aria-describedby="member-username-hint"/></Field><Field id="member-display" label="显示名称" required><Input id="member-display" value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={40} required disabled={pending}/></Field>{actionError && <Alert tone="danger" live="assertive">{actionError}</Alert>}<ActionGroup><Button variant="secondary" disabled={pending} onClick={closeDialog}>取消</Button><Button type="submit" busy={pending}>{pending ? '创建中…' : '确认创建'}</Button></ActionGroup></Stack></form></>}
        {dialog?.kind === 'rename' && <form onSubmit={(event) => { if (dialog.kind === 'rename') void rename(event, dialog.member) }}><Stack gap={16}><Field id="rename-display" label="显示名称" required><Input id="rename-display" value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={40} required disabled={pending}/></Field>{actionError && <Alert tone="danger" live="assertive">{actionError}</Alert>}<ActionGroup><Button variant="secondary" disabled={pending} onClick={closeDialog}>取消</Button><Button type="submit" busy={pending}>保存</Button></ActionGroup></Stack></form>}
        {dialog?.kind === 'reset' && <><p>重置 {dialog.member.display_name} 的口令后，该成员现有登录会失效，并需要在下次登录时更换密码。</p>{actionError && <Alert tone="danger" live="assertive">{actionError}</Alert>}<ActionGroup><Button variant="secondary" disabled={pending} onClick={closeDialog}>取消</Button><Button busy={pending} onClick={() => { if (dialog.kind === 'reset') void reset(dialog.member) }}>{pending ? '重置中…' : '确认重置'}</Button></ActionGroup></>}
        {dialog?.kind === 'temporary' && <><p>请现在将临时密码交给 <strong>{dialog.username}</strong>。关闭后此页面不会再次显示。</p><div className="jx-sensitive-result"><span>一次性临时密码</span><code>{dialog.password}</code></div><Button onClick={closeDialog}>关闭</Button></>}
      </Stack>
    </Dialog>
  </PageFrame>
}
