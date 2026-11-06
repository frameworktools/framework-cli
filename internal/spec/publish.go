package spec

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var publishEnvReference = regexp.MustCompile(`^env\(["']([A-Za-z_][A-Za-z0-9_]*)["']\)$`)

// ValidatePublishSecrets keeps credential values out of saved destination settings.
// Call it only on publishing settings, not arbitrary project files or skill content.
func ValidatePublishSecrets(value any) error {
	switch node := value.(type) {
	case map[string]any:
		for key, v := range node {
			normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
			secret := strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") || strings.Contains(normalized, "token") || strings.Contains(normalized, "accesskey") || normalized == "apikey" || normalized == "credential" || normalized == "privatekey"
			if normalized == "credentials" {
				if _, mapping := v.(map[string]any); !mapping {
					secret = true
				}
			}
			if secret && v != nil {
				if text, ok := v.(string); !ok || text != "" && !publishEnvReference.MatchString(text) {
					return fmt.Errorf("publishing destination credentials require env(\"VARIABLE\") references")
				}
			}
			if text, ok := v.(string); ok && (strings.Contains(normalized, "url") || normalized == "endpoint") {
				if parsed, e := url.Parse(text); e == nil && parsed.User != nil {
					if _, set := parsed.User.Password(); set {
						return fmt.Errorf("publishing URLs cannot contain embedded passwords; use environment credentials")
					}
				}
			}
			if e := ValidatePublishSecrets(v); e != nil {
				return e
			}
		}
	case []any:
		for _, v := range node {
			if e := ValidatePublishSecrets(v); e != nil {
				return e
			}
		}
	}
	return nil
}
