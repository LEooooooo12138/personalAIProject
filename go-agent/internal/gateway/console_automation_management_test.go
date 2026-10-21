package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func managementRequestJSON(t *testing.T, s *Server, method, path, body string, cookie *http.Cookie, csrf string, want int) map[string]any {
	t.Helper()
	return consoleJSON(t, consoleRequest(s, method, path, body, cookie, csrf, testConsoleOrigin, ""), want)
}

func TestManagementGatewayPostWritePersistenceFailurePreservesObservedAcknowledgment(t *testing.T) {
	s, f, m := newConsoleManagementFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	body := fmt.Sprintf(`{"action":"enable","expected_digest":%q}`, m.ExpectedDigest)
	op := managementRequestJSON(t, s, "POST", consolePrefix+"/admin/automations/"+m.ID+"/actions", body, admin, csrf, 200)["operation"].(map[string]any)
	endpoint := consolePrefix + "/admin/automation-actions/" + op["id"].(string)
	path := filepath.Join(f.archivePath, "rules", "install-state.json")
	saved := path + ".attempt"
	f.mu.Lock()
	f.beforeWrite = func() {
		if err := os.Rename(path, saved); err != nil {
			t.Error(err)
			return
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Error(err)
		}
	}
	f.mu.Unlock()
	response := consoleRequest(s, "POST", endpoint+"/confirm", `{}`, admin, csrf, testConsoleOrigin, "")
	result := consoleJSON(t, response, 503)
	partial, ok := result["operation"].(map[string]any)
	if !ok {
		t.Fatalf("lost HA acknowledgement when journal unreadable: %s", response.Body.String())
	}
	if partial["id"] != op["id"] || partial["write_status"] != "acknowledged" || partial["local_persistence"] != "unverified" || partial["result_source"] != "response_observation" || partial["status"] != "unknown" || partial["attempted_at"] == nil {
		t.Fatalf("partial facts %+v", partial)
	}
	if result["error"].(map[string]any)["code"] != "management_persistence_unverified" {
		t.Fatalf("local failure code %+v", result)
	}
	for _, capability := range []string{"can_confirm", "can_cancel", "can_reconcile"} {
		if partial[capability] != false {
			t.Fatalf("uncertain persistence granted %s", capability)
		}
	}
	for _, private := range []string{"user_id", "session_id", "config_key", "backup_ref", "raw_config", f.archivePath, admin.Value, "RAW_"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("partial result leak %q", private)
		}
	}
	if _, w := f.counts(); w != 1 {
		t.Fatalf("write count %d", w)
	}
	// Recovery cannot read the obstructed journal either; the observed response
	// must not depend on another successful local read to retain the HA fact.
	managementRequestJSON(t, s, "GET", endpoint, "", admin, "", 503)
	managementRequestJSON(t, s, "POST", endpoint+"/confirm", `{}`, admin, csrf, 503)
	f.mu.Lock()
	f.beforeWrite = nil
	f.mu.Unlock()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, path); err != nil {
		t.Fatal(err)
	}
	managementRequestJSON(t, s, "POST", endpoint+"/confirm", `{}`, admin, csrf, 409)
	recovered := managementRequestJSON(t, s, "GET", endpoint, "", admin, "", 200)["operation"].(map[string]any)
	if recovered["status"] != "applying" || recovered["write_status"] != "unknown" {
		t.Fatalf("unpersisted acknowledgement claimed durable: %+v", recovered)
	}
	reconciled := managementRequestJSON(t, s, "POST", endpoint+"/reconcile", `{}`, admin, csrf, 200)["operation"].(map[string]any)
	if reconciled["status"] != "confirmed" {
		t.Fatalf("readonly recovery %+v", reconciled)
	}
	if _, w := f.counts(); w != 1 {
		t.Fatal("persistence failure replayed HA write")
	}
}

