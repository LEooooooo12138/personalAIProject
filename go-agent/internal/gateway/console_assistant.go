package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/speech"
)

func (s *Server) setupAssistantRoutes(g *gin.RouterGroup) {
	g.GET("/admin/assistant", s.handleAssistantGet)
	g.PUT("/admin/assistant", s.handleAssistantPut)
	g.GET("/admin/personality", s.handlePersonalityGet)
	g.PUT("/admin/personality", s.handlePersonalityPut)
	g.GET("/admin/tts-reference", s.handleVoiceReferenceGet)
	g.PUT("/admin/tts-reference", s.handleVoiceReferencePut)
	g.POST("/admin/tts-reference/remove", s.handleVoiceReferenceRemove)
	g.GET("/speech/status", s.handleSpeechStatus)
	g.POST("/speech/jobs", s.handleSpeechStart)
	g.GET("/speech/jobs/:id", s.handleSpeechGet)
	g.POST("/speech/jobs/:id/cancel", s.handleSpeechCancel)
}
func (s *Server) assistantReady(c *gin.Context) bool {
	if s.assistantConfig == nil {
		consoleError(c, 503, "unavailable", "Assistant settings unavailable")
		return false
	}
	return true
}
func assistantError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, assistantconfig.ErrEndpointMismatch):
		consoleError(c, 400, "mimo_endpoint_mismatch", "MiMo key and endpoint do not match")
	case errors.Is(err, assistantconfig.ErrConflict):
		consoleError(c, 409, "revision_conflict", "Settings changed; reload and retry")
	case errors.Is(err, assistantconfig.ErrLocalASRURLInvalid):
		consoleError(c, 400, "local_asr_url_invalid", "Invalid local ASR service URL")
	case errors.Is(err, assistantconfig.ErrLocalTTSURLInvalid):
		consoleError(c, 400, "local_tts_url_invalid", "Invalid local TTS service URL")
	case errors.Is(err, assistantconfig.ErrInvalid), errors.Is(err, speech.ErrInvalid):
		consoleError(c, 400, "invalid_request", "Invalid settings or speech input")
	case errors.Is(err, speech.ErrVoiceReferenceRequired):
		consoleError(c, 409, "voice_reference_required", "Upload a voice reference before using voice cloning")
	case errors.Is(err, speech.ErrNotFound):
		consoleError(c, 404, "not_found", "Not found")
	case errors.Is(err, speech.ErrBusy):
		consoleError(c, 429, "speech_busy", "Speech service is busy")
	default:
		consoleError(c, 503, "unavailable", "Assistant service unavailable")
	}
}
func decodeAssistantJSON(c *gin.Context, dst any, limit int64) bool {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
	if err != nil || !utf8.Valid(data) {
		consoleError(c, 400, "invalid_request", "Invalid request body")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(dst) != nil || dec.Decode(new(any)) != io.EOF {
		consoleError(c, 400, "invalid_request", "Invalid request body")
		return false
	}
	return true
}
func (s *Server) handleAssistantGet(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	v, err := s.assistantConfig.Get()
	if err != nil {
		assistantError(c, err)
		return
	}
	c.JSON(200, v)
}
func (s *Server) handleAssistantPut(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	var req assistantconfig.Update
	if !decodeAssistantJSON(c, &req, 16384) {
		return
	}
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	var v assistantconfig.Settings
	err := s.consoleStore.WithSession(c.Request.Context(), consoleToken(c.Request), func(context.Context) error { var err error; v, err = s.assistantConfig.Update(req); return err })
	if err != nil {
		assistantError(c, err)
		return
	}
	if s.speechJobs != nil {
		s.speechJobs.CancelAll()
	}
	c.JSON(200, v)
}
func (s *Server) handlePersonalityGet(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	v, err := s.assistantConfig.Personality()
	if err != nil {
		assistantError(c, err)
		return
	}
	c.JSON(200, v)
}
func (s *Server) handlePersonalityPut(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	var req struct {
		Filename         string `json:"filename"`
		Content          string `json:"content"`
		ExpectedRevision string `json:"expected_revision"`
	}
	if !decodeAssistantJSON(c, &req, 2*1024*1024) {
		return
	}
	var v assistantconfig.Personality
	err := s.consoleStore.WithSession(c.Request.Context(), consoleToken(c.Request), func(context.Context) error {
		var err error
		v, err = s.assistantConfig.SavePersonality(req.Filename, req.Content, req.ExpectedRevision)
		return err
	})
	if err != nil {
		assistantError(c, err)
		return
	}
	c.JSON(200, v)
}
func (s *Server) handleSpeechStatus(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	v, err := s.assistantConfig.Get()
	if err != nil {
		assistantError(c, err)
		return
	}
	reference, referenceErr := s.assistantConfig.VoiceReference()
	if referenceErr != nil && v.TTSMode != assistantconfig.Local && v.TTSModel == "mimo-v2.5-tts-voiceclone" {
		assistantError(c, referenceErr)
		return
	}
	c.JSON(200, gin.H{"tts_model": v.TTSModel, "tts_voice": v.TTSVoice, "voice_reference_configured": referenceErr == nil && reference.Exists, "asr_mode": v.ASRMode, "tts_mode": v.TTSMode, "cloud_configured": v.KeyConfigured, "local_asr_configured": v.LocalASRURL != "", "local_tts_configured": v.LocalTTSURL != ""})
}
func (s *Server) handleSpeechStart(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	if s.speechJobs == nil || s.sessionStore == nil {
		assistantError(c, speech.ErrUnavailable)
		return
	}
	var req struct {
		Kind            string `json:"kind"`
		AudioBase64     string `json:"audio_base64"`
		MIMEType        string `json:"mime_type"`
		SessionID       string `json:"session_id"`
		MessageIndex    *int   `json:"message_index"`
		ContentRevision string `json:"content_revision"`
	}
	if !decodeAssistantJSON(c, &req, 12*1024*1024) {
		return
	}
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	v, key, err := s.assistantConfig.Snapshot()
	if err != nil {
		assistantError(c, err)
		return
	}
	cfg := speech.Config{MiMoEndpoint: v.MiMoEndpoint, ASRMode: string(v.ASRMode), TTSMode: string(v.TTSMode), TTSModel: v.TTSModel, TTSVoice: v.TTSVoice, APIKey: key, LocalASRURL: v.LocalASRURL, LocalTTSURL: v.LocalTTSURL}
	owner := "console:" + requestConsolePrincipal(c.Request).UserID
	jobReq := speech.Request{Kind: req.Kind, AudioBase64: req.AudioBase64, MIMEType: req.MIMEType}
	if req.Kind == "synthesize" {
		if req.SessionID == "" || req.MessageIndex == nil || *req.MessageIndex < 0 || req.AudioBase64 != "" || req.MIMEType != "" {
			assistantError(c, speech.ErrInvalid)
			return
		}
		messages, err := s.sessionStore.OwnedMessages("console", req.SessionID, owner)
		if err != nil || *req.MessageIndex >= len(messages) {
			assistantError(c, speech.ErrNotFound)
			return
		}
		msg := messages[*req.MessageIndex]
		if msg.Role != "assistant" {
			assistantError(c, speech.ErrInvalid)
			return
		}
		hash := sha256.Sum256([]byte(msg.Content))
		if req.ContentRevision != hex.EncodeToString(hash[:]) {
			consoleError(c, 409, "revision_conflict", "Message changed")
			return
		}
		localRequired := core.MessageRequiresLocalSpeech(msg)
		// Older saved local replies may lack inherited provenance. Only earlier
		// assistant messages can have supplied sensitive context to this reply.
		if !localRequired && msg.AssistantMode == "local" {
			for _, previous := range messages[:*req.MessageIndex] {
				if previous.Role == "assistant" && core.MessageRequiresLocalSpeech(previous) {
					localRequired = true
					break
				}
			}
		}
		if localRequired {
			cfg.TTSMode = "local"
			cfg.APIKey = ""
		}
		if cfg.TTSMode == "local" && cfg.LocalTTSURL == "" {
			if localRequired {
				consoleError(c, 409, "speech_local_required", "This reply requires local speech; no local speech service is configured")
			} else {
				consoleError(c, 409, "speech_local_unconfigured", "Local speech is selected but no local speech service is configured")
			}
			return
		}
		if cfg.TTSMode != "local" && cfg.TTSModel == "mimo-v2.5-tts-voiceclone" {
			cfg.VoiceReferenceWAV, err = s.assistantConfig.VoiceReferenceContent()
			if err != nil {
				assistantError(c, err)
				return
			}
			defer clear(cfg.VoiceReferenceWAV)
			if cfg.TTSMode == "cloud" && len(cfg.VoiceReferenceWAV) == 0 {
				assistantError(c, speech.ErrVoiceReferenceRequired)
				return
			}
		}
		jobReq.Text = speechText(msg.Content)
	} else if req.Kind == "transcribe" {
		if req.MessageIndex != nil || req.ContentRevision != "" {
			assistantError(c, speech.ErrInvalid)
			return
		}
		if req.SessionID != "" {
			if _, err := s.sessionStore.OwnedMessages("console", req.SessionID, owner); err != nil {
				assistantError(c, speech.ErrNotFound)
				return
			}
		}
	} else {
		assistantError(c, speech.ErrInvalid)
		return
	}
	var job speech.Job
	err = s.consoleStore.WithSession(c.Request.Context(), consoleToken(c.Request), func(context.Context) error {
		var err error
		job, err = s.speechJobs.Start(owner, cfg, jobReq)
		return err
	})
	if err != nil {
		assistantError(c, err)
		return
	}
	c.JSON(202, job)
}
func (s *Server) handleSpeechGet(c *gin.Context) {
	if s.speechJobs == nil {
		assistantError(c, speech.ErrUnavailable)
		return
	}
	v, err := s.speechJobs.Get("console:"+requestConsolePrincipal(c.Request).UserID, c.Param("id"))
	if err != nil {
		assistantError(c, err)
		return
	}
	c.JSON(200, v)
}
func (s *Server) handleSpeechCancel(c *gin.Context) {
	if s.speechJobs == nil {
		assistantError(c, speech.ErrUnavailable)
		return
	}
	if err := s.speechJobs.Cancel("console:"+requestConsolePrincipal(c.Request).UserID, c.Param("id")); err != nil {
		assistantError(c, err)
		return
	}
	v, err := s.speechJobs.Get("console:"+requestConsolePrincipal(c.Request).UserID, c.Param("id"))
	if err != nil {
		assistantError(c, err)
		return
	}
	c.JSON(200, v)
}

var speechCode = regexp.MustCompile("(?s)```.*?```")
var speechLinks = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)

func speechText(content string) string {
	content = speechCode.ReplaceAllString(content, "")
	content = speechLinks.ReplaceAllString(content, "$1")
	content = strings.Map(func(r rune) rune {
		if strings.ContainsRune("#*_`>|", r) {
			return ' '
		}
		return r
	}, content)
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) > 1000 {
		content = string(runes[:1000])
	}
	return content
}
