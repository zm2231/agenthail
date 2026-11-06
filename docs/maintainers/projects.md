# Projects, scopes and archive

The daemon that owns a session decides its project, its scope flags and
whether it is archived. Every surface (API, CLI, web, Mac, iOS) reads those
values from the catalog and never derives them from a path.

## Projects are folders you add

A project is a folder added on a host with `project-add` (`agenthail project
add <path> [--name <title>]`). Each host keeps its own list in its registry.
The folder can be anywhere, including a folder inside a Git repository, such as
`apps/web` in a monorepo.

- `project-add` takes `path`, an optional `title`, and `host` to add the
  folder on a paired host. A folder the host does not have is refused with
  `400` and code `folder_not_found`.
- A folder belongs to at most one project on a host. Adding it again changes
  nothing and answers with its project and `created: false`; a different
  title is not applied (rename it instead).
- A folder in a linked worktree is added at the same place in the main
  checkout. The project then holds that folder in every worktree, and adding
  it from another worktree is the same folder.
- The title defaults to the folder's name.
- `project-remove` (`agenthail project remove <name|id|folder>`) removes an
  added project. Its sessions move to the next project that holds them; no
  session is stopped or changed.
- `project-rename` (`agenthail project rename <name|id|folder> <new name>`)
  takes `projectId` and `title` (1 to 120 characters) and sets the title of
  every added checkout the id names, forwarded to paired hosts like archive,
  so one project has one name on every host. A project with no added checkout
  anywhere has no name of its own and is refused with `409`
  `project_not_added`; a host with only an implicit checkout of it is skipped.
- Project actions refuse with a typed error (`{"error":{"code","message"}}`):
  `invalid_request`, `folder_not_found`, `project_not_found`,
  `project_not_added`, `project_ambiguous`, `project_linked`,
  `project_not_linked`, `project_history`.

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
project channels are keyed by the project `id`. `#<name>` addresses a project
by its `id` or display name. A display name several projects share is
refused, and so is a name that is both a stored channel and a project. A
channel cannot be created or renamed to a project's name. See
[hosts](hosts.md#relays-and-channels-across-hosts).

A changed remote moves a project to its new key on the next catalog pass.

## Linked projects

A link joins one project into another, so a fork and its upstream, or a
repository and a folder that belongs with it, are one project with one lead and
one ledger:

- `project-link` (`agenthail project link <joined> <into>`) takes `projectId`
  (the project joined) and `survivor` (the project it joins). The resolver
  answers the survivor wherever it would have answered the joined project, so
  rows, the project list, `@lead` and ledger capture all follow the link.
  The joined checkout keeps its folder and carries `linkedFrom`, its own
  project before the link; the survivor lists it under `linked`.
- `project-unlink` (`agenthail project unlink <joined>`) makes the joined
  project its own again.
- Links are stored in `project_links`, one record per joined project. An
  unlink stays as a tombstone so a host that still holds the link takes the
  unlink instead of reviving it. Of two records for one project the later
  `changedAt` wins, the origin host id breaking a tie; a host writing a record
  dates it past the one it replaces even when its clock is behind.
- A link between two keyed projects reaches every paired host: the snapshot
  carries `projectLinks` and the `project.linked` catalog event carries each
  change, and a host adopts a record newer than its own. A link naming a
  host's own folder stays on that host. For a keyed project, a host's own
  record of the link decides over the `linkedFrom` a paired host's row
  arrived with, so a host that has not yet taken an unlink cannot keep the
  projects joined.
- A project that already has a lead, a ledger board (enabling the ledger
  starts one) or ledger grants is refused as the joined side
  (`409 project_history`):
  when only it has that, the message names the link the other way round, and
  when both do, merging them is not supported. A host that adopts a link onto
  such a project holds it: the project stays its own, with `linkHeld` naming
  why, and `agenthail doctor` fails until it is resolved. Chains are refused
  (`409 project_linked`) and held when they arrive.
- A joined project's past leads become the survivor's: each host moves the
  project's `coordinator_history` rows to the survivor in the transaction
  that records or adopts the link. A host where the joined project still has
  its own lead keeps those rows until the lead is cleared, and the clear moves
  them. An unlink leaves the moved history with the survivor, so the joined
  project comes back with no past leads.
- Two unlinked projects whose repositories share a root commit are named in
  `sameHistory` on the one with fewer sessions, and `project list` and
  `doctor` print the link command that would join them.

## Scopes

Each row carries `current`, which the session's surface computes, and
`archived`, which the daemon sets from the archive state. The snapshot and the
`scope` filter use these definitions:

| Scope | A session is in it when |
|---|---|
| `running` | it is working |
| `current`, `recent` | its row is `current`, or it is pinned |
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

## Snooze and inbox sections

Each row in the session view carries `inboxSection`, the one section of the
inbox it belongs to. The first that applies wins:

| Section | A session is in it when |
|---|---|
| `archived` | it or its project is archived |
| `needsYou` | it has `attention` |
| `snoozed` | its snooze still holds |
| `running` | it is working, finishing background work, or has queued messages |

Any other row has no `inboxSection`. A `done` section needs a session's
end-of-turn record, which rows do not carry yet.

A request for the person outranks a snooze, so snoozing never hides one.

`session-snooze` takes `sessionId` and either `until` (an RFC 3339 time in the
future) or `wake: "finished"`. A `until` snooze holds until that time. A
`finished` snooze holds while the turn that was running when it was set keeps
running; it needs a working session. A session that is archived, or whose
project is, cannot be snoozed but can be woken. Times are kept to the
millisecond.
`session-wake` takes `sessionId` and clears the snooze. Both answer the new
`snooze` and the `previous` one, so a client undoes either by sending the
previous snooze back, or `session-wake` when there was none. From the CLI:
`agenthail thread snooze <target> --until 2h|<time>`,
`agenthail thread snooze <target> --until-finished` and
`agenthail thread wake <target>`.

Rows carry `snooze: {snoozedAt, until, wake}` for the last snooze set, even
after it stopped holding; `inboxSection` says whether it still does. The view's
`refreshAt` includes the next `until`, so a client rereads when a snooze ends.
State lives in `sessions.snoozed_at_ms`, `snoozed_until_ms` and `snooze_wake`,
read with the pin in one query per catalog pass.

- A project id with a key applies to every checkout of this host with that key
  and is forwarded to every paired host whose mirrored sessions have it. A host
  that receives the forwarded action applies it to its own checkouts only.
- A `localId` applies to that one checkout, and a host-qualified `localId`
  routes the action to its host.
- The answer lists each checkout in `checkouts` (`host`, `ok`, `error`) and is
  `502` when any host failed.
