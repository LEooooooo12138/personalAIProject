package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// This subprocess substitutes only the unavailable codec binary. Tests still
// exercise real process creation, pipes, cancellation and output boundaries.
func TestMain(m *testing.M) {
	if helper := os.Getenv("SPEECH_FFMPEG_HELPER"); helper != "" {
		args := strings.Join(os.Args[1:], " ")
		format := os.Getenv("SPEECH_FFMPEG_FORMAT")
		if format == "" {
			format = "matroska"
		}
		if !strings.Contains(args, "-protocol_whitelist pipe -f "+format+" -i pipe:0") || !strings.Contains(args, "-t 61 -ac 1 -ar 16000 -acodec pcm_s16le -f s16le pipe:1") {
			os.Exit(2)
		}
		input, _ := io.ReadAll(os.Stdin)
		if (format == "matroska" && (len(input) < 4 || !bytes.Equal(input[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}))) || (format == "mp3" && (len(input) < 3 || string(input[:3]) != "ID3")) {
			os.Exit(3)
		}
		switch helper {
		case "valid":
			os.Stdout.Write(make([]byte, 32000))
		case "long":
			os.Stdout.Write(make([]byte, 61*32000))
		case "overflow":
			os.Stdout.Write(make([]byte, 70*32000))
		case "hang":
			time.Sleep(time.Minute)
		case "invalid":
			os.Exit(4)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestConversionPipesDurationAndCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		helper string
		valid  bool
	}{{"valid", true}, {"long", false}, {"overflow", false}, {"invalid", false}} {
		t.Run(tc.helper, func(t *testing.T) {
			t.Setenv("SPEECH_FFMPEG_HELPER", tc.helper)
			b, err := normalizeAudio(context.Background(), []byte{0x1a, 0x45, 0xdf, 0xa3}, "audio/webm;codecs=opus", executable)
			if tc.valid {
				if err != nil || len(b) != 32044 || validateWAV(b, 60) != nil {
					t.Fatal(len(b), err)
				}
			} else if err == nil {
				t.Fatal("invalid conversion accepted")
			}
		})
	}
	t.Setenv("SPEECH_FFMPEG_HELPER", "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = normalizeAudio(ctx, []byte{0x1a, 0x45, 0xdf, 0xa3}, "audio/webm", executable)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 5*time.Second {
		t.Fatal("conversion cancellation", err)
	}
}

func TestAudioValidationAndConverterUnavailability(t *testing.T) {
	missing := "does-not-exist-speech-ffmpeg"
	if _, err := normalizeAudio(context.Background(), wav(1), "audio/wav", missing); err != nil {
		t.Fatal("native WAV should not require converter", err)
	}
	if _, err := normalizeAudio(context.Background(), []byte{0x1a, 0x45, 0xdf, 0xa3}, "audio/webm", missing); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		b    []byte
		mime string
	}{{[]byte("http://remote/audio"), "audio/webm"}, {wav(1), "audio/webm"}, {wav(1), "text/plain"}, {wav(1), "bad mime;"}} {
		if _, err := normalizeAudio(context.Background(), tc.b, tc.mime, missing); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func([]byte){func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 1) }, func(b []byte) { binary.LittleEndian.PutUint32(b[28:], 1) }, func(b []byte) { binary.LittleEndian.PutUint16(b[32:], 0) }, func(b []byte) { binary.LittleEndian.PutUint16(b[34:], 8) }, func(b []byte) { binary.LittleEndian.PutUint16(b[22:], 0) }} {
		b := wav(1)
		mutate(b)
		if validateWAV(b, 60) == nil {
			t.Fatal("inconsistent PCM accepted")
		}
	}
}

func TestBoundedOutputDoesNotRetainOverflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &boundedOutput{limit: 4, cancel: cancel}
	w.Write([]byte{1, 2, 3})
	if _, err := w.Write([]byte{4, 5}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if w.buf.Len() != 3 || ctx.Err() == nil {
		t.Fatal("overflow retained or not cancelled")
	}
}
