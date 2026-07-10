package filter

import (
	"fmt"
	"regexp"
	"strings"
)

type Filter interface {
	Name() string
	Apply(text string, metadata map[string]string) (string, bool, string)
}

type Chain struct {
	filters []Filter
}

func NewChain() *Chain {
	return &Chain{
		filters: []Filter{
			&PIIFilter{},
			&SensitiveFilter{},
			&PersonalRefFilter{},
			&PlatformFilter{},
			&LengthFilter{maxChars: 2000},
		},
	}
}

func (c *Chain) Apply(text string, metadata map[string]string) (string, []AuditRecord) {
	var records []AuditRecord
	current := text
	for _, f := range c.filters {
		modified, filtered, reason := f.Apply(current, metadata)
		if filtered {
			records = append(records, AuditRecord{Filter: f.Name(), Reason: reason})
		}
		current = modified
	}
	return current, records
}

type AuditRecord struct {
	Filter string
	Reason string
}

// PII Filter

type PIIFilter struct{}

func (f *PIIFilter) Name() string { return "pii" }

var (
	phoneRe  = regexp.MustCompile(`1[3-9]\d{9}`)
	emailRe  = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	idCardRe = regexp.MustCompile(`\d{17}[\dXx]`)
)

func (f *PIIFilter) Apply(text string, _ map[string]string) (string, bool, string) {
	original := text
	text = phoneRe.ReplaceAllString(text, "[PHONE]")
	text = emailRe.ReplaceAllString(text, "[EMAIL]")
	text = idCardRe.ReplaceAllString(text, "[ID_CARD]")
	if text != original {
		return text, true, "redacted PII"
	}
	return text, false, ""
}

// Sensitive Filter

type SensitiveFilter struct{}

func (f *SensitiveFilter) Name() string { return "sensitive" }

var sensitiveKeywords = []string{
	"password", "secret", "token", "api_key", "api key",
	"private key", "access_key",
}

func (f *SensitiveFilter) Apply(text string, _ map[string]string) (string, bool, string) {
	lower := strings.ToLower(text)
	for _, kw := range sensitiveKeywords {
		if strings.Contains(lower, kw) {
			re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(kw) + `[=:]\s*\S+`)
			if re.MatchString(text) {
				text = re.ReplaceAllString(text, kw+"=[REDACTED]")
				return text, true, fmt.Sprintf("redacted sensitive keyword: %s", kw)
			}
		}
	}
	return text, false, ""
}

// Personal Reference Filter

type PersonalRefFilter struct{}

func (f *PersonalRefFilter) Name() string { return "personal_ref" }

var personalRefPatterns = []string{
	"personal-vault",
	"personal_vault",
}

func (f *PersonalRefFilter) Apply(text string, _ map[string]string) (string, bool, string) {
	original := text
	for _, pat := range personalRefPatterns {
		if strings.Contains(strings.ToLower(text), strings.ToLower(pat)) {
			text = strings.ReplaceAll(text, pat, "[internal-reference]")
		}
	}
	if text != original {
		return text, true, "blocked personal-vault reference"
	}
	return text, false, ""
}

// Platform Filter

type PlatformFilter struct{}

func (f *PlatformFilter) Name() string { return "platform" }

func (f *PlatformFilter) Apply(text string, metadata map[string]string) (string, bool, string) {
	platform := ""
	if metadata != nil {
		platform = metadata["platform"]
	}

	switch platform {
	case "wecom", "wechat":
		return adaptForWeChat(text)
	default:
		return text, false, ""
	}
}

func adaptForWeChat(text string) (string, bool, string) {
	original := text
	re := regexp.MustCompile(`!\[.*?\]\(.*?\)`)
	text = re.ReplaceAllString(text, "[image]")
	reHTML := regexp.MustCompile(`<[^>]+>`)
	text = reHTML.ReplaceAllString(text, "")
	if text != original {
		return text, true, "adapted for WeChat display"
	}
	return text, false, ""
}

// Length Filter

type LengthFilter struct {
	maxChars int
}

func (f *LengthFilter) Name() string { return "length" }

func (f *LengthFilter) Apply(text string, _ map[string]string) (string, bool, string) {
	runes := []rune(text)
	if len(runes) <= f.maxChars {
		return text, false, ""
	}
	truncated := string(runes[:f.maxChars]) + "\n\n[truncated]"
	return truncated, true, fmt.Sprintf("truncated from %d to %d chars", len(runes), f.maxChars)
}
