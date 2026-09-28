package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("synthetic transport failure")
}

func TestRequestErrorsDoNotExposeCredentials(t *testing.T) {
	m := NewTokenManager("corp", "secret-fixture", zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.GetToken(ctx)
	if err == nil || strings.Contains(err.Error(), "secret-fixture") {
		t.Fatalf("unsafe token error: %v", err)
	}
	m.token = "access-token-fixture"
	m.expiresAt = time.Now().Add(time.Hour)
	c := NewAPIClient(m, "sender", zap.NewNop())
	c.client = &http.Client{Transport: failingTransport{}}
	err = c.SendText("user", "hello")
	if err == nil || strings.Contains(err.Error(), "access-token-fixture") {
		t.Fatalf("unsafe send error: %v", err)
	}
}

// WeCom's reference protocol pads to 32 bytes (AES itself still uses 16-byte blocks).
func encryptedFixture(t *testing.T, msg, receiver string, corrupt func([]byte)) string {
	t.Helper()
	plain := make([]byte, 20)
	binary.BigEndian.PutUint32(plain[16:20], uint32(len(msg)))
	plain = append(plain, []byte(msg+receiver)...)
	pad := 32 - len(plain)%32
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	if corrupt != nil {
		corrupt(plain)
	}
	key := []byte(strings.Repeat("K", 32))
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(plain, plain)
	return base64.StdEncoding.EncodeToString(plain)
}

func TestDecryptProtocolValidation(t *testing.T) {
	key := strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("K", 32))), "=")
	c, err := NewCrypto("token", key, "corp", zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 33; n++ {
		msg := strings.Repeat("x", n)
		got, err := c.Decrypt(encryptedFixture(t, msg, "corp", nil))
		if err != nil || string(got) != msg {
			t.Errorf("message length %d: %q %v", n, got, err)
		}
	}
	for name, fixture := range map[string]string{
		"wrong receiver":  encryptedFixture(t, "hello", "evil", nil),
		"invalid padding": encryptedFixture(t, "hi", "corp", func(b []byte) { b[len(b)-2] = 0 }),
		"overflow length": encryptedFixture(t, "hi", "corp", func(b []byte) { binary.BigEndian.PutUint32(b[16:20], ^uint32(0)) }),
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("panic: %v", p)
				}
			}()
			if _, err := c.Decrypt(fixture); err == nil {
				t.Error("invalid envelope accepted")
			}
		})
	}
}

func TestCryptoDoesNotLogSecrets(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	key := strings.Repeat("K", 32)
	encoded := strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(key)), "=")
	c, err := NewCrypto("audit-synthetic-token", encoded, "test-corp", zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	c.VerifySignature("invalid", "1", "2", "synthetic-encrypted-body")
	for _, entry := range logs.All() {
		text := entry.Message + fmt.Sprint(entry.ContextMap())
		for _, secret := range []string{"audit-synthetic-token", encoded, fmt.Sprintf("%x", key), "synthetic-encrypted-body"} {
			if strings.Contains(text, secret) {
				t.Errorf("sensitive value logged under %s", entry.Message)
			}
		}
	}
}

func TestCallbackRejectsShortBodyWithoutPanic(t *testing.T) {
	key := strings.TrimRight(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("K", 32))), "=")
	c, err := NewCrypto("synthetic-token", key, "test-corp", zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	h := NewCallbackHandler(c, nil, zap.NewNop())
	for _, body := range []string{"", "x", "<xml/>"} {
		t.Run(fmt.Sprintf("len_%d", len(body)), func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("short request panicked: %v", p)
				}
			}()
			w := httptest.NewRecorder()
			h.handleMessage(w, httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(body)), nil)
			if w.Code != http.StatusBadRequest && w.Code != http.StatusForbidden {
				t.Fatalf("got %d", w.Code)
			}
		})
	}
}

func TestCallbackRejectsOversizedBody(t *testing.T) {
	h := NewCallbackHandler(nil, nil, zap.NewNop())
	w := httptest.NewRecorder()
	h.handleMessage(w, httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(strings.Repeat("x", 1024*1024+1))), nil)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413", w.Code)
	}
}
