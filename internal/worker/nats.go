package worker

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// NATSPublisher is a small dependency-free NATS Core publisher. It uses the
// wire protocol directly so the Hub can publish durable Outbox records to a
// concrete event bus without coupling the core to a vendor SDK. Durability and
// replay remain the responsibility of the Outbox and the NATS deployment.
type NATSPublisher struct {
	Endpoint string
	Subject  string
	Token    func(context.Context) (string, error)
	Timeout  time.Duration

	mu   sync.Mutex
	conn net.Conn
}

func NewNATSPublisher(endpoint, subject string, token func(context.Context) (string, error)) (*NATSPublisher, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (parsed.Scheme != "nats" && parsed.Scheme != "tls") || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("NATS endpoint must be nats:// or tls:// without user information")
	}
	if strings.TrimSpace(subject) == "" || strings.ContainsAny(subject, " \t\r\n") {
		return nil, errors.New("NATS subject is required and must not contain whitespace")
	}
	return &NATSPublisher{Endpoint: parsed.String(), Subject: subject, Token: token, Timeout: 10 * time.Second}, nil
}

func (p *NATSPublisher) Publish(ctx context.Context, item core.OutboxItem) error {
	if p == nil || p.Endpoint == "" || p.Subject == "" {
		return errors.New("NATS publisher is not configured")
	}
	payload, err := json.Marshal(struct {
		Outbox core.OutboxItem `json:"outbox"`
		Key    string          `json:"idempotencyKey"`
	}{Outbox: item, Key: item.TenantID + ":" + item.DedupKey})
	if err != nil {
		return fmt.Errorf("encode NATS envelope: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(p.timeout())
	if err := p.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(p.conn, "PUB %s %d\r\n", p.Subject, len(payload)); err != nil {
		p.closeLocked()
		return fmt.Errorf("write NATS publish command: %w", err)
	}
	if _, err := p.conn.Write(append(payload, '\r', '\n')); err != nil {
		p.closeLocked()
		return fmt.Errorf("write NATS payload: %w", err)
	}
	return nil
}

func (p *NATSPublisher) ensureConnected(ctx context.Context) error {
	if p.conn != nil {
		return nil
	}
	parsed, err := url.Parse(p.Endpoint)
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: p.timeout()}
	var conn net.Conn
	if parsed.Scheme == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", parsed.Host, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: parsed.Hostname()})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", parsed.Host)
	}
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	p.conn = conn
	deadline := time.Now().Add(p.timeout())
	_ = conn.SetDeadline(deadline)
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "INFO") {
		p.closeLocked()
		return errors.New("NATS server did not send INFO")
	}
	connect := `{"verbose":false,"pedantic":false,"lang":"agent-federation-hub","version":"1.0"}`
	if p.Token != nil {
		token, tokenErr := p.Token(ctx)
		if tokenErr != nil {
			p.closeLocked()
			return fmt.Errorf("resolve NATS token: %w", tokenErr)
		}
		if strings.TrimSpace(token) == "" {
			p.closeLocked()
			return errors.New("NATS token is empty")
		}
		encoded, _ := json.Marshal(token)
		connect = fmt.Sprintf(`{"verbose":false,"pedantic":false,"lang":"agent-federation-hub","version":"1.0","auth_token":%s}`, encoded)
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s\r\nPING\r\n", connect); err != nil {
		p.closeLocked()
		return fmt.Errorf("write NATS CONNECT: %w", err)
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			p.closeLocked()
			return fmt.Errorf("read NATS handshake: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "PONG" || line == "+OK" {
			if err := conn.SetDeadline(time.Time{}); err != nil {
				p.closeLocked()
				return err
			}
			return nil
		}
		if strings.HasPrefix(line, "-ERR") {
			p.closeLocked()
			return fmt.Errorf("NATS handshake rejected: %s", line)
		}
	}
}

func (p *NATSPublisher) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 10 * time.Second
}

func (p *NATSPublisher) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

func (p *NATSPublisher) closeLocked() error {
	if p.conn == nil {
		return nil
	}
	err := p.conn.Close()
	p.conn = nil
	return err
}
