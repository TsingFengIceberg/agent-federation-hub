package artifact

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type Scanner interface {
	Scan(context.Context, io.Reader) (core.ArtifactScanStatus, error)
}

type NoopScanner struct{}

func (NoopScanner) Scan(context.Context, io.Reader) (core.ArtifactScanStatus, error) {
	return core.ArtifactScanNotScanned, nil
}

type ClamAVScanner struct {
	Network     string
	Address     string
	Timeout     time.Duration
	DialContext func(context.Context, string, string) (net.Conn, error)
}

func (s ClamAVScanner) Scan(ctx context.Context, source io.Reader) (core.ArtifactScanStatus, error) {
	if s.Address == "" {
		return core.ArtifactScanError, errors.New("ClamAV address is required")
	}
	network := s.Network
	if network == "" {
		network = "tcp"
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := net.Dialer{Timeout: min(timeout, 10*time.Second)}
	dial := s.DialContext
	if dial == nil {
		dial = dialer.DialContext
	}
	connection, err := dial(ctx, network, s.Address)
	if err != nil {
		return core.ArtifactScanError, fmt.Errorf("connect to ClamAV: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(timeout))
	if _, err := connection.Write([]byte("zINSTREAM\x00")); err != nil {
		return core.ArtifactScanError, err
	}
	buffer := make([]byte, 32*1024)
	for {
		read, readErr := source.Read(buffer)
		if read > 0 {
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(read))
			if _, err := connection.Write(length[:]); err != nil {
				return core.ArtifactScanError, err
			}
			if _, err := connection.Write(buffer[:read]); err != nil {
				return core.ArtifactScanError, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return core.ArtifactScanError, readErr
		}
	}
	if _, err := connection.Write([]byte{0, 0, 0, 0}); err != nil {
		return core.ArtifactScanError, err
	}
	response, err := bufio.NewReader(io.LimitReader(connection, 4096)).ReadString(0)
	if err != nil && !errors.Is(err, io.EOF) {
		return core.ArtifactScanError, err
	}
	response = strings.TrimSpace(strings.TrimSuffix(response, "\x00"))
	switch {
	case strings.HasSuffix(response, " OK"):
		return core.ArtifactScanClean, nil
	case strings.HasSuffix(response, " FOUND"):
		return core.ArtifactScanInfected, nil
	default:
		return core.ArtifactScanError, errors.New("ClamAV returned an invalid or error response")
	}
}
