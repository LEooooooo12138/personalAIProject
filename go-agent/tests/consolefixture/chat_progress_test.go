package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestProgressMetricsDefaultFollowsFixtureLifetime(t *testing.T) {
	t.Setenv("CONSOLE_FIXTURE_METRICS_PATH", "")
	f, err := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	metrics := filepath.Join(f.dir, "chat-progress-metrics.jsonl")
	if _, err := os.Stat(metrics); err != nil {
		t.Fatalf("default metrics are not inside disposable fixture: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metrics); !os.IsNotExist(err) {
		t.Fatalf("metrics survived fixture cleanup: %v", err)
	}
}

func TestProgressMetricsExplicitEvidencePath(t *testing.T) {
	metrics := filepath.Join(t.TempDir(), "evidence", "progress.jsonl")
	t.Setenv("CONSOLE_FIXTURE_METRICS_PATH", metrics)
	f, err := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if _, err := os.Stat(metrics); err != nil {
		t.Fatalf("explicit evidence path not used: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metrics); err != nil {
		t.Fatalf("explicit browser evidence was removed: %v", err)
	}
}

func TestProgressMetricsRejectsRelativeOverride(t *testing.T) {
	t.Setenv("CONSOLE_FIXTURE_METRICS_PATH", "relative-progress.jsonl")
	original := http.DefaultTransport
	f, err := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081")
	if f != nil {
		_ = f.Close()
	}
	if err == nil {
		t.Fatal("relative metrics override accepted")
	}
	if http.DefaultTransport != original {
		t.Fatal("failed fixture changed transport")
	}
}
