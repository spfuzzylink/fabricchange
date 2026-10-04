package fabricchange

import (
	"fmt"
	"sort"
	"time"
)

const maxCauses = 16
const maxPath = 64

type state struct {
	value     string
	causes    []Cause
	truncated bool
}

func addCause(s *state, cause Cause, pathBudget *int) {
	if len(s.causes) >= maxCauses {
		s.truncated = true
		return
	}
	if len(cause.Path) > maxPath {
		cause.Path = cause.Path[:maxPath]
		cause.PathTruncated = true
	}
	// Conservatively charge text, escaping, and JSON framing overhead. This
	// budget is shared across baseline and all waves, not reset per node.
	cost := 2*len(cause.Reason) + 128
	for _, id := range cause.Path {
		cost += 2*len(id) + 64
	}
	if *pathBudget < cost {
		s.truncated = true
		return
	}
	*pathBudget -= cost
	s.causes = append(s.causes, cause)
}

func resourceStates(g graph, targets map[string]bool, pathBudget *int) map[string]state {
	states := make(map[string]state, len(g.ids))
	for _, id := range g.order {
		r := g.resources[id]
		s := state{value: r.Status}
		if r.Status != "up" {
			addCause(&s, Cause{Path: []string{id}, Reason: "snapshot resource status is " + r.Status}, pathBudget)
		}
		if targets[id] {
			s.value = "down"
			addCause(&s, Cause{Path: []string{id}, Reason: "proposed maintenance target is unavailable during this wave"}, pathBudget)
		}
		groups := append([]DependencyGroup(nil), r.Dependencies...)
		sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
		for _, group := range groups {
			up, unknown := 0, 0
			for _, member := range group.Members {
				switch states[member].value {
				case "up":
					up++
				case "unknown":
					unknown++
				}
			}
			if up >= group.MinAvailable {
				continue
			}
			value := "unknown"
			if up+unknown < group.MinAvailable {
				value = "down"
			}
			if value == "down" || s.value == "up" {
				s.value = value
			}
			addCause(&s, Cause{Path: []string{id}, Reason: fmt.Sprintf("dependency group %s needs %d available members; %d up, %d unknown, %d down", group.ID, group.MinAvailable, up, unknown, len(group.Members)-up-unknown)}, pathBudget)
			members := append([]string(nil), group.Members...)
			sort.Strings(members)
			for _, member := range members {
				child := states[member]
				if child.value == "up" {
					continue
				}
				for _, cause := range child.causes {
					path := make([]string, 1, 1+len(cause.Path))
					path[0] = id
					path = append(path, cause.Path...)
					addCause(&s, Cause{Path: path, Reason: cause.Reason, PathTruncated: cause.PathTruncated}, pathBudget)
				}
				if child.truncated {
					s.truncated = true
				}
			}
		}
		states[id] = s
	}
	return states
}

func capacities(g graph, states map[string]state) []Capacity {
	counts := map[string]Capacity{"*": {Domain: "*"}}
	for _, domain := range g.domains {
		counts[domain] = Capacity{Domain: domain}
	}
	for _, id := range g.ids {
		r := g.resources[id]
		if r.Kind != "compute" {
			continue
		}
		domains := append([]string{"*"}, r.FailureDomains...)
		for _, domain := range domains {
			c := counts[domain]
			switch states[id].value {
			case "up":
				c.Healthy++
			case "down":
				c.Unavailable++
			default:
				c.Unknown++
			}
			counts[domain] = c
		}
	}
	result := []Capacity{counts["*"]}
	for _, domain := range g.domains {
		result = append(result, counts[domain])
	}
	return result
}

func combine(a, b string) string {
	if a == "blocked" || b == "blocked" {
		return "blocked"
	}
	if a == "unknown" || b == "unknown" {
		return "unknown"
	}
	return "pass"
}

func jobState(job Job, states map[string]state) string {
	value := "up"
	for _, id := range job.Resources {
		if states[id].value == "down" {
			return "down"
		}
		if states[id].value == "unknown" {
			value = "unknown"
		}
	}
	return value
}

