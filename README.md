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

```bash
make dev
make check
make install
```

`make install` installs to `~/.local/bin/diffnote`. Override the destination when needed, for example `make install BINDIR=~/bin`. Remove it with `make uninstall` using the same `BINDIR`.

Pass CLI options during development with `ARGS`, for example `make dev ARGS=--no-browser`.
