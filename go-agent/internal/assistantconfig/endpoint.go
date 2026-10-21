package assistantconfig

import "strings"

// MiMoBaseURL only resolves documented official endpoints. Keys never determine
// arbitrary hosts, and explicit choices must agree with the credential type.
func MiMoBaseURL(endpoint, key string) (string, error) {
	plan := strings.HasPrefix(key, "tp-") || strings.HasPrefix(key, "ttp-")
	switch endpoint {
	case "", "auto":
		if plan {
			return "https://token-plan-cn.xiaomimimo.com/v1", nil
		}
		return "https://api.xiaomimimo.com/v1", nil
	case "payg":
		if plan {
			return "", ErrEndpointMismatch
		}
		return "https://api.xiaomimimo.com/v1", nil
	case "token_cn", "token_sgp", "token_ams":
		if key != "" && !plan {
			return "", ErrEndpointMismatch
		}
		region := strings.TrimPrefix(endpoint, "token_")
		return "https://token-plan-" + region + ".xiaomimimo.com/v1", nil
	default:
		return "", ErrInvalid
	}
}
