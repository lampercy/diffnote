# Smoke test results — 2026-10-27

Tested headless Chrome against an isolated DiffNote database, loopback-only
listener, and the read-only `/home/percy/form-extractor` repository. No checkout
or repository modification was performed.

## Real commits

Selected from the largest changes among the latest 500 commits:

| Commit | Initial rendered-source records | Post-GC JS heap |
| --- | ---: | ---: |
| `8e767be00` Storybook v8 | 41,056 | 6.6 MiB |
| `f0f107965` AI Analysis tab | 6,409 | 2.9 MiB |
| `424bbc925` Webhook configuration refactor | 6,292 | 2.9 MiB |
| `3659318a3` Dependency security patches | 9,463 | 3.2 MiB |
| `7dbc99b3f` React Intl migration | 4,279 | 3.0 MiB |

Traversed each diff, checked DOM bounds and absence of empty metadata rows,
expanded distant context where available, paginated actual history, and saved
and reloaded a comment. The server service (including Git children) peaked at
49.9 MiB under a 1 GiB service limit on the final clean-database run. These are observed measurements, not a
promise of constant total memory for arbitrary repositories. OOM was not
reproduced with these commits.

## Deterministic regression coverage

- Deep context expansion: exact source anchor and viewport offset.
- Comment creation/deletion, editing focus across virtualization, wrapped
  source lines, and manual editor resizing.
- History pagination: scroll offset and visible commit card position.
- Virtual block boundaries: no artificial gaps, duplicate/missing lines, or
  redundant hunk separators; parser preserves real blank source lines and
  no-newline markers.
- 100,000 source lines, 70 scroll operations: fewer than 250 mounted rows;
  post-GC DOM growth below 5,000 nodes and heap growth below 12 MiB. Observed
  retained heap growth was approximately 0.43 MiB.
- Observer targets remain limited to mounted blocks across repeated rerenders
  and replacement of the large fixture.
- Short-line patch identity allocation stays below 64 KiB per operation.
- Git diffs exceeding 250,000 output records fail explicitly rather than
  expanding without bound; API remains usable after rejection.

Reproduced failures before fixes: distant context expansion lost the target
line; virtual scrolling destroyed focused comment editors. Parser tests also
cover blank Git separators and source text resembling file markers.

Validation: eight browser tests (including the opt-in repository test),
`go test ./...`, `CGO_ENABLED=1 CC=/usr/bin/gcc go test -race ./...`, and
`go vet ./...` passed. See `README.md` for rerun instructions.
