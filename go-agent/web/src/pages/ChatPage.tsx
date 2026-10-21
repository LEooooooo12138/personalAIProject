import { useCallback, useEffect, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { ApiError, consoleApi, errorText, requestConsole, type ConsoleMessage, type ConsoleSession, type ControlProposalResponse } from '../api'
import { useAuth } from '../auth'
import SafeMarkdown from '../chat/SafeMarkdown'
import ControlCard, { DeviceResultCard } from '../chat/ControlCard'
import { isControlProposal, isDeviceResult, newChatRequestId, type ChatEntry, type ChatFrame, type ControlProposal } from '../chat/control-types'
import { chatStageText, parseChatStageFrame } from '../chat/chat-stage'
import { initialChatState, messagePresentation, storedControlMessage, transition, type Action } from '../chat/chat-state'
import { openChatSocket } from '../coordinator'
import { Alert, Avatar, Button, EmptyState, Field, Icon, IconButton, PageFrame, PageHeader, Select, Textarea } from '../ui'
import '../chat/presentation.css'
import { useVoice } from '../chat/useVoice'
import VoiceControls from '../chat/VoiceControls'
import { createProposalReader, type ProposalRead } from '../chat/proposalReads'
import { useProposalRefresh } from '../chat/useProposalRefresh'

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
  const [budgetNotice, setBudgetNotice] = useState<{ epoch: number; revision: number; sid: string | null; pairs: number } | null>(null)
  const [entry, setEntry] = useState<ChatEntry>('auto')
  const [entryBlockCode, setEntryBlockCode] = useState<'request_entry_conflict' | 'request_entry_limit' | null>(null)
  const entryBlockCodeRef = useRef<typeof entryBlockCode>(null)
  const entryBlocked = entryBlockCode !== null
  const [reconciling, setReconciling] = useState(false)
  const socketRef = useRef<WebSocket | null>(null)
  const handshakeRef = useRef<AbortController | null>(null)
  const historyRef = useRef<AbortController | null>(null)
  const proposalReaderRef = useRef<ProposalRead | null>(null)
  if (!proposalReaderRef.current) proposalReaderRef.current = createProposalReader()
  const readProposal = proposalReaderRef.current
  const listRef = useRef<AbortController | null>(null)
  const controlRef = useRef(new AbortController())
  const runningControlsRef = useRef(new Set<string>())
  const [controlsSettled, setControlsSettled] = useState(0)
  const recoveryAttemptRef = useRef<ConsoleMessage[] | null>(null)
  const requestRef = useRef<{ id: string; content: string; entry: ChatEntry } | null>(null)
  const retryRequestRef = useRef<{ id: string; content: string; entry: ChatEntry } | null>(null)
  const [controlErrors, setControlErrors] = useState<Record<string, string>>({})
  const composingRef = useRef(false)
  const transcriptRef = useRef<HTMLDivElement | null>(null)
  const atBottomRef = useRef(true)
  const userScrollRef = useRef(false)
  const internalNavigationSidRef = useRef<string | null | undefined>(undefined)
  const sendVoiceRef = useRef<(text: string) => void>(() => {})
  const voice = useVoice(state.sid, state.revision, identity?.capabilities.includes('chat:use') ?? false, (text) => sendVoiceRef.current(text))
  const voiceRef = useRef(voice)
  voiceRef.current = voice

  const commit = useCallback((action: Action) => {
    const next = transition(stateRef.current, action)
    stateRef.current = next
    setState(next)
    return next
  }, [])
  const closeActive = useCallback(() => {
    voiceRef.current.cancel()
    controlRef.current.abort()
    controlRef.current = new AbortController()
    runningControlsRef.current = new Set<string>()
    requestRef.current = null
    retryRequestRef.current = null
    setBudgetNotice(null)
    entryBlockCodeRef.current = null
    setEntryBlockCode(null)
    setEntry('auto')
    setControlErrors({})
    handshakeRef.current?.abort()
    handshakeRef.current = null
    const socket = socketRef.current
    socketRef.current = null
    socket?.close(1000)
    historyRef.current?.abort()
    historyRef.current = null
    recoveryAttemptRef.current = null
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
  const applyProposal = useCallback((proposal: ControlProposal, sid: string, revision: number, expectedEpoch: number, autoRead = false) => {
    // A chat-only request cannot acquire control capability through recovery GETs.
    // Other authenticated historical proposals keep their normal display and actions.
    if (requestRef.current?.entry === 'chat' && proposal.request_id === requestRef.current.id) {
      return commit({ type: 'proposal', epoch: expectedEpoch, revision, sid, id: proposal.id, proposal: null })
    }
    const envelope = proposal as ControlProposalResponse
    const before = stateRef.current
    const stored = storedControlMessage(envelope.assistant_message, proposal)
    const next = commit({ type: 'proposal', epoch: expectedEpoch, revision, sid, proposal, assistant_message: envelope.assistant_message })
    if (autoRead && stored && next.sid === sid && next.phase === 'idle' && !next.controlHistoryNeeded
      && next.messages.length === before.messages.length + 1 && next.messages[stored.index] === stored.message) {
      voiceRef.current.onFinal(sid, stored.index, stored.message.content)
    }
    return next
  }, [commit])
  const readHistory = useCallback(async (sid: string, revision: number, expectedEpoch: number, recoveringControl = false) => {
    historyRef.current?.abort()
    const controller = new AbortController()
    historyRef.current = controller
    const snapshot = stateRef.current.messages
    const phase = stateRef.current.phase
    const active = () => !controller.signal.aborted && isCurrent(expectedEpoch) && stateRef.current.epoch === expectedEpoch && stateRef.current.revision === revision && stateRef.current.sid === sid
    // An idle recovery must not replace a turn started or completed while GET was in flight.
    const unchanged = () => active() && stateRef.current.messages === snapshot && stateRef.current.phase === phase
    try {
      const answer = await consoleApi.messages(sid, controller.signal)
      if (!unchanged()) return undefined
      if (answer.id !== sid) return '对话记录与当前会话不一致，请重新核对。'
      const restored = commit({ type: 'history', sid: answer.id, messages: answer.messages, epoch: expectedEpoch, revision })
      if (recoveringControl) recoveryAttemptRef.current = restored.messages
      const ids = [...new Set(answer.messages.flatMap((message) => (message.attachments ?? []).filter((attachment) => attachment.kind === 'control_proposal' && typeof attachment.proposal_id === 'string').map((attachment) => attachment.proposal_id)))]
      await Promise.all(ids.map(async (id) => {
        try {
          const proposal = await readProposal(id, AbortSignal.any([signal, controller.signal]))
          if (!active()) return
          if (isControlProposal(proposal) && proposal.id === id && proposal.session_id === sid) {
            const next = applyProposal(proposal, sid, revision, expectedEpoch)
            if (recoveringControl && next.phase === 'idle') {
              // A newer GET may append safely. A remaining gap waits for an explicit read, never a polling loop.
              recoveryAttemptRef.current = next.messages
              if (next.controlHistoryNeeded) commit({ type: 'failure', epoch: expectedEpoch, revision, message: '设备结果与对话记录尚未同步，请重新读取对话后再朗读。' })
            }
          } else commit({ type: 'proposal', epoch: expectedEpoch, revision, sid, id, proposal: null })
        } catch (cause) {
          if (!active()) return
          if (cause instanceof ApiError && cause.status === 401) { expire(); return }
          commit({ type: 'proposal', epoch: expectedEpoch, revision, sid, id, proposal: null })
        }
      }))
      return null
    } catch (cause) {
      if (!unchanged()) return undefined
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
    } finally {
      // Includes the proposal GETs: those can repair a missing server-side message.
      if (historyRef.current === controller) historyRef.current = null
    }
  }, [applyProposal, commit, expire, isCurrent, navigate, readProposal, signal])
  useEffect(() => {
    if (state.phase !== 'idle' || !state.controlHistoryNeeded || !state.sid || runningControlsRef.current.size || recoveryAttemptRef.current === state.messages) return
    recoveryAttemptRef.current = state.messages
    void readHistory(state.sid, state.revision, state.epoch, true)
  }, [state.phase, state.controlHistoryNeeded, state.sid, state.revision, state.epoch, state.messages, controlsSettled, readHistory])
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
    voiceRef.current.cancel()
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
    if (current.phase === 'sending') commit({ type: 'controlInterleaved', epoch: expectedEpoch, revision, sid: sid! })
    setControlErrors((old) => ({ ...old, [id]: '' }))
    let mutationStarted = false
    try {
      if (operation === 'reconcile') {
        const latest = await readProposal(id, requestSignal)
        if (!active()) return
        if (!isControlProposal(latest) || latest.id !== id || latest.session_id !== sid) throw new ApiError(502, 'ha_invalid_response')
        applyProposal(latest, sid!, revision, expectedEpoch, true)
        // Local uncertainty may be a lost response to an already settled request.
        // Only a server-persisted unknown record needs the read-only reconcile POST.
        if (latest.status !== 'unknown') return
      }
      mutationStarted = true
      const answer = await requestConsole<unknown>('/control/proposals/' + encodeURIComponent(id) + '/' + operation, { method: 'POST', body: {}, csrfToken: actor.csrf_token, expected: { userId: actor.user.id, csrfToken: actor.csrf_token }, signal: requestSignal })
      if (!active()) return
      if (!isControlProposal(answer) || answer.id !== id || answer.session_id !== sid) throw new ApiError(502, 'ha_invalid_response')
      applyProposal(answer, sid!, revision, expectedEpoch, true)
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
            const answer = await readProposal(id, requestSignal)
            if (active()) {
              if (isControlProposal(answer) && answer.id === id && answer.session_id === sid) applyProposal(answer, sid!, revision, expectedEpoch, true)
              else commit({ type: 'proposal', epoch: expectedEpoch, revision, sid: sid!, id, proposal: null })
            }
          } catch { if (active()) commit({ type: 'proposal', epoch: expectedEpoch, revision, sid: sid!, id, proposal: null }) }
        }
      }
    } finally { runningControls.delete(id); if (active()) setControlsSettled((old) => old + 1) }
  }, [applyProposal, commit, expire, isCurrent, readProposal, signal])

  const send = useCallback(async (event?: FormEvent, override?: string) => {
    event?.preventDefault()
    if (override === undefined && voiceRef.current.phase !== 'idle') return
    const content = (override ?? draft).trim()
    const current = stateRef.current
    if (!content || current.phase !== 'idle' || entryBlockCodeRef.current || !identity?.capabilities.includes('chat:use')) return
    setBudgetNotice(null)
    atBottomRef.current = true
    userScrollRef.current = false
    const retry = retryRequestRef.current
    const request = retry?.content === content ? retry : { id: newChatRequestId(), content, entry }
    const requestId = request.id
    const next = commit({ type: 'send', content, requestId })
    if (next === current) return
    const historyPending = historyRef.current !== null
    historyRef.current?.abort()
    if (next.sid && (historyPending || runningControlsRef.current.size || Object.values(next.proposals).some((proposal) => proposal?.status === 'executing'))) commit({ type: 'controlInterleaved', epoch: next.epoch, revision: next.revision, sid: next.sid })
    if (override === undefined) setDraft('')
    setReconciling(false)
    requestRef.current = request
    setEntry(request.entry)
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
      requestRef.current = request
      retryRequestRef.current = request
      setDraft((prior) => override !== undefined && prior ? `${prior}\n${content}` : (override ?? draft))
      commit({ type: 'sendFailed', epoch: expectedEpoch, revision, unsent: true, message: `消息尚未发送，草稿已保留。${errorText(cause)}` })
      return
    } finally {
      if (handshakeRef.current === handshake) handshakeRef.current = null
    }
    if (!activeIdentity()) { socket.close(1000); return }
    socketRef.current = socket
    commit({ type: 'connected', epoch: expectedEpoch, revision, requestId })
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
      if (frame.type === 'chat_stage') {
        const stage = parseChatStageFrame(frame)
        if (stage) commit({ type: 'stage', epoch: expectedEpoch, revision, sid: stage.session_id, requestId: stage.request_id, stage: stage.stage })
        return
      }
      if (frame.request_id && frame.request_id !== requestId) return
      if (frame.type === 'session' && frame.session_id) {
        const oldSid = stateRef.current.sid
        const updated = commit({ type: 'serverSession', epoch: expectedEpoch, revision, sid: frame.session_id })
        if (updated.sid !== oldSid) {
          if (oldSid !== null) {
            voiceRef.current.cancel()
            controlRef.current.abort()
            controlRef.current = new AbortController()
            runningControlsRef.current = new Set<string>()
            setControlErrors({})
          }
          internalNavigationSidRef.current = updated.sid
          navigate(`/app/chat?sid=${encodeURIComponent(updated.sid ?? '')}`, { replace: true })
        }
      } else if (request.entry === 'chat' && (['device_result', 'control_proposal', 'control_result'].includes(frame.type ?? '')
        || (frame.type === 'response' && (frame.device_result !== undefined || frame.proposal !== undefined)))) {
        if (frame.session_id && frame.session_id !== stateRef.current.sid) return
        // Keep an incompatible current-request payload uncertain, never actionable.
        // Reconciliation may still restore other legitimate historical controls.
        uncertain()
        socket.close(1000)
      } else if (['device_result', 'control_proposal', 'control_result'].includes(frame.type ?? '')) {
        if (frame.session_id !== stateRef.current.sid || frame.request_id !== requestId) return
        if (frame.type === 'device_result' ? !isDeviceResult(frame.device_result) : !isControlProposal(frame.proposal) || frame.proposal.session_id !== frame.session_id || frame.proposal.request_id !== requestId) { uncertain(); return }
        finished = true
        const before = stateRef.current
        const answered = commit({ type: 'structured', epoch: expectedEpoch, revision, sid: frame.session_id!, content: typeof frame.content === 'string' ? frame.content : '', ...messagePresentation(frame), ...(frame.type === 'device_result' ? { device_result: frame.device_result } : { proposal: frame.proposal, ...(isDeviceResult(frame.device_result) ? { device_result: frame.device_result } : {}) }) })
        if (answered.sid && !answered.controlHistoryNeeded && !runningControlsRef.current.size && answered.messages.length === before.messages.length + 1 && frame.content?.trim()) voiceRef.current.onFinal(answered.sid, answered.messages.length - 1, frame.content)
        socketRef.current = null
        socket.close(1000)
        void refreshSessions(expectedEpoch)
      } else if (frame.type === 'response' && typeof frame.content === 'string') {
        if (frame.session_id && frame.session_id !== stateRef.current.sid) return
        finished = true
        const before = stateRef.current
        const answered = commit({ type: 'response', epoch: expectedEpoch, revision, content: frame.content, ...messagePresentation(frame), ...(isDeviceResult(frame.device_result) ? { device_result: frame.device_result } : {}) })
        const pairs = frame.omitted_history_pairs
        if (answered.messages.length === before.messages.length + 1 && typeof pairs === 'number' && Number.isSafeInteger(pairs) && pairs > 0) {
          setBudgetNotice({ epoch: expectedEpoch, revision, sid: answered.sid, pairs })
        }
        if (answered.sid && !answered.controlHistoryNeeded && !runningControlsRef.current.size && answered.messages.length === before.messages.length + 1) voiceRef.current.onFinal(answered.sid, answered.messages.length - 1, frame.content)
        socketRef.current = null
        socket.close(1000)
        void refreshSessions(expectedEpoch)
      } else if (frame.type === 'error') {
        finished = true
        if (frame.code === 'mode_changed') {
          commit({ type: 'sendFailed', epoch: expectedEpoch, revision, message: errorText(new ApiError(0, 'mode_changed')) })
        } else if (frame.code === 'session_not_found' && frame.session_id === stateRef.current.sid) {
          closeActive()
          const cleared = commit({ type: 'notFound', epoch: expectedEpoch, revision, sid: frame.session_id })
          if (cleared.sid === null) { internalNavigationSidRef.current = null; navigate('/app/chat', { replace: true }) }
        } else if (frame.code === 'request_entry_conflict' || frame.code === 'request_entry_limit') {
          entryBlockCodeRef.current = frame.code
          setEntryBlockCode(frame.code)
          commit({ type: 'sendFailed', epoch: expectedEpoch, revision, message: errorText(new ApiError(0, frame.code)) })
        } else if (frame.outcome === 'failed') {
          requestRef.current = null
          commit({ type: 'sendFailed', epoch: expectedEpoch, revision, message: errorText(new ApiError(0, typeof frame.code === 'string' ? frame.code : 'unknown')) })
        } else {
          commit({ type: 'unknown', epoch: expectedEpoch, revision, message: errorText(new ApiError(0, typeof frame.code === 'string' ? frame.code : 'unknown')) })
          reconcileUnknown(stateRef.current.sid, revision, expectedEpoch)
        }
        socketRef.current = null
        socket.close(1000)
      }
    }
    socket.onerror = uncertain
    socket.onclose = uncertain
    try { socket.send(JSON.stringify({ session_id: requestedSid, request_id: requestId, content, entry: request.entry })) }
    catch { uncertain(); socket.close(1000) }
  }, [commit, draft, entry, identity, signal, isCurrent, expire, navigate, closeActive, reconcileUnknown, refreshSessions])
  sendVoiceRef.current = (text) => { void send(undefined, text) }
  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (mobile || event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing || composingRef.current) return
    event.preventDefault()
    void send()
  }
  const proposalRefresh = useProposalRefresh({
    epoch, revision: state.revision, sid: state.sid, proposals: state.proposals, signal, readProposal,
    isCurrent: () => isCurrent(epoch) && stateRef.current.epoch === epoch && stateRef.current.revision === state.revision && stateRef.current.sid === state.sid,
    onProposal: (proposal) => applyProposal(proposal, state.sid!, state.revision, epoch, true),
    onUnauthorized: expire,
    onUnavailable: (id) => commit({ type: 'proposal', epoch, revision: state.revision, sid: state.sid!, id, proposal: null }),
  })
  const canChat = identity?.capabilities.includes('chat:use') ?? false
  const canRead = identity?.capabilities.includes('sessions:read') ?? false
  const canControl = identity?.capabilities.includes('devices:control') ?? false
  const visibleState = state.epoch === epoch ? state : initialChatState(epoch)
  const visibleSessions = state.epoch === epoch ? sessions : []
  const entryBlockMessage = state.epoch === epoch && entryBlockCode ? errorText(new ApiError(0, entryBlockCode)) : null
  const renderedProposalIDs = new Set<string>()
  return <PageFrame className="chat-page">
    <PageHeader title={canChat ? '和家序聊聊' : '对话记录'} description={canChat ? '你的想法，只留在你的对话里。' : '查看属于你的历史对话。'} actions={canChat && <Button onClick={() => selectSession(null)}><Icon name="plus"/>新对话</Button>}/>

    {mobile && <Button type="button" variant="secondary" className="mobile-history-toggle" onClick={() => setShowHistory((old) => !old)}><Icon name="clock"/>{showHistory ? '返回当前对话' : '查看对话列表'}</Button>}<div className="chat-layout">
      <aside className="chat-history" aria-label="最近对话" hidden={mobile && !showHistory}><div className="chat-history-title"><h2>最近对话</h2><IconButton variant="ghost" label="刷新对话列表" onClick={() => void refreshSessions(epoch)}><Icon name="refresh"/></IconButton></div>
        {listError && <Alert tone="danger" live="assertive">{listError}</Alert>}
        {!canRead ? <p className="chat-muted">历史记录当前不可用。</p> : visibleSessions.length === 0 ? <p className="chat-muted">{canChat ? '还没有对话。发一条消息，开始记录。' : '还没有对话记录。'}</p> : <ul>{visibleSessions.map((item) => <li key={item.id}><Button variant="ghost" type="button" className={`chat-history-item ${visibleState.sid === item.id ? 'selected' : ''}`} onClick={() => selectSession(item.id)} aria-current={visibleState.sid === item.id ? 'true' : undefined}><span>{item.preview || '未命名对话'}</span><small>{new Date(item.last_active_at).toLocaleDateString('zh-CN')} · {item.message_count} 条消息</small></Button></li>)}</ul>}
      </aside>
      <section className="chat-conversation" aria-label="当前对话" hidden={mobile && showHistory}>
        <div className="chat-conversation-head"><strong>{visibleState.sid ? '当前对话' : canChat ? '新的对话' : '选择对话'}</strong><span>仅自己可见</span></div>
        <div className="chat-transcript" role="log" aria-live="polite" aria-label="对话消息" tabIndex={0} ref={transcriptRef} onWheel={() => { userScrollRef.current = true }} onTouchStart={() => { userScrollRef.current = true }} onPointerDown={() => { userScrollRef.current = true }} onKeyDown={() => { userScrollRef.current = true }} onScroll={(event) => {
          if (!userScrollRef.current) return
          const area = event.currentTarget
          atBottomRef.current = area.scrollHeight - area.scrollTop - area.clientHeight < 96
        }}>
          {visibleState.messages.length === 0 && visibleState.phase !== 'loading' && <EmptyState icon="chat" title={canChat ? '今天想聊些什么？' : '选择一条对话'} description={canChat ? '可以问问题，也可以把一件小事讲给我听。' : '从最近对话中选择一条，阅读历史记录。'}/>}
          {visibleState.phase === 'loading' && <p className="chat-status" role="status">正在读取对话…</p>}
          {visibleState.messages.map((message, index) => <article key={`${index}-${message.timestamp}`} className={`chat-bubble ${message.role === 'user' ? 'from-user' : 'from-assistant'}`}>
            <div className="chat-role"><Avatar label={message.role === 'user' ? '你' : '家序'}/><strong>{message.role === 'user' ? '你' : '家序'}</strong></div>
            <div className="chat-markdown"><SafeMarkdown>{message.content}</SafeMarkdown></div>
            {canChat && message.role === 'assistant' && message.content.trim() && visibleState.sid && <Button type="button" variant="ghost" disabled={visibleState.phase !== 'idle' || visibleState.controlHistoryNeeded || voice.phase !== 'idle' || !voice.available} onClick={() => void voice.speak(visibleState.sid!, index, message.content)}>朗读回答</Button>}
            {message.device_result && <DeviceResultCard result={message.device_result}/>}
            {(message.attachments ?? []).filter((attachment) => {
              if (attachment.kind !== 'control_proposal' || renderedProposalIDs.has(attachment.proposal_id)) return false
              renderedProposalIDs.add(attachment.proposal_id)
              return true
            }).map((attachment) => {
              const proposal = visibleState.proposals[attachment.proposal_id]
              return proposal
                ? <ControlCard key={attachment.proposal_id} proposal={proposal} canControl={canControl} error={controlErrors[proposal.id]} refreshError={proposalRefresh.errors[proposal.id] || (proposalRefresh.staleIds.has(proposal.id) ? '暂未取得最新结果，请刷新状态。' : undefined)} onRefresh={proposalRefresh.refresh}
                    onConfirm={(id) => runControl(id, 'confirm')} onCancel={(id) => runControl(id, 'cancel')} onReconcile={(id) => runControl(id, 'reconcile')}/>
                : <p className="control-card control-unavailable" key={attachment.proposal_id} role="status">{proposal === null ? '此设备操作记录暂不可用。' : '正在读取设备操作记录…'}</p>
            })}
          </article>)}
          {visibleState.phase === 'sending' && <p className="chat-status" role="status">{visibleState.stage ? chatStageText[visibleState.stage] : visibleState.connected ? '正在等待服务器' : '正在连接'}</p>}
        </div>
        {entryBlockMessage && <Alert tone="warning" live="assertive">{entryBlockMessage}</Alert>}
        {visibleState.error && visibleState.error !== entryBlockMessage && <Alert tone="warning" live="assertive"><span>{visibleState.error}{visibleState.recoveryError && <small className="chat-recovery-error">核对失败：{visibleState.recoveryError}</small>}</span>{visibleState.phase === 'unknown' && <div>{reconciling ? <span>正在核对历史…</span> : <><Button type="button" onClick={() => { const current = stateRef.current; reconcileUnknown(current.sid, current.revision, current.epoch) }}>再次核对历史</Button>{visibleState.recovered && <>{visibleState.sid && <Button type="button" onClick={() => commit({ type: 'continue' })}>继续对话，不重发</Button>}<Button type="button" onClick={() => { const pending = stateRef.current.pending; if (pending) { retryRequestRef.current = requestRef.current; setDraft(pending); commit({ type: 'allowRetry' }) } }}>重试上一条</Button></>}</>}</div>}</Alert>}
        {visibleState.phase === 'idle' && visibleState.controlHistoryNeeded && visibleState.error && visibleState.sid && <Button type="button" variant="secondary" onClick={() => void readHistory(visibleState.sid!, visibleState.revision, visibleState.epoch, true)}>重新读取对话</Button>}
        {canChat && <VoiceControls voice={voice} disabled={visibleState.phase !== 'idle' || entryBlocked}/>}
        {budgetNotice && budgetNotice.epoch === epoch && budgetNotice.revision === visibleState.revision && budgetNotice.sid === visibleState.sid && visibleState.phase === 'idle' && <div role="status">本轮未包含较早的 {budgetNotice.pairs} 轮对话</div>}
        {!canChat ? <div className="chat-unavailable" role="status">当前仅可阅读历史记录。</div> : <form className="chat-composer" onSubmit={send}><Field id="chat-entry" label="聊天入口" hint="普通聊天只回答问题，不发起设备操作"><Select id="chat-entry" value={state.epoch === epoch ? entry : 'auto'} onChange={(event) => { if (stateRef.current.phase === 'idle' && !entryBlockCodeRef.current) setEntry(event.target.value as ChatEntry) }} aria-describedby="chat-entry-hint" disabled={visibleState.phase !== 'idle' || entryBlocked || voice.phase !== 'idle'}><option value="auto">自动识别</option><option value="chat">普通聊天</option></Select></Field><Field id="chat-draft" label="消息内容" hint={mobile ? 'Enter 换行 · 点击发送' : 'Enter 发送 · Shift + Enter 换行'}><Textarea id="chat-draft" aria-label="消息内容" value={state.epoch === epoch ? draft : ''} onChange={(event) => setDraft(event.target.value)} onKeyDown={onComposerKeyDown} onCompositionStart={() => { composingRef.current = true }} onCompositionEnd={() => { composingRef.current = false }} placeholder="写下你想说的…" aria-describedby="chat-draft-hint" rows={3} disabled={visibleState.phase === 'sending' || visibleState.phase === 'unknown' || entryBlocked}/></Field><div className="chat-composer-bottom"><Button type="submit" aria-label="发送消息" disabled={visibleState.phase !== 'idle' || entryBlocked || voice.phase !== 'idle' || !draft.trim()}>发送 <Icon name="send"/></Button></div></form>}
      </section>
    </div>
  </PageFrame>
}
