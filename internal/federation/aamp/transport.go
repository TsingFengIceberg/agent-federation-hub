package aampfederation

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

// Envelope is the transport-neutral mailbox representation. AAMP lifecycle
// semantics remain in MailEvent; this type only carries delivery metadata.
type Envelope struct {
	ID      string            `json:"id,omitempty"`
	To      string            `json:"to"`
	From    string            `json:"from,omitempty"`
	Subject string            `json:"subject,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body"`
}

type MailboxSender interface {
	Send(context.Context, Envelope) error
}

// MailboxReceiver is the asynchronous counterpart to MailboxSender. A
// concrete implementation owns provider-specific cursor, authentication, and
// acknowledgement semantics; the Hub only consumes normalized envelopes.
type MailboxReceiver interface {
	Receive(context.Context) ([]Envelope, error)
	Ack(context.Context, Envelope) error
}

// MailboxEventHandler receives one decoded AAMP event. Implementations should
// persist the observation before returning so a successful ACK cannot lose a
// message. The Poller adds process-local duplicate suppression; durable
// idempotency remains the Hub InboxStore's responsibility.
type MailboxEventHandler func(context.Context, Envelope, MailEvent, federation.Observation) error

// MailboxPoller turns mailbox envelopes into protocol-neutral observations.
// It deliberately does not know tenant or Agent mapping; those belong to the
// caller's authenticated mailbox policy.
type MailboxPoller struct {
	Receiver MailboxReceiver
	Handle   MailboxEventHandler
	Seen     map[string]struct{}
}

func (p *MailboxPoller) PollOnce(ctx context.Context) (int, error) {
	if p == nil || p.Receiver == nil || p.Handle == nil {
		return 0, errors.New("mailbox poller receiver and handler are required")
	}
	if p.Seen == nil {
		p.Seen = make(map[string]struct{})
	}
	envelopes, err := p.Receiver.Receive(ctx)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, envelope := range envelopes {
		event, err := DecodeMailEvent(envelope)
		if err != nil {
			return processed, err
		}
		key := event.MessageID
		if _, duplicate := p.Seen[key]; duplicate {
			if err := p.Receiver.Ack(ctx, envelope); err != nil {
				return processed, err
			}
			continue
		}
		observation, err := ObservationFromMail(event)
		if err != nil {
			return processed, err
		}
		if err := p.Handle(ctx, envelope, event, observation); err != nil {
			return processed, err
		}
		if err := p.Receiver.Ack(ctx, envelope); err != nil {
			return processed, err
		}
		p.Seen[key] = struct{}{}
		processed++
	}
	return processed, nil
}

// SMTPTransport submits an RFC 5322 message through an SMTP submission
// endpoint. TLS and AUTH are configured by the operator, never by MailEvent.
type SMTPTransport struct {
	Addr      string
	From      string
	Username  string
	Password  string
	TLSConfig *tls.Config
	SendMail  func(string, smtp.Auth, string, []string, []byte) error
}

func (s SMTPTransport) Send(ctx context.Context, envelope Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(s.Addr) == "" || strings.TrimSpace(s.From) == "" || strings.TrimSpace(envelope.To) == "" {
		return errors.New("SMTP transport requires addr, from, and recipient")
	}
	if len(envelope.Body) > 16<<20 {
		return errors.New("mailbox envelope exceeds 16 MiB")
	}
	message := make([]byte, 0, len(envelope.Body)+256)
	message = appendHeader(message, "From", s.From)
	message = appendHeader(message, "To", envelope.To)
	message = appendHeader(message, "Subject", envelope.Subject)
	for key, value := range envelope.Headers {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return errors.New("mailbox header contains invalid characters")
		}
		message = appendHeader(message, key, value)
	}
	message = append(message, '\r', '\n')
	message = append(message, envelope.Body...)
	send := s.SendMail
	if send == nil {
		send = smtp.SendMail
	}
	var auth smtp.Auth
	if s.Username != "" || s.Password != "" {
		host := s.Addr
		if index := strings.LastIndexByte(host, ':'); index > 0 {
			host = host[:index]
		}
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}
	return send(s.Addr, auth, s.From, []string{envelope.To}, message)
}

func appendHeader(message []byte, key, value string) []byte {
	return append(message, []byte(key+": "+value+"\r\n")...)
}

// HTTPMailboxTransport is a deliberately generic HTTP boundary for JMAP or
// another mailbox service. The endpoint contract is operator-owned and the
// body is a serialized Envelope; no JMAP compatibility claim is made here.
type HTTPMailboxTransport struct {
	Endpoint string
	Token    func(context.Context) (string, error)
	Client   *http.Client
	MaxBytes int64
}

