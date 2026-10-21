//go:build !windows

package assistantconfig

import (
	"bytes"
	"encoding/json"
	"os"
)

const supportsVoiceReference = false

const caseInsensitivePaths = false

var localPrefix = []byte("assistant-local-v1\x00")

func sealRecord(plain []byte, hasKey bool) ([]byte, error) {
	if hasKey {
		return nil, ErrUnavailable
	}
	return append(append([]byte{}, localPrefix...), plain...), nil
}
func unsealRecord(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, localPrefix) {
		return nil, ErrUnavailable
	}
	plain := data[len(localPrefix):]
	var rec record
	if json.Unmarshal(plain, &rec) != nil || rec.Key != "" || rec.Settings.KeyConfigured {
		return nil, ErrUnavailable
	}
	return plain, nil
}
func restrictPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0700)
	}
	return os.Chmod(path, 0600)
}
func rejectReparse(string) error { return nil }
