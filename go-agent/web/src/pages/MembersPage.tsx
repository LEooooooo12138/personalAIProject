import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { ApiError, consoleApi, errorText, type Member } from '../api'
import { useAuth } from '../auth'
import { Icon } from '../icons'

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null)
  const opener = useRef<HTMLElement | null>(null)
  useEffect(() => {
    opener.current = document.activeElement as HTMLElement | null
    const dialog = ref.current
    if (dialog?.showModal) dialog.showModal()
    else dialog?.setAttribute('open', '')
    return () => { dialog?.close?.(); opener.current?.focus() }
  }, [])
  return <dialog ref={ref} className="modal" aria-label={title} onCancel={(event) => { event.preventDefault(); onClose() }}><div className="modal-head"><h2>{title}</h2><button className="icon-button" type="button" aria-label={`关闭：${title}`} onClick={onClose}><Icon name="close" size={19}/></button></div>{children}</dialog>
}

type DialogState = { kind: 'create' } | { kind: 'rename'; member: Member } | { kind: 'temporary'; username: string; password: string } | { kind: 'reset'; member: Member } | null

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
  function actionFailure(cause: unknown, expected: number) {
    if (!auth.isCurrent(expected)) return
    if (cause instanceof ApiError && cause.status === 401) auth.expire()
    else setActionError(errorText(cause))
  }
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
  function closeDialog() { setDialog(null); setUsername(''); setDisplayName(''); setActionError('') }
  async function create(event: FormEvent) {
    event.preventDefault()
    const expected = auth.epoch
    setPending(true); setActionError('')
    try {
      const result = await consoleApi.createMember({ username: username.trim(), display_name: displayName.trim() }, auth.identity!.csrf_token, auth.signal)
      if (!auth.isCurrent(expected)) return
      setMembers((old) => old ? [...old, result.user] : old)
      setDialog({ kind: 'temporary', username: result.user.username, password: result.temporary_password })
      setUsername(''); setDisplayName('')
    } catch (cause) { actionFailure(cause, expected) } finally { if (auth.isCurrent(expected)) setPending(false) }
  }
  async function rename(event: FormEvent, member: Member) {
    event.preventDefault()
    const expected = auth.epoch
    setPending(true); setActionError('')
    try {
      const { user } = await consoleApi.updateMember(member.id, { display_name: displayName.trim() }, auth.identity!.csrf_token, auth.signal)
      if (!auth.isCurrent(expected)) return
      setMembers((old) => old?.map((item) => item.id === member.id ? user : item) ?? null)
      closeDialog()
    } catch (cause) { actionFailure(cause, expected) } finally { if (auth.isCurrent(expected)) setPending(false) }
  }
  async function toggle(member: Member) {
    const expected = auth.epoch
    setPending(true); setActionError('')
    try {
      const { user } = await consoleApi.updateMember(member.id, { disabled: !member.disabled }, auth.identity!.csrf_token, auth.signal)
      if (!auth.isCurrent(expected)) return
      setMembers((old) => old?.map((item) => item.id === member.id ? user : item) ?? null)
    } catch (cause) { actionFailure(cause, expected) } finally { if (auth.isCurrent(expected)) setPending(false) }
  }
  async function reset(member: Member) {
    const expected = auth.epoch
    setPending(true); setActionError('')
    try {
      const result = await consoleApi.resetMemberPassword(member.id, auth.identity!.csrf_token, auth.signal)
      if (!auth.isCurrent(expected)) return
      setDialog({ kind: 'temporary', username: member.username, password: result.temporary_password })
    } catch (cause) { actionFailure(cause, expected) } finally { if (auth.isCurrent(expected)) setPending(false) }
  }
  return <main className="content-page members-page"><div className="page-intro page-intro-row"><div><span className="eyebrow">FAMILY ACCOUNTS</span><h1>家庭成员</h1><p>为家人创建独立账号，管理他们的登录状态。</p></div><button className="button button-primary" onClick={() => { setActionError(''); setDialog({ kind: 'create' }) }}><Icon name="plus" size={18}/> 创建成员</button></div>
    <div className="panel member-panel"><div className="member-panel-head"><div><h2>成员列表</h2><p>每位成员只能查看自己的对话。</p></div><span className="count-pill">{members?.length ?? 0} 位成员</span></div>
      {loadError ? <div className="empty-card error-state" role="alert"><p>{loadError}</p><button className="button button-secondary" onClick={() => setReload(reload + 1)}>重新加载</button></div> : members === null ? <div className="empty-card" role="status"><span className="loader"/>正在读取成员…</div> : members.length === 0 ? <div className="empty-card"><Icon name="members" size={32}/><strong>还没有家庭成员</strong><p>创建账号后，家人可以用临时密码首次登录。</p></div> : <div className="member-list">{members.map((member) => <div className="member-row" key={member.id}><span className="avatar member-avatar">{member.display_name.slice(0, 1)}</span><div className="member-main"><strong>{member.display_name}</strong><small>@{member.username}</small></div><span className={`status-pill ${member.disabled ? 'disabled' : ''}`}>{member.disabled ? '已禁用' : '可登录'}</span><div className="member-actions"><button className="text-button" aria-label={`改名：${member.display_name}`} onClick={() => { setDisplayName(member.display_name); setDialog({ kind: 'rename', member }) }}>改名</button><button className="text-button" aria-label={`重置口令：${member.display_name}`} onClick={() => { setDialog({ kind: 'reset', member }); setActionError('') }}>重置口令</button><button className="text-button" aria-label={`${member.disabled ? '启用' : '禁用'}：${member.display_name}`} disabled={pending} onClick={() => void toggle(member)}>{member.disabled ? '启用' : '禁用'}</button></div></div>)}</div>}
      {actionError && !dialog && <p className="form-error" role="alert">{actionError}</p>}
    </div>
    {dialog?.kind === 'create' && <Modal title="创建家庭成员" onClose={closeDialog}><p className="modal-description">创建后会生成一次性临时密码，请当面交给家人。</p><form onSubmit={(event) => void create(event)}><label htmlFor="member-username">登录名</label><input id="member-username" value={username} onChange={(event) => setUsername(event.target.value)} pattern="[A-Za-z0-9_-]{3,32}" title="3 至 32 位英文字母、数字、下划线或连字符" autoComplete="off" required disabled={pending}/><p className="field-help">3–32 位英文字母、数字、下划线或连字符</p><label htmlFor="member-display">显示名称</label><input id="member-display" value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={40} required disabled={pending}/>{actionError && <p className="form-error" role="alert">{actionError}</p>}<div className="modal-actions"><button type="button" className="button button-secondary" onClick={closeDialog}>取消</button><button className="button button-primary" disabled={pending}>{pending ? '创建中…' : '确认创建'}</button></div></form></Modal>}
    {dialog?.kind === 'rename' && <Modal title={`修改显示名称：${dialog.member.display_name}`} onClose={closeDialog}><form onSubmit={(event) => { if (dialog.kind === 'rename') void rename(event, dialog.member) }}><label htmlFor="rename-display">显示名称</label><input id="rename-display" value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={40} required disabled={pending}/>{actionError && <p className="form-error" role="alert">{actionError}</p>}<div className="modal-actions"><button type="button" className="button button-secondary" onClick={closeDialog}>取消</button><button className="button button-primary" disabled={pending}>保存</button></div></form></Modal>}
    {dialog?.kind === 'reset' && <Modal title={`重置成员口令：${dialog.member.display_name}`} onClose={closeDialog}><p className="modal-description">重置 {dialog.member.display_name} 的口令后，该成员现有登录会失效，并需要在下次登录时更换密码。</p>{actionError && <p className="form-error" role="alert">{actionError}</p>}<div className="modal-actions"><button className="button button-secondary" onClick={closeDialog}>取消</button><button className="button button-primary" disabled={pending} onClick={() => { if (dialog.kind === 'reset') void reset(dialog.member) }}>{pending ? '重置中…' : '确认重置'}</button></div></Modal>}
    {dialog?.kind === 'temporary' && <Modal title="临时密码" onClose={closeDialog}><p className="modal-description">请现在将临时密码交给 <strong>{dialog.username}</strong>。关闭后此页面不会再次显示。</p><div className="temporary-password"><span>一次性临时密码</span><strong>{dialog.password}</strong></div><div className="modal-actions"><button className="button button-primary" onClick={closeDialog}>关闭</button></div></Modal>}
  </main>
}
