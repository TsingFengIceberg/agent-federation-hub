package artifact

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"
)

type Policy struct {
	MaxBytes     int64
	AllowedMIME  map[string]struct{}
	Quota        Quota
	Retention    time.Duration
	RequireClean bool
}

type Quota struct {
	MaxBytes   int64
	MaxObjects int64
}

func (p Policy) Validate(size int64, declared, detected string) error {
	if size < 0 || (p.MaxBytes > 0 && size > p.MaxBytes) {
		return fmt.Errorf("%w: object exceeds the configured size limit", ErrPolicy)
	}
	declared = baseMediaType(declared)
	detected = baseMediaType(detected)
	if detected == "" {
		return fmt.Errorf("%w: content type could not be detected", ErrPolicy)
	}
	if len(p.AllowedMIME) > 0 && !mimeAllowed(p.AllowedMIME, detected) {
		return fmt.Errorf("%w: detected media type %q is not allowed", ErrPolicy, detected)
	}
	if declared != "" && declared != "application/octet-stream" && declared != detected {
		return fmt.Errorf("%w: declared media type %q does not match detected type %q", ErrPolicy, declared, detected)
	}
	return nil
}

func DetectMediaType(header []byte) string {
	return baseMediaType(http.DetectContentType(header))
}

func ParseMIMEAllowlist(value string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(strings.ToLower(entry))
		if entry == "" {
			continue
		}
		if strings.HasSuffix(entry, "/*") {
			if strings.Count(entry, "/") != 1 {
				return nil, errors.New("invalid MIME wildcard")
			}
			result[entry] = struct{}{}
			continue
		}
		mediaType, _, err := mime.ParseMediaType(entry)
		if err != nil {
			return nil, err
		}
		result[strings.ToLower(mediaType)] = struct{}{}
	}
	return result, nil
}

func baseMediaType(value string) string {
	if value == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(mediaType)
}

func mimeAllowed(allowed map[string]struct{}, mediaType string) bool {
	if _, ok := allowed[mediaType]; ok {
		return true
	}
	major, _, found := strings.Cut(mediaType, "/")
	if !found {
		return false
	}
	_, ok := allowed[major+"/*"]
	return ok
}
