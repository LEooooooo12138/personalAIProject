package smarthome

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Stop must both cancel an active request and wait until it has unwound.
// The transport holds that unwind so the assertion does not race the scheduler.
func TestSmartHomeStopCancelsAndWaitsForCollection(t *testing.T) {
	for _, component := range []string{"manager", "collector"} {
		t.Run(component, func(t *testing.T) {
			m, err := NewManager(HAConfig{BaseURL: "http://ha.invalid", AgentVaultPath: t.TempDir()}, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			m.client.httpClient.Transport = lifecycleTransport(func(r *http.Request) (*http.Response, error) {
				close(entered)
				<-r.Context().Done()
				close(canceled)
				<-release
				return nil, r.Context().Err()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start, stop := m.Start, m.Stop
			if component == "collector" {
				start, stop = m.collector.Start, m.collector.Stop
			}
			start(ctx)
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("initial collection did not enter the request")
			}
			stopped := make(chan struct{})
			go func() { stop(); close(stopped) }()
			select {
			case <-stopped:
				t.Error("Stop returned while collection was still running")
			case <-canceled:
				select {
				case <-stopped:
					t.Error("Stop returned before the canceled request unwound")
				case <-time.After(25 * time.Millisecond):
				}
			case <-time.After(time.Second):
				t.Error("Stop did not cancel the active collection")
			}
			cancel()
			unblock()
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("Stop did not finish after collection exited")
			}
			stop() // Repeated shutdown must be safe and return immediately.
		})
	}
}

func TestRuleDocumentPreservesFrontmatterStrings(t *testing.T) {
	s := decodeSuggestion(t, executableSuggestion)
	s.Title = "Kitchen \"night\"\nSecond line"
	s.Trigger = "clock \\ schedule"
	s.Action = "switch.kitchen\nstatus: corrupted"
	s.DataSource = "history: \"verified\""
	s.HAAutomationID = "rule-kitchen"
	parts := strings.SplitN(RuleDocument(s), "---", 3)
	if len(parts) != 3 {
		t.Fatal("missing frontmatter")
	}
	var metadata map[string]interface{}
	if err := yaml.Unmarshal([]byte(parts[1]), &metadata); err != nil {
		t.Fatalf("invalid frontmatter: %v", err)
	}
	for key, want := range map[string]string{"title": s.Title, "trigger": s.Trigger, "action": s.Action, "data_source": s.DataSource, "status": "active"} {
		if metadata[key] != want {
			t.Errorf("%s=%q want %q", key, metadata[key], want)
		}
	}
}