func TestManagementGatewayPartialResultRequiresCurrentSession(t *testing.T) {
	s, f, m := newConsoleManagementFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	body := fmt.Sprintf(`{"action":"enable","expected_digest":%q}`, m.ExpectedDigest)
	op := managementRequestJSON(t, s, "POST", consolePrefix+"/admin/automations/"+m.ID+"/actions", body, admin, csrf, 200)["operation"].(map[string]any)
	endpoint := consolePrefix + "/admin/automation-actions/" + op["id"].(string)
	path := filepath.Join(f.archivePath, "rules", "install-state.json")
	f.mu.Lock()
	f.beforeWrite = func() {
		if err := os.Rename(path, path+".attempt"); err != nil {
			t.Error(err)
			return
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Error(err)
		}
		if err := s.consoleStore.Logout(admin.Value); err != nil {
			t.Error(err)
		}
	}
	f.mu.Unlock()
	response := consoleRequest(s, "POST", endpoint+"/confirm", `{}`, admin, csrf, testConsoleOrigin, "")
	data := consoleJSON(t, response, 401)
	if data["operation"] != nil || strings.Contains(response.Body.String(), op["id"].(string)) {
		t.Fatal("revoked session received private partial operation")
	}
	if _, w := f.counts(); w != 1 {
		t.Fatalf("fixture did not reach the single authorized write: %d", w)
	}
}

func TestManagementGatewayAllRoutesEnforceCookieAdminOriginAndCSRF(t *testing.T) {
	s, f, m := newConsoleManagementFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	consoleMemberLogin(t, s, "Member")
	member, memberCSRF, _ := consoleLogin(t, s, "Member", testConsolePassword, testConsoleOrigin)
	base := consolePrefix + "/admin/automations/" + m.ID
	routes := []struct{ method, path string }{{"GET", base + "/management"}, {"POST", base + "/revisions"}, {"POST", base + "/actions"}, {"GET", consolePrefix + "/admin/automation-actions/missing"}, {"POST", consolePrefix + "/admin/automation-actions/missing/confirm"}, {"POST", consolePrefix + "/admin/automation-actions/missing/cancel"}, {"POST", consolePrefix + "/admin/automation-actions/missing/reconcile"}}
	beforeR, beforeW := f.counts()
	for _, route := range routes {
		consoleJSON(t, consoleRequest(s, route.method, route.path, `{}`, nil, "", testConsoleOrigin, "test-key"), 401)
		consoleJSON(t, consoleRequest(s, route.method, route.path, `{}`, member, memberCSRF, testConsoleOrigin, ""), 403)
		if route.method == "POST" {
			consoleJSON(t, consoleRequest(s, route.method, route.path, `{}`, admin, "", testConsoleOrigin, ""), 403)
			consoleJSON(t, consoleRequest(s, route.method, route.path, `{}`, admin, csrf, "https://wrong.example", ""), 403)
		}
	}
	if r, w := f.counts(); r != beforeR || w != beforeW {
		t.Fatal("denied route contacted HA")
	}
	for _, body := range []string{
		fmt.Sprintf(`{"action":"disable","expected_digest":%q}`, m.ExpectedDigest),
		fmt.Sprintf(`{"action":"disable","expected_digest":%q,"stop_actions":null}`, m.ExpectedDigest),
		fmt.Sprintf(`{"action":"enable","expected_digest":%q,"user_id":"forged"}`, m.ExpectedDigest),
		fmt.Sprintf(`{"action":"enable","expected_digest":%q,"managed_id":%q}`, m.ExpectedDigest, m.ID),
		fmt.Sprintf(`{"action":"enable","action":"delete","expected_digest":%q}`, m.ExpectedDigest),
	} {
		managementRequestJSON(t, s, "POST", base+"/actions", body, admin, csrf, 400)
	}
	if _, w := f.counts(); w != 0 {
		t.Fatal("invalid body wrote HA")
	}
}

