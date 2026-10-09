import { useState, type FormEvent } from 'react'
import { useAuth } from '../auth'
import { ApiError, errorText } from '../api'
import { Icon } from '../icons'
import { Link } from 'react-router-dom'

export default function AccountPage() {
  const auth = useAuth()
  const { identity, changePassword } = auth
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (newPassword !== confirmation) { setError('两次输入的新密码不一致。'); return }
    if ([...newPassword].length < 12 || [...newPassword].length > 128) { setError('新密码需为 12 至 128 个字符。'); return }
    const expected = auth.epoch
    setPending(true); setError('')
    try { await changePassword(oldPassword, newPassword) } catch (cause) {
      if (!auth.isCurrent(expected)) return
      if (cause instanceof ApiError && cause.status === 401) auth.expire()
      else setError(errorText(cause))
    } finally { if (auth.isCurrent(expected)) { setPending(false); setOldPassword(''); setNewPassword(''); setConfirmation('') } }
  }
  return <main className="content-page"><div className="page-intro"><span className="eyebrow">YOUR ACCOUNT</span><h1>我的</h1><p>管理自己的登录信息，让家庭空间更安心。</p></div><section className="panel account-shortcuts" aria-label="家庭功能入口"><h2>家庭功能</h2><div className="account-entry-grid">{identity?.capabilities.includes('knowledge:read') && <Link to="/app/knowledge"><Icon name="book"/>公开知识</Link>}{identity?.capabilities.includes('suggestions:read') && <Link to="/app/automations"><Icon name="workflow"/>{identity.user.role === 'admin' ? '建议审核与结果共享' : '自动化结果'}</Link>}{identity?.user.role === 'admin' && identity.capabilities.includes('knowledge:manage') && <Link to="/app/admin/knowledge"><Icon name="lock"/>管理员私人知识</Link>}{identity?.user.role === 'admin' && identity.capabilities.includes('collection:read') && <Link to="/app/admin/collection"><Icon name="clock"/>采集详情与分析</Link>}</div><p>家庭账号登录与后台 HA 授权分别管理。设备授权失效请联系管理员；页面不接收 HA 密码或令牌。</p><button className="button button-secondary" onClick={() => void auth.logout()}><Icon name="logout"/>退出当前登录</button></section><div className="account-grid"><section className="panel profile-panel"><div className="panel-label">个人资料</div><div className="profile-feature"><span className="avatar avatar-large">{identity?.user.display_name.slice(0, 1)}</span><div><h2>{identity?.user.display_name}</h2><p>{identity?.user.role === 'admin' ? '家庭管理员' : '家庭成员'}</p></div></div><div className="detail-row"><span>登录名</span><strong>{identity?.user.username}</strong></div><div className="detail-row"><span>账号角色</span><strong>{identity?.user.role === 'admin' ? '管理员' : '成员'}</strong></div>{identity?.user.role === 'admin' && identity.capabilities.includes('members:manage') && <Link className="account-management-link" to="/app/members"><Icon name="members" size={18}/>成员管理<Icon name="chevron" size={17}/></Link>}</section><section className="panel security-panel"><div className="panel-label"><Icon name="lock" size={18}/> 修改密码</div><p className="panel-description">修改成功后，所有登录会失效。请使用新密码重新登录。</p><form onSubmit={(event) => void submit(event)}><label htmlFor="account-current">当前密码</label><input id="account-current" type="password" autoComplete="current-password" value={oldPassword} onChange={(event) => setOldPassword(event.target.value)} required disabled={pending}/><label htmlFor="account-new">新密码</label><input id="account-new" type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} minLength={12} required disabled={pending}/><label htmlFor="account-confirm">确认新密码</label><input id="account-confirm" type="password" autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required disabled={pending}/>{error && <p className="form-error" role="alert">{error}</p>}<button className="button button-primary" disabled={pending}>{pending ? '正在保存…' : '保存新密码'}</button></form></section></div></main>
}
