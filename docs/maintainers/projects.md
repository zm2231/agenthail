# Projects, scopes and archive

The daemon that owns a session decides its project, its scope flags and
whether it is archived. Every surface (API, CLI, web, Mac, iOS) reads those
values from the catalog and never derives them from a path.

## Project identity

A session's `project` is resolved from its working directory on the host that
owns the session (see `session-stream-catalog.md` for the row fields):

- In a Git repository the project is the main checkout. A linked worktree, a
  subfolder and the main checkout share one project, whose `id` comes from the
  repository's common directory. The worktree and branch stay on the row as
  `checkout`.
- A directory that no longer exists resolves from its nearest existing
  ancestor, so a session keeps its project after its worktree is removed.
- Outside Git, a session belongs to the nearest registered folder project
  (`kind` `folder`). Any other directory (home, scratch and temporary folders)
  gives the session no project.
- A Git failure other than "not a repository" keeps the last known project.

A folder becomes a project with the `project-add` action. A folder inside a
Git repository is already part of that repository's project; `project-add`
returns it and registers nothing.

## Scopes

Each row carries `current`, `recent` and `archived`. The snapshot and the
`scope` filter use these definitions:

| Scope | A session is in it when |
|---|---|
| `running` | it is working |
| `current` | it is working, has queued work, has an open attention item, is an open Claude session, or is a Codex session active within the Codex recent window |
| `recent` | it is current or was active in the last 24 hours |
| `all` | it is not archived |
| `archived` | it or its project is archived |

`running`, `current` and `recent` exclude archived sessions. Without `scope`
a snapshot returns every row. The full snapshot's `counts` gives the size of
each scope in session families: a subagent counts with its root when the root
is listed.

The full snapshot also lists `projects`: `id`, `displayName`, `rootPath`,
`kind`, `host`, `lastActive` and `sessionCount` (over its non-archived
top-level sessions), and `archived`, newest activity first. Registered
folder projects appear even before they have sessions.

## Archive

`session-archive` and `session-unarchive` take `sessionId`;
`project-archive` and `project-unarchive` take `projectId`. Archiving only
hides: it never stops, interrupts or deletes a session, and queued delivery
continues. A session is archived when it or its project is.

New activity unarchives the session and its project: a turn after the
archive, work in progress, a queued item, or an open attention item. Nothing
that needs the user stays hidden.

Archive state lives in the owning host's registry. A qualified session or
project id routes the action to its host, and the mirrored rows carry the
result.
