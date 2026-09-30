package console

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostReplacementSyncFailurePoisonsStoreAndCancelsBindings(t *testing.T) {
	s, dir := newTestStore(t)
	admin := bootstrapTest(t, s)
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop, err := s.BindSession(context.Background(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	s.syncDir = func(string) error { return errors.New("injected directory sync failure") }
	if err := s.ChangePassword(login.Token, ownerPassword, "Another-local-password"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("sync failure: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("bound request survived uncertain replacement")
	}
	if err := s.CheckAvailable(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("store available after uncertain replacement: %v", err)
	}
	if _, err := s.Resolve(login.Token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("old token answered from stale memory: %v", err)
	}
	if _, err := s.Authenticate("owner", ownerPassword); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("authentication continued: %v", err)
	}
	if _, err := s.GetSharing(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("sharing continued: %v", err)
	}
	if _, err := s.PutSharing(0, nil, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("writes continued: %v", err)
	}
	if _, err := s.Bootstrap("other", "Other", ownerPassword); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("bootstrap continued: %v", err)
	}
	if _, err := s.GetUser(admin.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("user lookup continued: %v", err)
	}
	if _, err := s.CSRFToken(login.Token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("csrf continued: %v", err)
	}
	if err := s.Logout(login.Token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("logout continued: %v", err)
	}
	if err := s.RevokeAllSessions(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("all-session revoke continued: %v", err)
	}
	if _, _, err := s.CreateMember("member", "M"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("member creation continued: %v", err)
	}
	if _, err := s.UpdateMember(admin.ID, "M", false); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("member update continued: %v", err)
	}
	if _, err := s.ResetMemberPassword(admin.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("member reset continued: %v", err)
	}
	if err := s.ResetAdminPassword(ownerPassword); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("admin reset continued: %v", err)
	}
	if _, err := s.ListMembers(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("member list continued: %v", err)
	}
	if _, _, err := s.BindSession(context.Background(), login.Token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("binding continued: %v", err)
	}
	reopened, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Authenticate("owner", "Another-local-password"); err != nil {
		t.Fatalf("replacement not on disk: %v", err)
	}
}

func TestPostReplacementBootstrapFailureCannotBootstrapAgain(t *testing.T) {
	s, dir := newTestStore(t)
	s.syncDir = func(string) error { return errors.New("injected directory sync failure") }
	if _, err := s.Bootstrap("owner", "主人", ownerPassword); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := s.CheckAvailable(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("bootstrap ambiguity not reported: %v", err)
	}
	if _, err := s.Bootstrap("owner", "主人", ownerPassword); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("repeat bootstrap: %v", err)
	}
	reopened, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Initialized() {
		t.Fatal("renamed bootstrap state absent after restart")
	}
}

func TestResetMemberPasswordRevokesSessionsAcrossRestart(t *testing.T) {
	s, dir := newTestStore(t)
	bootstrapTest(t, s)
	member, initial, err := s.CreateMember("member", "M")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Authenticate("member", initial)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Authenticate("member", initial)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop, err := s.BindSession(context.Background(), b.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	reset, err := s.ResetMemberPassword(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset == initial {
		t.Fatal("temporary password reused")
	}
	for _, token := range []string{a.Token, b.Token} {
		if _, err := s.Resolve(token); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("old member token: %v", err)
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("member binding survived reset")
	}
	reopened, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Authenticate("member", initial); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old member password: %v", err)
	}
	login, err := reopened.Authenticate("member", reset)
	if err != nil {
		t.Fatal(err)
	}
	if !login.Principal.MustChangePassword {
		t.Fatal("reset password did not require change")
	}
}

func TestResetAdminPasswordRevokesOnlyAdminAcrossRestart(t *testing.T) {
	s, dir := newTestStore(t)
	bootstrapTest(t, s)
	_, temp, err := s.CreateMember("member", "M")
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.Authenticate("member", temp)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResetAdminPassword("Admin-reset-password"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{a.Token, b.Token} {
		if _, err := reopened.Resolve(token); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("old admin token: %v", err)
		}
	}
	if _, err := reopened.Resolve(member.Token); err != nil {
		t.Fatalf("member token revoked by admin reset: %v", err)
	}
	if _, err := reopened.Authenticate("owner", ownerPassword); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old admin password: %v", err)
	}
	if _, err := reopened.Authenticate("owner", "Admin-reset-password"); err != nil {
		t.Fatalf("new admin password: %v", err)
	}
}

func TestRevokeAllSessionsSurvivesRestart(t *testing.T) {
	s, dir := newTestStore(t)
	bootstrapTest(t, s)
	_, temp, err := s.CreateMember("member", "M")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Authenticate("member", temp)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop, err := s.BindSession(context.Background(), b.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := s.RevokeAllSessions(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("bound member request survived revoke-all")
	}
	reopened, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{a.Token, b.Token} {
		if _, err := reopened.Resolve(token); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("old token: %v", err)
		}
	}
	if _, err := reopened.Authenticate("owner", ownerPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Authenticate("member", temp); err != nil {
		t.Fatal(err)
	}
}

func TestHashCapacityReturnsBusy(t *testing.T) {
	s, _ := newTestStore(t)
	bootstrapTest(t, s)
	hashSlots <- struct{}{}
	hashSlots <- struct{}{}
	defer func() { <-hashSlots; <-hashSlots }()
	if _, err := s.Authenticate("owner", ownerPassword); !errors.Is(err, ErrBusy) {
		t.Fatalf("valid login at capacity: %v", err)
	}
	if _, err := s.Authenticate("missing", ownerPassword); !errors.Is(err, ErrBusy) {
		t.Fatalf("unknown-account verification at capacity: %v", err)
	}
}

func TestOversizedWritePreservesReadableState(t *testing.T) {
	s, dir := newTestStore(t)
	admin := bootstrapTest(t, s)
	s.mu.Lock()
	st := s.copyState()
	st.Sessions[strings.Repeat("a", 64)] = session{ID: strings.Repeat("x", 16<<20), UserID: admin.ID, ExpiresAt: time.Now().Add(sessionLifetime)}
	err := s.commitLocked(st)
	s.mu.Unlock()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("oversized write: %v", err)
	}
	if err := s.CheckAvailable(); err != nil {
		t.Fatalf("pre-replacement rejection poisoned store: %v", err)
	}
	reopened, err := OpenStore(dir, time.Now)
	if err != nil {
		t.Fatalf("last committed state unreadable: %v", err)
	}
	if !reopened.Initialized() {
		t.Fatal("last committed state lost")
	}
}

func TestNextWritePrunesExpiredSessions(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	s, err := OpenStore(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	bootstrapTest(t, s)
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * 24 * time.Hour)
	if _, err := s.PutSharing(0, []string{"light.kitchen"}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatal(err)
	}
	var persisted diskState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if _, exists := persisted.Sessions[tokenHash(login.Token)]; exists {
		t.Fatal("expired session retained on disk")
	}
	if _, err := OpenStore(dir, time.Now); err != nil {
		t.Fatalf("restart after prune: %v", err)
	}
}
