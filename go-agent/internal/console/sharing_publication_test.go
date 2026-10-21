package console

import (
	"context"
	"errors"
	"testing"
	"time"
)

type sharingPublisher interface {
	WithSessionSharingRevision(context.Context, string, uint64, func(context.Context) error) error
}

func sharingPublicationFixture(t *testing.T) (*Store, sharingPublisher, Login, Sharing) {
	t.Helper()
	s, _ := newTestStore(t)
	bootstrapTest(t, s)
	publish, ok := any(s).(sharingPublisher)
	if !ok {
		t.Fatal("missing atomic session and sharing-revision publication guard")
	}
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	sharing, err := s.GetSharing()
	if err != nil {
		t.Fatal(err)
	}
	return s, publish, login, sharing
}

// Dropping the revision check would publish evidence after its sharing grant
// has been withdrawn, even though the member's login is still valid.
func TestSharingPublicationRejectsChangedRevision(t *testing.T) {
	s, publish, login, initial := sharingPublicationFixture(t)
	granted, err := s.PutSharing(initial.Revision, nil, []string{"shared-rule"})
	if err != nil {
		t.Fatal(err)
	}
	withdrawn, err := s.PutSharingForSession(context.Background(), login.Token, granted.Revision, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = publish.WithSessionSharingRevision(context.Background(), login.Token, granted.Revision, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrConflict) || called {
		t.Fatalf("stale sharing published: called=%v err=%v", called, err)
	}
	marker := errors.New("publication result")
	err = publish.WithSessionSharingRevision(context.Background(), login.Token, withdrawn.Revision, func(ctx context.Context) error {
		called = true
		if _, ok := ctx.Deadline(); !ok {
			t.Error("publication has no session expiry deadline")
		}
		return marker
	})
	if !errors.Is(err, marker) || !called {
		t.Fatalf("current sharing publication lost callback result: called=%v err=%v", called, err)
	}
}

// Moving revision validation/publication outside the session lock allows a
// concurrent sharing withdrawal or logout to finish before publication ends.
func TestSharingPublicationOrdersRevocation(t *testing.T) {
	for _, action := range []string{"sharing", "logout"} {
		t.Run(action, func(t *testing.T) {
			s, publish, login, sharing := sharingPublicationFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- publish.WithSessionSharingRevision(context.Background(), login.Token, sharing.Revision, func(context.Context) error {
					close(entered)
					<-release
					return nil
				})
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("publication did not enter: %v", err)
			case <-time.After(time.Second):
				t.Fatal("publication did not enter")
			}
			started, revoked := make(chan struct{}), make(chan error, 1)
			go func() {
				close(started)
				if action == "logout" {
					revoked <- s.Logout(login.Token)
					return
				}
				_, err := s.PutSharingForSession(context.Background(), login.Token, sharing.Revision, nil, nil)
				revoked <- err
			}()
			<-started
			select {
			case err := <-revoked:
				close(release)
				<-done
				t.Fatalf("revocation crossed active publication: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := <-revoked; err != nil {
				t.Fatal(err)
			}
			called := false
			err := publish.WithSessionSharingRevision(context.Background(), login.Token, sharing.Revision, func(context.Context) error { called = true; return nil })
			want := ErrConflict
			if action == "logout" {
				want = ErrUnauthenticated
			}
			if !errors.Is(err, want) || called {
				t.Fatalf("post-revocation publication: called=%v err=%v", called, err)
			}
		})
	}
}

// Bypassing WithSession would let cancelled, expired or password-reset sessions
// publish a projection merely because its sharing revision still matches.
func TestSharingPublicationRejectsInvalidSession(t *testing.T) {
	for _, action := range []string{"cancelled", "expired", "temporary_password"} {
		t.Run(action, func(t *testing.T) {
			s, publish, login, sharing := sharingPublicationFixture(t)
			ctx := context.Background()
			want := ErrUnauthenticated
			switch action {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "expired":
				s.mu.Lock()
				session := s.state.Sessions[tokenHash(login.Token)]
				session.ExpiresAt = s.now().Add(-time.Second)
				s.state.Sessions[tokenHash(login.Token)] = session
				s.mu.Unlock()
			case "temporary_password":
				member, password, err := s.CreateMember("member", "Member")
				if err != nil {
					t.Fatal(err)
				}
				login, err = s.Authenticate(member.Username, password)
				if err != nil {
					t.Fatal(err)
				}
				want = ErrForbidden
			}
			called := false
			err := publish.WithSessionSharingRevision(ctx, login.Token, sharing.Revision, func(context.Context) error { called = true; return nil })
			if !errors.Is(err, want) || called {
				t.Fatalf("unauthorized publication: called=%v err=%v", called, err)
			}
		})
	}
}

func TestSharingPublicationExpiresDuringCallback(t *testing.T) {
	s, publish, login, sharing := sharingPublicationFixture(t)
	s.mu.Lock()
	session := s.state.Sessions[tokenHash(login.Token)]
	session.ExpiresAt = s.now().Add(40 * time.Millisecond)
	s.state.Sessions[tokenHash(login.Token)] = session
	s.mu.Unlock()
	err := publish.WithSessionSharingRevision(context.Background(), login.Token, sharing.Revision, func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("publication outlived login")
		}
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expiry deadline not preserved: %v", err)
	}
}
