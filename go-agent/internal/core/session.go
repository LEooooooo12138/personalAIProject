package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ── Session State Machine ──
//
// 会话有三种状态，像红绿灯一样流转：
//
//   Active ──(超时 或 达到20轮)──▶ Ending ──(记忆沉淀完成)──▶ Closed
//
// - Active:  正在对话，可以收发消息
// - Ending:  对话结束，正在执行记忆沉淀（生成摘要、去重、保存）
// - Closed:  沉淀完成，会话资源可以被清理

// SessionState represents where a session is in its lifecycle.
type SessionState string

const (
	SessionActive SessionState = "active"
	SessionEnding SessionState = "ending"
	SessionClosed SessionState = "closed"
)

// ── Session ──

// Session is a single conversation between a user and the agent on one channel.
// 每个 Session 记录了一段完整的对话：谁、在哪个通道、聊了什么、聊了多久。
type Session struct {
	ID           string
	ChannelID    string       // 通道标识：internal / qclaw / webchat
	UserID       string       // 用户标识（微信 OpenID 或网页 session ID）
	State        SessionState
	Messages     []Message    // 对话历史，按时间顺序排列
	RoundCount   int          // 已对话轮数（一问一答算一轮）
	StartedAt    time.Time    // 会话开始时间
	LastActiveAt time.Time    // 最后一条消息的时间
	CreatedAt    time.Time

	mu sync.RWMutex // 保护并发访问
}

// Message is a single turn in a conversation.
// 一条消息 = 谁说的 + 说了什么 + 什么时候说的
type Message struct {
	Role      string    `json:"role"`    // "user" 或 "assistant"
	Content   string    `json:"content"` // 消息正文
	Timestamp time.Time `json:"timestamp"`
}

// ── Session Key ──
// 用 "channelID:userID" 作为唯一标识，保证同一个用户在不同通道有独立会话

func sessionKey(channelID, userID string) string {
	return channelID + ":" + userID
}

// ── Session Manager ──

// SessionConfig controls session lifecycle parameters.
// 这些参数控制会话的"寿命"：

// DefaultSessionConfig returns sensible defaults.
// 默认值：20 轮对话、30 分钟超时、每分钟扫描一次、最多 100 个并发会话
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		MaxRounds:    20,
		IdleTimeout:  30 * time.Minute,
		ScanInterval: 1 * time.Minute,
		MaxSessions:  100,
	}
}

// SessionEndCallback is called when a session transitions to Ending state.
// 当会话结束时，这个回调会被调用，用于触发记忆沉淀流程。
// 回调接收的是会话的深拷贝，外部修改不影响原数据。
type SessionEndCallback func(session *Session)

// SessionManager owns all active sessions and manages their lifecycle.
// 会话管理器：保管所有正在进行的对话，定时检查是否有过期会话。
// 就像一个"对话管家"——谁在聊天、聊了多久、该不该结束了，都由它管。
type SessionManager struct {
	cfg       SessionConfig
	logger    *zap.Logger
	sessions  map[string]*Session // key → session
	mu        sync.RWMutex
	endCh    chan *Session        // 通知外部：有会话结束了

	ctx    context.Context
	cancel context.CancelFunc
}

// NewSessionManager creates a session manager with the given config.
// 创建一个新的会话管理器。cfg 是配置，logger 是日志记录器。
func NewSessionManager(cfg SessionConfig, logger *zap.Logger) *SessionManager {
	return &SessionManager{
		cfg:      cfg,
		logger:   logger,
		sessions: make(map[string]*Session),
		endCh:    make(chan *Session, 50), // 缓冲 50 个结束通知
	}
}

// Start begins the background session scanner.
// 启动后台扫描器：每隔一段时间检查是否有会话过期。
// 必须在 Start 之后，会话的超时检测才会工作。
func (m *SessionManager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)
	go m.scanLoop()
	m.logger.Info("session manager started",
		zap.Duration("idle_timeout", m.cfg.IdleTimeout),
		zap.Duration("scan_interval", m.cfg.ScanInterval),
	)
}

