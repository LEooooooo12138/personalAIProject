package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"strings"
	"testing"
)

type sharedResultMissingCatalog struct{ suggestionCatalogSource }

func (sharedResultMissingCatalog) GetRegistry(context.Context) (smarthome.RegistrySnapshot, error) {
	return smarthome.RegistrySnapshot{Entities: []smarthome.RegistryEntity{{EntityID: "binary_sensor.house"}}}, nil
}
func (sharedResultMissingCatalog) GetStates(context.Context) ([]smarthome.EntityState, error) {
	return []smarthome.EntityState{{EntityID: "binary_sensor.house", State: "on"}}, nil
}

func TestConsoleSharedResultsFailClosed(t *testing.T) {
	for _, mode := range []string{"stale", "unavailable", "missing_entity", "withdraw_during_read", "logout_during_read"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := suggestionsFixture(t)
			seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("shared", "confirmed")})
			admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			_, member := consoleMemberLogin(t, s, "Member")
			sh, _ := s.consoleStore.GetSharing()
			body, _ := json.Marshal(map[string]any{"revision": sh.Revision, "suggestion_ids": []string{"shared"}})
			consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", string(body), admin, csrf, testConsoleOrigin, ""), 200)
			source := s.areaCatalog
			switch mode {
			case "stale":
				frozen := source.(testAreaCatalog)
				frozen.snapshot.Meta.Freshness = "stale"
				s.areaCatalog = frozen
			case "missing_entity":
				catalog := smarthome.NewCatalogService(context.Background(), sharedResultMissingCatalog{})
				defer catalog.Close()
				snapshot, e := catalog.Get(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				s.areaCatalog = testAreaCatalog{snapshot: snapshot}
			default:
				s.areaCatalog = controlledAreaCatalog(func(ctx context.Context) (smarthome.CatalogSnapshot, error) {
					if mode == "unavailable" {
						return smarthome.CatalogSnapshot{}, errors.New("PRIVATE_CATALOG_ERROR")
					}
					if mode == "logout_during_read" {
						if e := s.consoleStore.Logout(member.Value); e != nil {
							t.Fatal(e)
						}
					} else {
						latest, _ := s.consoleStore.GetSharing()
						body, _ := json.Marshal(map[string]any{"revision": latest.Revision, "suggestion_ids": []string{}})
						consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", string(body), admin, csrf, testConsoleOrigin, ""), 200)
					}
					return source.Catalog(ctx)
				})
			}
			response := consoleRequest(s, "GET", consolePrefix+"/suggestions", "", member, "", "", "")
			want := 200
			if mode == "stale" || mode == "unavailable" {
				want = 503
			}
			if mode == "logout_during_read" {
				want = 401
			}
			payload := consoleJSON(t, response, want)
			if want == 200 && len(payload["shared_results"].([]any)) != 0 {
				t.Fatal("invalidated result published")
			}
			if strings.Contains(response.Body.String(), "light.room") || strings.Contains(response.Body.String(), "PRIVATE_CATALOG_ERROR") {
				t.Fatal("closed scope leaked original payload/error")
			}
		})
	}
}

