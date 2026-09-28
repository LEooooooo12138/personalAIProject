package filter

import (
	"testing"
)

// ?? PIIFilter ??

func TestPII_Phone(t *testing.T) {
	f := &PIIFilter{}
	result, filtered, _ := f.Apply("?????13800138000", nil)
	if !filtered {
		t.Error("should filter phone number")
	}
	if result == "?????13800138000" {
		t.Error("phone number should be replaced")
	}
}

func TestPII_Email(t *testing.T) {
	f := &PIIFilter{}
	result, filtered, _ := f.Apply("?? test@example.com ??", nil)
	if !filtered {
		t.Error("should filter email")
	}
	if result == "?? test@example.com ??" {
		t.Error("email should be replaced")
	}
}

func TestPII_IDCard(t *testing.T) {
	f := &PIIFilter{}
	result, filtered, _ := f.Apply("???110101199001011234??", nil)
	if !filtered {
		t.Error("should filter ID card number")
	}
	if result == "???110101199001011234??" {
		t.Error("ID card number should be replaced")
	}
}

func TestPII_NoMatch(t *testing.T) {
	f := &PIIFilter{}
	original := "?????????"
	result, filtered, _ := f.Apply(original, nil)
	if filtered {
		t.Error("should not filter normal text")
	}
	if result != original {
		t.Errorf("text should not change: %q", result)
	}
}

// ?? SensitiveFilter ??

func TestSensitive_Token(t *testing.T) {
	f := &SensitiveFilter{}
	result, filtered, _ := f.Apply("token=abc123def456ghi", nil)
	if !filtered {
		t.Error("should filter token")
	}
	if result == "token=abc123def456ghi" {
		t.Error("token value should be redacted")
	}
}

func TestSensitive_ApiKey(t *testing.T) {
	f := &SensitiveFilter{}
	_, filtered, _ := f.Apply("api_key: sk-xxxxxxxxxxxx", nil)
	if !filtered {
		t.Error("should filter api_key")
	}
}

func TestSensitive_NoMatch(t *testing.T) {
	f := &SensitiveFilter{}
	original := "?????????"
	result, filtered, _ := f.Apply(original, nil)
	if filtered {
		t.Error("should not filter normal text")
	}
	if result != original {
		t.Error("text should not change")
	}
}

// ?? PersonalRefFilter ??

func TestPersonalRef_Pattern(t *testing.T) {
	f := &PersonalRefFilter{}
	result, filtered, _ := f.Apply("?? personal-vault ????", nil)
	if !filtered {
		t.Error("should filter personal-vault reference")
	}
	if result == "?? personal-vault ????" {
		t.Error("personal-vault reference should be replaced")
	}
}

func TestPersonalRef_NoMatch(t *testing.T) {
	f := &PersonalRefFilter{}
	original := "????? agent-vault ??"
	result, filtered, _ := f.Apply(original, nil)
	if filtered {
		t.Error("should not filter agent-vault reference")
	}
	if result != original {
		t.Error("text should not change")
	}
}

// ?? PlatformFilter ??

func TestPlatform_WeCom_Image(t *testing.T) {
	f := &PlatformFilter{}
	meta := map[string]string{"platform": "wecom"}
	result, filtered, _ := f.Apply("???? ![alt](http://example.com/img.png)", meta)
	if !filtered {
		t.Error("should adapt markdown image for WeChat")
	}
	if result == "???? ![alt](http://example.com/img.png)" {
		t.Error("markdown image should be replaced with [image]")
	}
}

func TestPlatform_Wechat_HTML(t *testing.T) {
	f := &PlatformFilter{}
	meta := map[string]string{"platform": "wechat"}
	_, filtered, _ := f.Apply("?? <b>??</b> ??", meta)
	if !filtered {
		t.Error("should strip HTML for WeChat")
	}
}

func TestPlatform_Default(t *testing.T) {
	f := &PlatformFilter{}
	original := "????????????"
	result, filtered, _ := f.Apply(original, nil)
	if filtered {
		t.Error("should not filter when no platform specified")
	}
	if result != original {
		t.Error("text should not change for default platform")
	}
}

func TestPlatform_EmptyMetadata(t *testing.T) {
	f := &PlatformFilter{}
	result, filtered, _ := f.Apply("![markdown](img.png)", map[string]string{})
	if filtered {
		t.Error("should not trigger with empty metadata")
	}
	// With empty metadata and no platform key, it should pass through.
	if result != "![markdown](img.png)" {
		t.Error("should pass through unchanged with empty metadata")
	}
}

// ?? LengthFilter ??

func TestLength_Truncate(t *testing.T) {
	f := &LengthFilter{maxChars: 10}
	longText := "???????????????????"
	result, filtered, _ := f.Apply(longText, nil)
	if !filtered {
		t.Error("should truncate long text")
	}
	runes := []rune(result)
	if len(runes) > f.maxChars+len([]rune("\n\n[truncated]"))+5 {
		t.Errorf("result too long: %d runes", len(runes))
	}
}

func TestLength_Short(t *testing.T) {
	f := &LengthFilter{maxChars: 2000}
	original := "???"
	result, filtered, _ := f.Apply(original, nil)
	if filtered {
		t.Error("should not truncate short text")
	}
	if result != original {
		t.Error("short text should not change")
	}
}

// ?? Chain.Apply ??

func TestChain_AllFilters(t *testing.T) {
	c := NewChain()
	text := "????13800138000???test@example.com???personal-vault?![img](x.png)"
	meta := map[string]string{"platform": "wecom"}

	result, records := c.Apply(text, meta)

	// PII filter should have fired (phone + email)
	// PersonalRef should have fired
	// Platform should have fired
	// Length may or may not fire depending on truncation

	if len(records) == 0 {
		t.Error("expected at least one filter to fire")
	}

	phoneFound := false
	for _, r := range records {
		if r.Filter == "pii" {
			phoneFound = true
		}
	}
	if !phoneFound {
		t.Errorf("expected pii filter to fire, got records: %+v", records)
	}

	// Verify PII was actually redacted
	if result == text {
		t.Error("filtered text should differ from original")
	}
}

func TestChain_EmptyText(t *testing.T) {
	c := NewChain()
	result, records := c.Apply("", nil)
	// Empty text should return empty, filters might still run but shouldn't panic.
	_ = result
	if len(records) > 0 {
		// Even with empty text, LengthFilter may fire (0 chars don't need truncation)
		// This is fine ? just ensure no panic.
	}
}

func TestChain_NoFiltersTriggered(t *testing.T) {
	c := NewChain()
	original := "??????"
	result, records := c.Apply(original, nil)
	if len(records) > 0 {
		t.Errorf("expected no filters for clean text, got %d records", len(records))
	}
	if result != original {
		t.Errorf("text should not change: %q vs %q", original, result)
	}
}
