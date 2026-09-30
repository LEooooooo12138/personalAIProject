package console

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const ownerPassword = "A-strong-local-password"

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func bootstrapTest(t *testing.T, s *Store) User {
	t.Helper()
	u, err := s.Bootstrap("owner", "主人", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBootstrapRejectsCorruptState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":1,"users":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir, time.Now); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("corrupt state: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "state.json")); err != nil || !strings.Contains(string(got), `"users":`) {
		t.Fatalf("state overwritten: %q, %v", got, err)
	}
}

func TestConcurrentBootstrapCreatesOneAdmin(t *testing.T) {
	s, dir := newTestStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Bootstrap("owner", "主人", ownerPassword); errs <- err }()
	}
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Errorf("bootstrap: %v", err)
		}
	}
	if success != 1 || conflicts != 7 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	s2, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Bootstrap("other", "Other", ownerPassword); !errors.Is(err, ErrConflict) {
		t.Fatalf("second bootstrap: %v", err)
	}
}

func TestLoginSurvivesRestart(t *testing.T) {
	s, dir := newTestStore(t)
	bootstrapTest(t, s)
	login, err := s.Authenticate("OWNER", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	if login.Token == "" || login.CSRFToken == "" || login.Principal.Role != "admin" {
		t.Fatalf("login missing fields: %+v", login)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ownerPassword) || strings.Contains(string(data), login.Token) || strings.Contains(string(data), login.CSRFToken) {
		t.Fatal("plaintext credential on disk")
	}
	s2, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Resolve(login.Token); err != nil {
		t.Fatalf("login did not survive restart: %v", err)
	}
	if csrf, err := s2.CSRFToken(login.Token); err != nil || csrf != login.CSRFToken {
		t.Fatalf("csrf after restart: %q %v", csrf, err)
	}
}

func TestPasswordChangeRevokesAllSessions(t *testing.T) {
	s, _ := newTestStore(t)
	bootstrapTest(t, s)
	a, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop, err := s.BindSession(context.Background(), b.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := s.ChangePassword(a.Token, ownerPassword, "Another-local-password"); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{a.Token, b.Token} {
		if _, err := s.Resolve(tok); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("old token survived: %v", err)
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("bound request not cancelled")
	}
	if _, err := s.Authenticate("owner", "Another-local-password"); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestSharingRevisionConflict(t *testing.T) {
	s, dir := newTestStore(t)
	bootstrapTest(t, s)
	a, err := s.PutSharing(0, []string{"light.kitchen"}, []string{"suggestion-1"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != 1 {
		t.Fatalf("revision=%d", a.Revision)
	}
	if _, err := s.PutSharing(0, []string{"light.other"}, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	s2, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s2.GetSharing()
	if err != nil {
		t.Fatal(err)
	}
	if b.Revision != 1 || len(b.EntityIDs) != 1 || b.EntityIDs[0] != "light.kitchen" {
		t.Fatalf("sharing overwritten: %+v", b)
	}
}

func TestFailedWritePreservesSessionAndBoundRequest(t *testing.T) {
	s, dir := newTestStore(t)
	bootstrapTest(t, s)
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop, err := s.BindSession(context.Background(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	path := filepath.Join(dir, "state.json")
	backup := filepath.Join(dir, "state.backup")
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(login.Token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed persistence: %v", err)
	}
	if _, err := s.Resolve(login.Token); err != nil {
		t.Fatalf("session revoked before successful write: %v", err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("request cancelled before successful write")
	default:
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(login.Token); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("request not cancelled after successful logout")
	}
}

func TestTemporaryPasswordAndDisableRevoke(t *testing.T) {
	s, dir := newTestStore(t)
	admin := bootstrapTest(t, s)
	member, temp, err := s.CreateMember("Family_1", "家人")
	if err != nil {
		t.Fatal(err)
	}
	if !member.MustChangePassword || member.Role != "member" {
		t.Fatalf("member state: %+v", member)
	}
	if _, err := s.UpdateMember(admin.ID, "X", true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin modified by member API: %v", err)
	}
	login, err := s.Authenticate("family_1", temp)
	if err != nil {
		t.Fatal(err)
	}
	if !login.Principal.MustChangePassword {
		t.Fatal("temporary password not marked")
	}
	if err := s.ChangePassword(login.Token, temp, "Member-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(login.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("temporary login survived change: %v", err)
	}
	login, err = s.Authenticate("family_1", "Member-new-password")
	if err != nil {
		t.Fatal(err)
	}
	if login.Principal.MustChangePassword {
		t.Fatal("change flag persisted")
	}
	if _, err := s.UpdateMember(member.ID, "家人", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(login.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("disabled login survived: %v", err)
	}
	if _, err := s.UpdateMember(member.ID, "家人", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(login.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("re-enabled old login revived: %v", err)
	}
	if _, err := OpenStore(dir, time.Now); err != nil {
		t.Fatalf("member state after restart: %v", err)
	}
}

func TestBindSessionCancelsAtExpiry(t *testing.T) {
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	dir := t.TempDir()
	s, err := OpenStore(dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapTest(t, s)
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	now = login.ExpiresAt.Add(-25 * time.Millisecond)
	mu.Unlock()
	ctx, stop, err := s.BindSession(context.Background(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expiry did not cancel bound request")
	}
	mu.Lock()
	now = login.ExpiresAt
	mu.Unlock()
	if _, err := s.Resolve(login.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired token: %v", err)
	}
}

func TestRejectsUnsupportedHashParametersBeforeVerify(t *testing.T) {
	s, dir := newTestStore(t)
	bootstrapTest(t, s)
	path := filepath.Join(dir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), "m=65536,t=3,p=4", "m=4294967295,t=3,p=4", 1)
	if changed == string(data) {
		t.Fatal("fixture did not contain hash params")
	}
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir, time.Now); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsupported hash: %v", err)
	}
}

func TestRejectsUnicodeUsernameConfusable(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Bootstrap("Ken", "主人", ownerPassword); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-ASCII username accepted: %v", err)
	}
}

func TestAuthenticateRejectsUnicodeUsernameConfusable(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Bootstrap("ken", "主人", ownerPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("Ken", ownerPassword); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("non-ASCII login accepted: %v", err)
	}
}

func TestOpenStoreRejectsDanglingStateSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "absent"), filepath.Join(dir, "state.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := OpenStore(dir, time.Now); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("dangling state link appeared uninitialized: %v", err)
	}
}