// HTTPMailboxReceiver is a deliberately generic polling contract. A mailbox
// endpoint returns either []Envelope or {"messages": []Envelope}; ACKs are
// POSTed to <endpoint>/<id>/ack. This is an adapter seam, not a JMAP claim.
type HTTPMailboxReceiver struct {
	Endpoint string
	Token    func(context.Context) (string, error)
	Client   *http.Client
	MaxBytes int64
}

func (r *HTTPMailboxReceiver) Receive(ctx context.Context) ([]Envelope, error) {
	if r == nil || strings.TrimSpace(r.Endpoint) == "" {
		return nil, errors.New("HTTP mailbox receiver endpoint is required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Endpoint, nil)
	if err != nil {
		return nil, err
	}
	if err := r.authorize(ctx, request); err != nil {
		return nil, err
	}
	response, err := r.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, r.maxBytes()+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > r.maxBytes() {
		return nil, errors.New("mailbox response exceeds configured limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("mailbox endpoint returned status %d", response.StatusCode)
	}
	var envelopes []Envelope
	if err := json.Unmarshal(body, &envelopes); err == nil {
		return envelopes, nil
	}
	var wrapper struct {
		Messages []Envelope `json:"messages"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("decode mailbox response: %w", err)
	}
	return wrapper.Messages, nil
}

func (r *HTTPMailboxReceiver) Ack(ctx context.Context, envelope Envelope) error {
	if r == nil || strings.TrimSpace(r.Endpoint) == "" || strings.TrimSpace(envelope.ID) == "" {
		return errors.New("HTTP mailbox ACK requires endpoint and envelope ID")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.Endpoint, "/")+"/"+url.PathEscape(envelope.ID)+"/ack", nil)
	if err != nil {
		return err
	}
	if err := r.authorize(ctx, request); err != nil {
		return err
	}
	response, err := r.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, r.maxBytes())); err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("mailbox ACK returned status %d", response.StatusCode)
	}
	return nil
}

func (r *HTTPMailboxReceiver) authorize(ctx context.Context, request *http.Request) error {
	if r.Token == nil {
		return nil
	}
	token, err := r.Token(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("mailbox credential is empty")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (r *HTTPMailboxReceiver) client() *http.Client {
	if r != nil && r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (r *HTTPMailboxReceiver) maxBytes() int64 {
	if r != nil && r.MaxBytes > 0 {
		return r.MaxBytes
	}
	return 1 << 20
}

func (s *HTTPMailboxTransport) Send(ctx context.Context, envelope Envelope) error {
	if s == nil || strings.TrimSpace(s.Endpoint) == "" {
		return errors.New("HTTP mailbox endpoint is required")
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if s.Token != nil {
		token, tokenErr := s.Token(ctx)
		if tokenErr != nil {
			return tokenErr
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	maximum := s.MaxBytes
	if maximum <= 0 {
		maximum = 1 << 20
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if readErr != nil {
		return readErr
	}
	if int64(len(body)) > maximum {
		return errors.New("mailbox response exceeds configured limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("mailbox endpoint returned status %d", response.StatusCode)
	}
	return nil
}

func EncodeMailEvent(event MailEvent) ([]byte, error) {
	if event.Version == "" {
		event.Version = Version
	}
	if event.Intent == "" || event.TaskID == "" || event.MessageID == "" {
		return nil, errors.New("AAMP event intent, taskId, and messageId are required")
	}
	return json.Marshal(event)
}

func DecodeMailEvent(envelope Envelope) (MailEvent, error) {
	if len(envelope.Body) == 0 {
		return MailEvent{}, errors.New("AAMP envelope body is required")
	}
	var event MailEvent
	if err := json.Unmarshal(envelope.Body, &event); err != nil {
		return MailEvent{}, fmt.Errorf("decode AAMP event: %w", err)
	}
	if event.Version == "" || event.Intent == "" || event.TaskID == "" || event.MessageID == "" {
		return MailEvent{}, errors.New("AAMP event version, intent, taskId, and messageId are required")
	}
	return event, nil
}

func NewAAMPEnvelope(to, from, subject string, event MailEvent) (Envelope, error) {
	body, err := EncodeMailEvent(event)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{To: to, From: from, Subject: subject, Headers: map[string]string{
		"Content-Type":   "application/aamp+json",
		"X-AAMP-Version": Version,
	}, Body: body}, nil
}
