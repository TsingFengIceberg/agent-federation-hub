// Package conformance summarizes repository-owned A2A evidence without
// converting skipped or waived requirements into passes.
package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type Matrix struct {
	// SchemaVersion versions the repository-owned evidence document. It is
	// intentionally separate from the A2A protocol version so a report parser
	// cannot silently reinterpret a changed evidence contract.
	SchemaVersion     int           `json:"schemaVersion"`
	ProtocolVersion   string        `json:"protocolVersion"`
	ProtocolSource    string        `json:"protocolSourceCommit"`
	LatestProtocol    string        `json:"latestProtocolCandidateCommit"`
	GoSDKModule       string        `json:"goSDKModule"`
	GoSDKVersion      string        `json:"goSDKVersion"`
	GoSDKSource       string        `json:"goSDKSourceCommit"`
	TCKCommit         string        `json:"tckCommit"`
	TCKProtocolCommit string        `json:"tckProtocolCommit"`
	Profiles          []MatrixEntry `json:"profiles"`
}

type MatrixEntry struct {
	Name             string `json:"name"`
	Binding          string `json:"binding"`
	Stream           string `json:"streamTransport"`
	Status           string `json:"status"`
	MustPassed       int    `json:"tckMustPassed"`
	MustSkipped      int    `json:"tckMustSkipped"`
	MustNotTested    int    `json:"tckMustNotTested"`
	MustFailed       int    `json:"tckMustFailed"`
	SUTBinding       string `json:"sutBindingFlag"`
	TransportPassed  int    `json:"tckTransportPassed"`
	TransportSkipped int    `json:"tckTransportSkipped"`
	TransportFailed  int    `json:"tckTransportFailed"`
}

type Waiver struct {
	ID       string `json:"id"`
	Scope    string `json:"scope"`
	Reason   string `json:"reason"`
	Evidence string `json:"evidence"`
}

type WaiverDocument struct {
	Status            string   `json:"status"`
	TCKCommit         string   `json:"tckCommit"`
	TCKProtocolCommit string   `json:"tckProtocolCommit"`
	Waivers           []Waiver `json:"waivers"`
}

type ProfileReport struct {
	Name          string `json:"name"`
	Binding       string `json:"binding"`
	Stream        string `json:"streamTransport"`
	Status        string `json:"status"`
	MustPassed    int    `json:"mustPassed"`
	MustSkipped   int    `json:"mustSkipped"`
	MustNotTested int    `json:"mustNotTested"`
	MustFailed    int    `json:"mustFailed"`
	Complete      bool   `json:"complete"`
}

type Report struct {
	EvidenceStatus      string          `json:"evidenceStatus"`
	ProtocolVersion     string          `json:"protocolVersion"`
	ProtocolSource      string          `json:"protocolSourceCommit"`
	GoSDKVersion        string          `json:"goSDKVersion"`
	TCKCommit           string          `json:"tckCommit"`
	TCKProtocolCommit   string          `json:"tckProtocolCommit"`
	Profiles            []ProfileReport `json:"profiles"`
	Waivers             []Waiver        `json:"waivers"`
	CompleteConformance bool            `json:"completeConformance"`
	Reasons             []string        `json:"reasons"`
}

// Profile statuses are evidence labels, not protocol states. Keeping the
// vocabulary closed prevents a typo or an unreviewed status from being
// interpreted as a conformance claim by preflight and report tooling.
const (
	StatusPlanned                = "planned"
	StatusNotImplemented         = "not-implemented"
	StatusPartial                = "partial"
	StatusVerifiedLocal          = "verified-local"
	StatusVerifiedLocalWithSkips = "verified-local-with-skips"
	StatusAcceptedWithWaivers    = "accepted-with-waivers"
	StatusAccepted               = "accepted"
	StatusComplete               = "complete"
)

var validProfileStatuses = map[string]struct{}{
	StatusPlanned:                {},
	StatusNotImplemented:         {},
	StatusPartial:                {},
	StatusVerifiedLocal:          {},
	StatusVerifiedLocalWithSkips: {},
	StatusAcceptedWithWaivers:    {},
	StatusAccepted:               {},
	StatusComplete:               {},
}

func LoadMatrix(path string) (Matrix, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Matrix{}, fmt.Errorf("read profile matrix: %w", err)
	}
	var matrix Matrix
	if err := decodeStrict(encoded, &matrix); err != nil {
		return Matrix{}, fmt.Errorf("decode profile matrix: %w", err)
	}
	if err := matrix.Validate(); err != nil {
		return Matrix{}, fmt.Errorf("validate profile matrix: %w", err)
	}
	return matrix, nil
}

