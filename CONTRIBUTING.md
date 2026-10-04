# Contributing to fabricchange

Start with a reproducible operator problem, a synthetic fixture, and the expected decision. Explain the producer/tool version for an adapter. Keep the core in Go and use standard-library dependencies where practical.

Run `gofmt -w .`, `go vet ./...` and `go test -race ./...`. Add a regression test for bugs that could produce false confidence. Public examples must contain no real customer identifiers, secrets, proprietary logs or unapproved topology.

Label every claim: synthetic behavior, live integration, measured hardware result, or operator feedback. An adapter is supported only to the extent documented and tested. New execution or remediation features need a separate threat model and design review.

This project is licensed under Apache-2.0; submitted contributions use that license. Do not contribute material you lack rights to share.
