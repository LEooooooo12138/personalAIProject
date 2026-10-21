import { useState, type FormEvent } from 'react'
import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import { ApiError, errorText } from './api'
import { ActionLink, Alert, AppShell, AuthFrame, Avatar, Button, Field, Icon, IconButton, Input, LoadingState, Navigation, PageFrame, PageHeader, navigationItemClass } from './ui'
import { Brand } from './components/Brand'
import { PreviewNotice } from './PreviewNotice'
import './styles/auth.css'
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
import AssistantPage from './pages/AssistantPage'

const navigation = [
  { to: '/app/', label: '首页', icon: 'home' as const },
  { to: '/app/account', label: '账号', icon: 'user' as const },
]

function LoginScreen() {
  const { login, status, authWaiting, error: statusError, refresh, retryLogout } = useAuth()
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
  const disabled = pending || status === 'uninitialized' || status === 'logout_failed'
  return <AuthFrame brand={<Brand />} title="欢迎回家" description="登录后，继续和家庭助手聊聊今天的事。" footer={<div className="jx-auth-footnote"><Icon name="lock" />仅供家庭成员在本地服务中使用</div>}>
    {status === 'uninitialized' && <Alert tone="warning" live="polite">家庭控制台尚未初始化，请联系管理员完成本地初始化。</Alert>}
    {status === 'unavailable' && <Alert tone="danger" live="assertive" actions={<><Button variant="secondary" onClick={() => void refresh()}>重试</Button><Button variant="ghost" onClick={() => window.location.reload()}>重新打开页面</Button></>}>{statusError}</Alert>}
    {status === 'logout_failed' && <Alert tone="danger" live="assertive" actions={<Button variant="secondary" onClick={() => void retryLogout()}>重试退出</Button>}>{statusError}</Alert>}
    {authWaiting && <Alert tone="info" live="polite">认证操作仍在等待；当前界面已隐藏私人内容</Alert>}
    <form className="jx-auth-form" onSubmit={(event) => void submit(event)}>
      <Field id="login-name" label="用户名" required><Input id="login-name" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} required disabled={disabled} /></Field>
      <Field id="login-password" label="密码" required><Input id="login-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required disabled={disabled} /></Field>
      {message && <Alert tone="danger" live="assertive">{message}</Alert>}
      <Button type="submit" busy={pending} disabled={disabled} className="jx-auth-submit">{pending ? '正在登录…' : '登录'}<Icon name="arrow" size={18}/></Button>
    </form>
  </AuthFrame>
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
  return <AuthFrame brand={<Brand />} title="设置新密码" description="首次登录需要更换临时密码。完成后请使用新密码重新登录。">
    <form className="jx-auth-form" onSubmit={(event) => void submit(event)}>
      <Field id="old-password" label="当前密码" required><Input id="old-password" type="password" autoComplete="current-password" value={oldPassword} onChange={(event) => setOldPassword(event.target.value)} required disabled={pending}/></Field>
      <Field id="new-password" label="新密码" required><Input id="new-password" type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} minLength={12} required disabled={pending}/></Field>
      <Field id="confirm-password" label="确认新密码" required><Input id="confirm-password" type="password" autoComplete="new-password" value={confirm} onChange={(event) => setConfirm(event.target.value)} required disabled={pending}/></Field>
      {error && <Alert tone="danger" live="assertive">{error}</Alert>}
      <Button type="submit" busy={pending} className="jx-auth-submit">{pending ? '保存中…' : '保存并重新登录'}</Button>
    </form>
    <Button variant="ghost" className="jx-auth-exit" onClick={() => void logout()}>退出登录</Button>
  </AuthFrame>
}
function PrivateApp() {
  const { identity, status, authWaiting, epoch, logout } = useAuth()
  if (status === 'loading') return <AuthFrame brand={<Brand/>} title={authWaiting ? '认证操作仍在等待' : '正在检查登录状态'}><LoadingState label={authWaiting ? '认证操作仍在等待；当前界面已隐藏私人内容' : '正在检查登录状态…'}/></AuthFrame>
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
  const denied = (title: string, description: string) => <PageFrame><PageHeader title={title}/><Alert tone="warning" live="assertive" actions={<ActionLink href="/app/account">返回我的</ActionLink>}>{description}</Alert></PageFrame>
  const links = (mobile = false) => (mobile ? items.filter((item) => item.to !== '/app/automations') : items).map((item) => <NavLink key={item.to} to={item.to} end={item.to === '/app/'} className={({ isActive }) => navigationItemClass(isActive)}><Icon name={item.icon}/><span>{mobile && item.to === '/app/areas' ? '设备' : item.label}</span></NavLink>)
  return <AppShell key={`${identity.user.id}:${epoch}`} brand={<Brand/>}
    navigation={<Navigation label="主导航" variant="side">{links()}</Navigation>}
    profile={<div data-jx-ui="" className="jx-shell-member"><Avatar label={identity.user.display_name}/><div className="jx-shell-member-text"><strong>{identity.user.display_name}</strong><span>{admin ? '管理员' : '家庭成员'}</span></div><IconButton label="退出登录" variant="ghost" onClick={() => void logout()}><Icon name="logout"/></IconButton></div>}
    topbar={<div data-jx-ui="" className="jx-topbar-person"><span className="jx-greeting topbar-greeting">你好，{identity.user.display_name}</span><Avatar label={identity.user.display_name}/><Button variant="ghost" aria-label="退出登录" onClick={() => void logout()}><Icon name="logout"/><span className="jx-logout-text">退出登录</span></Button></div>}
    mobileNavigation={<Navigation label="底部导航" variant="bottom">{links(true)}</Navigation>}><Routes>
        <Route path="/app/admin/assistant" element={admin && (has('providers:manage') || has('personality:manage')) ? <AssistantPage/> : denied('无权管理助手设置', '此页面仅向获得助手管理能力的管理员开放。')}/>
        <Route path="/app/" element={<HomePage/>}/><Route path="/app/chat" element={canOpenConversation ? <ChatPage/> : denied('当前无法使用对话', '当前服务未提供对话或历史记录能力。')}/>
        <Route path="/app/areas" element={canReadAreas ? <AreasPage/> : denied('当前无法查看家庭目录', '当前账号未获得区域读取能力。')}/><Route path="/app/areas/:areaID" element={canReadAreas ? <AreaPage/> : <Navigate to="/app/areas" replace/>}/><Route path="/app/areas/:areaID/devices/:deviceID" element={canReadAreas ? <DevicePage/> : <Navigate to="/app/areas" replace/>}/>
        <Route path="/app/knowledge" element={canReadKnowledge ? <KnowledgePage/> : denied('当前无法查看公开知识', '当前服务未提供公开知识读取能力。')}/><Route path="/app/admin/knowledge" element={canPrivate ? <AdminKnowledgePage/> : denied('无权查看私人知识', '此页面仅向有知识管理能力的管理员开放。')}/>
        <Route path="/app/automations" element={canReadSuggestions ? <AutomationsPage/> : denied('当前无法查看自动化结果', '当前服务未提供建议读取能力。')}/><Route path="/app/admin/collection" element={canCollect ? <CollectionPage/> : denied('无权查看采集详情', '此页面仅向管理员开放。')}/>
        <Route path="/app/account" element={<AccountPage/>}/><Route path="/app/members" element={canManage ? <MembersPage/> : denied('无权查看成员管理', '此页面仅向管理员开放。')}/><Route path="*" element={<Navigate to="/app/" replace/>}/>
      </Routes>
  </AppShell>
}

export function App() { return <><PreviewNotice/><AuthProvider><PrivateApp/></AuthProvider></> }
