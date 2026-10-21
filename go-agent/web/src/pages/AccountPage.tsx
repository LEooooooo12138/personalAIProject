import { Alert, Avatar, Button, Card, Field, Grid, Input, KeyValueList, PageFrame, PageHeader, Section, Stack, StatusBadge } from "../ui"
import { useState, type FormEvent } from 'react'
import { useAuth } from '../auth'
import { ApiError, errorText } from '../api'
import { Icon } from '../ui'
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
  return <PageFrame>
    <PageHeader title="我的" description="管理自己的登录信息，让家庭空间更安心。"/>
    <Grid columns={2} gap={24}>
      <Card heading="个人资料">
        <Stack gap={16}><div className="jx-profile"><Avatar label={identity?.user.display_name ?? ''} size="large"/><div><h2>{identity?.user.display_name}</h2><StatusBadge tone="neutral" label={identity?.user.role === 'admin' ? '家庭管理员' : '家庭成员'}/></div></div>
        <KeyValueList items={[{ id: 'username', label: '登录名', value: identity?.user.username }, { id: 'role', label: '账号角色', value: identity?.user.role === 'admin' ? '管理员' : '成员' }]}/>
        {identity?.user.role === 'admin' && identity.capabilities.includes('members:manage') && <Link className="jx-action-link" to="/app/members"><Icon name="members"/>成员管理<Icon name="chevron"/></Link>}
        <Button variant="secondary" onClick={() => void auth.logout()}><Icon name="logout"/>退出当前登录</Button></Stack>
      </Card>
      <Card heading="修改密码" description="修改成功后，所有登录会失效。请使用新密码重新登录。">
        <form onSubmit={(event) => void submit(event)}><Stack gap={16}>
          <Field id="account-current" label="当前密码" required><Input id="account-current" type="password" autoComplete="current-password" value={oldPassword} onChange={(event) => setOldPassword(event.target.value)} required disabled={pending}/></Field>
          <Field id="account-new" label="新密码" hint="12 至 128 个字符" required><Input id="account-new" type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} minLength={12} required disabled={pending} aria-describedby="account-new-hint"/></Field>
          <Field id="account-confirm" label="确认新密码" required><Input id="account-confirm" type="password" autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required disabled={pending}/></Field>
          {error && <Alert tone="danger" live="assertive">{error}</Alert>}
          <Button type="submit" busy={pending}>{pending ? '正在保存…' : '保存新密码'}</Button>
        </Stack></form>
      </Card>
    </Grid>
    <Section title="家庭功能" description="家庭账号登录与后台 HA 授权分别管理。设备授权失效请联系管理员；页面不接收 HA 密码或令牌。">
      <Grid columns={3} gap={16}>
        {identity?.user.role === 'admin' && (identity.capabilities.includes('providers:manage') || identity.capabilities.includes('personality:manage')) && <Link className="jx-entry" to="/app/admin/assistant"><Icon name="spark"/>助手设置<Icon name="arrow"/></Link>}
        {identity?.capabilities.includes('knowledge:read') && <Link className="jx-entry" to="/app/knowledge"><Icon name="book"/>公开知识<Icon name="arrow"/></Link>}
        {identity?.capabilities.includes('suggestions:read') && <Link className="jx-entry" to="/app/automations"><Icon name="workflow"/>{identity.user.role === 'admin' ? '建议审核与结果共享' : '自动化结果'}<Icon name="arrow"/></Link>}
        {identity?.user.role === 'admin' && identity.capabilities.includes('knowledge:manage') && <Link className="jx-entry" to="/app/admin/knowledge"><Icon name="lock"/>管理员私人知识<Icon name="arrow"/></Link>}
        {identity?.user.role === 'admin' && identity.capabilities.includes('collection:read') && <Link className="jx-entry" to="/app/admin/collection"><Icon name="clock"/>采集详情与分析<Icon name="arrow"/></Link>}
      </Grid>
    </Section>
  </PageFrame>

}
