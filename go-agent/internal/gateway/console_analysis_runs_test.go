package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func awaitConsoleAnalysis(t *testing.T, s *Server, id string) smarthome.AnalysisRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.smartHome.GetAnalysisRun(id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "queued" && r.Status != "running" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("analysis did not finish")
	return smarthome.AnalysisRun{}
}
func TestConsoleAnalysisRunsAuthority(t *testing.T) {
	s, writes := suggestionsFixture(t)
	admin, csrf, identity := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	owner := identity["user"].(map[string]any)["id"].(string)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/analyze", `{"days":14,"owner_id":"system"}`, admin, csrf, testConsoleOrigin, ""), 422)
	_, member := consoleMemberLogin(t, s, "Member")
	for _, path := range []string{"/admin/analyze", "/admin/backfills"} {
		body := `{"days":14}`
		if strings.HasSuffix(path, "backfills") {
			body = `{}`
		}
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+path, body, admin, "", testConsoleOrigin, ""), 403)
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+path, body, member, csrf, testConsoleOrigin, ""), 403)
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", consolePrefix+"/admin/analyze", strings.NewReader(`{"days":14}`)).WithContext(requestCtx)
	req.AddCookie(admin)
	req.Header.Set("Origin", testConsoleOrigin)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.engine.ServeHTTP(response, req)
	cancel()
	accepted := consoleJSON(t, response, 202)
	id, ok := accepted["run_id"].(string)
	if !ok || id == "" || accepted["status"] == nil {
		t.Fatal(accepted)
	}
	run := awaitConsoleAnalysis(t, s, id)
	if run.Status != "completed" || run.OwnerID != owner || run.Outcome != "insufficient_data" {
		t.Fatalf("HTTP lifetime/owner escaped: %+v", run)
	}
	view := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/analysis-runs/"+id, "", admin, "", "", ""), 200)
	if view["outcome"] != "insufficient_data" || view["report"] != nil || view["owner_id"] != nil {
		t.Fatal(view)
	}
	for _, foreignOwner := range []string{"another-admin", "system"} {
		r, err := s.smartHome.StartAnalysis(smarthome.JobScope{Context: context.Background(), Release: func() {}, OwnerID: foreignOwner}, 14)
		if err != nil {
			t.Fatal(err)
		}
		awaitConsoleAnalysis(t, s, r.ID)
		code := 404
		if foreignOwner == "system" {
			code = 200
		}
		consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/analysis-runs/"+r.ID, "", admin, "", "", ""), code)
		cancelCode := 404
		if foreignOwner == "system" {
			cancelCode = 202
		}
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/analysis-runs/"+r.ID+"/cancel", `{}`, admin, csrf, testConsoleOrigin, ""), cancelCode)
		if foreignOwner != "system" {
			latest := consoleRequest(s, "GET", consolePrefix+"/admin/collection", "", admin, "", "", "")
			consoleJSON(t, latest, 200)
			if strings.Contains(latest.Body.String(), r.ID) || strings.Contains(latest.Body.String(), foreignOwner) {
				t.Fatal("foreign latest run leak")
			}
		}
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/analysis-runs/"+id, "", member, "", "", ""), 403)
	if writes.Load() != 0 {
		t.Fatal("analysis wrote to HA")
	}
}

func TestConsoleAnalysisRunsBackfillDetachedAndRevocable(t *testing.T) {
	for _, revoke := range []string{"logout", "revoke", "cancel"} {
		t.Run(revoke, func(t *testing.T) {
			entered, done := make(chan struct{}, 1), make(chan struct{}, 1)
			var writes atomic.Int32
			ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes.Add(1)
				}
				if strings.HasPrefix(r.URL.Path, "/api/history/period/") {
					select {
					case entered <- struct{}{}:
					default:
					}
					<-r.Context().Done()
					select {
					case done <- struct{}{}:
					default:
					}
					return
				}
				fmt.Fprint(w, `{"time_zone":"UTC"}`)
			}))
			defer ha.Close()
			m, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			defer m.Stop()
			if err = m.GetStore().SaveSnapshot([]smarthome.EntityState{{EntityID: "light.room", State: "off"}}); err != nil {
				t.Fatal(err)
			}
			s, _ := suggestionsFixture(t)
			s.smartHome = m
			admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			end := time.Now().UTC().Add(-time.Hour)
			start := end.Add(-time.Hour)
			body := fmt.Sprintf(`{"start":%q,"end":%q}`, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
			ctx, cancel := context.WithCancel(context.Background())
			req := httptest.NewRequest("POST", consolePrefix+"/admin/backfills", strings.NewReader(body)).WithContext(ctx)
			req.AddCookie(admin)
			req.Header.Set("Origin", testConsoleOrigin)
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			s.engine.ServeHTTP(response, req)
			cancel()
			accepted := consoleJSON(t, response, 202)
			id := accepted["run_id"].(string)
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("work did not continue after HTTP")
			}
			select {
			case <-done:
				t.Fatal("HTTP end cancelled job")
			default:
			}
			switch revoke {
			case "logout":
				consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/auth/logout", `{}`, admin, csrf, testConsoleOrigin, ""), 204)
			case "revoke":
				if err := s.consoleStore.RevokeAllSessions(); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/backfills/"+id+"/cancel", `{}`, admin, csrf, testConsoleOrigin, ""), 202)
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("work survived revocation")
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				r, e := m.GetBackfillRun(id)
				if e != nil {
					t.Fatal(e)
				}
				if r.Status == "cancelled" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("not cancelled %+v", r)
				}
				time.Sleep(time.Millisecond)
			}
			if writes.Load() != 0 {
				t.Fatal("backfill wrote to HA")
			}
		})
	}
}