// Evaluate performs pure, offline evaluation at an explicit reference time.
// Verdict pass only means no modeled blocker was found under the stated
// assumptions; it never certifies a production change as safe.
func Evaluate(in Input, now time.Time) (Report, error) {
	var report Report
	g, err := validate(in)
	if err != nil {
		return report, err
	}
	if now.IsZero() {
		return report, fmt.Errorf("evaluation time is required")
	}
	report = Report{
		SchemaVersion: SchemaVersion, EvaluatedAt: now.UTC(), SnapshotAt: in.Snapshot.CapturedAt.UTC(), Verdict: "pass",
		Assumptions: []string{
			"Each wave starts from the same snapshot. Every earlier wave must be fully restored and revalidated before the next wave; restoration is assumed, not observed.",
			"All targets in a wave are unavailable simultaneously. Existing down resources remain down in every wave.",
			"Declared dependencies are complete; every group needs its min_available members and all groups must be satisfied.",
			"All running, suspended, and completing allocations remain active. No job completion, migration, checkpoint, or requeue is assumed.",
			"Pass means the declared offline checks passed; zero affected jobs does not establish production safety.",
		},
		Limitations: []string{
			"Reservations, pending-job scheduling, licenses, checkpointability, maintenance duration, bandwidth headroom, and scheduler policy are not modeled.",
			"A topology is a customer-supplied dependency model, not measured packet routing, storage reachability, or proof of k-of-n independence.",
			"Snapshot completeness is a caller assertion. Slurm capture visibility and cross-system atomicity must be checked separately.",
			"No hardware validation or production Slurm validation has been performed for this pre-alpha.",
		},
		SnapshotFindings: []Finding{}, Waves: []WaveReport{},
	}
	issue := func(code, message string) {
		report.SnapshotFindings = append(report.SnapshotFindings, Finding{Code: code, Verdict: "unknown", Message: message})
		report.Verdict = combine(report.Verdict, "unknown")
	}
	if !*in.Snapshot.Complete {
		issue("incomplete_snapshot", "Snapshot completeness is false; missing jobs or dependencies can hide impact.")
	}
	if len(in.Snapshot.MissingScopes) > 0 {
		scopes := append([]string(nil), in.Snapshot.MissingScopes...)
		sort.Strings(scopes)
		issue("missing_scopes", fmt.Sprintf("Snapshot declares missing scopes: %v", scopes))
	}
	if in.Snapshot.CapturedAt.After(now) {
		issue("future_snapshot", "Snapshot timestamp is later than evaluation time.")
	} else if now.Sub(in.Snapshot.CapturedAt) > time.Duration(in.Policy.MaxSnapshotAgeSeconds)*time.Second {
		issue("stale_snapshot", "Snapshot exceeds max_snapshot_age_seconds.")
	}
	for _, id := range g.ids {
		if g.resources[id].Status == "unknown" {
			issue("unknown_resource_status", "Resource "+id+" has unknown status.")
		}
	}
	pathBudget := 4 << 20 // Conservative diagnostic byte budget; truncation never changes decisions.
	baseline := resourceStates(g, nil, &pathBudget)
	report.BaselineCapacity = capacities(g, baseline)
	jobs := append([]Job(nil), in.Snapshot.Jobs...)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	minimums := append([]DomainMinimum(nil), in.Policy.MinHealthyByDomain...)
	sort.Slice(minimums, func(i, j int) bool { return minimums[i].Domain < minimums[j].Domain })
	for _, wave := range in.Change.Waves {
		targets := map[string]bool{}
		for _, id := range wave.Targets {
			targets[id] = true
		}
		states := resourceStates(g, targets, &pathBudget)
		w := WaveReport{ID: wave.ID, Targets: append([]string(nil), wave.Targets...), Verdict: "pass", Resources: []ResourceImpact{}, Jobs: []JobImpact{}, Capacity: capacities(g, states), Findings: []Finding{}}
		sort.Strings(w.Targets)
		if len(report.SnapshotFindings) > 0 {
			w.Verdict = "unknown"
		}
		finding := func(code, verdict, message string) {
			w.Findings = append(w.Findings, Finding{Code: code, Verdict: verdict, Message: message})
			w.Verdict = combine(w.Verdict, verdict)
		}
		for _, id := range g.ids {
			s := states[id]
			if s.value == "up" {
				continue
			}
			w.Resources = append(w.Resources, ResourceImpact{ID: id, Kind: g.resources[id].Kind, State: s.value, BaselineState: baseline[id].value, NewlyAffected: baseline[id].value == "up", Causes: s.causes, CausesTruncated: s.truncated})
		}
		for _, job := range jobs {
			if !active(job.Status) {
				continue
			}
			value := jobState(job, states)
			if value == "up" {
				continue
			}
			affected := []string{}
			for _, id := range job.Resources {
				if states[id].value != "up" {
					affected = append(affected, id)
				}
			}
			sort.Strings(affected)
			w.Jobs = append(w.Jobs, JobImpact{ID: job.ID, Owner: job.Owner, Status: job.Status, State: value, BaselineState: jobState(job, baseline), Resources: affected})
			verdict := "unknown"
			if value == "down" {
				verdict = "blocked"
			}
			finding("active_job_affected", verdict, fmt.Sprintf("Active job %s (%s) has %s resources; no completion or checkpoint is assumed.", job.ID, job.Owner, value))
		}
		if limit := in.Policy.MaxUnavailableComputeNodes; limit != nil {
			c := w.Capacity[0]
			if c.Unavailable > *limit {
				finding("unavailable_compute_budget", "blocked", fmt.Sprintf("%d compute nodes unavailable exceeds budget %d (includes pre-existing failures).", c.Unavailable, *limit))
			} else if c.Unavailable+c.Unknown > *limit {
				finding("unavailable_compute_budget", "unknown", fmt.Sprintf("%d unavailable and %d unknown compute nodes may exceed budget %d.", c.Unavailable, c.Unknown, *limit))
			}
		}
		byDomain := map[string]Capacity{}
		for _, c := range w.Capacity {
			byDomain[c.Domain] = c
		}
		for _, minimum := range minimums {
			c := byDomain[minimum.Domain]
			if c.Healthy >= minimum.MinHealthyNodes {
				continue
			}
			verdict := "unknown"
			if c.Healthy+c.Unknown < minimum.MinHealthyNodes {
				verdict = "blocked"
			}
			finding("domain_capacity", verdict, fmt.Sprintf("Domain %s requires %d healthy compute nodes; %d healthy, %d unknown, %d unavailable.", minimum.Domain, minimum.MinHealthyNodes, c.Healthy, c.Unknown, c.Unavailable))
		}
		report.Verdict = combine(report.Verdict, w.Verdict)
		report.Waves = append(report.Waves, w)
	}
	return report, nil
}
