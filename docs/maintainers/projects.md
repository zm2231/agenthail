# Projects, scopes and archive

The daemon that owns a session decides its project, its scope flags and
whether it is archived. Every surface (API, CLI, web, Mac, iOS) reads those
values from the catalog and never derives them from a path.

## Projects are folders you add

A project is a folder added on a host with `project-add` (`agenthail project
add <path> [--name <title>]`). Each host keeps its own list in its registry.
The folder can be anywhere, including a folder inside a Git repository, such as
`apps/web` in a monorepo.

- A folder belongs to at most one project on a host. Adding it again is
  refused with `409` and `Workspace <folder> already belongs to project <id>.`
- A folder in a linked worktree is added at the same place in the main
  checkout. The project then holds that folder in every worktree, and adding
  it from another worktree is the same folder.
- The title defaults to the folder's name.
- `project-remove` (`agenthail project remove <name|id|folder>`) removes an
  added project. Its sessions move to the next project that holds them; no
  session is stopped or changed.

## Which project a session belongs to

The host that owns a session resolves its `project` from its working
directory (see `session-stream-catalog.md` for the row fields):

1. In a Git repository, the folder is translated to the same place in the main
   checkout: the checkout's path relative to its own root, joined to the main
   checkout root. The worktree and branch stay on the row as `checkout`.
2. The session belongs to the deepest added project that holds the folder or
   its translation. A session in a worktree of `merchantry` working in
   `apps/web` lands in the `apps/web` project even though that project was
   added in the main checkout.
3. Otherwise, a session in a Git repository belongs to the repository's
   implicit project at its main checkout root (`added` false), named after the
   repository. Every repository groups its sessions with nothing added, and an
   added project inside it takes its sessions from it.
4. Otherwise the session has no project. Home, scratch and temporary folders
   outside Git stay out of projects.

A directory that no longer exists resolves from its nearest existing ancestor,
so a session keeps its project after its worktree is removed. A Git failure
other than "not a repository" keeps the last known project.

## Identity across hosts

A project inside a repository with a remote has a `key` that names it on every
host:

- `repo:<canonical remote>` at the repository root, and
  `repo:<canonical remote>::<folder>` for a folder inside it, such as
  `repo:github.com/example/merchantry::apps/web`.
- The canonical remote is the URL lowercased to `host/path`, without its
  scheme, user, port, `.git` suffix and trailing `/`. The https, ssh and scp
  spellings of one repository match.
- The remote is `origin`, else the first remote by name. A local remote (a
  path, `.`, or a `file:` URL) names a place on one machine and is skipped.
  t3code, the model this follows, prefers `upstream` over `origin`. We do not:
  `upstream` is added by hand, usually on one machine only, so preferring it
  would split one project between a checkout that has it and a plain clone
  that does not. It would also put a fork in the same project as every other
  clone of its upstream, which is wrong for a project's coordinator.

The project's `id` is its `key` when it has one, so the same folder of the same
repository is one project on every paired host. `repository` carries
`canonicalKey`, `remoteName`, `remoteUrl` and `name`. A project without a key
(a repository without a remote, or a folder outside Git) belongs to its host:
its `id` is its `localId`, and a mirrored row qualifies it as `host::localId`.
`localId` is the host's own id for the project: `p-<uuid>` for an added project
and `implicit:<main checkout root>` for an implicit one. Coordinators and
project channels are keyed by the project `id`.

A changed remote moves a project to its new key on the next catalog pass.

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
`displayName`, `kind`, `key` and `repository` when it has a key, `lastActive`,
`sessionCount` (over its non-archived top-level sessions), `archived`, and
`checkouts`. Each checkout is one host's project with that id: `host`,
`projectId` (its `localId`, host-qualified for a paired host), `rootPath`,
`added`, `sessionCount` and `archived`, this host first. Two clones of one
repository on a host are two checkouts. Added projects appear even before they
have sessions.

## Archive

`session-archive` and `session-unarchive` take `sessionId`;
`project-archive` and `project-unarchive` take `projectId`. Archiving only
hides: it never stops, interrupts or deletes a session, and queued delivery
continues. A session is archived when it or its project is.

New activity unarchives the session and its project: a turn after the
archive, work in progress, a queued item, or an open attention item. Nothing
that needs the user stays hidden.

Archive state is per checkout, in the owning host's registry. A project entry
is archived only when every checkout is.

- A project id with a key applies to every checkout of this host with that key
  and is forwarded to every paired host whose mirrored sessions have it. A host
  that receives the forwarded action applies it to its own checkouts only.
- A `localId` applies to that one checkout, and a host-qualified `localId`
  routes the action to its host.
- The answer lists each checkout in `checkouts` (`host`, `ok`, `error`) and is
  `502` when any host failed.
