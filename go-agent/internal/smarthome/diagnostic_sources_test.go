package smarthome

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const evidenceRun = "0123456789abcdef0123456789abcdef"

var evidenceWindow = TimeWindow{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}

type diagnosticFixture struct {
	list     string
	detail   string
	code     string
	getCode  string
	version  string
	runtime  string
	details  map[string]string
	state    string
	getCount atomic.Int32
	writes   atomic.Int32
}

func traceJSON(execution, nodes string) string {
	return fmt.Sprintf(`{"run_id":%q,"domain":"automation","item_id":"rule-1","state":"stopped","script_execution":%q,"timestamp":{"start":"2026-10-01T01:00:00Z","finish":"2026-10-01T01:01:00Z"},"config":%s,"trace":%s,"context":{"user_id":"PRIVATE","id":"PRIVATE"},"trigger":"PRIVATE"}`, evidenceRun, execution, observationConfig, nodes)
}
func newDiagnosticFixture(t *testing.T) (*diagnosticFixture, *Manager) {
	t.Helper()
	f := &diagnosticFixture{version: "2026.7.2", state: "on", runtime: observationConfig, detail: traceJSON("finished", `{"action/0":[{"path":"action/0","timestamp":"2026-10-01T01:00:01Z","changed_variables":{"secret":"PRIVATE"}}]}`)}
	f.list = "[" + f.detail + "]"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			f.writes.Add(1)
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/api/config":
			json.NewEncoder(w).Encode(map[string]any{"version": f.version, "components": []string{"config", "automation"}})
		case "/api/config/automation/config/rule-1":
			io.WriteString(w, observationConfig)
		case "/api/states/automation.rule":
			json.NewEncoder(w).Encode(map[string]any{"entity_id": "automation.rule", "state": f.state, "attributes": map[string]any{"PRIVATE": "PRIVATE"}})
		case "/api/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.WriteJSON(map[string]any{"type": "auth_required"})
			var auth any
			if conn.ReadJSON(&auth) != nil {
				return
			}
			conn.WriteJSON(map[string]any{"type": "auth_ok"})
			for {
				var req map[string]any
				if conn.ReadJSON(&req) != nil {
					return
				}
				res := map[string]any{"id": req["id"], "type": "result", "success": true}
				code := ""
				switch req["type"] {
				case "config/entity_registry/list":
					res["result"] = json.RawMessage(`[{"entity_id":"automation.rule","platform":"automation","unique_id":"rule-1"}]`)
				case "automation/config":
					res["result"] = map[string]any{"config": json.RawMessage(f.runtime)}
				case "trace/list", "trace/get":
					if req["domain"] != "automation" || req["item_id"] != "rule-1" {
						t.Error("wrong trace identity")
					}
					expected := 4
					if req["type"] == "trace/get" {
						expected = 5
					}
					if len(req) != expected {
						t.Error("unsupported remote trace parameters")
					}
					code = f.code
					res["result"] = json.RawMessage(f.list)
					if req["type"] == "trace/get" {
						f.getCount.Add(1)
						code = f.getCode
						res["result"] = json.RawMessage(f.detail)
						if f.details != nil {
							res["result"] = json.RawMessage(f.details[req["run_id"].(string)])
						} else if req["run_id"] != evidenceRun {
							t.Error("unexpected run")
						}
					}
				default:
					f.writes.Add(1)
					t.Errorf("unexpected command %v", req["type"])
					return
				}
				if code != "" {
					delete(res, "result")
					res["success"] = false
					res["error"] = map[string]any{"code": code, "message": "PRIVATE"}
				}
				if conn.WriteJSON(res) != nil {
					return
				}
			}
		default:
			f.writes.Add(1)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		if f.writes.Load() != 0 {
			t.Fatal("read-only boundary broken")
		}
	})
	return f, &Manager{client: NewHomeAssistantClient(srv.URL, "fixture", time.Second)}
}
func TestDiagnosticSourcesDegradeExplicitly(t *testing.T) {
	for _, tc := range []struct {
		code, getCode string
		want          SourceStatus
	}{{"unknown_command", "", SourceUnsupported}, {"not_allowed", "", SourceForbidden}, {"", "not_found", SourceRetentionGap}, {"unknown_error", "", SourceUnavailable}, {"invalid_format", "", SourceUnavailable}} {
		t.Run(tc.code+tc.getCode, func(t *testing.T) {
			f, m := newDiagnosticFixture(t)
			f.code = tc.code
			f.getCode = tc.getCode
			got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
			if err == nil || got.Trace.SourceStatus != tc.want || got.Activity.SourceStatus != SourceUnsupported || got.State.SourceStatus != SourceAvailable {
				t.Fatalf("got %+v err %v", got, err)
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("private error leaked")
			}
		})
	}
}
func TestTraceEvidenceExcludesPrivateVariables(t *testing.T) {
	f, m := newDiagnosticFixture(t)
	f.runtime = `{"id":"rule-1","actions":[{"delay":1}]}`
	got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	if err != nil || len(got.Trace.Runs) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	run := got.Trace.Runs[0]
	if run.Outcome != "completed" || run.ConfigDigest == "" || run.RunID != evidenceRun || run.Start.IsZero() {
		t.Fatalf("missing historical evidence: %+v", run)
	}
	if got.RuntimeLoadProof.Verified {
		t.Fatal("old trace incorrectly verified current runtime")
	}
	if got.Trace.CoverageStatus != "retention_unknown" || len(got.Trace.CoveredWindows) != 0 {
		t.Fatal("count-limited trace became complete coverage")
	}
	raw, _ := json.Marshal(got)
	for _, private := range []string{"PRIVATE", "changed_variables", "Shared name", "9007199254740993"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("leaked %s", private)
		}
	}
}
func TestTraceEvidenceStructuralOutcomes(t *testing.T) {
	for _, tc := range []struct{ name, execution, nodes, want string }{
		{"condition", "failed_conditions", `{"condition/0":[{"result":{"result":false}}]}`, "conditions_not_met"},
		{"error", "error", `{"action/0":[{"error":"PRIVATE"}]}`, "action_failed"},
		{"continued_error", "finished", `{"action/0":[{"error":"PRIVATE"}]}`, "action_failed"},
		{"stopped_only", "", `{}`, "unknown"},
		{"cancelled", "cancelled", `{}`, "cancelled"},
		{"finished", "finished", `{}`, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, m := newDiagnosticFixture(t)
			f.detail = traceJSON(tc.execution, tc.nodes)
			got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
			if err != nil || len(got.Trace.Runs) != 1 || got.Trace.Runs[0].Outcome != tc.want {
				t.Fatalf("%+v %v", got.Trace, err)
			}
		})
	}
}
func TestDiagnosticSourcesEmptyAndInvalidSelection(t *testing.T) {
	f, m := newDiagnosticFixture(t)
	f.list = "[]"
	got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	if err != nil || got.Trace.SourceStatus != SourceAvailable || got.Trace.CoverageStatus != "retention_unknown" || len(got.Trace.CoveredWindows) != 0 {
		t.Fatalf("%+v %v", got.Trace, err)
	}
	for _, ref := range []AutomationRef{{Kind: "managed", ID: "missing"}, {Kind: "external", ID: "automation.not_observed"}, {Kind: "external", ID: "../rule"}} {
		got, err = m.ReadAutomationEvidence(context.Background(), ref, evidenceWindow)
		if err == nil || got.Trace.SourceStatus == SourceAvailable {
			t.Fatal("untrusted selection accepted")
		}
	}
	if f.getCount.Load() != 0 {
		t.Fatal("invalid selection read traces")
	}
}

