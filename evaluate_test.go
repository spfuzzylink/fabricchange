package fabricchange

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var referenceTime = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

func fixture() Input {
	return Input{SchemaVersion: "1", Snapshot: Snapshot{CapturedAt: referenceTime.Add(-time.Minute), Complete: boolPtr(true), Jobs: []Job{{ID: "101", Owner: "team-a", Status: "running", Resources: []string{"gpu01", "gpu02"}}}, Resources: []Resource{
		{ID: "fabric-a", Kind: "fabric", Status: "up"}, {ID: "fabric-b", Kind: "fabric", Status: "up"}, {ID: "shared-store", Kind: "storage", Status: "up"},
		{ID: "gpu01", Kind: "compute", Status: "up", FailureDomains: []string{"rack-a"}, Dependencies: []DependencyGroup{{ID: "fabric", Members: []string{"fabric-a", "fabric-b"}, MinAvailable: 1}, {ID: "storage", Members: []string{"shared-store"}, MinAvailable: 1}}},
		{ID: "gpu02", Kind: "compute", Status: "up", FailureDomains: []string{"rack-b"}, Dependencies: []DependencyGroup{{ID: "fabric", Members: []string{"fabric-a", "fabric-b"}, MinAvailable: 1}, {ID: "storage", Members: []string{"shared-store"}, MinAvailable: 1}}},
	}}, Change: Change{PriorWaveRestored: true, Waves: []Wave{{ID: "wave-1", Targets: []string{"fabric-a"}}}}, Policy: Policy{MaxSnapshotAgeSeconds: 300}}
}

