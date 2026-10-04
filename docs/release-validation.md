# Initial release validation

Validated October 4, 2026 using Go 1.27.1 on macOS arm64. This records software validation, not hardware or production validation.

- Independent implementation review, followed by regression checks for each identified false-pass/input-parsing issue.
- `go test -race -cover ./...` and `go vet ./...` passed on the reviewed source.
- Bundled synthetic examples produced the documented decisions and process exit codes.
- Linux/macOS amd64/arm64 archives were cross-built. Archive checksums, executable/license/example contents, and the extracted macOS arm64 executable were checked.
- CI repeats formatting, vet, tests and build on Ubuntu. Check the actual workflow run for its result; this document does not claim a future CI run passed.

The redundant-storage fixture passed; existing storage failure plus maintenance on the remaining path blocked jobs in two racks; an unknown alternate path yielded unknown. Mixed-case field aliases, null/missing required capacity thresholds and the reserved global-domain collision are rejected. Excessive graph/report expansion is bounded, with explicit diagnostic truncation that does not omit resource verdicts. A mocked Slurm capture failure emitted no partial capture.

Generated graph tests include 1,000 and 10,000 compute nodes; these exercise CPU graph evaluation only. Live Slurm captures, real resource dependencies and operator maintenance outcomes remain to be validated.
