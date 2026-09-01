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

func LoadMatrix(path string) (Matrix, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Matrix{}, fmt.Errorf("read profile matrix: %w", err)
	}
	var matrix Matrix
	if err := decodeStrict(encoded, &matrix); err != nil {
		return Matrix{}, fmt.Errorf("decode profile matrix: %w", err)
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
	if strings.TrimSpace(matrix.ProtocolVersion) == "" || strings.TrimSpace(matrix.GoSDKVersion) == "" || len(matrix.Profiles) == 0 {
		return Report{}, errors.New("profile matrix must contain a protocol version and at least one profile")
	}
	if !validCommit(matrix.ProtocolSource) || !validCommit(matrix.TCKCommit) || !validCommit(matrix.TCKProtocolCommit) {
		return Report{}, errors.New("profile matrix protocol and TCK pins must be full commit IDs")
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
