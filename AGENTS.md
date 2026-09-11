# DiffNote Agent Guide

DiffNote is a local-first commit review tool distributed as one Go executable.

- Keep the server bound to loopback by default.
- Use Git subprocesses as repository truth; never mutate the reviewed repository.
- Keep review data outside the worktree under the XDG data directory.
- Preserve comments across pure rebases using stable patch IDs. Never silently move a comment when its anchor is uncertain.
- Keep changes small and add focused tests for Git parsing, persistence, and API behavior.
- Run `go test ./...` and `go vet ./...` before finishing.
- Inspect the complete staged diff before each commit and include only one coherent behavior change.
