package filter

import (
	"strings"
	"testing"
)

func TestSensitiveRedactsEscapedQuotedValue(t *testing.T) {
	for _, input := range []string{`{"password":"prefix\"secret-tail"}`, `secret='prefix\'secret-tail'`} {
		got, _, _ := (&SensitiveFilter{}).Apply(input, nil)
		if strings.Contains(got, "prefix") || strings.Contains(got, "secret-tail") {
			t.Errorf("quoted value leaked: %s", got)
		}
	}
}

func TestSensitiveRedactsAllFields(t *testing.T) {
	input := `password=alpha token=beta API_KEY : "gamma value" secret='delta value'`
	got, changed, _ := (&SensitiveFilter{}).Apply(input, nil)
	for _, secret := range []string{"alpha", "beta", "gamma value", "delta value"} {
		if strings.Contains(got, secret) {
			t.Errorf("secret %q remains in %q", secret, got)
		}
	}
	if !changed || strings.Count(got, "[REDACTED]") != 4 {
		t.Fatalf("not all fields redacted: %q", got)
	}
}

func TestPersonalReferencesAreCaseInsensitive(t *testing.T) {
	got, changed, _ := (&PersonalRefFilter{}).Apply("PERSONAL-VAULT Personal_Vault personal-vault", nil)
	if !changed || strings.Count(got, "[internal-reference]") != 3 {
		t.Fatalf("reference escaped filter: %q", got)
	}
}
