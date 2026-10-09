package console

import (
	"context"
	"errors"
	"testing"
	"time"
)

type sessionSharingWriter interface {
	PutSharingForSession(context.Context, string, uint64, []string, []string) (Sharing, error)
}

func TestPutSharingForSessionRejectsRevokedCancelledAndMember(t *testing.T) {
	s, _ := newTestStore(t)
	bootstrapTest(t, s)
	writer, ok := any(s).(sessionSharingWriter)
	if !ok {
		t.Fatal("sharing mutation has no atomic session guard")
	}
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetSharing()
	if err != nil {
		t.Fatal(err)
	}
	updated, err := writer.PutSharingForSession(context.Background(), login.Token, before.Revision, []string{"legacy.device"}, []string{"shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.PutSharingForSession(context.Background(), login.Token, updated.Revision, nil, nil); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked mutation accepted: %v", err)
	}
	login, err = s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = writer.PutSharingForSession(ctx, login.Token, updated.Revision, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation accepted: %v", err)
	}
	member, password, err := s.CreateMember("member", "Member")
	if err != nil {
		t.Fatal(err)
	}
	memberLogin, err := s.Authenticate(member.Username, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.PutSharingForSession(context.Background(), memberLogin.Token, updated.Revision, nil, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member mutation accepted: %v", err)
	}
	s.mu.Lock()
	session := s.state.Sessions[tokenHash(login.Token)]
	session.ExpiresAt = time.Now().Add(-time.Second)
	s.state.Sessions[tokenHash(login.Token)] = session
	s.mu.Unlock()
	if _, err = writer.PutSharingForSession(context.Background(), login.Token, updated.Revision, nil, nil); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired mutation accepted: %v", err)
	}
	final, _ := s.GetSharing()
	if final.Revision != updated.Revision || len(final.SuggestionIDs) != 1 || final.SuggestionIDs[0] != "shared" {
		t.Fatal("denied write changed sharing")
	}
}
