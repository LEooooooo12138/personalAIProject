package memory

import (
	"testing"
)

func TestExtractJSON_Valid(t *testing.T) {
	input := `{"title":"test","decisions":"x","follow_ups":"y"}`
	sr, err := extractJSON(input)
	if err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	if sr.Title != "test" {
		t.Errorf("Title = %q, want test", sr.Title)
	}
	if sr.Decisions != "x" {
		t.Errorf("Decisions = %q, want x", sr.Decisions)
	}
	if sr.FollowUps != "y" {
		t.Errorf("FollowUps = %q, want y", sr.FollowUps)
	}
}

func TestExtractJSON_WithExtraText(t *testing.T) {
	input := `???? {"title":"t","decisions":"d","follow_ups":"f"} ????`
	sr, err := extractJSON(input)
	if err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	if sr.Title != "t" {
		t.Errorf("Title = %q, want t", sr.Title)
	}
}

func TestExtractJSON_Invalid(t *testing.T) {
	_, err := extractJSON("not json at all")
	if err == nil {
		t.Error("expected error for non-JSON input")
	}
}