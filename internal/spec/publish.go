package spec

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidatePublishSecrets keeps credential values out of saved destination
// settings: a credential key (CredentialKey) must hold env("VARIABLE") or
// env("VARIABLE", ""), or nothing written out (LiteralSecret), and URLs
// can't embed passwords. Call it only on publishing settings, not arbitrary
// project files or skill content.
func ValidatePublishSecrets(value any) error {
	switch node := value.(type) {
	case map[string]any:
		for key, v := range node {
			normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
			if CredentialKey(key) && LiteralSecret(v) {
				return fmt.Errorf("publishing destination credentials require env(\"VARIABLE\") references")
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
