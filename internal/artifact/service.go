package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type Input struct {
	TenantID   string
	TaskID     string
	ArtifactID string
	DedupKey   string
	PartIndex  int
	MediaType  string
	Filename   string
	SourceURI  string
}

type Service struct {
	Metadata   core.ArtifactMetadataStore
	Objects    ObjectStore
	Policy     Policy
	Scanner    Scanner
	HTTPClient *http.Client
	Now        func() time.Time
}

func (s *Service) IngestBase64(ctx context.Context, input Input, encoded string) (core.ArtifactObject, error) {
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	return s.Ingest(ctx, input, decoder)
}

func (s *Service) IngestURI(ctx context.Context, input Input, rawURI string) (core.ArtifactObject, error) {
	if s.HTTPClient == nil {
		return core.ArtifactObject{}, errors.New("artifact URI retrieval is not configured")
	}
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return core.ArtifactObject{}, fmt.Errorf("%w: artifact URI must be an HTTPS URL without user information", ErrPolicy)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return core.ArtifactObject{}, err
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := s.HTTPClient.Do(request)
	if err != nil {
		return core.ArtifactObject{}, fmt.Errorf("retrieve artifact URI: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return core.ArtifactObject{}, fmt.Errorf("retrieve artifact URI: remote status %d", response.StatusCode)
	}
	if response.ContentLength > 0 && s.Policy.MaxBytes > 0 && response.ContentLength > s.Policy.MaxBytes {
		return core.ArtifactObject{}, fmt.Errorf("%w: remote object exceeds the configured size limit", ErrPolicy)
	}
	if input.MediaType == "" {
		input.MediaType = response.Header.Get("Content-Type")
	}
	cleanURI := *parsed
	cleanURI.RawQuery = ""
	cleanURI.Fragment = ""
	input.SourceURI = cleanURI.String()
	return s.Ingest(ctx, input, response.Body)
}

func (s *Service) Ingest(ctx context.Context, input Input, source io.Reader) (core.ArtifactObject, error) {
	if s.Metadata == nil || s.Objects == nil {
		return core.ArtifactObject{}, errors.New("artifact metadata and object stores are required")
	}
	if input.TenantID == "" || input.TaskID == "" || input.ArtifactID == "" || input.PartIndex < 0 {
		return core.ArtifactObject{}, errors.New("artifact tenant, Task, Artifact, and non-negative Part index are required")
	}
	temporary, err := os.CreateTemp("", "afh-artifact-*")
	if err != nil {
		return core.ArtifactObject{}, err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	hash := sha256.New()
	maximum := s.Policy.MaxBytes
	if maximum <= 0 {
		maximum = 32 << 20
	}
	size, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(&contextReader{ctx: ctx, reader: source}, maximum+1))
	if err != nil {
		return core.ArtifactObject{}, err
	}
	if size > maximum {
		return core.ArtifactObject{}, fmt.Errorf("%w: object exceeds the configured size limit", ErrPolicy)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return core.ArtifactObject{}, err
	}
	header := make([]byte, 512)
	headerLength, err := io.ReadFull(temporary, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return core.ArtifactObject{}, err
	}
	detected, err := detectFileMediaType(temporary, header[:headerLength], input.MediaType)
	if err != nil {
		return core.ArtifactObject{}, err
	}
	if err := s.Policy.Validate(size, input.MediaType, detected); err != nil {
		return core.ArtifactObject{}, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	identity := strings.Join([]string{
		input.TenantID, input.TaskID, input.ArtifactID, input.DedupKey,
		strconv.Itoa(input.PartIndex), digest,
	}, "\x00")
	id := core.DigestString(identity)
	now := s.now()
	retention := s.Policy.Retention
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	object := core.ArtifactObject{
		ID: id, TenantID: input.TenantID, TaskID: input.TaskID,
		ArtifactID: input.ArtifactID, PartIndex: input.PartIndex,
		SHA256: digest, SizeBytes: size,
		DeclaredMediaType: baseMediaType(input.MediaType), DetectedMediaType: detected,
		Filename: input.Filename, SourceURI: input.SourceURI,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(retention),
	}
	reserved, created, err := s.Metadata.ReserveArtifact(ctx, object, core.ArtifactQuota{
		MaxBytes: s.Policy.Quota.MaxBytes, MaxObjects: s.Policy.Quota.MaxObjects,
	})
	if err != nil {
		return core.ArtifactObject{}, err
	}
	if !created && (reserved.Status == core.ArtifactObjectAvailable || reserved.Status == core.ArtifactObjectQuarantined) {
		return reserved, nil
	}
	fail := func(code string, cause error) (core.ArtifactObject, error) {
		_, failErr := s.Metadata.FailArtifact(ctx, input.TenantID, id, code, s.now())
		if failErr != nil {
			return core.ArtifactObject{}, errors.Join(cause, failErr)
		}
		return core.ArtifactObject{}, cause
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return fail("TEMPFILE_SEEK_FAILED", err)
	}
	scanner := s.Scanner
	if scanner == nil {
		scanner = NoopScanner{}
	}
	scanStatus, err := scanner.Scan(ctx, temporary)
	if err != nil || scanStatus == core.ArtifactScanError {
		if err == nil {
			err = errors.New("artifact scanner returned an error status")
		}
		return fail("SCAN_FAILED", err)
	}
	if s.Policy.RequireClean && scanStatus != core.ArtifactScanClean {
		return fail("CLEAN_SCAN_REQUIRED", fmt.Errorf("%w: a clean malware scan is required", ErrPolicy))
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return fail("TEMPFILE_SEEK_FAILED", err)
	}
	storageKey := id[:2] + "/" + id
	if err := s.Objects.Put(ctx, storageKey, temporary, size, detected); err != nil {
		return fail("OBJECT_WRITE_FAILED", err)
	}
	status := core.ArtifactObjectAvailable
	if scanStatus == core.ArtifactScanInfected {
		status = core.ArtifactObjectQuarantined
	}
	finalized, err := s.Metadata.FinalizeArtifact(
		ctx, input.TenantID, id, storageKey, detected, scanStatus, status, s.now(),
	)
	if err != nil {
		_ = s.Objects.Delete(ctx, storageKey)
		return fail("METADATA_FINALIZE_FAILED", err)
	}
	return finalized, nil
}

func (s *Service) Get(ctx context.Context, tenantID, id string) (core.ArtifactObject, error) {
	if s.Metadata == nil {
		return core.ArtifactObject{}, errors.New("artifact metadata store is required")
	}
	return s.Metadata.GetArtifact(ctx, tenantID, id)
}

func (s *Service) Open(ctx context.Context, tenantID, id string) (io.ReadCloser, core.ArtifactObject, error) {
	object, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return nil, core.ArtifactObject{}, err
	}
	if object.Status != core.ArtifactObjectAvailable ||
		(s.Policy.RequireClean && object.ScanStatus != core.ArtifactScanClean) {
		return nil, core.ArtifactObject{}, ErrUnavailable
	}
	reader, info, err := s.Objects.Open(ctx, object.StorageKey)
	if err != nil {
		return nil, core.ArtifactObject{}, err
	}
	if info.SizeBytes != object.SizeBytes {
		_ = reader.Close()
		return nil, core.ArtifactObject{}, errors.New("artifact object size does not match metadata")
	}
	return reader, object, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func detectFileMediaType(file *os.File, header []byte, declared string) (string, error) {
	detected := DetectMediaType(header)
	if baseMediaType(declared) != "application/json" {
		return detected, nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	decoder := json.NewDecoder(file)
	var value any
	if err := decoder.Decode(&value); err != nil {
		return detected, nil
	}
	if err := decoder.Decode(&struct{}{}); errors.Is(err, io.EOF) {
		return "application/json", nil
	}
	return detected, nil
}
