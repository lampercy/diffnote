# DiffNote MVP Specification

## Goal

Provide a local GitHub-like interface for reviewing commits and handing structured inline feedback to a coding agent.

## Behavior

- `diffnote` opens the reviewer; users add and select local Git repositories in the web interface.
- The left panel lists local and remote branches and the selected branch's recent commits.
- Selecting a commit displays its unified diff in the right panel.
- Reviewers can attach comments to old, new, or unchanged diff lines.
- Comments autosave locally and can be copied together as Markdown with commit, file, side, and line context.
- Comments load by exact commit hash and survive pure rebases through Git's stable patch ID.
- Comments whose patch changed remain stored but are never silently attached to different code.

## Constraints

- Bind to loopback by default and never mutate the reviewed repository.
- Store review data outside the worktree.
- Ship the frontend inside one executable.
- Support Linux first.

## Deferred

- GitHub synchronization, split diffs, source editing, automatic fuzzy re-anchoring, and Windows packaging.
