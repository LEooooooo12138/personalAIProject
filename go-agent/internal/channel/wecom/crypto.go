package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"
)

type WeComCallbackCrypto struct {
	token          string
	encodingAESKey string
	aesKey         []byte
	corpID         string
	logger         *zap.Logger
}

func NewCrypto(token, encodingAESKey, corpID string, logger *zap.Logger) (*WeComCallbackCrypto, error) {
	aesKey, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, fmt.Errorf("wecom: invalid encoding_aes_key: %w", err)
	}
	logger.Info("wecom crypto initialized",
		zap.String("token", token),
		zap.String("encoding_aes_key", encodingAESKey),
		zap.String("aes_key_hex", fmt.Sprintf("%x", aesKey)),
		zap.String("corp_id", corpID),
	)
	return &WeComCallbackCrypto{
		token:          token,
		encodingAESKey: encodingAESKey,
		aesKey:         aesKey,
		corpID:         corpID,
		logger:         logger,
	}, nil
}

func (c *WeComCallbackCrypto) VerifySignature(signature, timestamp, nonce, encrypt string) bool {
	sl := []string{c.token, timestamp, nonce, encrypt}
	sort.Strings(sl)
	s := sha1.New()
	combined := strings.Join(sl, "")
	s.Write([]byte(combined))
	got := fmt.Sprintf("%x", s.Sum(nil))
	ok := got == signature
	if !ok {
		c.logger.Warn("signature mismatch",
			zap.String("expected", signature),
			zap.String("got", got),
			zap.String("combined", combined),
		)
	}
	return ok
}

// Decrypt decrypts a WeCom callback message using AES-256-CBC.
// The IV for WeCom is always the first 16 bytes of the AES key itself.
// The entire base64-decoded ciphertext is the AES input (no IV prefix).
func (c *WeComCallbackCrypto) Decrypt(encryptedMsg string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedMsg)
	if err != nil {
		return nil, fmt.Errorf("wecom: base64 decode: %w", err)
	}

	block, err := aes.NewCipher(c.aesKey)
	if err != nil {
		return nil, fmt.Errorf("wecom: new cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("wecom: invalid ciphertext length %d", len(ciphertext))
	}

	// IV = first 16 bytes of aesKey (WeCom convention)
	iv := c.aesKey[:aes.BlockSize]
	mode := cipher.NewCBCDecrypter(block, iv)

	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS7 padding
	paddingLen := int(plaintext[len(plaintext)-1])
	if paddingLen < 1 || paddingLen > aes.BlockSize {
		return nil, fmt.Errorf("wecom: invalid pkcs7 padding %d", paddingLen)
	}
	plaintext = plaintext[:len(plaintext)-paddingLen]

	// Structure after decryption: 16 bytes random + 4 bytes length + content + corp_id
	if len(plaintext) < 20 {
		return nil, fmt.Errorf("wecom: plaintext too short: %d bytes", len(plaintext))
	}

	msgLen := binary.BigEndian.Uint32(plaintext[16:20])
	if uint32(len(plaintext)) < 20+msgLen {
		return nil, fmt.Errorf("wecom: invalid message length: want %d, have %d", msgLen, uint32(len(plaintext))-20)
	}

	return plaintext[20 : 20+msgLen], nil
}
