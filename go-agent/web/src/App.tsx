import { useState, type FormEvent } from 'react'
import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import { ApiError, errorText } from './api'
import { Icon } from './icons'
import HomePage from './pages/HomePage'
import AccountPage from './pages/AccountPage'
import MembersPage from './pages/MembersPage'
import ChatPage from './pages/ChatPage'
import AreasPage from './pages/AreasPage'
import AreaPage from './pages/AreaPage'
import DevicePage from './pages/DevicePage'
import KnowledgePage from './pages/KnowledgePage'
import AdminKnowledgePage from './pages/AdminKnowledgePage'
import AutomationsPage from './pages/AutomationsPage'
import CollectionPage from './pages/CollectionPage'

const navigation = [
  { to: '/app/', label: '首页', icon: 'home' as const },
  { to: '/app/account', label: '账号', icon: 'user' as const },
]

function Brand({ compact = false }: { compact?: boolean }) {
  return <div className={`brand ${compact ? 'brand-compact' : ''}`}>
    <span className="brand-mark"><Icon name="spark" size={24} /></span>
    <span className="brand-word"><strong>家序</strong><small>家庭助手</small></span>
  </div>
}

function LoginScreen() {
  const { login, status, error: statusError, refresh, retryLogout } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [message, setMessage] = useState('')
  const [pending, setPending] = useState(false)
  async function submit(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    setPending(true)
    try { await login(username.trim(), password) }
    catch (cause) {
      setMessage(cause instanceof ApiError && cause.status === 401 ? '用户名或密码不正确，请再试一次。' : errorText(cause))
    } finally { setPending(false); setPassword('') }
  }
  return <main className="auth-screen">
    <div className="auth-art" aria-hidden="true"><span className="art-orbit orbit-one"/><span className="art-orbit orbit-two"/><span className="art-house"><Icon name="home" size={64}/></span><span className="art-star"><Icon name="spark" size={24}/></span></div>
    <div className="auth-card">
      <Brand />
      <div className="auth-heading"><span className="eyebrow">YOUR PRIVATE FAMILY SPACE</span><h1>欢迎回家</h1><p>登录后，继续和家庭助手聊聊今天的事。</p></div>
      {status === 'uninitialized' ? <div className="notice" role="status">家庭控制台尚未初始化，请联系管理员完成本地初始化。</div> : null}
      {status === 'unavailable' ? <div className="notice notice-error" role="alert">{statusError}<button className="text-button" onClick={() => void refresh()}>重试</button></div> : null}
      {status === 'logout_failed' ? <div className="notice notice-error" role="alert">{statusError}<button className="text-button" onClick={() => void retryLogout()}>重试退出</button></div> : null}
      <form onSubmit={(event) => void submit(event)}>
        <label htmlFor="login-name">用户名</label><input id="login-name" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} required disabled={pending || status === 'uninitialized' || status === 'logout_failed'} />
        <label htmlFor="login-password">密码</label><input id="login-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required disabled={pending || status === 'uninitialized' || status === 'logout_failed'} />
        {message && <p className="form-error" role="alert">{message}</p>}
        <button className="button button-primary button-full" disabled={pending || status === 'uninitialized' || status === 'logout_failed'}>{pending ? '正在登录…' : '登录'}<Icon name="arrow" size={18}/></button>
      </form>
      <p className="auth-footnote"><Icon name="lock" size={15}/> 仅供家庭成员在本地服务中使用</p>
    </div>
  </main>
}

function ForcedPassword() {
  const auth = useAuth()
  const { changePassword, logout } = auth
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (newPassword !== confirm) { setError('两次输入的新密码不一致。'); return }
    if ([...newPassword].length < 12 || [...newPassword].length > 128) { setError('新密码需为 12 至 128 个字符。'); return }
    const expected = auth.epoch
    setError(''); setPending(true)
    try { await changePassword(oldPassword, newPassword) } catch (cause) {
      if (!auth.isCurrent(expected)) return
      if (cause instanceof ApiError && cause.status === 401) auth.expire()
      else setError(errorText(cause))
    } finally { if (auth.isCurrent(expected)) { setPending(false); setOldPassword(''); setNewPassword(''); setConfirm('') } }
  }
  return <main className="auth-screen"><div className="auth-card password-card"><Brand/><div className="auth-heading"><span className="eyebrow">ACCOUNT SECURITY</span><h1>设置新密码</h1><p>首次登录需要更换临时密码。完成后请使用新密码重新登录。</p></div>
    <form onSubmit={(event) => void submit(event)}><label htmlFor="old-password">当前密码</label><input id="old-password" type="password" autoComplete="current-password" value={oldPassword} onChange={(event) => setOldPassword(event.target.value)} required disabled={pending}/><label htmlFor="new-password">新密码</label><input id="new-password" type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} minLength={12} required disabled={pending}/><label htmlFor="confirm-password">确认新密码</label><input id="confirm-password" type="password" autoComplete="new-password" value={confirm} onChange={(event) => setConfirm(event.target.value)} required disabled={pending}/>{error && <p className="form-error" role="alert">{error}</p>}<button className="button button-primary button-full" disabled={pending}>{pending ? '保存中…' : '保存并重新登录'}</button></form>
    <button className="text-button password-exit" onClick={() => void logout()}>退出登录</button>
  </div></main>
}

