package fabricchange

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type AllocationCapture struct {
	SchemaVersion string    `json:"schema_version"`
	Source        string    `json:"source"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Complete      bool      `json:"complete"`
	Jobs          []Job     `json:"jobs"`
	Limitations   []string  `json:"limitations"`
}

// ParseSlurmCapture parses the explicit expanded-node format emitted by the
// companion script. It intentionally never treats job capture as a complete
// inventory and never expands compressed Slurm hostlists itself.
func ParseSlurmCapture(r io.Reader) (AllocationCapture, error) {
	c := AllocationCapture{SchemaVersion: SchemaVersion, Source: "squeue-expanded-v1", Jobs: []Job{}, Limitations: []string{
		"Allocation capture only: add independently verified resource health, dependencies, failure domains, and a completeness assessment before planning.",
		"squeue visibility depends on cluster permissions; capture is not atomic and reservations, job steps, and pending scheduling are omitted.",
		"Validation is limited to documented scenarios and versions; verify capture visibility and behavior on the target cluster.",
	}}
	limited := &io.LimitedReader{R: r, N: MaxInputBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	seen := map[string]bool{}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !c.FinishedAt.IsZero() {
			return c, fmt.Errorf("line %d: content after capture footer", lineNumber)
		}
		if strings.HasPrefix(line, "# started_at=") {
			if !c.StartedAt.IsZero() || len(c.Jobs) > 0 {
				return c, fmt.Errorf("duplicate or misplaced start header")
			}
			t, err := time.Parse(time.RFC3339, strings.TrimPrefix(line, "# started_at="))
			if err != nil {
				return c, err
			}
			c.StartedAt = t
			continue
		}
		if c.StartedAt.IsZero() {
			return c, fmt.Errorf("missing capture start header")
		}
		if strings.HasPrefix(line, "# finished_at=") {
			t, err := time.Parse(time.RFC3339, strings.TrimPrefix(line, "# finished_at="))
			if err != nil {
				return c, err
			}
			c.FinishedAt = t
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			return c, fmt.Errorf("line %d: expected job_id|owner|state|expanded,nodes", lineNumber)
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		job := Job{ID: fields[0], Owner: fields[1], Status: strings.ToLower(fields[2]), Resources: strings.Split(fields[3], ",")}
		if !validID(job.ID) || seen[job.ID] {
			return c, fmt.Errorf("line %d: duplicate/invalid job ID", lineNumber)
		}
		seen[job.ID] = true
		if !validID(job.Owner) || !active(job.Status) {
			return c, fmt.Errorf("line %d: invalid owner or unsupported active job status", lineNumber)
		}
		if err := uniqueIDs(job.Resources, "Slurm job "+job.ID); err != nil {
			return c, err
		}
		for _, node := range job.Resources {
			if strings.ContainsAny(node, "[]()|") {
				return c, fmt.Errorf("line %d: node names must be expanded by scontrol show hostnames", lineNumber)
			}
		}
		sort.Strings(job.Resources)
		c.Jobs = append(c.Jobs, job)
	}
	if err := scanner.Err(); err != nil {
		return c, err
	}
	if limited.N <= 0 {
		return c, fmt.Errorf("capture exceeds byte limit")
	}
	if c.StartedAt.IsZero() || c.FinishedAt.IsZero() || c.FinishedAt.Before(c.StartedAt) {
		return c, fmt.Errorf("capture requires ordered started_at/finished_at headers")
	}
	sort.Slice(c.Jobs, func(i, j int) bool { return c.Jobs[i].ID < c.Jobs[j].ID })
	return c, nil
}