func evaluateTest(t *testing.T, in Input) Report {
	t.Helper()
	r, err := Evaluate(in, referenceTime)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func setStatus(in *Input, id, status string) {
	for i := range in.Snapshot.Resources {
		if in.Snapshot.Resources[i].ID == id {
			in.Snapshot.Resources[i].Status = status
		}
	}
}

func TestSharedStorageImpactsUntouchedNodesAndMultiNodeJob(t *testing.T) {
	in := fixture()
	in.Change.Waves[0].Targets = []string{"shared-store"}
	r := evaluateTest(t, in)
	if r.Verdict != "blocked" || len(r.Waves[0].Jobs) != 1 || len(r.Waves[0].Jobs[0].Resources) != 2 {
		t.Fatalf("unexpected job impact: %+v", r.Waves[0])
	}
	found := false
	for _, impact := range r.Waves[0].Resources {
		if impact.ID == "gpu01" {
			for _, c := range impact.Causes {
				if reflect.DeepEqual(c.Path, []string{"gpu01", "shared-store"}) {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("missing causal path from untouched compute node to shared storage target")
	}
}

func TestRedundancyAndExistingFailures(t *testing.T) {
	in := fixture()
	if r := evaluateTest(t, in); r.Verdict != "pass" || len(r.Waves[0].Jobs) != 0 {
		t.Fatalf("one redundant fabric path should remain: %+v", r)
	}
	setStatus(&in, "fabric-b", "down")
	r := evaluateTest(t, in)
	if r.Verdict != "blocked" || r.Waves[0].Capacity[0].Unavailable != 2 {
		t.Fatalf("pre-existing failed path ignored: %+v", r.Waves[0])
	}
}

func TestKOfNAndEveryGroup(t *testing.T) {
	in := fixture()
	in.Snapshot.Resources = append(in.Snapshot.Resources, Resource{ID: "fabric-c", Kind: "fabric", Status: "up"})
	for i := range in.Snapshot.Resources {
		if in.Snapshot.Resources[i].Kind == "compute" {
			in.Snapshot.Resources[i].Dependencies[0] = DependencyGroup{ID: "fabric", Members: []string{"fabric-a", "fabric-b", "fabric-c"}, MinAvailable: 2}
		}
	}
	if r := evaluateTest(t, in); r.Verdict != "pass" {
		t.Fatal("2 of 3 should satisfy group")
	}
	in.Change.Waves[0].Targets = []string{"fabric-a", "fabric-b"}
	if r := evaluateTest(t, in); r.Verdict != "blocked" {
		t.Fatal("1 of 3 cannot satisfy 2-required group")
	}
}

func TestFalseAssuranceCases(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Input)
	}{
		{"stale", func(in *Input) { in.Snapshot.CapturedAt = referenceTime.Add(-301 * time.Second) }},
		{"future", func(in *Input) { in.Snapshot.CapturedAt = referenceTime.Add(time.Second) }},
		{"incomplete", func(in *Input) { in.Snapshot.Complete = boolPtr(false) }},
		{"missing_scope_despite_complete_flag", func(in *Input) { in.Snapshot.MissingScopes = []string{"storage"} }},
		{"unknown_status_even_when_redundancy_works", func(in *Input) { setStatus(in, "fabric-b", "unknown") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture()
			tc.change(&in)
			r := evaluateTest(t, in)
			if r.Verdict != "unknown" {
				t.Fatalf("got %s, want unknown", r.Verdict)
			}
			if r.Waves[0].Verdict != "unknown" {
				t.Fatal("wave hid snapshot uncertainty")
			}
		})
	}
}

func TestKnownBlockerRemainsVisibleWithUncertainty(t *testing.T) {
	in := fixture()
	in.Snapshot.Complete = boolPtr(false)
	in.Change.Waves[0].Targets = []string{"gpu01"}
	r := evaluateTest(t, in)
	if r.Verdict != "blocked" || len(r.SnapshotFindings) == 0 {
		t.Fatal("must report known blocker and uncertainty together")
	}
}

func TestSuspendedAndCompletingJobsRemainActive(t *testing.T) {
	for _, status := range []string{"running", "suspended", "completing"} {
		t.Run(status, func(t *testing.T) {
			in := fixture()
			in.Snapshot.Jobs[0].Status = status
			in.Change.Waves[0].Targets = []string{"gpu01"}
			if evaluateTest(t, in).Verdict != "blocked" {
				t.Fatal("active allocation ignored")
			}
		})
	}
	for _, status := range []string{"pending", "completed", "cancelled", "failed"} {
		t.Run(status, func(t *testing.T) {
			in := fixture()
			in.Snapshot.Jobs[0].Status = status
			in.Change.Waves[0].Targets = []string{"gpu01"}
			if evaluateTest(t, in).Verdict != "pass" {
				t.Fatal("inactive job treated as active")
			}
		})
	}
}

func TestCapacityBudgetsIncludePreExistingDownNodes(t *testing.T) {
	in := fixture()
	in.Snapshot.Jobs = []Job{}
	setStatus(&in, "gpu02", "down")
	in.Change.Waves[0].Targets = []string{"gpu01"}
	in.Policy.MaxUnavailableComputeNodes = intPtr(1)
	r := evaluateTest(t, in)
	if r.Verdict != "blocked" || r.Waves[0].Capacity[0].Unavailable != 2 {
		t.Fatalf("wrong capacity: %+v", r)
	}
	if r.BaselineCapacity[0].Unavailable != 1 {
		t.Fatal("baseline failure not reported")
	}
}

func TestDomainCapacityCannotBorrowFromAnotherDomain(t *testing.T) {
	in := fixture()
	in.Snapshot.Jobs = []Job{}
	in.Change.Waves[0].Targets = []string{"gpu01"}
	in.Policy.MinHealthyByDomain = []DomainMinimum{{Domain: "rack-a", MinHealthyNodes: 1}}
	r := evaluateTest(t, in)
	if r.Verdict != "blocked" {
		t.Fatal("healthy rack-b cannot satisfy rack-a minimum")
	}
	setStatus(&in, "gpu01", "unknown")
	in.Change.Waves[0].Targets = []string{"fabric-a"}
	if evaluateTest(t, in).Verdict != "unknown" {
		t.Fatal("unknown capacity should not pass or become proven unavailable")
	}
}

func TestWaveRestorationIsExplicitAndNonCumulative(t *testing.T) {
	in := fixture()
	in.Change.Waves = append(in.Change.Waves, Wave{ID: "wave-2", Targets: []string{"fabric-b"}})
	r := evaluateTest(t, in)
	if r.Verdict != "pass" || len(r.Assumptions) == 0 {
		t.Fatal("restored waves should retain one working path each")
	}
	in.Change.PriorWaveRestored = false
	if _, err := Evaluate(in, referenceTime); err == nil {
		t.Fatal("missing restoration assumption accepted")
	}
}

func TestValidationRejectsGraphAndIdentityAmbiguity(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Input)
	}{
		{"duplicate_resource", func(in *Input) { in.Snapshot.Resources = append(in.Snapshot.Resources, in.Snapshot.Resources[0]) }},
		{"unknown_dependency", func(in *Input) { in.Snapshot.Resources[3].Dependencies[0].Members = []string{"absent"} }},
		{"cycle", func(in *Input) {
			in.Snapshot.Resources[0].Dependencies = []DependencyGroup{{ID: "back", Members: []string{"gpu01"}, MinAvailable: 1}}
		}},
		{"self_cycle", func(in *Input) {
			in.Snapshot.Resources[0].Dependencies = []DependencyGroup{{ID: "self", Members: []string{"fabric-a"}, MinAvailable: 1}}
		}},
		{"duplicate_job", func(in *Input) { in.Snapshot.Jobs = append(in.Snapshot.Jobs, in.Snapshot.Jobs[0]) }},
		{"unknown_job_resource", func(in *Input) { in.Snapshot.Jobs[0].Resources = []string{"absent"} }},
		{"missing_active_allocation", func(in *Input) { in.Snapshot.Jobs[0].Resources = nil }},
		{"missing_jobs", func(in *Input) { in.Snapshot.Jobs = nil }},
		{"missing_completeness", func(in *Input) { in.Snapshot.Complete = nil }},
		{"duplicate_group_member", func(in *Input) { in.Snapshot.Resources[3].Dependencies[0].Members = []string{"fabric-a", "fabric-a"} }},
		{"invalid_k", func(in *Input) { in.Snapshot.Resources[3].Dependencies[0].MinAvailable = 3 }},
		{"zero_k", func(in *Input) { in.Snapshot.Resources[3].Dependencies[0].MinAvailable = 0 }},
		{"unknown_target", func(in *Input) { in.Change.Waves[0].Targets = []string{"absent"} }},
		{"duplicate_target", func(in *Input) { in.Change.Waves[0].Targets = []string{"fabric-a", "fabric-a"} }},
		{"duplicate_wave", func(in *Input) { in.Change.Waves = append(in.Change.Waves, in.Change.Waves[0]) }},
		{"global_sentinel_domain", func(in *Input) { in.Snapshot.Resources[3].FailureDomains = []string{"*"} }},
		{"unknown_policy_domain", func(in *Input) {
			in.Policy.MinHealthyByDomain = []DomainMinimum{{Domain: "absent", MinHealthyNodes: 1}}
		}},
		{"negative_budget", func(in *Input) { in.Policy.MaxUnavailableComputeNodes = intPtr(-1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture()
			tc.change(&in)
			if _, err := Evaluate(in, referenceTime); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestDeterministicOutputUnderInventoryPermutation(t *testing.T) {
	in := fixture()
	in.Change.Waves[0].Targets = []string{"shared-store", "fabric-a"}
	a := evaluateTest(t, in)
	for i, j := 0, len(in.Snapshot.Resources)-1; i < j; i, j = i+1, j-1 {
		in.Snapshot.Resources[i], in.Snapshot.Resources[j] = in.Snapshot.Resources[j], in.Snapshot.Resources[i]
	}
	for i := range in.Snapshot.Resources {
		r := &in.Snapshot.Resources[i]
		for a, b := 0, len(r.Dependencies)-1; a < b; a, b = a+1, b-1 {
			r.Dependencies[a], r.Dependencies[b] = r.Dependencies[b], r.Dependencies[a]
		}
	}
	in.Change.Waves[0].Targets = []string{"fabric-a", "shared-store"}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(evaluateTest(t, in))
	if string(x) != string(y) {
		t.Fatal("output changed with inventory ordering")
	}
}

func TestDeepGraphDoesNotUseRecursiveTraversal(t *testing.T) {
	in := fixture()
	in.Snapshot.Jobs = []Job{}
	in.Snapshot.Resources = []Resource{{ID: "r0", Kind: "service", Status: "up"}}
	for i := 1; i < 2000; i++ {
		in.Snapshot.Resources = append(in.Snapshot.Resources, Resource{ID: fmt.Sprintf("r%d", i), Kind: "service", Status: "up", Dependencies: []DependencyGroup{{ID: "parent", Members: []string{fmt.Sprintf("r%d", i-1)}, MinAvailable: 1}}})
	}
	in.Change.Waves[0].Targets = []string{"r0"}
	r := evaluateTest(t, in)
	if len(r.Waves[0].Resources) != 2000 {
		t.Fatal("lost deep dependency impact")
	}
	for _, impact := range r.Waves[0].Resources {
		if len(impact.Causes) > 16 {
			t.Fatal("unbounded cause list")
		}
		for _, c := range impact.Causes {
			if len(c.Path) > 64 {
				t.Fatal("unbounded path")
			}
		}
	}
}

func TestDecodeStrictness(t *testing.T) {
	for _, input := range []string{`{"schema_version":"1","schema_version":"2"}`, `{"extra":1}`, `{} {}`, `{"snapshot":{"complete":true,"complete":false}}`, `{"snapshot":{"complete":false,"Complete":true}}`, `{"ſnapshot":{}}`, `{"policy":{"min_healthy_by_domain":[{"domain":"rack-a","min_healthy_nodes":null}]}}`, `{"policy":{"min_healthy_by_domain":[{"domain":"rack-a"}]}}`, strings.Repeat("[", 66) + strings.Repeat("]", 66)} {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	b, _ := json.Marshal(fixture())
	in, err := Decode(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if evaluateTest(t, in).Verdict != "pass" {
		t.Fatal("valid encoded input failed")
	}
}

func scaleFixture(n int) Input {
	in := fixture()
	in.Snapshot.Resources = in.Snapshot.Resources[:3]
	in.Snapshot.Jobs = []Job{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("gpu%06d", i)
		in.Snapshot.Resources = append(in.Snapshot.Resources, Resource{ID: id, Kind: "compute", Status: "up", FailureDomains: []string{fmt.Sprintf("rack-%d", i/64)}, Dependencies: []DependencyGroup{{ID: "fabric", Members: []string{"fabric-a", "fabric-b"}, MinAvailable: 1}, {ID: "storage", Members: []string{"shared-store"}, MinAvailable: 1}}})
		if i%8 == 0 {
			in.Snapshot.Jobs = append(in.Snapshot.Jobs, Job{ID: fmt.Sprintf("job%d", i), Owner: "benchmark", Status: "running", Resources: []string{id}})
		}
	}
	in.Change.Waves[0].Targets = []string{"shared-store"}
	return in
}

func TestGeneratedScale(t *testing.T) {
	for _, n := range []int{1000, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			r := evaluateTest(t, scaleFixture(n))
			if r.Waves[0].Capacity[0].Unavailable != n || len(r.Waves[0].Jobs) != (n+7)/8 {
				t.Fatal("large synthetic graph missed resources/jobs")
			}
		})
	}
}

func BenchmarkPlanner(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			in := scaleFixture(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Evaluate(in, referenceTime); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestPlanningWorkLimitAndDiagnosticBudget(t *testing.T) {
	in := scaleFixture(10000)
	in.Change.Waves = append(in.Change.Waves, Wave{ID: "second", Targets: []string{"fabric-a"}})
	if _, err := Evaluate(in, referenceTime); err == nil {
		t.Fatal("combined work limit was not enforced")
	}
	g, err := validate(fixture())
	if err != nil {
		t.Fatal(err)
	}
	budget := 0
	states := resourceStates(g, map[string]bool{"shared-store": true}, &budget)
	if states["gpu01"].value != "down" || !states["gpu01"].truncated {
		t.Fatal("diagnostic truncation changed or hid the decision")
	}
	in = fixture()
	in.Snapshot.Resources[3].FailureDomains = nil
	for i := 0; i < 1000; i++ {
		in.Snapshot.Resources[3].FailureDomains = append(in.Snapshot.Resources[3].FailureDomains, fmt.Sprintf("domain-%d", i))
	}
	for i := 1; i < 100; i++ {
		in.Change.Waves = append(in.Change.Waves, Wave{ID: fmt.Sprintf("wave-%d", i+1), Targets: []string{"fabric-a"}})
	}
	if _, err := Evaluate(in, referenceTime); err == nil {
		t.Fatal("domain/wave amplification accepted")
	}
}
