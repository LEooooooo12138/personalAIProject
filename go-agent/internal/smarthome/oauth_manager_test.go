package smarthome

import (
	"context"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerOAuthConfigurationAndShutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/token" {
			w.Write([]byte(`{"access_token":"manager-token","expires_in":1800,"token_type":"Bearer"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer manager-token" {
			t.Error("manager did not use provider")
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	cfg := HAConfig{BaseURL: srv.URL, OAuthCredentialsFile: oauthFixture(t, srv.URL), AgentVaultPath: t.TempDir()}
	m, e := NewManager(cfg, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.client.GetStates(context.Background()); e != nil {
		t.Fatal(e)
	}
	m.Stop()
	if _, e = m.client.accessToken(context.Background()); e == nil {
		t.Fatal("provider survives shutdown")
	}
	cfg.Token = "conflicting-token"
	if _, e = NewManager(cfg, zap.NewNop()); e == nil {
		t.Fatal("ambiguous credentials accepted")
	}
}
