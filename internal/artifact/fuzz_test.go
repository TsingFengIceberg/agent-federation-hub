package artifact

import "testing"

func FuzzMIMEPolicyNeverPanics(f *testing.F) {
	f.Add("text/plain", "text/plain")
	f.Add("application/json", "text/html")
	f.Add("", "application/octet-stream")
	f.Fuzz(func(t *testing.T, declared, detected string) {
		policy := Policy{AllowedMIME: map[string]struct{}{"text/*": {}, "application/json": {}}}
		_ = policy.Validate(32, declared, detected)
	})
}