func LoadWaivers(path string) (WaiverDocument, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return WaiverDocument{}, fmt.Errorf("read waiver file: %w", err)
	}
	var document WaiverDocument
	if err := decodeStrict(encoded, &document); err != nil {
		return WaiverDocument{}, fmt.Errorf("decode waiver file: %w", err)
	}
	return document, nil
}

func Summarize(matrix Matrix, waivers WaiverDocument) (Report, error) {
	if err := matrix.Validate(); err != nil {
		return Report{}, err
	}
	if err := waivers.Validate(); err != nil {
		return Report{}, err
	}
	report := Report{
		EvidenceStatus:  "repository-owned-evidence",
		ProtocolVersion: matrix.ProtocolVersion, ProtocolSource: matrix.ProtocolSource,
		GoSDKVersion: matrix.GoSDKVersion, TCKCommit: matrix.TCKCommit,
		TCKProtocolCommit: matrix.TCKProtocolCommit, Profiles: []ProfileReport{},
		Waivers: append([]Waiver(nil), waivers.Waivers...), Reasons: []string{},
	}
	if waivers.TCKCommit != "" && waivers.TCKCommit != matrix.TCKCommit {
		return Report{}, errors.New("waiver TCK pin does not match profile matrix")
	}
	if waivers.TCKProtocolCommit != "" && waivers.TCKProtocolCommit != matrix.TCKProtocolCommit {
		return Report{}, errors.New("waiver protocol pin does not match profile matrix")
	}
	for _, profile := range matrix.Profiles {
		if profile.Name == "" || profile.Binding == "" || profile.Stream == "" || profile.Status == "" {
			return Report{}, errors.New("profile matrix contains an incomplete profile")
		}
		if profile.MustPassed < 0 || profile.MustSkipped < 0 || profile.MustNotTested < 0 || profile.MustFailed < 0 {
			return Report{}, fmt.Errorf("profile %q contains negative evidence counts", profile.Name)
		}
		complete := profile.MustSkipped == 0 && profile.MustNotTested == 0 && profile.MustFailed == 0 && len(waivers.Waivers) == 0 && (profile.Status == "complete" || profile.Status == "accepted")
		report.Profiles = append(report.Profiles, ProfileReport{
			Name: profile.Name, Binding: profile.Binding, Stream: profile.Stream, Status: profile.Status,
			MustPassed: profile.MustPassed, MustSkipped: profile.MustSkipped,
			MustNotTested: profile.MustNotTested, MustFailed: profile.MustFailed, Complete: complete,
		})
		if profile.MustSkipped > 0 {
			report.Reasons = append(report.Reasons, fmt.Sprintf("%s has %d skipped MUST requirements", profile.Name, profile.MustSkipped))
		}
		if profile.MustNotTested > 0 {
			report.Reasons = append(report.Reasons, fmt.Sprintf("%s has %d not-tested MUST requirements", profile.Name, profile.MustNotTested))
		}
		if profile.MustFailed > 0 {
			report.Reasons = append(report.Reasons, fmt.Sprintf("%s has %d failed MUST requirements", profile.Name, profile.MustFailed))
		}
		if profile.Status != "complete" && profile.Status != "accepted" {
			report.Reasons = append(report.Reasons, fmt.Sprintf("%s status %q is not a complete/accepted conformance status", profile.Name, profile.Status))
		}
	}
	if len(waivers.Waivers) > 0 {
		report.Reasons = append(report.Reasons, fmt.Sprintf("%d explicit waiver(s) remain", len(waivers.Waivers)))
	}
	report.CompleteConformance = len(report.Reasons) == 0
	return report, nil
}

