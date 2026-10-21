package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig/voiceaudio"
	"mime"
	"os/exec"
	"strings"
	"time"
)

func validateWAV(b []byte, maxSeconds int) error {
	if voiceaudio.ValidateWAV(b, maxSeconds, maxAudio) != nil {
		return ErrInvalid
	}
	return nil
}

func normalizeAudio(ctx context.Context, b []byte, mimeType, ffmpeg string) ([]byte, error) {
	if len(b) == 0 || len(b) > maxAudio {
		return nil, ErrInvalid
	}
	media, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return nil, ErrInvalid
	}
	media = strings.ToLower(media)
	if media == "audio/wav" || media == "audio/x-wav" || media == "audio/wave" {
		if err := validateWAV(b, 60); err != nil {
			return nil, err
		}
		return b, nil
	}
	format := ""
	switch media {
	case "audio/webm", "video/webm":
		if len(b) >= 4 && bytes.Equal(b[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
			format = "matroska"
		}
	case "audio/ogg", "application/ogg":
		if len(b) >= 4 && string(b[:4]) == "OggS" {
			format = "ogg"
		}
	case "audio/mp4", "video/mp4":
		if len(b) >= 8 && string(b[4:8]) == "ftyp" {
			format = "mov"
		}
	case "audio/mpeg", "audio/mp3":
		if len(b) >= 3 && (string(b[:3]) == "ID3" || b[0] == 0xff && b[1]&0xe0 == 0xe0) {
			format = "mp3"
		}
	}
	if format == "" {
		return nil, ErrInvalid
	}
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	path, err := exec.LookPath(ffmpeg)
	if err != nil {
		return nil, ErrUnavailable
	}
	convertCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Only pipe input is allowed. Demuxer selection prevents a crafted playlist
	// from requesting files or network resources. Decode one extra second so a
	// too-long recording is rejected instead of silently truncated to 60 sec.
	cmd := exec.CommandContext(convertCtx, path, "-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-f", format, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-t", "61", "-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le", "-f", "s16le", "pipe:1")
	cmd.Stdin = bytes.NewReader(b)
	cmd.WaitDelay = time.Second
	output := &boundedOutput{limit: 61 * 32000, cancel: cancel}
	cmd.Stdout = output
	// Stderr is discarded: decoder diagnostics may contain attacker input.
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if convertCtx.Err() != nil {
		return nil, ErrUnavailable
	}
	if err != nil || output.buf.Len() == 0 || output.buf.Len() > 60*32000 || output.buf.Len()%2 != 0 {
		return nil, ErrInvalid
	}
	raw := output.buf.Bytes()
	wav := make([]byte, 44+len(raw))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(raw)))
	copy(wav[44:], raw)
	return wav, nil
}

type boundedOutput struct {
	buf    bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (w *boundedOutput) Write(b []byte) (int, error) {
	if len(b) > w.limit-w.buf.Len() {
		w.cancel()
		return 0, ErrInvalid
	}
	return w.buf.Write(b)
}

// NormalizeVoiceReference accepts only bounded uploaded WAV/MP3 bytes, never a
// path or URL. MP3 uses the same restricted pipe-only decoder as recordings.
func NormalizeVoiceReference(ctx context.Context, b []byte, mimeType, ffmpeg string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > 6<<20 {
		return nil, ErrInvalid
	}
	media, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return nil, ErrInvalid
	}
	switch strings.ToLower(media) {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/mpeg", "audio/mp3":
	default:
		return nil, ErrInvalid
	}
	snapshot := append([]byte(nil), b...)
	normalized, err := normalizeAudio(ctx, snapshot, mimeType, ffmpeg)
	if err != nil {
		clear(snapshot)
		return nil, err
	}
	if len(normalized) > 6<<20 {
		clear(snapshot)
		clear(normalized)
		return nil, ErrInvalid
	}
	return normalized, nil
}