func TestManagementGatewaySessionBoundReviewAndRotatedUnknownRecovery(t *testing.T) {
	s, f, m := newConsoleManagementFixture(t)
	original, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	rotated, newCSRF, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	base := consolePrefix + "/admin/automations/" + m.ID
	req := fmt.Sprintf(`{"action":"enable","expected_digest":%q}`, m.ExpectedDigest)
	op := managementRequestJSON(t, s, "POST", base+"/actions", req, original, csrf, 200)["operation"].(map[string]any)
	endpoint := consolePrefix + "/admin/automation-actions/" + op["id"].(string)
	managementRequestJSON(t, s, "POST", endpoint+"/confirm", `{}`, rotated, newCSRF, 403)
	got := managementRequestJSON(t, s, "GET", endpoint, "", rotated, "", 200)["operation"].(map[string]any)
	if got["can_confirm"] != false || got["can_cancel"] != true {
		t.Fatalf("rotated capabilities %+v", got)
	}
	managementRequestJSON(t, s, "POST", endpoint+"/cancel", `{}`, rotated, newCSRF, 200)
	if _, w := f.counts(); w != 0 {
		t.Fatal("rotated review wrote HA")
	}
	op = managementRequestJSON(t, s, "POST", base+"/actions", req, original, csrf, 200)["operation"].(map[string]any)
	endpoint = consolePrefix + "/admin/automation-actions/" + op["id"].(string)
	f.mu.Lock()
	f.loseResponse = true
	f.mu.Unlock()
	result := managementRequestJSON(t, s, "POST", endpoint+"/confirm", `{}`, original, csrf, 200)["operation"].(map[string]any)
	if result["status"] != "unknown" || result["can_reconcile"] != true || result["can_confirm"] != false {
		t.Fatalf("ambiguous result %+v", result)
	}
	managementRequestJSON(t, s, "POST", endpoint+"/confirm", `{}`, original, csrf, 409)
	managementRequestJSON(t, s, "POST", endpoint+"/cancel", `{}`, rotated, newCSRF, 409)
	managementRequestJSON(t, s, "POST", base+"/actions", req, rotated, newCSRF, 409)
	if err := s.consoleStore.Logout(original.Value); err != nil {
		t.Fatal(err)
	}
	beforeR, beforeW := f.counts()
	recovered := managementRequestJSON(t, s, "GET", base+"/management", "", rotated, "", 200)["management"].(map[string]any)
	if recovered["latest_operation"].(map[string]any)["id"] != op["id"] {
		t.Fatal("lost unknown operation not recovered")
	}
	if r, w := f.counts(); r != beforeR || w != beforeW {
		t.Fatal("unknown GET read or wrote HA")
	}
	f.mu.Lock()
	f.loseResponse = false
	f.mu.Unlock()
	reconciled := managementRequestJSON(t, s, "POST", endpoint+"/reconcile", `{}`, rotated, newCSRF, 200)["operation"].(map[string]any)
	if reconciled["status"] != "confirmed" {
		t.Fatalf("readonly reconcile %+v", reconciled)
	}
	terminal := managementRequestJSON(t, s, "GET", base+"/management", "", rotated, "", 200)["management"].(map[string]any)["latest_operation"].(map[string]any)
	if terminal["id"] != op["id"] || terminal["status"] != "confirmed" {
		t.Fatal("terminal outcome not recoverable")
	}
	if _, w := f.counts(); w != 1 {
		t.Fatal("unknown operation replayed")
	}
}

func TestManagementGatewayFreshAuthorityAfterSlowObservation(t *testing.T) {
	for _, duringRead := range []bool{false, true} {
		t.Run(fmt.Sprint(duringRead), func(t *testing.T) {
			s, f, m := newConsoleManagementFixture(t)
			admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			base := consolePrefix + "/admin/automations/" + m.ID
			body := fmt.Sprintf(`{"action":"enable","expected_digest":%q}`, m.ExpectedDigest)
			op := managementRequestJSON(t, s, "POST", base+"/actions", body, admin, csrf, 200)["operation"].(map[string]any)
			revoke := func() {
				if err := s.consoleStore.Logout(admin.Value); err != nil {
					t.Error(err)
				}
			}
			if duringRead {
				f.mu.Lock()
				f.onRead = revoke
				f.mu.Unlock()
			} else {
				revoke()
			}
			endpoint := consolePrefix + "/admin/automation-actions/" + op["id"].(string)
			managementRequestJSON(t, s, "POST", endpoint+"/confirm", `{}`, admin, csrf, 401)
			if _, w := f.counts(); w != 0 {
				t.Fatal("revoked session wrote HA")
			}
			rotated, newCSRF, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			got := managementRequestJSON(t, s, "GET", endpoint, "", rotated, "", 200)["operation"].(map[string]any)
			if got["status"] != "prepared" || got["attempted_at"] != nil {
				t.Fatalf("revoked attempt persisted %+v", got)
			}
			managementRequestJSON(t, s, "POST", endpoint+"/cancel", `{}`, rotated, newCSRF, 200)
		})
	}
}

