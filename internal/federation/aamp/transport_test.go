package aampfederation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/smtp"
	"strings"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

func TestSMTPTransportBuildsAAMPEnvelopeWithoutSendingSecrets(t *testing.T) {
	var captured []byte
	transport := SMTPTransport{Addr: "smtp.example:587", From: "hub@example.com", Username: "user", Password: "secret", SendMail: func(_ string, _ smtp.Auth, _ string, _ []string, body []byte) error { captured = body; return nil }}
	envelope, err := NewAAMPEnvelope("agent@example.com", "hub@example.com", "AAMP task", MailEvent{Intent: "task.dispatch", TaskID: "task-1", MessageID: "message-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Send(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(captured), "task.dispatch") || strings.Contains(string(captured), "secret") {
		t.Fatalf("message=%s", captured)
	}
}

func TestHTTPMailboxTransportSendsBoundedJSON(t *testing.T) {
	called := false
	transport := &HTTPMailboxTransport{Endpoint: "https://mail.example/send", Token: func(context.Context) (string, error) { return "token", nil }, Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called = true
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(request.Body)
		var payload Envelope
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload.Body), "task-1") {
			t.Fatalf("body=%s", body)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}}
	envelope, err := NewAAMPEnvelope("to", "from", "subject", MailEvent{Intent: "task.ack", TaskID: "task-1", MessageID: "message-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Send(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("mailbox endpoint was not called")
	}
}

func TestHTTPMailboxReceiverPollsAndAcknowledges(t *testing.T) {
	acknowledged := make([]string, 0, 1)
	mailBody := []byte(`{"version":"1.1","intent":"task.result","taskId":"remote-1","messageId":"message-1","status":"completed","body":"done"}`)
	encodedBody := base64.StdEncoding.EncodeToString(mailBody)
	receiver := &HTTPMailboxReceiver{Endpoint: "https://mail.example/mail", Token: func(context.Context) (string, error) { return "mailbox-token", nil }, Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer mailbox-token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		if request.Method == http.MethodGet {
			payload := `{"messages":[{"id":"mail-1","to":"hub","body":"` + encodedBody + `"}]}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
		}
		if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/mail-1/ack") {
			acknowledged = append(acknowledged, request.URL.Path)
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("missing"))}, nil
	})}}
	var got core.TaskState
	poller := &MailboxPoller{Receiver: receiver, Handle: func(_ context.Context, _ Envelope, _ MailEvent, observation federation.Observation) error {
		got = observation.State
		return nil
	}}
	count, err := poller.PollOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || got != core.TaskStateCompleted || len(acknowledged) != 1 {
		t.Fatalf("count=%d state=%s acknowledged=%v", count, got, acknowledged)
	}
	count, err = poller.PollOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(acknowledged) != 2 {
		t.Fatalf("duplicate count=%d acknowledged=%v", count, acknowledged)
	}
}

func TestDecodeMailEventRejectsMalformedEnvelope(t *testing.T) {
	if _, err := DecodeMailEvent(Envelope{Body: []byte("not-json")}); err == nil {
		t.Fatal("malformed envelope was accepted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
