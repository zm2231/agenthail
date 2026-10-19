# Projects, scopes and archive

The daemon that owns a session decides its project, its scope flags and
whether it is archived. Every surface (API, CLI, web, Mac, iOS) reads those
values from the catalog and never derives them from a path.

## Project identity

A session's `project` is resolved from its working directory on the host that
owns the session (see `session-stream-catalog.md` for the row fields):

- In a Git repository the project is the repository. A linked worktree, a
  subfolder and the main checkout share one project. The worktree and branch
  stay on the row as `checkout`, and `rootPath` is the main checkout.
- The project `id` comes from the repository's key, read from its common
  directory. The key is `origin`'s URL, else the URL of the only remote; else
  the smallest root commit of `HEAD`; else, in a repository with no commits,
  the common directory's path. A remote URL is compared without its scheme,
  user, port and `.git` suffix, so `https://github.com/example/gadget.git` and
  `git@github.com:example/gadget` are one project. A local path or `file://`
  remote does not count as a remote.
- `displayName` is the remote's repository name, else the main checkout's
  folder name.
- A remote or root commit key names the same repository on every host, and the
  project carries `portable: true`. Every clone and worktree of it, on any
  paired host, is one project. Forks have their own remote URL and stay
  separate projects. Changing `origin` moves the project within two minutes.
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

Each row carries `current`, which the session's surface computes, and
`archived`, which the daemon sets from the archive state. The snapshot and the
`scope` filter use these definitions:

| Scope | A session is in it when |
|---|---|
| `running` | it is working |
| `current`, `recent` | its row is `current` |
| `all` | it is not archived |
| `archived` | it or its project is archived |

`running`, `current` and `recent` exclude archived sessions. Without `scope`
a snapshot returns every row. The full snapshot's `counts` gives the size of
each scope in session families: a subagent counts with its root when the root
is listed.

The full snapshot also lists `projects`, newest activity first: `id`,
`displayName`, `kind`, `lastActive`, `sessionCount` (over its non-archived
top-level sessions), `archived`, and `checkouts`. Each checkout is one host's
copy of the project: `host`, `rootPath` (that host's main checkout),
`sessionCount` and `archived`, this host first. A portable project with
sessions on several hosts is one entry with a checkout per host. Registered
folder projects appear even before they have sessions.

## Archive

`session-archive` and `session-unarchive` take `sessionId`;
`project-archive` and `project-unarchive` take `projectId`. Archiving only
hides: it never stops, interrupts or deletes a session, and queued delivery
continues. A session is archived when it or its project is.

New activity unarchives the session and its project: a turn after the
archive, work in progress, a queued item, or an open attention item. Nothing
that needs the user stays hidden.

Archive state lives in the owning host's registry, per host. A project entry
is archived only when every checkout is. `project-archive` and
`project-unarchive` with a portable id apply to this host's checkout and are
forwarded to every paired host whose mirrored sessions have the project. The
answer lists each host in `checkouts` (`host`, `ok`, `error`) and is `502`
when any host failed. A host that receives the forwarded action applies it to
its own checkout only. A host-qualified project id routes the action to that
host alone, and the mirrored rows carry the result.
