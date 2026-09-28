package core

import (
	"testing"
)

// ?? ModelRouter.Decide ??

func TestDecide_ExplicitModel(t *testing.T) {
	r := NewModelRouter("gemma4:12b", "llava:7b")
	d := r.Decide(nil, "gemma4:12b", nil)

	if d.TargetModel != "gemma4:12b" {
		t.Errorf("TargetModel = %q, want gemma4:12b", d.TargetModel)
	}
	if d.Backend != "local" {
		t.Errorf("Backend = %q, want local", d.Backend)
	}
	if d.Fallback != "fail" {
		t.Errorf("Fallback = %q, want fail", d.Fallback)
	}
	if d.Reason != "explicit model hint" {
		t.Errorf("Reason = %q, want explicit model hint", d.Reason)
	}
}

func TestDecide_CloudHint(t *testing.T) {
	r := NewModelRouter("gemma4:12b", "llava:7b")
	d := r.Decide(nil, "cloud", nil)

	if d.TargetModel != "deepseek-chat" {
		t.Errorf("TargetModel = %q, want deepseek-chat", d.TargetModel)
	}
	if d.Backend != "cloud" {
		t.Errorf("Backend = %q, want cloud", d.Backend)
	}
	if d.Fallback != "fail" {
		t.Errorf("Fallback = %q, want fail", d.Fallback)
	}
	if d.Reason == "" {
		t.Error("Reason should not be empty")
	}
}

func TestDecide_ImageDetection_ImageURL(t *testing.T) {
	r := NewModelRouter("gemma4:12b", "llava:7b")
	// Body large enough (>100 bytes) with "image_url" keyword.
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/img.png"}}]}],"_pad":"` + string(make([]byte, 80)) + `"}`)
	d := r.Decide(body, "auto", nil)

	if d.TargetModel != "llava:7b" {
		t.Errorf("TargetModel = %q, want llava:7b", d.TargetModel)
	}
	if d.Backend != "local" {
		t.Errorf("Backend = %q, want local", d.Backend)
	}
}

func TestDecide_ImageDetection_Base64(t *testing.T) {
	r := NewModelRouter("gemma4:12b", "llava:7b")
	// Body large enough (>100 bytes) with "data:image/" pattern.
	body := []byte(`{"messages":[{"role":"user","content":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}],"_pad":"` + string(make([]byte, 80)) + `"}`)
	d := r.Decide(body, "auto", nil)

	if d.TargetModel != "llava:7b" {
		t.Errorf("TargetModel = %q, want vision model for base64 image", d.TargetModel)
	}
}

func TestDecide_SensitiveContent(t *testing.T) {
	r := NewModelRouter("gemma4:12b", "llava:7b")
	meta := map[string]string{"sensitive": "true"}
	d := r.Decide(nil, "auto", meta)

	if d.TargetModel != "gemma4:12b" {
		t.Errorf("TargetModel = %q, want gemma4:12b for sensitive", d.TargetModel)
	}
	if d.Backend != "local" {
		t.Errorf("Backend = %q, want local for sensitive content", d.Backend)
	}
}

func TestDecide_SensitiveSkipsImage(t *testing.T) {
	// Image check runs before sensitive check in Decide().
	// Verify image priority: image + sensitive still routes to vision.
	r := NewModelRouter("gemma4:12b", "llava:7b")
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}],"_pad":"` + string(make([]byte, 80)) + `"}`)
	meta := map[string]string{"sensitive": "true"}
	d := r.Decide(body, "auto", meta)

	if d.TargetModel != "llava:7b" {
		t.Errorf("TargetModel = %q, want llava:7b (image priority over sensitive)", d.TargetModel)
	}
}

func TestDecide_SkillOperation(t *testing.T) {
	r := NewModelRouter("gemma4:12b", "llava:7b")
	meta := map[string]string{"skill": "wiki-query"}
	d := r.Decide(nil, "auto", meta)

	if d.TargetModel != "gemma4:12b" {
		t.Errorf("TargetModel = %q, want gemma4:12b for skill", d.TargetModel)
	}
	if d.Backend != "local" {
		t.Errorf("Backend = %q, want local for skill", d.Backend)
	}
}

func TestDecide_AutoDefault(t *testing.T) {
	r := NewModelRouter("gemma4:12b", "llava:7b")
	d := r.Decide(nil, "auto", nil)

	if d.TargetModel != "gemma4:12b" {
		t.Errorf("TargetModel = %q, want gemma4:12b", d.TargetModel)
	}
	if d.Backend != "local" {
		t.Errorf("Backend = %q, want local", d.Backend)
	}
	if d.Reason == "" {
		t.Error("Reason should not be empty")
	}
}

func TestDecide_CustomRouter(t *testing.T) {
	r := NewModelRouter("custom-chat", "custom-vision")
	d := r.Decide(nil, "auto", nil)

	if d.TargetModel != "custom-chat" {
		t.Errorf("TargetModel = %q, want custom-chat", d.TargetModel)
	}
}

func TestDecide_EmptyHint(t *testing.T) {
	r := NewModelRouter("gemma4:12b", "llava:7b")
	d := r.Decide(nil, "", nil)

	if d.TargetModel != "gemma4:12b" {
		t.Errorf("TargetModel = %q, want default for empty hint", d.TargetModel)
	}
}

// ?? hasImage ??

func TestHasImage_SmallBody(t *testing.T) {
	if hasImage([]byte("short")) {
		t.Error("small body should not detect image")
	}
}

func TestHasImage_WithImageURL(t *testing.T) {
	body := []byte(`{"messages":[{"image_url":"http://x.com/img.png"}],"_pad":"` + string(make([]byte, 80)) + `"}`)
	if !hasImage(body) {
		t.Error("should detect image_url in body")
	}
}

func TestHasImage_WithBase64(t *testing.T) {
	body := []byte(`{"messages":[{"content":"data:image/png;base64,xxx"}],"_pad":"` + string(make([]byte, 80)) + `"}`)
	if !hasImage(body) {
		t.Error("should detect data:image/ base64 in body")
	}
}

func TestHasImage_NoImage(t *testing.T) {
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)
	if hasImage(body) {
		t.Error("plain text body should not detect image")
	}
}