// Stop gracefully shuts down the session manager.
// 停止会话管理器：关闭所有会话、停止扫描器。
func (m *SessionManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Close all remaining sessions.
	for _, s := range m.sessions {
		s.mu.Lock()
		s.State = SessionClosed
		s.mu.Unlock()
	}
	m.sessions = make(map[string]*Session)
	m.logger.Info("session manager stopped")
}

// EndChan returns the channel that receives ended sessions.
// 返回一个只读 channel，当会话结束时，可以从这里读到结束的会话。
// 外部（记忆沉淀模块）监听这个 channel 来处理结束的会话。
func (m *SessionManager) EndChan() <-chan *Session {
	return m.endCh
}

// ── Session Operations ──

// GetOrCreate returns an existing session or creates a new one.
// 获取或创建会话：如果该用户在该通道已有活跃会话，直接返回；
// 否则创建一个新的。这是外部和会话管理器交互的主入口。
//
// 使用的技术：Go 的 map + 读写锁（sync.RWMutex），
//           RLock（读锁）允许多个 goroutine 同时读，
//           Lock（写锁）只允许一个 goroutine 写。
//           这样在绝大多数"获取已有会话"的场景下性能很好。
func (m *SessionManager) GetOrCreate(channelID, userID string) (*Session, error) {
	key := sessionKey(channelID, userID)

	// 先用读锁快速查找
	m.mu.RLock()
	if s, ok := m.sessions[key]; ok {
		// 检查会话是否仍然活跃
		s.mu.RLock()
		active := s.State == SessionActive
		s.mu.RUnlock()

		if active {
			m.mu.RUnlock()
			return s, nil
		}
	}
	m.mu.RUnlock()

	// 不存在或不活跃 → 用写锁创建新的
	m.mu.Lock()
	defer m.mu.Unlock()

	// 双重检查：可能在等待写锁期间已被其他 goroutine 创建
	if s, ok := m.sessions[key]; ok {
		s.mu.RLock()
		active := s.State == SessionActive
		s.mu.RUnlock()
		if active {
			return s, nil
		}
	}

	// 检查会话数上限
	if len(m.sessions) >= m.cfg.MaxSessions {
		return nil, fmt.Errorf("session limit reached (%d)", m.cfg.MaxSessions)
	}

	now := time.Now()
	s := &Session{
		ID:           key,
		ChannelID:    channelID,
		UserID:       userID,
		State:        SessionActive,
		Messages:     make([]Message, 0, m.cfg.MaxRounds*2), // 预分配容量
		RoundCount:   0,
		StartedAt:    now,
		LastActiveAt: now,
		CreatedAt:    now,
	}

	m.sessions[key] = s
	m.logger.Debug("session created",
		zap.String("session_id", s.ID),
		zap.String("channel", channelID),
	)
	return s, nil
}

// AddMessage appends a message to the session and bumps the activity timestamp.
// 向会话中添加一条消息（用户说的或 AI 回的）。
// 同时更新"最后活跃时间"和对话轮数。
//
// 返回值 tells 调用方该会话是否应该结束了。
// 如果返回 false，说明已达到轮数上限，调用方应该触发会话结束流程。
func (m *SessionManager) AddMessage(s *Session, msg Message) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.State != SessionActive {
		return false
	}

	s.Messages = append(s.Messages, msg)
	s.LastActiveAt = time.Now()

	// 只有用户消息计数（一轮 = 用户说 + AI 回）
	if msg.Role == "user" {
		s.RoundCount++
	}

	// 检查是否达到轮数上限
	if s.RoundCount >= m.cfg.MaxRounds {
		m.logger.Debug("session round limit reached",
			zap.String("session_id", s.ID),
			zap.Int("rounds", s.RoundCount),
		)
		return false // 应该结束
	}
	return true
}

