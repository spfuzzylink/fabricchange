// Package fabricchange evaluates offline maintenance proposals against declared
// resource dependencies and active job allocations. It never changes a cluster.
package fabricchange

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

const SchemaVersion = "1"

type Input struct {
	SchemaVersion string   `json:"schema_version"`
	Snapshot      Snapshot `json:"snapshot"`
	Change        Change   `json:"change"`
	Policy        Policy   `json:"policy"`
}

type Snapshot struct {
	CapturedAt    time.Time  `json:"captured_at"`
	Complete      *bool      `json:"complete"`
	MissingScopes []string   `json:"missing_scopes,omitempty"`
	Resources     []Resource `json:"resources"`
	Jobs          []Job      `json:"jobs"`
}

type Resource struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`   // compute, fabric, storage, power, service
	Status         string            `json:"status"` // up, down, unknown
	FailureDomains []string          `json:"failure_domains,omitempty"`
	Dependencies   []DependencyGroup `json:"dependencies,omitempty"`
}

// DependencyGroup requires at least MinAvailable of Members to be available.
// Every dependency group on a resource must be satisfied.
type DependencyGroup struct {
	ID           string   `json:"id"`
	Members      []string `json:"members"`
	MinAvailable int      `json:"min_available"`
}

type Job struct {
	ID        string   `json:"id"`
	Owner     string   `json:"owner"`
	Status    string   `json:"status"`
	Resources []string `json:"resources"`
}

type Change struct {
	// Must be explicitly true. Waves are independent counterfactuals against
	// the same snapshot, conditional on restoration before each next wave.
	PriorWaveRestored bool   `json:"prior_wave_restored"`
	Waves             []Wave `json:"waves"`
}

type Wave struct {
	ID      string   `json:"id"`
	Targets []string `json:"targets"`
}

type Policy struct {
	MaxSnapshotAgeSeconds      int64           `json:"max_snapshot_age_seconds"`
	MaxUnavailableComputeNodes *int            `json:"max_unavailable_compute_nodes,omitempty"`
	MinHealthyByDomain         []DomainMinimum `json:"min_healthy_by_domain,omitempty"`
}

type DomainMinimum struct {
	Domain          string `json:"domain"`
	MinHealthyNodes int    `json:"min_healthy_nodes"`
}

// UnmarshalJSON distinguishes an explicit zero minimum from an omitted field.
func (m *DomainMinimum) UnmarshalJSON(b []byte) error {
	var raw struct {
		Domain  string `json:"domain"`
		Minimum *int   `json:"min_healthy_nodes"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&raw); err != nil {
		return err
	}
	if raw.Minimum == nil {
		return fmt.Errorf("domain minimum requires an explicit min_healthy_nodes integer")
	}
	m.Domain, m.MinHealthyNodes = raw.Domain, *raw.Minimum
	return nil
}

type Cause struct {
	Path          []string `json:"path"`
	Reason        string   `json:"reason"`
	PathTruncated bool     `json:"path_truncated,omitempty"`
}

type ResourceImpact struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	State           string  `json:"state"`
	BaselineState   string  `json:"baseline_state"`
	NewlyAffected   bool    `json:"newly_affected"`
	Causes          []Cause `json:"causes"`
	CausesTruncated bool    `json:"causes_truncated,omitempty"`
}

type JobImpact struct {
	ID            string   `json:"id"`
	Owner         string   `json:"owner"`
	Status        string   `json:"status"`
	State         string   `json:"state"`
	BaselineState string   `json:"baseline_state"`
	Resources     []string `json:"resources"`
}

type Capacity struct {
	Domain      string `json:"domain"`
	Healthy     int    `json:"healthy"`
	Unavailable int    `json:"unavailable"`
	Unknown     int    `json:"unknown"`
}

type Finding struct {
	Code    string `json:"code"`
	Verdict string `json:"verdict"`
	Message string `json:"message"`
}

type WaveReport struct {
	ID        string           `json:"id"`
	Targets   []string         `json:"targets"`
	Verdict   string           `json:"verdict"`
	Resources []ResourceImpact `json:"affected_resources"`
	Jobs      []JobImpact      `json:"affected_jobs"`
	Capacity  []Capacity       `json:"compute_capacity"`
	Findings  []Finding        `json:"findings"`
}

type Report struct {
	SchemaVersion    string       `json:"schema_version"`
	EvaluatedAt      time.Time    `json:"evaluated_at"`
	SnapshotAt       time.Time    `json:"snapshot_at"`
	Verdict          string       `json:"verdict"`
	Assumptions      []string     `json:"assumptions"`
	Limitations      []string     `json:"limitations"`
	SnapshotFindings []Finding    `json:"snapshot_findings"`
	BaselineCapacity []Capacity   `json:"baseline_compute_capacity"`
	Waves            []WaveReport `json:"waves"`
}

// ExitCode distinguishes a modeled blocker, unknown assessment, and valid pass.
// Malformed input is returned as an error by Decode/Evaluate; the CLI exits 2.
func ExitCode(verdict string) int {
	switch verdict {
	case "pass":
		return 0
	case "blocked":
		return 1
	case "unknown":
		return 3
	default:
		return 2
	}
}

func active(status string) bool {
	return status == "running" || status == "suspended" || status == "completing"
}
