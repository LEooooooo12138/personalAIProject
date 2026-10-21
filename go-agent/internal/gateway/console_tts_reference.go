package gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/speech"
)

const voiceReferenceUploadLimit = 6 << 20

func (s *Server) handleVoiceReferenceGet(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	metadata, err := s.assistantConfig.VoiceReference()
	if err != nil {
		assistantError(c, err)
		return
	}
	c.JSON(200, metadata)
}

func (s *Server) handleVoiceReferencePut(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	var req struct {
		Filename         string `json:"filename"`
		AudioBase64      string `json:"audio_base64"`
		ExpectedRevision string `json:"expected_revision"`
	}
	if !decodeAssistantJSON(c, &req, int64(base64.StdEncoding.EncodedLen(voiceReferenceUploadLimit)+4096)) {
		return
	}
	if len(req.Filename) > 255 || strings.ContainsAny(req.Filename, "/\\\x00:") || len(req.AudioBase64) > base64.StdEncoding.EncodedLen(voiceReferenceUploadLimit) {
		assistantError(c, speech.ErrInvalid)
		return
	}
	mimeType := ""
	switch strings.ToLower(filepath.Ext(req.Filename)) {
	case ".wav":
		mimeType = "audio/wav"
	case ".mp3":
		mimeType = "audio/mpeg"
	default:
		assistantError(c, speech.ErrInvalid)
		return
	}
	audio, err := base64.StdEncoding.Strict().DecodeString(req.AudioBase64)
	if err != nil || len(audio) == 0 || len(audio) > voiceReferenceUploadLimit {
		assistantError(c, speech.ErrInvalid)
		return
	}
	defer clear(audio)
	// Decode before taking the settings/auth locks; permission is checked again
	// immediately before storage, after the bounded local decoder finishes.
	normalized, err := speech.NormalizeVoiceReference(c.Request.Context(), audio, mimeType, "")
	if err != nil {
		if errors.Is(err, speech.ErrUnavailable) && mimeType == "audio/mpeg" {
			consoleError(c, 503, "voice_reference_decoder_unavailable", "MP3 decoder unavailable; use PCM16 WAV")
		} else {
			assistantError(c, err)
		}
		return
	}
	defer clear(normalized)
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	var metadata assistantconfig.VoiceReference
	err = s.consoleStore.WithSession(c.Request.Context(), consoleToken(c.Request), func(context.Context) error {
		var err error
		metadata, err = s.assistantConfig.SaveVoiceReference(normalized, req.ExpectedRevision)
		return err
	})
	if err != nil {
		assistantError(c, err)
		return
	}
	if s.speechJobs != nil {
		s.speechJobs.CancelAll()
	}
	c.JSON(200, metadata)
}

func (s *Server) handleVoiceReferenceRemove(c *gin.Context) {
	if !s.assistantReady(c) {
		return
	}
	var req struct {
		ExpectedRevision string `json:"expected_revision"`
	}
	if !decodeAssistantJSON(c, &req, 4096) {
		return
	}
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	var metadata assistantconfig.VoiceReference
	err := s.consoleStore.WithSession(c.Request.Context(), consoleToken(c.Request), func(context.Context) error {
		var err error
		metadata, err = s.assistantConfig.RemoveVoiceReference(req.ExpectedRevision)
		return err
	})
	if err != nil {
		assistantError(c, err)
		return
	}
	if s.speechJobs != nil {
		s.speechJobs.CancelAll()
	}
	c.JSON(200, metadata)
}