// Validate checks the machine-readable evidence contract before a report is
// interpreted. The validation deliberately does not claim that a profile is
// standards-conformant; it only prevents stale, malformed, or contradictory
// local evidence from being presented as a result.
func (m Matrix) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("profile matrix schemaVersion must be 1")
	}
	if strings.TrimSpace(m.ProtocolVersion) == "" || strings.TrimSpace(m.GoSDKModule) == "" || strings.TrimSpace(m.GoSDKVersion) == "" || len(m.Profiles) == 0 {
		return errors.New("profile matrix must contain protocol, SDK metadata, and at least one profile")
	}
	for name, value := range map[string]string{
		"protocol source": m.ProtocolSource,
		"TCK commit":      m.TCKCommit,
		"TCK protocol":    m.TCKProtocolCommit,
	} {
		if !validCommit(value) {
			return fmt.Errorf("profile matrix %s pin must be a full commit ID", name)
		}
	}
	if m.GoSDKSource != "" && !validCommit(m.GoSDKSource) {
		return errors.New("profile matrix Go SDK source pin must be a full commit ID")
	}
	seenNames := make(map[string]struct{}, len(m.Profiles))
	seenBindings := make(map[string]struct{}, len(m.Profiles))
	for _, profile := range m.Profiles {
		if strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.Binding) == "" || strings.TrimSpace(profile.Stream) == "" || strings.TrimSpace(profile.Status) == "" {
			return errors.New("profile matrix contains an incomplete profile")
		}
		if _, exists := seenNames[profile.Name]; exists {
			return fmt.Errorf("profile matrix repeats profile %q", profile.Name)
		}
		if _, exists := seenBindings[profile.Binding]; exists {
			return fmt.Errorf("profile matrix repeats binding %q", profile.Binding)
		}
		if _, ok := map[string]struct{}{"JSONRPC": {}, "HTTP+JSON": {}, "GRPC": {}}[profile.Binding]; !ok {
			return fmt.Errorf("profile %q uses unsupported binding %q", profile.Name, profile.Binding)
		}
		switch profile.Binding {
		case "JSONRPC", "HTTP+JSON":
			if profile.Stream != "SSE" {
				return fmt.Errorf("profile %q with binding %q must use SSE streaming", profile.Name, profile.Binding)
			}
		case "GRPC":
			if profile.Stream != "server-streaming" {
				return fmt.Errorf("profile %q with gRPC must use server-streaming", profile.Name)
			}
		}
		if _, ok := validProfileStatuses[profile.Status]; !ok {
			return fmt.Errorf("profile %q has unsupported status %q", profile.Name, profile.Status)
		}
		seenNames[profile.Name] = struct{}{}
		seenBindings[profile.Binding] = struct{}{}
		if profile.MustPassed < 0 || profile.MustSkipped < 0 || profile.MustNotTested < 0 || profile.MustFailed < 0 ||
			profile.TransportPassed < 0 || profile.TransportSkipped < 0 || profile.TransportFailed < 0 {
			return fmt.Errorf("profile %q contains a negative evidence count", profile.Name)
		}
		if profile.MustFailed != 0 {
			return fmt.Errorf("profile %q records failed MUST requirements", profile.Name)
		}
		if profile.TransportFailed != 0 {
			return fmt.Errorf("profile %q records failed transport tests", profile.Name)
		}
		mustTotal := profile.MustPassed + profile.MustSkipped + profile.MustNotTested + profile.MustFailed
		if mustTotal <= 0 {
			return fmt.Errorf("profile %q must record at least one MUST requirement", profile.Name)
		}
		transportTotal := profile.TransportPassed + profile.TransportSkipped + profile.TransportFailed
		if transportTotal <= 0 {
			return fmt.Errorf("profile %q must record at least one transport test", profile.Name)
		}
		if profile.Status == StatusComplete || profile.Status == StatusAccepted {
			if profile.MustSkipped != 0 || profile.MustNotTested != 0 || profile.MustFailed != 0 ||
				profile.TransportSkipped != 0 || profile.TransportFailed != 0 {
				return fmt.Errorf("profile %q status %q cannot contain skipped, not-tested, or failed evidence", profile.Name, profile.Status)
			}
		}
	}
	return nil
}

// Validate checks waiver metadata independently of the selected profile. A
// waiver is an explicit evidence boundary and must be attributable to a
// durable source; an empty waiver would make complete-conformance reporting
// ambiguous.
func (d WaiverDocument) Validate() error {
	if d.Status == "" && d.TCKCommit == "" && d.TCKProtocolCommit == "" && len(d.Waivers) == 0 {
		return nil
	}
	if d.Status == "" {
		return errors.New("waiver document status is required")
	}
	if d.TCKCommit != "" && !validCommit(d.TCKCommit) {
		return errors.New("waiver TCK pin must be a full commit ID")
	}
	if d.TCKProtocolCommit != "" && !validCommit(d.TCKProtocolCommit) {
		return errors.New("waiver protocol pin must be a full commit ID")
	}
	seen := make(map[string]struct{}, len(d.Waivers))
	for _, waiver := range d.Waivers {
		if strings.TrimSpace(waiver.ID) == "" || strings.TrimSpace(waiver.Scope) == "" || strings.TrimSpace(waiver.Reason) == "" || strings.TrimSpace(waiver.Evidence) == "" {
			return errors.New("waiver entries require id, scope, reason, and evidence")
		}
		if _, exists := seen[waiver.ID]; exists {
			return fmt.Errorf("waiver %q is duplicated", waiver.ID)
		}
		seen[waiver.ID] = struct{}{}
	}
	return nil
}

func decodeStrict(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("file contains trailing data")
	}
	return nil
}

func validCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
