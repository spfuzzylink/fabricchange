Experimental first release: offline maintenance impact analysis across declared resource dependencies and active job allocations.

- Go library and CLI with pass, blocked, unknown and invalid-input outcomes.
- Shared-resource impact, k-of-n redundancy, existing outages, failure-domain capacity and explicit wave restoration assumptions.
- Causal reports identifying jobs and owners affected even when their compute nodes are not direct maintenance targets.
- Read-only Slurm capture script and fixture-tested import format; defensive schema and resource limits.

**Validation boundary:** synthetic fixtures and local software tests only. Live Slurm integration, real GPU infrastructure and production changes remain unvalidated. No maintenance execution, checkpoint recovery guarantee or production-safety certification is provided.

Download the archive for your OS/architecture, verify it against SHA256SUMS and extract it. Archives include the binary and examples. This example intentionally returns exit 1 because the modeled change is blocked:

```sh
./fabricchange version
./fabricchange plan -input examples/shared-storage-blocked.json -at 2026-10-04T12:01:00Z -format text
```

Linux and macOS builds are provided for amd64/arm64. The macOS arm64 package was executed locally; other archives were cross-compiled. See the README for assumptions and integration limitations. Checksums are not signatures.

Apache-2.0. Independent project, not affiliated with or endorsed by NVIDIA or SchedMD.
