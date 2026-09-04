package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"

	artifactstore "github.com/TsingFengIceberg/agent-federation-hub/internal/artifact"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

const defaultOutboundArtifactBytes int64 = 32 << 20

// NormalizeMessageParts keeps the original text field as a backwards-compatible
// shorthand while making the A2A Part list the canonical outbound payload. Text
// is placed first so existing providers that inspect only the first Part retain
// the expected instruction.
func NormalizeMessageParts(text string, values []core.Part) ([]core.Part, error) {
	parts := make([]core.Part, 0, len(values)+1)
	if strings.TrimSpace(text) != "" {
		parts = append(parts, core.Part{Kind: core.PartText, Text: text})
	}
	for index, value := range values {
		if err := validateMessagePart(value); err != nil {
			return nil, fmt.Errorf("message part %d: %w", index, err)
		}
		parts = append(parts, value)
	}
	if len(parts) == 0 {
		return nil, errors.New("task text or at least one message part is required")
	}
	return parts, nil
}

func validateMessagePart(part core.Part) error {
	if len(part.Filename) > 512 || len(part.MediaType) > 256 || len(part.ObjectID) > 512 || len(part.URI) > 4096 {
		return errors.New("message part field exceeds its maximum size")
	}
	if part.MediaType != "" {
		if _, _, err := mime.ParseMediaType(part.MediaType); err != nil {
			return fmt.Errorf("invalid media type %q", part.MediaType)
		}
	}
	switch part.Kind {
	case core.PartText:
		if strings.TrimSpace(part.Text) == "" {
			return errors.New("text part must not be blank")
		}
		if part.BytesBase64 != "" || part.URI != "" || part.ObjectID != "" || part.Data != nil {
			return errors.New("text part must contain only text")
		}
	case core.PartData:
		if part.Data == nil {
			return errors.New("data part requires a JSON value")
		}
		if _, err := json.Marshal(part.Data); err != nil {
			return fmt.Errorf("data part must be JSON: %w", err)
		}
		if part.Text != "" || part.BytesBase64 != "" || part.URI != "" || part.ObjectID != "" {
			return errors.New("data part must contain only data")
		}
	case core.PartFile:
		selectors := 0
		if part.BytesBase64 != "" {
			selectors++
			if _, err := base64.StdEncoding.DecodeString(part.BytesBase64); err != nil {
				return errors.New("file bytesBase64 is not valid standard base64")
			}
		}
		if part.URI != "" {
			selectors++
			parsed, err := url.Parse(part.URI)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
				return errors.New("file URI must be an HTTPS URL without user information or fragment")
			}
		}
		if part.ObjectID != "" {
			selectors++
			if strings.TrimSpace(part.ObjectID) != part.ObjectID || strings.ContainsAny(part.ObjectID, "\r\n") {
				return errors.New("file objectId is invalid")
			}
		}
		if selectors != 1 {
			return errors.New("file part requires exactly one of bytesBase64, uri, or objectId")
		}
		if part.Text != "" || part.Data != nil {
			return errors.New("file part must not include text or data")
		}
	default:
		return fmt.Errorf("unsupported part kind %q", part.Kind)
	}
	return nil
}

// MaterializeMessageParts resolves only Hub-owned Artifact object references.
// The Artifact service applies tenant ownership, availability, malware-scan,
// integrity, and encryption checks before the bytes are exposed to an A2A
// adapter. URL Parts remain references: the Hub never fetches them merely to
// forward a Message.
func MaterializeMessageParts(ctx context.Context, tenantID string, artifacts *artifactstore.Service, values []core.Part) ([]core.Part, error) {
	parts := make([]core.Part, len(values))
	copy(parts, values)
	for index := range parts {
		part := &parts[index]
		if part.Kind != core.PartFile || part.ObjectID == "" {
			continue
		}
		if artifacts == nil {
			return nil, errors.New("artifact object storage is required for file object references")
		}
		reader, object, err := artifacts.Open(ctx, tenantID, part.ObjectID)
		if err != nil {
			return nil, fmt.Errorf("open file object reference: %w", err)
		}
		maximum := defaultOutboundArtifactBytes
		if artifacts.Policy.MaxBytes > 0 {
			maximum = artifacts.Policy.MaxBytes
		}
		if object.SizeBytes > maximum {
			_ = reader.Close()
			return nil, fmt.Errorf("file object reference exceeds outbound limit of %d bytes", maximum)
		}
		contents, readErr := io.ReadAll(io.LimitReader(reader, maximum+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read file object reference: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close file object reference: %w", closeErr)
		}
		if int64(len(contents)) != object.SizeBytes || int64(len(contents)) > maximum {
			return nil, errors.New("file object reference changed while being read")
		}
		part.ObjectID = ""
		part.BytesBase64 = base64.StdEncoding.EncodeToString(contents)
		part.SizeBytes = object.SizeBytes
		part.SHA256 = object.SHA256
		if part.MediaType == "" {
			part.MediaType = object.DetectedMediaType
		}
		if part.Filename == "" {
			part.Filename = object.Filename
		}
	}
	return parts, nil
}

func firstTextPart(parts []core.Part) string {
	for _, part := range parts {
		if part.Kind == core.PartText {
			return part.Text
		}
	}
	return ""
}
