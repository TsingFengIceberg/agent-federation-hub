package netpolicy

import "testing"

func FuzzValidateURLNeverPanics(f *testing.F) {
	f.Add("https://example.com/a")
	f.Add("http://127.0.0.1:8080")
	f.Add("https://[::1]/")
	f.Add("not a URL")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = HTTPSOnlyPolicy().ValidateURL(raw)
	})
}
