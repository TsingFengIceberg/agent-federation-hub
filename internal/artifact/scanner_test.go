package artifact

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func TestClamAVInstreamProtocol(t *testing.T) {
	for name, test := range map[string]struct {
		response string
		want     core.ArtifactScanStatus
	}{
		"clean":    {response: "stream: OK\x00", want: core.ArtifactScanClean},
		"infected": {response: "stream: Eicar-Test-Signature FOUND\x00", want: core.ArtifactScanInfected},
	} {
		t.Run(name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				command := make([]byte, len("zINSTREAM\x00"))
				_, _ = io.ReadFull(server, command)
				if string(command) != "zINSTREAM\x00" {
					t.Errorf("command=%q", command)
					return
				}
				var payload strings.Builder
				for {
					var encodedLength [4]byte
					if _, err := io.ReadFull(server, encodedLength[:]); err != nil {
						t.Errorf("read chunk length: %v", err)
						return
					}
					length := binary.BigEndian.Uint32(encodedLength[:])
					if length == 0 {
						break
					}
					chunk := make([]byte, length)
					if _, err := io.ReadFull(server, chunk); err != nil {
						t.Errorf("read chunk: %v", err)
						return
					}
					payload.Write(chunk)
				}
				if payload.String() != "scan me" {
					t.Errorf("payload=%q", payload.String())
					return
				}
				_, _ = io.WriteString(server, test.response)
			}()
			scanner := ClamAVScanner{
				Address: "clamav", Timeout: time.Second,
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					return client, nil
				},
			}
			status, err := scanner.Scan(context.Background(), strings.NewReader("scan me"))
			if err != nil || status != test.want {
				t.Fatalf("status=%s err=%v", status, err)
			}
			<-done
		})
	}
}
