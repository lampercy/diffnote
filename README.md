# DiffNote

DiffNote is a local-first, GitHub-style commit reviewer for handing precise feedback to coding agents.

```bash
make build
./diffnote
```

DiffNote opens a loopback web server. Add and select local Git repositories from the Project control in the sidebar. Projects, settings, viewed files, and comments are stored in `~/.local/share/diffnote/reviews.db` by default.

Use the **Copy review** button or `Ctrl+Shift+Enter` (`Cmd+Shift+Enter` on macOS) to copy all comments for the selected commit as Markdown.

## Development

```bash
make dev
make check
make install
```

Pass CLI options during development with `ARGS`, for example `make dev ARGS=--no-browser`.
