# DiffNote

DiffNote is a local-first, GitHub-style commit reviewer for handing precise feedback to coding agents.

```bash
make build
./diffnote
```

DiffNote opens a loopback web server. Add and select local Git repositories from the Project control in the sidebar. Projects, settings, viewed files, and comments are stored in `~/.local/share/diffnote/reviews.db` by default.

Use the **Copy review** button or `Ctrl+Shift+Enter` (`Cmd+Shift+Enter` on macOS) to copy all comments for the selected commit as Markdown.

When the current branch has tracked uncommitted changes, **Uncommitted tracked changes** appears at the top of the commit list. It includes staged and unstaged edits but excludes untracked files. Select it to leave review comments; reselect it to refresh the diff after editing files. Comments whose lines have changed remain available as unattached comments rather than moving to another line.

## Development

Browser regression tests use Node.js and Chromium. Run `npm ci` once, then
`npm test` (also included in `make check`). Set `CHROME_PATH` if Chromium is not
at `/usr/bin/google-chrome`.

```bash
make dev
make check
make install
```

`make install` installs to `~/.local/bin/diffnote`. Override the destination when needed, for example `make install BINDIR=~/bin`. Remove it with `make uninstall` using the same `BINDIR`.

Pass CLI options during development with `ARGS`, for example `make dev ARGS=--no-browser`.

### Large-commit smoke tests

Start a separate DiffNote instance with a temporary database, then run:

```bash
# In another terminal (choose a fresh database path):
go run . --no-browser --addr=127.0.0.1:4174 --database=/tmp/diffnote-smoke.db

DIFFNOTE_SMOKE_URL=http://127.0.0.1:4174 \
DIFFNOTE_SMOKE_REPO="$HOME/form-extractor" npm test
```

The opt-in test reviews five large commits, scrolls through them, expands context,
checks pagination, saves/reloads a comment, and measures browser heap/DOM size.
Override the comma-separated commit list with `DIFFNOTE_SMOKE_COMMITS` for other
repositories. Use an isolated database: the test saves review feedback there.
It never modifies the reviewed repository.

Diffs exceeding 250,000 Git output lines are rejected with an actionable error,
in addition to the existing 64 MiB subprocess output limit. This bounds parsed
record growth for commits containing huge numbers of short lines.