func TestDiagnosticSourcesRejectInvalidWindowBeforeNetwork(t *testing.T) {
	m := &Manager{}
	for _, w := range []TimeWindow{{}, {Start: evidenceWindow.Start, End: evidenceWindow.Start}, {Start: evidenceWindow.Start, End: evidenceWindow.Start.Add(15 * 24 * time.Hour)}} {
		_, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, w)
		if HAErrorCode(err) != "ha_invalid_window" {
			t.Fatalf("got %v", err)
		}
	}
}
func TestDiagnosticSourcesRejectUnreviewedProfile(t *testing.T) {
	f, m := newDiagnosticFixture(t)
	f.version = "2026.7.3"
	got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	if err == nil || got.Trace.SourceStatus != SourceUnavailable || f.getCount.Load() != 0 || got.State.SourceStatus != SourceAvailable {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestTraceEvidenceRejectsMismatchedAndMalformedRecords(t *testing.T) {
	for _, tc := range []struct{ name, detail string }{
		{"identity", strings.Replace(traceJSON("finished", `{}`), `"item_id":"rule-1"`, `"item_id":"other"`, 1)},
		{"timestamp", strings.Replace(traceJSON("finished", `{}`), `2026-10-01T01:00:00Z`, `2026-10-01T02:00:00Z`, 1)},
		{"trace_missing", strings.Replace(traceJSON("finished", `{}`), `"trace":{}`, `"trace":null`, 1)},
		{"duplicate", strings.Replace(traceJSON("finished", `{}`), `"state":"stopped"`, `"state":"running","state":"stopped"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, m := newDiagnosticFixture(t)
			f.detail = tc.detail
			got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
			if err == nil || got.Trace.SourceStatus != SourceUnavailable || len(got.Trace.Runs) != 0 {
				t.Fatalf("%+v %v", got.Trace, err)
			}
		})
	}
}
func TestTraceEvidenceHalfOpenWindowAndListBound(t *testing.T) {
	f, m := newDiagnosticFixture(t)
	f.list = "[" + strings.Replace(f.detail, "2026-10-01T01:00:00Z", "2026-10-02T00:00:00Z", 1) + "]"
	// Keep the finish valid; a run exactly at end is excluded.
	f.list = strings.Replace(f.list, "2026-10-01T01:01:00Z", "2026-10-02T00:01:00Z", 1)
	got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	if err != nil || f.getCount.Load() != 0 || len(got.Trace.Runs) != 0 {
		t.Fatalf("%+v %v", got.Trace, err)
	}
	var records []string
	for i := 0; i < 257; i++ {
		records = append(records, strings.Replace(f.detail, evidenceRun, fmt.Sprintf("%032x", i), 1))
	}
	f.list = "[" + strings.Join(records, ",") + "]"
	got, err = m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	if err == nil || got.Trace.CoverageStatus != "read_limit" || f.getCount.Load() != 0 {
		t.Fatalf("%+v %v", got.Trace, err)
	}
}
func TestTraceEvidenceDoesNotGuessConfigOrOutcome(t *testing.T) {
	for _, tc := range []struct{ name, detail, want string }{
		{"not_triggered", strings.Replace(traceJSON("", `{}`), `"state":"stopped"`, `"state":"stopped","not_triggered":true`, 1), "not_triggered"},
		{"unknown_execution", traceJSON("PRIVATE", `{}`), "unknown"},
		{"condition_error", traceJSON("", `{"condition/0":[{"error":"PRIVATE"}]}`), "execution_error"},
		{"aborted", traceJSON("aborted", `{}`), "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, m := newDiagnosticFixture(t)
			f.detail = strings.Replace(tc.detail, observationConfig, `{"id":"other","secret":"PRIVATE"}`, 1)
			got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
			if err != nil || len(got.Trace.Runs) != 1 {
				t.Fatalf("%+v %v", got.Trace, err)
			}
			run := got.Trace.Runs[0]
			if run.Outcome != tc.want || run.ConfigDigest != "" || run.ConfigStatus != SourceUnavailable {
				t.Fatalf("%+v", run)
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("private unknown fields escaped allowlist")
			}
		})
	}
}
func TestTraceEvidenceNormalizesFinishToUTC(t *testing.T) {
	f, m := newDiagnosticFixture(t)
	f.detail = strings.Replace(f.detail, "2026-10-01T01:01:00Z", "2026-10-01T09:01:00+08:00", 1)
	got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	if err != nil || len(got.Trace.Runs) != 1 {
		t.Fatalf("%+v %v", got.Trace, err)
	}
	if got.Trace.Runs[0].Finish.Location() != time.UTC {
		t.Fatal("historical finish timestamp was not normalized to UTC")
	}
}

func TestTraceEvidenceBoundsSelectedRunsAndAggregateBytes(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprint(large), func(t *testing.T) {
			f, m := newDiagnosticFixture(t)
			f.details = map[string]string{}
			list := []string{}
			for i := 0; i < 40; i++ {
				id := fmt.Sprintf("%032x", i)
				record := strings.Replace(traceJSON("finished", `{}`), evidenceRun, id, 1)
				list = append(list, record)
				if large {
					record = strings.Replace(record, `"trace":{}`, `"trace":{},"private_extra":"`+strings.Repeat("x", 700000)+`"`, 1)
				}
				f.details[id] = record
			}
			f.list = "[" + strings.Join(list, ",") + "]"
			got, err := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
			if got.Trace.CoverageStatus != "read_limit" || f.getCount.Load() > 32 || len(got.Trace.CoveredWindows) != 0 {
				t.Fatalf("%+v %v requests %d", got.Trace, err, f.getCount.Load())
			}
			if large {
				if err == nil || f.getCount.Load() >= 32 {
					t.Fatal("aggregate bytes did not bound fetching")
				}
			} else if err != nil || len(got.Trace.Runs) != 32 {
				t.Fatalf("%+v %v", got.Trace, err)
			}
		})
	}
}
func TestDiagnosticSourcesCancellationAndOversizedTrace(t *testing.T) {
	f, m := newDiagnosticFixture(t)
	m.client.observationReads <- struct{}{}
	m.client.observationReads <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := m.ReadAutomationEvidence(ctx, AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	if err == nil || got.Trace.SourceStatus == SourceAvailable || f.getCount.Load() != 0 {
		t.Fatal("cancelled waiter proceeded")
	}
	<-m.client.observationReads
	<-m.client.observationReads
	f.detail = strings.Replace(f.detail, `"trace":`, `"oversized":"`+strings.Repeat("x", (4<<20)+1)+`","trace":`, 1)
	got, err = m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	if err == nil || got.Trace.SourceStatus != SourceUnavailable || len(got.Trace.Runs) != 0 {
		t.Fatalf("oversized trace accepted: %+v %v", got.Trace, err)
	}
}
func TestDiagnosticSourcesStateAllowlist(t *testing.T) {
	f, m := newDiagnosticFixture(t)
	f.state = "PRIVATE"
	got, _ := m.ReadAutomationEvidence(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"}, evidenceWindow)
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "PRIVATE") || got.State.SourceStatus != SourceUnavailable {
		t.Fatal("unrecognized automation state leaked into evidence")
	}
}