function PrivateApp() {
  const { identity, status, epoch, logout } = useAuth()
  if (status === 'loading') return <main className="loading-screen" role="status"><Brand/><span className="loader"/>正在检查登录状态…</main>
  if (!identity || status !== 'ready') return <LoginScreen />
  if (identity.user.must_change_password) return <ForcedPassword />
  const has = (capability: string) => identity.capabilities.includes(capability)
  const admin = identity.user.role === 'admin'
  const canManage = admin && has('members:manage')
  const canChat = has('chat:use')
  const canRead = has('sessions:read')
  const canReadAreas = has('areas:read')
  const canReadKnowledge = has('knowledge:read')
  const canReadSuggestions = has('suggestions:read')
  const canPrivate = admin && has('knowledge:manage')
  const canCollect = admin && has('collection:read')
  const canOpenConversation = canChat || canRead
  const items = [navigation[0], ...(canOpenConversation ? [{ to: '/app/chat', label: canChat ? '对话' : '对话记录', icon: 'chat' as const }] : []), ...(canReadAreas ? [{ to: '/app/areas', label: '区域与设备', icon: 'grid' as const }] : []), ...(canReadKnowledge ? [{ to: '/app/knowledge', label: '知识', icon: 'book' as const }] : []), ...(canReadSuggestions ? [{ to: '/app/automations', label: '自动化结果', icon: 'workflow' as const }] : []), { ...navigation[1], label: '我的' }]
  const denied = (title: string, description: string) => <main className="state-card" role="alert"><h1>{title}</h1><p>{description}</p><a href="/app/account">返回我的</a></main>
  return <div className="app-shell" key={`${identity.user.id}:${epoch}`}>
    <aside className="sidebar"><Brand/><div className="sidebar-caption">家庭空间</div><nav aria-label="主导航" className="side-nav">{items.map((item) => <NavLink key={item.to} to={item.to} end={item.to === '/app/'} className={({isActive}) => `nav-item ${isActive ? 'active' : ''}`}><Icon name={item.icon}/><span>{item.label}</span></NavLink>)}</nav><div className="sidebar-bottom"><div className="sidebar-profile"><span className="avatar">{identity.user.display_name.slice(0, 1)}</span><div><strong>{identity.user.display_name}</strong><small>{admin ? '管理员' : '家庭成员'}</small></div><button className="icon-button" title="退出登录" aria-label="退出登录" onClick={() => void logout()}><Icon name="logout"/></button></div></div></aside>
    <div className="main-column"><header className="topbar"><div className="mobile-brand"><Brand compact/></div><div className="topbar-label">家序 · 家庭助手</div><div className="topbar-profile"><span className="topbar-greeting">你好，{identity.user.display_name}</span><span className="avatar avatar-small">{identity.user.display_name.slice(0, 1)}</span><button className="topbar-logout" onClick={() => void logout()}>退出登录</button></div></header>
      <div className="page-container"><Routes>
        <Route path="/app/" element={<HomePage/>}/><Route path="/app/chat" element={canOpenConversation ? <ChatPage/> : denied('当前无法使用对话', '当前服务未提供对话或历史记录能力。')}/>
        <Route path="/app/areas" element={canReadAreas ? <AreasPage/> : denied('当前无法查看家庭目录', '当前账号未获得区域读取能力。')}/><Route path="/app/areas/:areaID" element={canReadAreas ? <AreaPage/> : <Navigate to="/app/areas" replace/>}/><Route path="/app/areas/:areaID/devices/:deviceID" element={canReadAreas ? <DevicePage/> : <Navigate to="/app/areas" replace/>}/>
        <Route path="/app/knowledge" element={canReadKnowledge ? <KnowledgePage/> : denied('当前无法查看公开知识', '当前服务未提供公开知识读取能力。')}/><Route path="/app/admin/knowledge" element={canPrivate ? <AdminKnowledgePage/> : denied('无权查看私人知识', '此页面仅向有知识管理能力的管理员开放。')}/>
        <Route path="/app/automations" element={canReadSuggestions ? <AutomationsPage/> : denied('当前无法查看自动化结果', '当前服务未提供建议读取能力。')}/><Route path="/app/admin/collection" element={canCollect ? <CollectionPage/> : denied('无权查看采集详情', '此页面仅向管理员开放。')}/>
        <Route path="/app/account" element={<AccountPage/>}/><Route path="/app/members" element={canManage ? <MembersPage/> : denied('无权查看成员管理', '此页面仅向管理员开放。')}/><Route path="*" element={<Navigate to="/app/" replace/>}/>
      </Routes></div>
    </div><nav className="bottom-nav" aria-label="底部导航">{items.filter((item) => item.to !== '/app/automations').map((item) => <NavLink key={item.to} to={item.to} end={item.to === '/app/'} className={({isActive}) => `bottom-nav-item ${isActive ? 'active' : ''}`}><Icon name={item.icon}/><span>{item.to === '/app/areas' ? '设备' : item.label}</span></NavLink>)}</nav>
  </div>
}

export function App() { return <AuthProvider><PrivateApp/></AuthProvider> }