func TestConsoleAnalysisRunsEvidenceProjection(t *testing.T) {
	item := persistedTimeRule("evidence", "confirmed")
	hits, eligible := 11, 14
	item.Evidence = []smarthome.SuggestionEvidence{{SampleSize: 11, PeriodDays: 14, HitDays: &hits, EligibleDays: &eligible, Denominator: 14, DenominatorType: "requested_days", AlgorithmVersion: "deterministic-v1", RunID: "PRIVATE-RUN", Window: smarthome.TimeWindow{Start: time.Now().Add(-14 * 24 * time.Hour), End: time.Now()}, EventRefs: []smarthome.LocalEventReference{{EntityID: "light.room", At: time.Now()}, {EntityID: "light.private", At: time.Now()}}}}
	view := suggestionProjection(item, false, true, nil)
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"hit_days":11`) || !strings.Contains(string(b), `"denominator":14`) {
		t.Fatalf("missing typed evidence: %s", b)
	}
	if strings.Contains(string(b), "PRIVATE-RUN") || strings.Contains(string(b), "light.private") || strings.Contains(string(b), "RAW_") {
		t.Fatalf("evidence leak: %s", b)
	}
}

func TestConsoleAnalysisRunsForeignBackfillDoesNotLeak(t *testing.T) {
	entered := make(chan struct{}, 1)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/history/period/") {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"time_zone":"UTC"}`)
	}))
	defer ha.Close()
	m, e := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Stop()
	if e = m.GetStore().SaveSnapshot([]smarthome.EntityState{{EntityID: "light.private", State: "off"}}); e != nil {
		t.Fatal(e)
	}
	s, _ := suggestionsFixture(t)
	s.smartHome = m
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	end := time.Now().UTC().Add(-time.Hour)
	start := end.Add(-time.Hour)
	foreign, e := m.StartBackfill(smarthome.JobScope{Context: context.Background(), Release: func() {}, OwnerID: "another-admin"}, start, end)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("backfill not running")
	}
	for _, method := range []string{"GET", "POST"} {
		path := consolePrefix + "/admin/backfills/" + foreign.ID
		body := ""
		if method == "POST" {
			path += "/cancel"
			body = `{}`
		}
		response := consoleRequest(s, method, path, body, admin, csrf, testConsoleOrigin, "")
		consoleJSON(t, response, 404)
		if strings.Contains(response.Body.String(), foreign.ID) || strings.Contains(response.Body.String(), "private") {
			t.Fatal("foreign run leaked")
		}
	}
	body := fmt.Sprintf(`{"start":%q,"end":%q}`, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
	busy := consoleRequest(s, "POST", consolePrefix+"/admin/backfills", body, admin, csrf, testConsoleOrigin, "")
	consoleJSON(t, busy, 409)
	if strings.Contains(busy.Body.String(), foreign.ID) || strings.Contains(busy.Body.String(), "another-admin") {
		t.Fatal("busy leaked")
	}
	collection := consoleRequest(s, "GET", consolePrefix+"/admin/collection", "", admin, "", "", "")
	consoleJSON(t, collection, 200)
	if strings.Contains(collection.Body.String(), foreign.ID) || strings.Contains(collection.Body.String(), "another-admin") {
		t.Fatal("collection leaked foreign run")
	}
}
