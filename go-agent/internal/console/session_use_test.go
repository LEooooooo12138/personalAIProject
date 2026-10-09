package console

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type sessionUser interface {
	WithSession(context.Context, string, func(context.Context) error) error
}

func TestSessionBindingRevocationRace(t *testing.T) {
	s, _ := newTestStore(t)
	bootstrapTest(t, s)
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	results := make(chan error, 33)
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			ctx, stop, err := s.BindSession(context.Background(), login.Token)
			if errors.Is(err, ErrUnauthenticated) {
				results <- nil
				return
			}
			if err != nil {
				results <- err
				return
			}
			defer stop()
			select {
			case <-ctx.Done():
				results <- nil
			case <-time.After(time.Second):
				results <- errors.New("binding missed concurrent revocation")
			}
		}()
	}
	workers.Add(1)
	go func() { defer workers.Done(); <-start; results <- s.Logout(login.Token) }()
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestWithSessionOrdersRevocation(t *testing.T) {
	s, _ := newTestStore(t)
	bootstrapTest(t, s)
	use, ok := any(s).(sessionUser)
	if !ok {
		t.Fatal("missing session publication guard")
	}
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- use.WithSession(context.Background(), login.Token, func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				return errors.New("missing expiry deadline")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	revoked := make(chan error, 1)
	go func() { revoked <- s.Logout(login.Token) }()
	select {
	case err := <-revoked:
		close(release)
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
	err = use.WithSession(context.Background(), login.Token, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrUnauthenticated) || called {
		t.Fatalf("revoked publication: called=%v err=%v", called, err)
	}
}

func TestWithSessionCancellationAndExpiry(t *testing.T) {
	s, _ := newTestStore(t)
	bootstrapTest(t, s)
	use, ok := any(s).(sessionUser)
	if !ok {
		t.Fatal("missing session publication guard")
	}
	login, err := s.Authenticate("owner", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err = use.WithSession(ctx, login.Token, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled use: %v %v", called, err)
	}
	s.mu.Lock()
	sess := s.state.Sessions[tokenHash(login.Token)]
	sess.ExpiresAt = s.now().Add(40 * time.Millisecond)
	s.state.Sessions[tokenHash(login.Token)] = sess
	s.mu.Unlock()
	err = use.WithSession(context.Background(), login.Token, func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("guard outlived login")
		}
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
