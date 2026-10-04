package fabricchange

import (
	"fmt"
	"sort"
	"strings"
)

type graph struct {
	resources map[string]Resource
	ids       []string
	order     []string
	domains   []string
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 256 || strings.TrimSpace(s) != s {
		return false
	}
	for _, c := range s {
		if c <= 32 || c == 127 {
			return false
		}
	}
	return true
}

func uniqueIDs(values []string, context string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if !validID(value) {
			return fmt.Errorf("%s: invalid identifier %q", context, value)
		}
		if seen[value] {
			return fmt.Errorf("%s: duplicate %q", context, value)
		}
		seen[value] = true
	}
	return nil
}

func validate(in Input) (graph, error) {
	g := graph{resources: map[string]Resource{}}
	if in.SchemaVersion != SchemaVersion {
		return g, fmt.Errorf("schema_version must be %q", SchemaVersion)
	}
	if in.Snapshot.CapturedAt.IsZero() {
		return g, fmt.Errorf("snapshot.captured_at is required")
	}
	if in.Snapshot.Complete == nil {
		return g, fmt.Errorf("snapshot.complete must be explicitly true or false")
	}
	if in.Snapshot.Jobs == nil {
		return g, fmt.Errorf("snapshot.jobs is required; use [] for an explicitly empty allocation snapshot")
	}
	if len(in.Snapshot.Resources) == 0 || len(in.Snapshot.Resources) > 100000 {
		return g, fmt.Errorf("snapshot must contain 1..100000 resources")
	}
	if len(in.Snapshot.Jobs) > 100000 {
		return g, fmt.Errorf("at most 100000 jobs are supported")
	}
	if in.Policy.MaxSnapshotAgeSeconds <= 0 || in.Policy.MaxSnapshotAgeSeconds > 86400*365 {
		return g, fmt.Errorf("max_snapshot_age_seconds must be 1..31536000")
	}
	if in.Policy.MaxUnavailableComputeNodes != nil && *in.Policy.MaxUnavailableComputeNodes < 0 {
		return g, fmt.Errorf("max_unavailable_compute_nodes cannot be negative")
	}
	if !in.Change.PriorWaveRestored {
		return g, fmt.Errorf("change.prior_wave_restored must explicitly acknowledge the restoration assumption with true")
	}
	if len(in.Change.Waves) == 0 || len(in.Change.Waves) > 100 {
		return g, fmt.Errorf("change must contain 1..100 waves")
	}
	if (len(in.Snapshot.Resources)+len(in.Snapshot.Jobs))*(len(in.Change.Waves)+1) > 30000 {
		return g, fmt.Errorf("planning work limit exceeded: (resources + jobs) * (waves + baseline) must be <= 30000; split the assessment")
	}
	domains := map[string]bool{}
	domainMemberships := 0
	for _, r := range in.Snapshot.Resources {
		if !validID(r.ID) {
			return g, fmt.Errorf("invalid resource ID %q", r.ID)
		}
		if _, ok := g.resources[r.ID]; ok {
			return g, fmt.Errorf("duplicate resource %q", r.ID)
		}
		switch r.Kind {
		case "compute", "fabric", "storage", "power", "service":
		default:
			return g, fmt.Errorf("resource %q has unsupported kind %q", r.ID, r.Kind)
		}
		switch r.Status {
		case "up", "down", "unknown":
		default:
			return g, fmt.Errorf("resource %q has invalid status %q", r.ID, r.Status)
		}
		if err := uniqueIDs(r.FailureDomains, "failure domains of "+r.ID); err != nil {
			return g, err
		}
		domainMemberships += len(r.FailureDomains)
		for _, domain := range r.FailureDomains {
			if domain == "*" {
				return g, fmt.Errorf("failure domain '*' is reserved for the global capacity summary")
			}
		}
		if r.Kind == "compute" {
			for _, d := range r.FailureDomains {
				domains[d] = true
			}
		}
		g.resources[r.ID] = r
		g.ids = append(g.ids, r.ID)
	}
	sort.Strings(g.ids)
	for d := range domains {
		g.domains = append(g.domains, d)
	}
	sort.Strings(g.domains)
	if (len(in.Snapshot.Resources)+len(in.Snapshot.Jobs)+len(g.domains))*(len(in.Change.Waves)+1) > 30000 {
		return g, fmt.Errorf("planning work limit exceeded: (resources + jobs + domains) * (waves + baseline) must be <= 30000")
	}
	indegree := map[string]int{}
	reverse := map[string][]string{}
	edges := domainMemberships
	for _, id := range g.ids {
		r := g.resources[id]
		groups := map[string]bool{}
		deps := map[string]bool{}
		for _, group := range r.Dependencies {
			if !validID(group.ID) || groups[group.ID] {
				return g, fmt.Errorf("resource %q has duplicate/invalid dependency group %q", id, group.ID)
			}
			groups[group.ID] = true
			if group.MinAvailable < 1 || group.MinAvailable > len(group.Members) {
				return g, fmt.Errorf("resource %q group %q min_available must be 1..member count", id, group.ID)
			}
			if err := uniqueIDs(group.Members, "members of "+id+"/"+group.ID); err != nil {
				return g, err
			}
			for _, member := range group.Members {
				if _, ok := g.resources[member]; !ok {
					return g, fmt.Errorf("resource %q references unknown resource %q", id, member)
				}
				deps[member] = true
				edges++
				if edges > 1000000 {
					return g, fmt.Errorf("dependency member limit of 1000000 exceeded")
				}
			}
		}
		indegree[id] = len(deps)
		for member := range deps {
			reverse[member] = append(reverse[member], id)
		}
	}
	// Kahn traversal is iterative; a long or cyclic graph cannot overflow stack.
	queue := []string{}
	for _, id := range g.ids {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		g.order = append(g.order, id)
		for _, dependent := range reverse[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	if len(g.order) != len(g.ids) {
		return g, fmt.Errorf("dependency graph contains a cycle")
	}
	jobs := map[string]bool{}
	for _, job := range in.Snapshot.Jobs {
		if !validID(job.ID) || jobs[job.ID] {
			return g, fmt.Errorf("duplicate/invalid job ID %q", job.ID)
		}
		jobs[job.ID] = true
		if !validID(job.Owner) {
			return g, fmt.Errorf("job %q must have a valid owner", job.ID)
		}
		switch job.Status {
		case "running", "suspended", "completing", "pending", "completed", "cancelled", "failed":
		default:
			return g, fmt.Errorf("job %q has unsupported status %q", job.ID, job.Status)
		}
		if active(job.Status) && len(job.Resources) == 0 {
			return g, fmt.Errorf("active job %q has no resource allocation", job.ID)
		}
		if err := uniqueIDs(job.Resources, "job "+job.ID); err != nil {
			return g, err
		}
		for _, id := range job.Resources {
			edges++ // Shared dependency/allocation work accounting, below.
			if _, ok := g.resources[id]; !ok {
				return g, fmt.Errorf("job %q references unknown resource %q", job.ID, id)
			}
		}
	}
	if edges*(len(in.Change.Waves)+1) > 500000 {
		return g, fmt.Errorf("dependency/allocation/domain work limit exceeded: memberships * (waves + baseline) must be <= 500000")
	}
	waves := map[string]bool{}
	for _, wave := range in.Change.Waves {
		if !validID(wave.ID) || waves[wave.ID] {
			return g, fmt.Errorf("duplicate/invalid wave ID %q", wave.ID)
		}
		waves[wave.ID] = true
		if len(wave.Targets) == 0 {
			return g, fmt.Errorf("wave %q has no targets", wave.ID)
		}
		if err := uniqueIDs(wave.Targets, "wave "+wave.ID); err != nil {
			return g, err
		}
		for _, id := range wave.Targets {
			if _, ok := g.resources[id]; !ok {
				return g, fmt.Errorf("wave %q targets unknown resource %q", wave.ID, id)
			}
		}
	}
	mins := map[string]bool{}
	for _, minimum := range in.Policy.MinHealthyByDomain {
		if !domains[minimum.Domain] {
			return g, fmt.Errorf("policy references domain %q without compute nodes", minimum.Domain)
		}
		if mins[minimum.Domain] {
			return g, fmt.Errorf("duplicate domain minimum %q", minimum.Domain)
		}
		mins[minimum.Domain] = true
		if minimum.MinHealthyNodes < 0 {
			return g, fmt.Errorf("domain minimum cannot be negative")
		}
	}
	return g, nil
}
