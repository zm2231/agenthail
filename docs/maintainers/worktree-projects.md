# Worktree projects

A linked worktree that the person adds becomes a project of its own. A
worktree nobody added keeps belonging to its main checkout's project, as
[projects.md](projects.md#which-project-a-session-belongs-to) describes.

The case: a long-lived worktree of a repository, on a branch that will take
weeks (`agenthail-v2` on `v2/cockpit-design`), wants its own lead, its own
ledger and its own worker worktrees. Today `project add` reads the worktree as
its main checkout and answers with the repository's project, and a separate
clone with the same remote gets the same key, so there is no way to split it
off.

## What counts as a worktree project

`project-add` on a folder whose path is the root of a linked worktree (git's
`--show-toplevel` for the folder is the folder itself, and its git directory
is not the common directory) adds that folder as it is, without translating it
to the main checkout. Every other add is unchanged:

- a folder below a worktree's root is translated to the main checkout as
  today, unless it lies inside a worktree project (below);
- the main checkout, a submodule and a folder outside Git are unchanged;
- a worktree of a bare repository is translated as today. A bare repository
  has no main checkout, so every one of its worktrees is a peer and the
  repository's project is the one that holds them.

The record is an added project like any other (`p-<uuid>`, title defaulting to
the folder's name) with a new `worktree` flag on its `projects` row, stored by
a named migration. The flag is what tells the resolver to stop translating at
this folder; without it the resolver could only rediscover it with git on
every pass.

Adding the same worktree again answers with its project and `created: false`.

## Which project a session belongs to

Resolution gains one step in front of the existing ones:

0. If the session's folder lies inside an added worktree project (by its
   literal path, not translated), it belongs to the deepest added project
   whose root is inside that worktree project and holds the folder. That is
   the worktree project itself, or a folder added inside it. Nothing is
   translated to the main checkout, so a project added in the main checkout
   (such as `apps/web`) does not take a session working in the worktree's
   `apps/web`.

Otherwise steps 1 to 4 run as today, and worktree projects take no part in
them: a session in another worktree is translated to the main checkout and
lands in the repository's project.

This covers worktrees nested inside the worktree project. Git keeps
`agenthail-v2/.worktrees/x` as a worktree of the shared repository, but its
path is inside `agenthail-v2`, so step 0 puts its sessions in the worktree
project. Worker worktrees of the main checkout (`agenthail/.worktrees/y`) are
not inside it and stay in the repository's project.

A folder added inside a worktree project (`agenthail-v2/apps/web`) is added at
its own path, not translated, and is a keyless folder project of this host.

A worktree project whose folder is gone keeps its sessions: their paths still
lie inside its root, the same as a removed worktree keeps its repository's
project today.

## Key: this host only

A worktree project has no `key`. Its `id` is its `localId`, its ledger is
`project:<id>` (`registry.LedgerProjectKey`), and a paired host sees it as
`<host>::<id>`, like a repository without a remote.

A worktree is a place on one machine: its path differs per host and its branch
can be switched at any time. A key from the remote and the branch
(`repo:<remote>@<branch>`) was considered and rejected: switching the branch
would move the project, and two hosts that happen to check out the same branch
would be merged into one project with one lead without anyone asking. This
follows the existing rule that a local remote names no key.

So on a paired host:

- the project, its sessions and its lead are visible as this host's project,
  and actions on it are forwarded here like any host-qualified id;
- the paired host's own clones and worktrees of the repository stay in the
  repository's keyed project;
- the paired host can add a worktree of its own as its own project, which is a
  separate project. Links between keyless projects stay on their host, so the
  two cannot be joined across hosts.

The repository key refresh (`refreshProjectRepositories`) skips worktree
projects, so one never gains a key from the folder's remote, including a
worktree inside the main checkout's `.worktrees`, whose path would otherwise
map to `repo:<remote>::.worktrees/<name>`.

## Worktrees Agenthail makes

A session started with its own worktree from a folder inside a worktree
project gets that worktree under the worktree project's root
(`agenthail-v2/.worktrees/<slug>`), not under the main checkout, so it lands
in the same project. Branch and base are chosen as today, from the folder the
session starts in. The worktree is still made with the repository's
`git worktree add` and removed by the same rules.

The hand-made worktree sweep also searches each worktree project's
`.worktrees` folder, and never removes a worktree that is the root of an added
project, whichever folder it sits in.

Moving a session to another host makes its worktree under the destination's
main checkout, as today: a worktree project is this host's, so the destination
has no such project and the session lands in the repository's project there.

## Adding a worktree project

- Existing sessions inside the worktree move to it on the next catalog pass,
  including sessions that were in the repository's project. Nothing is
  stopped or changed; their rows show the new project.
- The repository's project keeps its lead, ledger, settings, links and archive
  state. The add hands nothing over (`AddProject` gets no superseded project),
  because the worktree project does not replace the repository's project; it
  splits off part of it.
- The worktree project starts with no lead and no ledger. `@lead` from its
  sessions has no lead to reach until one is set with `coordinator-set`.
  A session that leads the repository's project and happens to work in the
  worktree stays that project's lead: a lead is held by project id, not by the
  folder its session works in.
- Work items already in the repository's ledger stay there. A session that
  moved keeps its claimed items; its ledger calls from then on name the
  worktree project's ledger, since they resolve from the session's project.

## Removing a worktree project

`project-remove` works as for any added folder:

- Its sessions return to the repository's project on the next catalog pass.
- Its ledger stays under its id and comes back when the folder is added again
  (`removed_project_ledgers`), as for a removed subfolder project today.
- Its lead is not handed to the repository's project, which has its own lead
  and would refuse the hand-over. The lead stays with the removed id, and the
  project stays listed while that lead is set, as for any removed added folder
  with a lead.
- Adding the folder again takes back the id its ledger or lead is kept under:
  `addProject` reuses the removed project's id when the folder kept a ledger
  (today) or when this host's lead is still set for that folder under a
  `p-` id no added project holds (new). Without that, a re-add of a worktree
  project with a lead but no ledger would make a second project next to the
  orphaned lead.

## Tests

Through the daemon's actions on a real repository with linked worktrees:

- adding a linked worktree root creates a separate keyless project; adding a
  subfolder of an unadded worktree still answers with the main checkout's
  folder; adding a bare repository's worktree is unchanged;
- sessions in the worktree project and in worktrees nested in it belong to it;
  sessions in the main checkout's worker worktrees stay in the repository's
  project; a folder project added in the main checkout does not take a session
  in the worktree project;
- the worktree project's lead and ledger are its own; the repository's lead is
  unchanged by the add;
- removing it returns its sessions to the repository's project, and adding it
  again takes back its lead;
- a session started with its own worktree from inside the worktree project
  gets a worktree under it and belongs to it;
- the sweep never removes an added worktree project's folder;
- the key refresh leaves a worktree project inside `.worktrees` without a key.