func TestManagementGatewayManagedDraftCurrentVersionAndTombstone(t *testing.T) {
	s, f, m := newConsoleManagementFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	rotated, newCSRF, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	sharing, err := s.consoleStore.GetSharing()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.consoleStore.PutSharing(sharing.Revision, []string{"light.room", "binary_sensor.house"}, []string{m.SuggestionID}); err != nil {
		t.Fatal(err)
	}
	base := consolePrefix + "/admin/automations/" + m.ID
	revision := fmt.Sprintf(`{"expected_version":%q,"intent":{"schema_version":1,"kind":"time","entity_id":"switch.source","at":"19:00","time_zone":"UTC","required_conditions":["presence_home"]},"presence_binding":{"entity_id":"binary_sensor.house","state":"on"}}`, m.SuggestionVersion)
	draft := managementRequestJSON(t, s, "POST", base+"/revisions", revision, admin, csrf, 200)["draft"].(map[string]any)
	r0, w0 := f.counts()
	got := managementRequestJSON(t, s, "GET", base+"/management", "", admin, "", 200)["management"].(map[string]any)
	if got["draft"].(map[string]any)["id"] != draft["id"] || got["current_version"] != m.SuggestionVersion {
		t.Fatal("draft replaced installed version or was not recovered")
	}
	other := managementRequestJSON(t, s, "GET", base+"/management", "", rotated, "", 200)["management"].(map[string]any)
	if other["draft"] != nil {
		t.Fatal("other session recovered private draft")
	}
	if r, w := f.counts(); r != r0 || w != w0 || w != 0 {
		t.Fatal("draft/recovery wrote or recovery read HA")
	}
	req := fmt.Sprintf(`{"action":"update","expected_digest":%q,"revision_id":%q}`, m.ExpectedDigest, draft["id"])
	managementRequestJSON(t, s, "POST", base+"/actions", req, rotated, newCSRF, 403)
	op := managementRequestJSON(t, s, "POST", base+"/actions", req, admin, csrf, 200)["operation"].(map[string]any)
	if op["revision"].(map[string]any)["id"] != draft["id"] {
		t.Fatal("prepared update lacks exact reviewed version")
	}
	endpoint := consolePrefix + "/admin/automation-actions/" + op["id"].(string)
	updated := managementRequestJSON(t, s, "POST", endpoint+"/confirm", `{}`, admin, csrf, 200)["operation"].(map[string]any)
	if updated["status"] != "confirmed" || updated["archive_status"] != "complete" {
		t.Fatalf("update facts %+v", updated)
	}
	got = managementRequestJSON(t, s, "GET", base+"/management", "", rotated, "", 200)["management"].(map[string]any)
	intent := got["current"].(map[string]any)["intent"].(map[string]any)
	if got["suggestion_id"] != m.SuggestionID || got["current_version"] != draft["id"] || got["shared"] != true || intent["entity_id"] != "switch.source" || got["draft"] != nil {
		t.Fatalf("current version/anchor %+v", got)
	}
	suggestions, err := s.smartHome.GetStore().GetSuggestions()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range suggestions {
		if item.ID == draft["id"] {
			t.Fatal("managed draft entered ordinary suggestions")
		}
	}
	req = fmt.Sprintf(`{"action":"delete","expected_digest":%q}`, got["expected_digest"])
	op = managementRequestJSON(t, s, "POST", base+"/actions", req, rotated, newCSRF, 200)["operation"].(map[string]any)
	endpoint = consolePrefix + "/admin/automation-actions/" + op["id"].(string)
	deleted := managementRequestJSON(t, s, "POST", endpoint+"/confirm", `{}`, rotated, newCSRF, 200)["operation"].(map[string]any)
	if deleted["status"] != "confirmed" || deleted["backup_status"] != "saved" {
		t.Fatalf("deletion %+v", deleted)
	}
	data := managementRequestJSON(t, s, "GET", base+"/management", "", rotated, "", 200)["management"].(map[string]any)
	if data["deleted_at"] == nil || data["can_prepare"] != false || data["current_version"] != draft["id"] {
		t.Fatalf("tombstone %+v", data)
	}
	managementRequestJSON(t, s, "POST", base+"/actions", req, rotated, newCSRF, 409)
	if _, w := f.counts(); w != 2 {
		t.Fatalf("update/delete write count %d", w)
	}
	raw, _ := json.Marshal(data)
	for _, private := range []string{"user_id", "session_id", "config_key", "backup_ref", "raw_config", "RAW_", "fixture-only"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("tombstone leak %s", private)
		}
	}
}

