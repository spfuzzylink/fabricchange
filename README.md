# FabricChange

**Offline maintenance impact analysis for GPU-cluster operators. Experimental pre-alpha; synthetic validation only.**

A storage restart can interrupt jobs on compute nodes in another rack, even when no compute node is a maintenance target. FabricChange combines a declared resource dependency graph with active Slurm allocations to explain that impact before a change review.

FabricChange is a Go library and CLI. It evaluates simultaneous targets within a wave, accounts for existing failures, models k-of-n redundancy, and reports affected resources, job owners, causal paths, and compute-capacity policy violations. It never executes maintenance, drains nodes, submits jobs, or connects to a cluster. An optional, separately invoked Bash script captures read-only Slurm allocation data.

## Try the synthetic demo

Requires Go 1.25 or later. No dependencies beyond the Go standard library.

```bash
go build -o bin/fabricchange ./cmd/fabricchange
bin/fabricchange version
bin/fabricchange plan -input examples/shared-storage-blocked.json \
  -at 2026-10-04T12:01:00Z -format text
```

Expected exit **1, blocked**: `storage-b` is already down; restarting `storage-a` removes the remaining storage dependency for `gpu01` in `rack-a` and `gpu02` in `rack-b`. Running job `101` and suspended job `102` are affected. Output includes owners and paths such as `gpu02 → storage-a`, plus the existing `storage-b` failure. No compute node was directly targeted.

Compare the other fixtures at the same explicit time:

```bash
bin/fabricchange plan -input examples/redundant-pass.json -at 2026-10-04T12:01:00Z
bin/fabricchange plan -input examples/uncertain-storage.json -at 2026-10-04T12:01:00Z
bin/fabricchange import-slurm -input examples/slurm-capture.txt
```

The first returns **0, pass** because the declared second storage path remains available. The second returns **3, unknown** because that path's status is unknown. The importer emits job data with `complete: false`; allocation data alone cannot establish complete inventory.

All examples are invented. The `-at` flag makes demonstrations reproducible; real assessment should use current time (the default). Replaying an old fixture without `-at` produces a stale-snapshot finding. Build the binary to observe its exact exit code; `go run` adds its own process wrapper.

## Release archives

The [GitHub releases](https://github.com/spfuzzylink/fabricchange/releases) provide experimental binaries for Linux and macOS on amd64 and arm64, with SHA-256 checksums. Archives include the executable, license, documentation and synthetic examples. After extracting an archive, run:

```bash
./fabricchange version
./fabricchange plan -input examples/shared-storage-blocked.json -at 2026-10-04T12:01:00Z -format text
```

This example intentionally exits 1 to report the modeled blocker. No Go installation is needed for the prebuilt executable. The source checkout uses `bin/fabricchange`; the archive uses `./fabricchange`. Verify the archive against `SHA256SUMS`; checksums detect differing bytes but are not a code signature.

## Verdicts

| Verdict | Exit | Meaning |
|---|---:|---|
| `pass` | 0 | No modeled blocker or snapshot uncertainty found under the declared assumptions. |
| `blocked` | 1 | An active job loses a required resource, or an explicit capacity constraint is violated. |
| invalid input | 2 | Malformed data, unknown fields/references, ambiguous IDs, cycles, or missing required declarations. |
| `unknown` | 3 | Evidence is stale, future-dated, incomplete, has unknown status, or cannot determine a policy outcome. |

Known blockers take precedence over `unknown`, but uncertainty remains visible in `snapshot_findings`. **Pass is not authorization or a production-safety claim.** Every report states that earlier waves must be restored and revalidated before later waves. The planner assumes that restoration; it does not observe it. Existing failures persist in every wave.

## Input and integration

The [example input](examples/shared-storage-blocked.json) is the complete version-1 JSON contract. [Architecture and semantics](docs/architecture.md) describes validation and propagation. The public Go API is `Decode(io.Reader)` followed by `Evaluate(input, referenceTime)`; both return errors for invalid input. `ExitCode(report.Verdict)` implements CLI verdict mapping.

Resources have IDs, kinds (`compute`, `fabric`, `storage`, `power`, `service`), observed status (`up`, `down`, `unknown`), and optional dependency groups. All groups must be satisfied; each group states how many of its members must remain available. Nodes can belong to several failure domains. The domain name `*` is reserved for the global capacity summary.

Optional policy controls are `max_unavailable_compute_nodes` and `min_healthy_by_domain`. The freshness limit, `max_snapshot_age_seconds`, is required. No omitted policy is silently invented. A complete snapshot must explicitly include `jobs: []` when no allocations exist.

### Slurm adapter status

The capture script and parser are implemented and fixture-tested; **live Slurm validation is pending**. Run the script only on an appropriate Slurm client host, then import its result offline:

```bash
bash scripts/capture-slurm.sh > allocations.txt
bin/fabricchange import-slurm -input allocations.txt > allocations.json
```

The script uses documented `squeue` fields `%i`, `%u`, `%T`, `%N` with running, suspended, and completing jobs, then uses `scontrol show hostnames` to expand hostlists. It publishes stdout only after every query succeeds. The parser rejects partial captures, duplicate jobs, unsupported states, and unexpanded hostlists. [Slurm squeue documentation](https://slurm.schedmd.com/squeue.html), [scontrol documentation](https://slurm.schedmd.com/scontrol.html).

Copy imported `jobs` into a verified snapshot; supply health, dependencies, domains, and completeness separately. Use a capture time conservative for all included sources. Permission-filtered visibility and non-atomic queries can miss changes. In a completing job, Slurm `%N` contains the nodes still held by the allocation. Federation, reservations, job-step topology, pending placement, and checkpointability are not captured or modeled. Do not infer future job completion from this tool.

## What is validated

```bash
go test ./...
go vet ./...
go test -run '^$' -bench BenchmarkPlanner -benchmem .
```

Tests cover shared storage across racks, redundant paths and existing failures, k-of-n groups, multi-node running/suspended allocations, domain capacity, stale/future/incomplete data, cycles and references, duplicate JSON keys, deterministic output, deep graphs, and 1,000/10,000-node generated cases. Benchmarks measure CPU-side graph evaluation and allocation analysis only; they are not GPU-cluster benchmarks or evidence of production scale.

## Boundaries and next evidence

This is a working offline prototype, not a differentiated production platform. Graph propagation and redundancy analysis are established techniques. The proposed product value is making a customer-specific maintenance review reproducible across resource and scheduler boundaries; customer usefulness remains unvalidated.

No real GPU hardware, live Slurm installation, operator deployment, or production maintenance event has validated this release. Reservations, checkpoints, scheduler policies, bandwidth/latency, degraded storage performance, change duration, and recovery are unmodeled. A declared k-of-n relationship is not proof that paths are operationally independent. The next milestones are operator-reviewed topology/allocations, a replay against a real maintenance plan, then a published comparison of decisions and operator effort with and without this tool.

FabricChange is independent and is not affiliated with or endorsed by NVIDIA or SchedMD.
