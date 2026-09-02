package hub

import "testing"

func FuzzTrustBundleDecodeNeverPanics(f *testing.F) {
	f.Add([]byte(`{"version":1,"generation":1,"notBefore":"2026-01-01T00:00:00Z","expiresAt":"2027-01-01T00:00:00Z","issuers":{}}`))
	f.Add([]byte{0, 1, 2, 255})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		_, _ = decodeTrustBundle(encoded)
	})
}
