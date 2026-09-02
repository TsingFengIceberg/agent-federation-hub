package aampfederation

import "testing"

func FuzzDecodeMailEventNeverPanics(f *testing.F) {
	f.Add([]byte(`{"version":"1.1","intent":"task.ack","taskId":"task-1","messageId":"message-1"}`))
	f.Add([]byte("not-json"))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = DecodeMailEvent(Envelope{Body: body})
	})
}
