package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
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
	if len(aesKey) != 32 {
		return nil, fmt.Errorf("wecom: encoding_aes_key must decode to 32 bytes")
	}
	logger.Info("wecom crypto initialized")
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
	ok := subtle.ConstantTimeCompare([]byte(got), []byte(signature)) == 1
	if !ok {
		c.logger.Warn("signature mismatch")
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

	// WeCom pads to 32 bytes, independently of AES's 16-byte block size.
	paddingLen := int(plaintext[len(plaintext)-1])
	if paddingLen < 1 || paddingLen > 32 || paddingLen > len(plaintext) {
		return nil, fmt.Errorf("wecom: invalid pkcs7 padding %d", paddingLen)
	}
	for _, b := range plaintext[len(plaintext)-paddingLen:] {
		if int(b) != paddingLen {
			return nil, fmt.Errorf("wecom: invalid pkcs7 padding bytes")
		}
	}
	plaintext = plaintext[:len(plaintext)-paddingLen]

	// Structure after decryption: 16 bytes random + 4 bytes length + content + corp_id
	if len(plaintext) < 20 {
		return nil, fmt.Errorf("wecom: plaintext too short: %d bytes", len(plaintext))
	}

	msgLen := binary.BigEndian.Uint32(plaintext[16:20])
	if uint64(msgLen) > uint64(len(plaintext)-20) {
		return nil, fmt.Errorf("wecom: invalid message length: want %d, have %d", msgLen, uint32(len(plaintext))-20)
	}
	end := 20 + int(msgLen)
	if string(plaintext[end:]) != c.corpID {
		return nil, fmt.Errorf("wecom: receiver does not match configured corp_id")
	}
	return plaintext[20:end], nil
}
