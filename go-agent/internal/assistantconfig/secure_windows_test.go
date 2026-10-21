//go:build windows

package assistantconfig

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

func TestDPAPICiphertextTamperingFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "test-only-key"
	_, err = s.Update(Update{LLMMode: Local, ASRMode: Local, TTSMode: Local, APIKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secrets", "mimo.dpapi")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("modified ciphertext accepted: %v", err)
	}
}

func TestFailedAtomicReplacePreservesPreviousRecord(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Update(Update{LLMMode: Local, ASRMode: Local, TTSMode: Local})
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(filepath.Join(dir, "secrets", "mimo.dpapi"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	_, err = s.Update(Update{ExpectedRevision: saved.Revision, LLMMode: Cloud, ASRMode: Hybrid, TTSMode: Local})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("locked replacement: %v", err)
	}
	got, err := s.Get()
	if err != nil || got != saved {
		t.Fatalf("failed replacement changed previous settings: %+v %v", got, err)
	}
	pending, err := filepath.Glob(filepath.Join(dir, "secrets", ".pending-*"))
	if err != nil || len(pending) != 0 {
		t.Fatalf("orphaned temp records: %v %v", pending, err)
	}
}

func TestSecretACLOnlyAllowsCurrentUser(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update(Update{LLMMode: Local, ASRMode: Local, TTSMode: Local})
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"secrets", "secrets/mimo.dpapi"} {
		sd, err := windows.GetNamedSecurityInfo(filepath.Join(dir, name), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if acl.AceCount != 1 {
			t.Fatalf("unexpected access entries: %d", acl.AceCount)
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, 0, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid) {
			t.Fatal("secret ACL grants a different principal")
		}
	}
}