// EndSession marks a session as ending and sends it to the end channel.
// 手动结束一个会话：将其状态设为 Ending，并发送到结束 channel。
// 记忆沉淀模块会从 EndChan 收到这个会话并处理。
func (m *SessionManager) EndSession(s *Session) {
	s.mu.Lock()
	if s.State != SessionActive {
		s.mu.Unlock()
		return
	}
	s.State = SessionEnding
	s.mu.Unlock()

	m.logger.Info("session ending",
		zap.String("session_id", s.ID),
		zap.Int("rounds", s.RoundCount),
		zap.Int("messages", len(s.Messages)),
	)

	// 非阻塞发送：如果 channel 满了，用 goroutine 异步发送
	select {
	case m.endCh <- s:
	default:
		go func() { m.endCh <- s }()
	}
}

// CompleteSession marks a session as closed (after memory sedimentation).
// 标记会话完成：记忆沉淀处理完后调用，清理会话状态。
func (m *SessionManager) CompleteSession(s *Session) {
	s.mu.Lock()
	s.State = SessionClosed
	s.mu.Unlock()

	m.logger.Debug("session closed", zap.String("session_id", s.ID))
}

// ── Background Scanner ──

// scanLoop periodically checks for expired sessions.
// 后台扫描循环：每隔 ScanInterval 检查一遍所有会话，
// 把空闲超时的会话标记为 Ending。
//
// 用到的技术：time.Ticker —— 一个定时器，每隔固定时间发送一个信号。
//           在 Go 中，Ticker 比 time.Sleep + 循环更优雅，
//           因为它可以被 ctx.Done() 干净地中断。
func (m *SessionManager) scanLoop() {
	ticker := time.NewTicker(m.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.scanExpired()
		}
	}
}

// scanExpired checks all sessions and ends those that have timed out.
// 扫描所有活跃会话，把空闲超时的标记为结束。
func (m *SessionManager) scanExpired() {
	m.mu.RLock()
	// 用快照避免长时间持有锁
	snapshot := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		snapshot = append(snapshot, s)
	}
	m.mu.RUnlock()

	now := time.Now()
	for _, s := range snapshot {
		s.mu.RLock()
		active := s.State == SessionActive
		idle := now.Sub(s.LastActiveAt)
		s.mu.RUnlock()

		if active && idle > m.cfg.IdleTimeout {
			m.logger.Info("session idle timeout",
				zap.String("session_id", s.ID),
				zap.Duration("idle", idle),
			)
			m.EndSession(s)
		}
	}
}


// Mu returns the internal mutex for external coordination (used by SessionStore).
func (m *SessionManager) Mu() *sync.RWMutex {
	return &m.mu
}

// Sessions returns the sessions map for external iteration (used by SessionStore).
func (m *SessionManager) Sessions() map[string]*Session {
	return m.sessions
}

// ── Helpers ──

// ActiveCount returns the number of currently active sessions.
// 返回当前活跃会话的数量（用于监控/调试）。
func (m *SessionManager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, s := range m.sessions {
		s.mu.RLock()
		if s.State == SessionActive {
			count++
		}
		s.mu.RUnlock()
	}
	return count
}

// CloneSession returns a deep copy of the session (safe for external use).
// 深拷贝会话数据，用于在记忆沉淀时安全地读取，不担心并发修改。
func CloneSession(s *Session) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clone := &Session{
		ID:           s.ID,
		ChannelID:    s.ChannelID,
		UserID:       s.UserID,
		State:        s.State,
		Messages:     make([]Message, len(s.Messages)),
		RoundCount:   s.RoundCount,
		StartedAt:    s.StartedAt,
		LastActiveAt: s.LastActiveAt,
		CreatedAt:    s.CreatedAt,
	}
	copy(clone.Messages, s.Messages)
	return clone
}
