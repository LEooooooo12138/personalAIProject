import { useCallback, useEffect, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { ApiError, consoleApi, errorText, requestConsole, type ConsoleSession } from '../api'
import { useAuth } from '../auth'
import SafeMarkdown from '../chat/SafeMarkdown'
import ControlCard, { DeviceResultCard } from '../chat/ControlCard'
import { isControlProposal, isDeviceResult, newChatRequestId, type ChatFrame } from '../chat/control-types'
import { initialChatState, transition, type Action } from '../chat/chat-state'
import { openChatSocket } from '../coordinator'
import './chat.css'
import { Icon } from '../icons'

export default function ChatPage() {
  const { identity, epoch, signal, isCurrent, expire } = useAuth()
  const [mobile, setMobile] = useState(() => window.matchMedia?.('(max-width: 760px)').matches ?? false)
  const [showHistory, setShowHistory] = useState(false)
  useEffect(() => {
    const media = window.matchMedia?.('(max-width: 760px)')
    if (!media) return
    const update = () => setMobile(media.matches)
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [])
  const navigate = useNavigate()
  const location = useLocation()
  const [state, setState] = useState(() => initialChatState(epoch))
  const stateRef = useRef(state)
  const identityRef = useRef(identity)
  identityRef.current = identity
  const [sessions, setSessions] = useState<ConsoleSession[]>([])
  const [listError, setListError] = useState('')
  const [draft, setDraft] = useState('')
  const [reconciling, setReconciling] = useState(false)
  const socketRef = useRef<WebSocket | null>(null)
  const handshakeRef = useRef<AbortController | null>(null)
  const historyRef = useRef<AbortController | null>(null)
  const listRef = useRef<AbortController | null>(null)
  const controlRef = useRef(new AbortController())
  const runningControlsRef = useRef(new Set<string>())
  const requestRef = useRef<{ id: string; content: string } | null>(null)
  const retryRequestRef = useRef<{ id: string; content: string } | null>(null)
  const [controlErrors, setControlErrors] = useState<Record<string, string>>({})
  const composingRef = useRef(false)
  const transcriptRef = useRef<HTMLDivElement | null>(null)
  const atBottomRef = useRef(true)
  const userScrollRef = useRef(false)
  const internalNavigationSidRef = useRef<string | null | undefined>(undefined)

  const commit = useCallback((action: Action) => {
    const next = transition(stateRef.current, action)
    stateRef.current = next
    setState(next)
    return next
  }, [])
  const closeActive = useCallback(() => {
    controlRef.current.abort()
    controlRef.current = new AbortController()
    runningControlsRef.current = new Set<string>()
    requestRef.current = null
    retryRequestRef.current = null
    setControlErrors({})
    handshakeRef.current?.abort()
    handshakeRef.current = null
    const socket = socketRef.current
    socketRef.current = null
    socket?.close(1000)
    historyRef.current?.abort()
    historyRef.current = null
  }, [])
  const refreshSessions = useCallback(async (expectedEpoch: number) => {
    listRef.current?.abort()
    const controller = new AbortController()
    listRef.current = controller
    try {
      const answer = await consoleApi.sessions(controller.signal)
      if (!controller.signal.aborted && isCurrent(expectedEpoch) && stateRef.current.epoch === expectedEpoch) {
        setSessions(answer.sessions)
        setListError('')
        return null
      }
      return undefined
    } catch (cause) {
      if (controller.signal.aborted || !isCurrent(expectedEpoch)) return undefined
      if (cause instanceof ApiError && cause.status === 401) { expire(); return undefined }
      const message = errorText(cause)
      setListError(message)
      return message
    }
  }, [expire, isCurrent])
  const readHistory = useCallback(async (sid: string, revision: number, expectedEpoch: number) => {
    historyRef.current?.abort()
    const controller = new AbortController()
    historyRef.current = controller
    try {
      const answer = await consoleApi.messages(sid, controller.signal)
      if (controller.signal.aborted || !isCurrent(expectedEpoch)) return undefined
      if (answer.id !== sid) return '对话记录与当前会话不一致，请重新核对。'
      commit({ type: 'history', sid: answer.id, messages: answer.messages, epoch: expectedEpoch, revision })
      const ids = [...new Set(answer.messages.flatMap((message) => (message.attachments ?? []).filter((attachment) => attachment.kind === 'control_proposal' && typeof attachment.proposal_id === 'string').map((attachment) => attachment.proposal_id)))]
      await Promise.all(ids.map(async (id) => {
        try {
          const proposal = await requestConsole<unknown>('/control/proposals/' + encodeURIComponent(id), { signal: controller.signal })
          if (controller.signal.aborted || !isCurrent(expectedEpoch)) return
          commit({ type: 'proposal', epoch: expectedEpoch, revision, sid, id, proposal: isControlProposal(proposal) && proposal.id === id && proposal.session_id === sid ? proposal : null })
        } catch (cause) {
          if (controller.signal.aborted || !isCurrent(expectedEpoch)) return
          if (cause instanceof ApiError && cause.status === 401) { expire(); return }
          commit({ type: 'proposal', epoch: expectedEpoch, revision, sid, id, proposal: null })
        }
      }))
      return null
    } catch (cause) {
      if (controller.signal.aborted || !isCurrent(expectedEpoch)) return undefined
      if (cause instanceof ApiError && cause.status === 401) { expire(); return undefined }
      if (cause instanceof ApiError && cause.status === 404) {
        if (stateRef.current.epoch !== expectedEpoch || stateRef.current.revision !== revision || stateRef.current.sid !== sid) return undefined
        const next = commit({ type: 'notFound', sid, epoch: expectedEpoch, revision })
        if (next.sid === null) { internalNavigationSidRef.current = null; navigate('/app/chat', { replace: true }) }
        return undefined
      }
      const message = errorText(cause)
      commit({ type: 'failure', epoch: expectedEpoch, revision, message })
      return message
    }
  }, [commit, expire, isCurrent, navigate])
  const reconcileUnknown = useCallback((sid: string | null, revision: number, expectedEpoch: number) => {
    setReconciling(true)
    const task = sid ? readHistory(sid, revision, expectedEpoch) : refreshSessions(expectedEpoch)
    void task.then((error) => {
      if (!isCurrent(expectedEpoch) || stateRef.current.revision !== revision) return
      if (error === null) commit({ type: 'checked', epoch: expectedEpoch, revision })
      else if (typeof error === 'string') commit({ type: 'failure', epoch: expectedEpoch, revision, message: error })
      setReconciling(false)
    })
  }, [commit, isCurrent, readHistory, refreshSessions])
  const selectSession = useCallback((sid: string | null) => {
    setShowHistory(false)
    if (sid === stateRef.current.sid && stateRef.current.phase !== 'unknown' && !(sid === null && stateRef.current.phase === 'sending')) return
    closeActive()
    atBottomRef.current = true
    userScrollRef.current = false
    const next = commit({ type: 'select', sid })
    setDraft('')
    setReconciling(false)
    internalNavigationSidRef.current = sid
    navigate(sid ? `/app/chat?sid=${encodeURIComponent(sid)}` : '/app/chat')
    if (sid) void readHistory(sid, next.revision, next.epoch)
  }, [closeActive, commit, navigate, readHistory])

  useEffect(() => {
    if (stateRef.current.epoch !== epoch) {
      closeActive()
      listRef.current?.abort()
      commit({ type: 'identity', epoch })
      setSessions([])
      setListError('')
      setDraft('')
      setReconciling(false)
    }
    void refreshSessions(epoch)
    return () => { listRef.current?.abort() }
  }, [epoch, closeActive, commit, refreshSessions])
  useEffect(() => () => { closeActive(); listRef.current?.abort() }, [closeActive])
  useEffect(() => {
    const sid = new URLSearchParams(location.search).get('sid')
    if (internalNavigationSidRef.current !== undefined) {
      if (internalNavigationSidRef.current === sid) internalNavigationSidRef.current = undefined
      return
    }
    if (sid !== stateRef.current.sid) {
      closeActive()
      atBottomRef.current = true
      userScrollRef.current = false
      const next = commit({ type: 'select', sid })
      if (sid) void readHistory(sid, next.revision, next.epoch)
    }
  }, [location.search, closeActive, commit, readHistory])
  useEffect(() => {
    const incoming = (location.state as { draft?: unknown } | null)?.draft
    if (typeof incoming === 'string' && incoming) setDraft((prior) => prior || incoming)
  }, [location.key, location.state])
  useLayoutEffect(() => {
    const transcript = transcriptRef.current
    if (transcript && atBottomRef.current) transcript.scrollTop = transcript.scrollHeight
  }, [state.messages, state.phase])

  const runControl = useCallback(async (id: string, operation: 'confirm' | 'cancel' | 'reconcile') => {
    const current = stateRef.current
    const proposal = current.proposals[id]
    const actor = identityRef.current
    const runningControls = runningControlsRef.current
    if (runningControls.has(id) || !actor || !proposal || proposal.session_id !== current.sid) return
    if (operation !== 'reconcile' && !actor.capabilities.includes('devices:control')) return
    if (operation === 'confirm' && (proposal.status !== 'pending' || Date.now() >= Date.parse(proposal.expires_at))) return
    if (operation === 'cancel' && proposal.status !== 'pending') return
    if (operation === 'reconcile' && proposal.status !== 'unknown') return
    const expectedEpoch = current.epoch
    const revision = current.revision
    const sid = current.sid
    const requestSignal = AbortSignal.any([signal, controlRef.current.signal])
    const active = () => !requestSignal.aborted && isCurrent(expectedEpoch) && stateRef.current.epoch === expectedEpoch && stateRef.current.revision === revision && stateRef.current.sid === sid && identityRef.current?.user.id === actor.user.id && identityRef.current?.csrf_token === actor.csrf_token
    runningControls.add(id)
    setControlErrors((old) => ({ ...old, [id]: '' }))
    let mutationStarted = false
    try {
      if (operation === 'reconcile') {
        const latest = await requestConsole<unknown>('/control/proposals/' + encodeURIComponent(id), { signal: requestSignal })
        if (!active()) return
        if (!isControlProposal(latest) || latest.id !== id || latest.session_id !== sid) throw new ApiError(502, 'ha_invalid_response')
        commit({ type: 'proposal', epoch: expectedEpoch, revision, sid: sid!, proposal: latest })
        // Local uncertainty may be a lost response to an already settled request.
        // Only a server-persisted unknown record needs the read-only reconcile POST.
        if (latest.status !== 'unknown') return
      }
      mutationStarted = true
      const answer = await requestConsole<unknown>('/control/proposals/' + encodeURIComponent(id) + '/' + operation, { method: 'POST', body: {}, csrfToken: actor.csrf_token, expected: { userId: actor.user.id, csrfToken: actor.csrf_token }, signal: requestSignal })
      if (!active()) return
      if (!isControlProposal(answer) || answer.id !== id || answer.session_id !== sid) throw new ApiError(502, 'ha_invalid_response')
      commit({ type: 'proposal', epoch: expectedEpoch, revision, sid: sid!, proposal: answer })
    } catch (cause) {
      if (!active()) return
      if (cause instanceof ApiError && cause.status === 401) { expire(); return }
      // Lost execution responses are never presented as safe to repeat.
      if (operation === 'confirm' && (!(cause instanceof ApiError) || cause.status === 0 || cause.status >= 500)) {
        commit({ type: 'proposal', epoch: expectedEpoch, revision, sid: sid!, proposal: { ...proposal, status: 'unknown' } })
        setControlErrors((old) => ({ ...old, [id]: '结果尚未确认，请核对状态，勿重复执行。' }))
      } else {
        setControlErrors((old) => ({ ...old, [id]: errorText(cause) }))
        if (operation !== 'reconcile' || mutationStarted) {
          try {
            const answer = await requestConsole<unknown>('/control/proposals/' + encodeURIComponent(id), { signal: requestSignal })
            if (active()) commit({ type: 'proposal', epoch: expectedEpoch, revision, sid: sid!, id, proposal: isControlProposal(answer) && answer.id === id && answer.session_id === sid ? answer : null })
          } catch { if (active()) commit({ type: 'proposal', epoch: expectedEpoch, revision, sid: sid!, id, proposal: null }) }
        }
      }
    } finally { runningControls.delete(id) }
  }, [commit, expire, isCurrent, signal])

  const send = useCallback(async (event?: FormEvent) => {
    event?.preventDefault()
    const content = draft.trim()
    const current = stateRef.current
    if (!content || current.phase !== 'idle' || !identity?.capabilities.includes('chat:use')) return
    atBottomRef.current = true
    userScrollRef.current = false
    const next = commit({ type: 'send', content })
    if (next === current) return
    setDraft('')
    setReconciling(false)
    const retry = retryRequestRef.current
    const requestId = retry?.content === content ? retry.id : newChatRequestId()
    requestRef.current = { id: requestId, content }
    retryRequestRef.current = null
    const expectedEpoch = next.epoch
    const revision = next.revision
    const requestedSid = next.sid ?? ''
    const requestedIdentity = identity
    const handshake = new AbortController()
    handshakeRef.current = handshake
    const handshakeSignal = AbortSignal.any([signal, handshake.signal])
    const activeIdentity = () => !handshakeSignal.aborted && isCurrent(expectedEpoch) && stateRef.current.epoch === expectedEpoch && stateRef.current.revision === revision && identityRef.current?.user.id === requestedIdentity.user.id && identityRef.current?.csrf_token === requestedIdentity.csrf_token
    let socket: WebSocket
    try { socket = await openChatSocket(requestedIdentity, handshakeSignal) }
    catch (cause) {
      if (!activeIdentity() || (cause instanceof DOMException && cause.name === 'AbortError')) return
      if (cause instanceof ApiError && cause.status === 401) { expire(); return }
      commit({ type: 'unknown', epoch: expectedEpoch, revision })
      reconcileUnknown(requestedSid || null, revision, expectedEpoch)
      return
    } finally {
      if (handshakeRef.current === handshake) handshakeRef.current = null
    }
    if (!activeIdentity()) { socket.close(1000); return }
    socketRef.current = socket
    let finished = false
    const active = () => socketRef.current === socket && activeIdentity()
    const uncertain = () => {
      if (!active() || finished) return
      finished = true
      const unknown = commit({ type: 'unknown', epoch: expectedEpoch, revision })
      reconcileUnknown(unknown.sid, revision, expectedEpoch)
      void consoleApi.me(signal).catch((cause) => { if (cause instanceof ApiError && cause.status === 401 && isCurrent(expectedEpoch)) expire() })
    }
    socket.onmessage = (message) => {
      if (!active() || finished) return
      let frame: ChatFrame
      try {
        const decoded: unknown = JSON.parse(message.data)
        if (!decoded || typeof decoded !== 'object' || Array.isArray(decoded)) { uncertain(); return }
        frame = decoded as ChatFrame
      } catch { uncertain(); return }
      if (frame.request_id && frame.request_id !== requestId) return
      if (frame.type === 'session' && frame.session_id) {
        const oldSid = stateRef.current.sid
        const updated = commit({ type: 'serverSession', epoch: expectedEpoch, revision, sid: frame.session_id })
        if (updated.sid !== oldSid) {
          internalNavigationSidRef.current = updated.sid
          navigate(`/app/chat?sid=${encodeURIComponent(updated.sid ?? '')}`, { replace: true })
        }
      } else if (['device_result', 'control_proposal', 'control_result'].includes(frame.type ?? '')) {
        if (frame.session_id !== stateRef.current.sid || frame.request_id !== requestId) return
        if (frame.type === 'device_result' ? !isDeviceResult(frame.device_result) : !isControlProposal(frame.proposal) || frame.proposal.session_id !== frame.session_id || frame.proposal.request_id !== requestId) { uncertain(); return }
        finished = true
        commit({ type: 'structured', epoch: expectedEpoch, revision, sid: frame.session_id!, content: typeof frame.content === 'string' ? frame.content : '', ...(frame.type === 'device_result' ? { device_result: frame.device_result } : { proposal: frame.proposal }) })
        socketRef.current = null
        socket.close(1000)
        void refreshSessions(expectedEpoch)
      } else if (frame.type === 'response' && typeof frame.content === 'string') {
        finished = true
        commit({ type: 'response', epoch: expectedEpoch, revision, content: frame.content })
        socketRef.current = null
        socket.close(1000)
        void refreshSessions(expectedEpoch)
      } else if (frame.type === 'error') {
        finished = true
        if (frame.code === 'session_not_found' && frame.session_id === stateRef.current.sid) {
          closeActive()
          const cleared = commit({ type: 'notFound', epoch: expectedEpoch, revision, sid: frame.session_id })
          if (cleared.sid === null) { internalNavigationSidRef.current = null; navigate('/app/chat', { replace: true }) }
        } else {
          commit({ type: 'unknown', epoch: expectedEpoch, revision })
          reconcileUnknown(stateRef.current.sid, revision, expectedEpoch)
        }
        socketRef.current = null
        socket.close(1000)
      }
    }
    socket.onerror = uncertain
    socket.onclose = uncertain
    try { socket.send(JSON.stringify({ session_id: requestedSid, request_id: requestId, content })) }
    catch { uncertain(); socket.close(1000) }
  }, [commit, draft, identity, signal, isCurrent, expire, navigate, closeActive, reconcileUnknown, refreshSessions])
  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (mobile || event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing || composingRef.current) return
    event.preventDefault()
    void send()
  }
  const canChat = identity?.capabilities.includes('chat:use') ?? false
  const canRead = identity?.capabilities.includes('sessions:read') ?? false
  const canControl = identity?.capabilities.includes('devices:control') ?? false
  const visibleState = state.epoch === epoch ? state : initialChatState(epoch)
  const visibleSessions = state.epoch === epoch ? sessions : []
  return <main className="chat-page">
    <div className="chat-heading"><div><span className="eyebrow">PRIVATE CONVERSATION</span><h1>{canChat ? '和家序聊聊' : '对话记录'}</h1><p>{canChat ? '你的想法，只留在你的对话里。' : '查看属于你的历史对话。'}</p></div>{canChat && <button className="button chat-new" type="button" onClick={() => selectSession(null)}><Icon name="plus"/>新对话</button>}</div>
    {mobile && <button type="button" className="button button-secondary mobile-history-toggle" onClick={() => setShowHistory((old) => !old)}><Icon name="clock"/>{showHistory ? '返回当前对话' : '查看对话列表'}</button>}<div className="chat-layout">
      <aside className="chat-history" aria-label="最近对话" hidden={mobile && !showHistory}><div className="chat-history-title"><h2>最近对话</h2><button type="button" className="chat-refresh" onClick={() => void refreshSessions(epoch)} aria-label="刷新对话列表"><Icon name="refresh"/></button></div>
        {listError && <p className="chat-inline-error" role="alert">{listError}</p>}
        {!canRead ? <p className="chat-muted">历史记录当前不可用。</p> : visibleSessions.length === 0 ? <p className="chat-muted">{canChat ? '还没有对话。发一条消息，开始记录。' : '还没有对话记录。'}</p> : <ul>{visibleSessions.map((item) => <li key={item.id}><button type="button" className={`chat-history-item ${visibleState.sid === item.id ? 'selected' : ''}`} onClick={() => selectSession(item.id)} aria-current={visibleState.sid === item.id ? 'true' : undefined}><span>{item.preview || '未命名对话'}</span><small>{new Date(item.last_active_at).toLocaleDateString('zh-CN')} · {item.message_count} 条消息</small></button></li>)}</ul>}
      </aside>
      <section className="chat-conversation" aria-label="当前对话" hidden={mobile && showHistory}>
        <div className="chat-conversation-head"><strong>{visibleState.sid ? '当前对话' : canChat ? '新的对话' : '选择对话'}</strong><span>仅自己可见</span></div>
        <div className="chat-transcript" role="log" aria-live="polite" aria-label="对话消息" tabIndex={0} ref={transcriptRef} onWheel={() => { userScrollRef.current = true }} onTouchStart={() => { userScrollRef.current = true }} onPointerDown={() => { userScrollRef.current = true }} onKeyDown={() => { userScrollRef.current = true }} onScroll={(event) => {
          if (!userScrollRef.current) return
          const area = event.currentTarget
          atBottomRef.current = area.scrollHeight - area.scrollTop - area.clientHeight < 96
        }}>
          {visibleState.messages.length === 0 && visibleState.phase !== 'loading' && <div className="chat-empty"><div className="chat-empty-mark"><Icon name="chat" size={32}/></div><h2>{canChat ? '今天想聊些什么？' : '选择一条对话'}</h2><p>{canChat ? '可以问问题，也可以把一件小事讲给我听。' : '从最近对话中选择一条，阅读历史记录。'}</p></div>}
          {visibleState.phase === 'loading' && <p className="chat-status" role="status">正在读取对话…</p>}
          {visibleState.messages.map((message, index) => <article key={`${index}-${message.timestamp}`} className={`chat-bubble ${message.role === 'user' ? 'from-user' : 'from-assistant'}`}>
            <span className="chat-role">{message.role === 'user' ? '你' : '家序'}</span>
            <div className="chat-markdown"><SafeMarkdown>{message.content}</SafeMarkdown></div>
            {message.device_result && <DeviceResultCard result={message.device_result}/>}
            {(message.attachments ?? []).filter((attachment) => attachment.kind === 'control_proposal').map((attachment) => {
              const proposal = visibleState.proposals[attachment.proposal_id]
              return proposal
                ? <ControlCard key={attachment.proposal_id} proposal={proposal} canControl={canControl} error={controlErrors[proposal.id]}
                    onConfirm={(id) => runControl(id, 'confirm')} onCancel={(id) => runControl(id, 'cancel')} onReconcile={(id) => runControl(id, 'reconcile')}/>
                : <p className="control-card control-unavailable" key={attachment.proposal_id} role="status">{proposal === null ? '此设备操作记录暂不可用。' : '正在读取设备操作记录…'}</p>
            })}
          </article>)}
          {visibleState.phase === 'sending' && <p className="chat-status" role="status">正在生成完整回答…</p>}
        </div>
        {visibleState.error && <div className="chat-alert" role="alert"><span>{visibleState.error}{visibleState.recoveryError && <small className="chat-recovery-error">核对失败：{visibleState.recoveryError}</small>}</span>{visibleState.phase === 'unknown' && <div>{reconciling ? <span>正在核对历史…</span> : <><button type="button" onClick={() => { const current = stateRef.current; reconcileUnknown(current.sid, current.revision, current.epoch) }}>再次核对历史</button>{visibleState.recovered && <>{visibleState.sid && <button type="button" onClick={() => commit({ type: 'continue' })}>继续对话，不重发</button>}<button type="button" onClick={() => { const pending = stateRef.current.pending; if (pending) { retryRequestRef.current = requestRef.current; setDraft(pending); commit({ type: 'allowRetry' }) } }}>重试上一条</button></>}</>}</div>}</div>}
        {!canChat ? <div className="chat-unavailable" role="status">当前仅可阅读历史记录。</div> : <form className="chat-composer" onSubmit={send}><label htmlFor="chat-draft">消息内容</label><textarea id="chat-draft" aria-label="消息内容" value={state.epoch === epoch ? draft : ''} onChange={(event) => setDraft(event.target.value)} onKeyDown={onComposerKeyDown} onCompositionStart={() => { composingRef.current = true }} onCompositionEnd={() => { composingRef.current = false }} placeholder="写下你想说的…" rows={3} disabled={visibleState.phase === 'sending' || visibleState.phase === 'unknown'}/><div className="chat-composer-bottom"><small>{mobile ? 'Enter 换行 · 点击发送' : 'Enter 发送 · Shift + Enter 换行'}</small><button type="submit" className="button button-primary" aria-label="发送消息" disabled={visibleState.phase !== 'idle' || !draft.trim()}>发送 <Icon name="send"/></button></div></form>}
      </section>
    </div>
  </main>
}