func TestConsoleSharedResultsPublicGrantRemainsHistorical(t *testing.T) {
	s, writes := newAutomationProjectionFixture(t)
	original := persistedTimeRule("original", "pending")
	seedSuggestions(t, s, []smarthome.RuleSuggestion{original, persistedTimeRule("legacy", "confirmed"), persistedTimeRule("private", "confirmed")})
	if _, err := s.smartHome.ConfirmSuggestion(context.Background(), original.ID); err != nil {
		t.Fatal(err)
	}
	op, _ := s.smartHome.GetInstallOperationForSuggestion(original.ID)
	managed, _ := s.smartHome.GetManagedAutomation(op.ManagedAutomationID)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "Member")
	grant := func(ids []string) {
		t.Helper()
		sh, _ := s.consoleStore.GetSharing()
		body, _ := json.Marshal(map[string]any{"revision": sh.Revision, "suggestion_ids": ids})
		consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", string(body), admin, csrf, testConsoleOrigin, ""), 200)
		after, _ := s.consoleStore.GetSharing()
		if len(after.EntityIDs) != 0 {
			t.Fatal("public result grant added hidden entity ACL")
		}
	}
	read := func() map[string]any {
		t.Helper()
		return consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", member, "", "", ""), 200)
	}
	grant([]string{"original", "legacy"})
	assertResult := func(want string) {
		t.Helper()
		body := read()
		raw, ok := body["shared_results"].([]any)
		if !ok || len(raw) != 2 {
			t.Fatalf("public anchor grant did not reach member: %+v", body)
		}
		if len(body["suggestions"].([]any)) != 0 {
			t.Fatal("historical grant authorized current suggestion")
		}
		var found map[string]any
		for _, entry := range raw {
			row := entry.(map[string]any)
			if row["id"] == "original" {
				found = row
			}
			if row["id"] == "legacy" && row["history_status"] != "legacy_unverified" {
				t.Fatal("legacy result claims verified install")
			}
		}
		if found == nil || found["historical"] != true || found["history_status"] != want {
			t.Fatalf("history truth: %+v", found)
		}
		encoded, _ := json.Marshal(found)
		for _, secret := range []string{"switch.source", "19:30", "current_version", "current_operation", "installation", "observed_state", "diagnostic", "can_confirm", "PRIVATE", "RAW_"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("historical result contains %s: %s", secret, encoded)
			}
		}
		if !strings.Contains(string(encoded), "light.room") || !strings.Contains(string(encoded), "18:00") {
			t.Fatal("original payload not retained")
		}
		list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", member, "", "", ""), 200)
		if len(list["automations"].([]any)) != 0 {
			t.Fatal("anchor-only grant authorized diagnostic row")
		}
		consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations/"+managed.ID+"/diagnostics/latest", "", member, "", "", ""), 404)
	}
	baseline := writes.Load()
	assertResult("recorded")
	if writes.Load() != baseline {
		t.Fatal("result reads wrote HA")
	}
	ctx := smarthome.WithManagementAuthority(context.Background(), smarthome.ManagementAuthority{UserID: "admin", SessionID: "test", Check: func(context.Context) error { return nil }})
	intent := *original.Intent
	intent.EntityID = "switch.source"
	intent.At = "19:30"
	draft, err := s.smartHome.ReviseManagedAutomation(ctx, managed.ID, smarthome.SuggestionRevisionRequest{ExpectedVersion: original.ID, Intent: intent, PresenceBinding: original.PresenceBinding})
	if err != nil {
		t.Fatal(err)
	}
	update, err := s.smartHome.PrepareAutomationAction(ctx, smarthome.AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result, e := s.smartHome.ConfirmAutomationAction(ctx, update.ID); e != nil || result.Status != "confirmed" {
		t.Fatalf("update %+v %v", result, e)
	}
	assertResult("updated")
	// The admin's sharing candidate is the same original payload, independently
	// of the current projection returned beside it.
	adminBody := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)
	for _, entry := range adminBody["shared_results"].([]any) {
		row := entry.(map[string]any)
		if row["id"] == "original" {
			raw, _ := json.Marshal(row)
			if strings.Contains(string(raw), "switch.source") || !strings.Contains(string(raw), "light.room") {
				t.Fatal("admin shares a different payload than member receives")
			}
		}
	}
	managed, _, _ = s.smartHome.GetManagedAutomationVersion(managed.ID)
	deletion, err := s.smartHome.PrepareAutomationAction(ctx, smarthome.AutomationActionRequest{ManagedID: managed.ID, Action: "delete", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	if result, e := s.smartHome.ConfirmAutomationAction(ctx, deletion.ID); e != nil || result.Status != "confirmed" {
		t.Fatalf("delete %+v %v", result, e)
	}
	assertResult("deleted")
	grant([]string{})
	if len(read()["shared_results"].([]any)) != 0 {
		t.Fatal("revoked historical result survived")
	}
}