func TestManagementGatewayLocalRecoveryAndSeparateConfirmation(t *testing.T) {
	s, f, m := newConsoleManagementFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	base := consolePrefix + "/admin/automations/" + m.ID
	beforeReads, beforeWrites := f.counts()
	data := consoleJSON(t, consoleRequest(s, "GET", base+"/management", "", admin, "", "", ""), 200)["management"].(map[string]any)
	if data["current_version"] != m.SuggestionVersion || data["latest_operation"] != nil || data["draft"] != nil {
		t.Fatalf("initial local recovery %+v", data)
	}
	if r, w := f.counts(); r != beforeReads || w != beforeWrites {
		t.Fatal("local recovery contacted HA")
	}
	req := fmt.Sprintf(`{"action":"disable","expected_digest":%q,"stop_actions":false}`, m.ExpectedDigest)
	prepared := consoleJSON(t, consoleRequest(s, "POST", base+"/actions", req, admin, csrf, testConsoleOrigin, ""), 200)["operation"].(map[string]any)
	id := prepared["id"].(string)
	created, _ := time.Parse(time.RFC3339Nano, prepared["created_at"].(string))
	expires, _ := time.Parse(time.RFC3339Nano, prepared["expires_at"].(string))
	if prepared["status"] != "prepared" || prepared["can_confirm"] != true || expires.Sub(created) != 5*time.Minute {
		t.Fatalf("review %+v", prepared)
	}
	r0, w0 := f.counts()
	if w0 != 0 {
		t.Fatal("prepare wrote HA")
	}
	recovered := consoleRequest(s, "GET", base+"/management", "", admin, "", "", "")
	data = consoleJSON(t, recovered, 200)["management"].(map[string]any)
	if data["latest_operation"].(map[string]any)["id"] != id {
		t.Fatal("lost ID not recovered")
	}
	for _, private := range []string{"user_id", "session_id", "config_key", "raw_config", "backup_ref", "RAW_", "fixture-only"} {
		if strings.Contains(recovered.Body.String(), private) {
			t.Fatalf("private DTO field %s", private)
		}
	}
	if r, w := f.counts(); r != r0 || w != w0 {
		t.Fatal("recovery contacted HA")
	}
	managementRequestJSON(t, s, "POST", base+"/actions", req, admin, csrf, 409)
	if r, w := f.counts(); r != r0 || w != w0 {
		t.Fatal("duplicate prepare contacted HA or replaced review")
	}
	endpoint := consolePrefix + "/admin/automation-actions/" + id
	result := consoleJSON(t, consoleRequest(s, "POST", endpoint+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 200)["operation"].(map[string]any)
	if result["status"] != "confirmed" || result["write_status"] != "acknowledged" {
		t.Fatalf("confirmation %+v", result)
	}
	if _, w := f.counts(); w != 1 {
		t.Fatalf("write count %d", w)
	}
	f.mu.Lock()
	payload := f.payload
	f.mu.Unlock()
	if payload["stop_actions"] != false || payload["entity_id"] != "automation.managed_fixture" {
		t.Fatalf("disable payload %+v", payload)
	}
	consoleJSON(t, consoleRequest(s, "POST", endpoint+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 409)
	consoleJSON(t, consoleRequest(s, "GET", endpoint, "", admin, "", "", ""), 200)
	if _, w := f.counts(); w != 1 {
		t.Fatal("repeat confirmation replayed")
	}
}

func TestManagementGatewayExternalDriftAndClosedFailures(t *testing.T) {
	s, f, m := newConsoleManagementFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	base := consolePrefix + "/admin/automations/" + m.ID
	managementRequestJSON(t, s, "GET", consolePrefix+"/admin/automations/not-owned/management", "", admin, "", 404)
	req := fmt.Sprintf(`{"action":"enable","expected_digest":%q}`, m.ExpectedDigest)
	op := managementRequestJSON(t, s, "POST", base+"/actions", req, admin, csrf, 200)["operation"].(map[string]any)
	f.mu.Lock()
	var config map[string]any
	err := json.Unmarshal(f.config, &config)
	if err == nil {
		config["private_external_note"] = "UPSTREAM_SECRET"
		f.config, err = json.Marshal(config)
	}
	f.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := consolePrefix + "/admin/automation-actions/" + op["id"].(string)
	response := consoleRequest(s, "POST", endpoint+"/confirm", `{}`, admin, csrf, testConsoleOrigin, "")
	consoleJSON(t, response, 409)
	if strings.Contains(response.Body.String(), "UPSTREAM_SECRET") || strings.Contains(response.Body.String(), "private_external_note") {
		t.Fatal("upstream failure leaked")
	}
	recovered := managementRequestJSON(t, s, "GET", endpoint, "", admin, "", 200)["operation"].(map[string]any)
	if recovered["attempted_at"] != nil || recovered["status"] != "prepared" {
		t.Fatalf("drift consumed review %+v", recovered)
	}
	if _, w := f.counts(); w != 0 {
		t.Fatal("drift wrote HA")
	}
}
