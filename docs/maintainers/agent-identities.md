# Agent identities: a name and an inbox for an agent outside Agenthail

An agent that is not an Agenthail session, such as an assistant that runs shell commands on a machine with Agenthail installed, can send with `agenthail send`. Without an identity its messages come from the Agenthail CLI, which nothing reads, so no one can answer it. An agent identity gives it a durable `@name`: its sends name it, and messages sent to it wait in an inbox it reads with the CLI. No model runs behind an identity.

## Use

```
agenthail identity add scout
export AGENTHAIL_SESSION_ID=scout           # or pass --from scout on each send
agenthail send @builder "the deploy is green"
agenthail inbox --wait 10m                # prints unread messages and marks them read
```

| Command | What it does |
| --- | --- |
| `identity add <name>` | Records the identity and binds `@<name>` to it. |
| `identity rm <name>` | Removes the identity, its alias and its inbox. |
| `identity list [--json]` | Lists identities with unread and total counts. |
| `inbox [<name>] [--all] [--peek] [--limit <n>] [--wait <duration>] [--json]` | Prints unread messages oldest first and marks them read in the same transaction, so two readers never both take one. `--all` includes read ones, `--peek` leaves them unread, `--wait` blocks until one is unread and exits 1 when the time runs out. The name defaults to the identity in `AGENTHAIL_SESSION_ID`. |
| `inbox [<name>] --prune <age>` | Deletes read messages received before an age such as `30d` or `12h`. Unread messages are never pruned. |

Each message shows who sent it as a reply address on this host: `@builder`, `laptop::@builder` for a session on a paired host, or the session id when it has no alias. `agenthail send <that address> ...` answers it.

A name follows the alias rules: lowercase letters, digits, `.`, `_` and `-`, at most 64, starting with a letter or digit. A name an alias, a coordinator, an alias reservation or a session id already holds is refused, and so is `lead`. `identity rm` refuses while `@<name>` names another session.

## How it is stored

An identity is a local session row (`sessions`, id `agent:<name>`, surface `agent`, runtime kind `agent-inbox`) with its alias in `aliases`, plus a row in `agent_identities`. Its messages are rows in `agent_inbox`, which keeps the full text, the sender's id, the reply address the sender had when it arrived, and when it was read. A message's id is the delivery intent's uuid, so a send replayed to this host is stored once. Both tables arrived with schema 114.

The `agentinbox` adapter serves the `agent-inbox` runtime. Its `Send` stores the message and reports it delivered; the dispatcher takes the inbox route for it, which records the intent as sent but starts no turn, and its receipt says the message is in the inbox and that nothing replies. `send --reply` and `--stream` to an identity are refused before anything is sent. Interrupt, steer, goals, compaction and models are refused. `agenthail last @scout` lists the inbox without marking anything read.

The adapter lists identities to the catalog like any surface, so each identity has a catalog row. The session list leaves identity rows out (`CatalogListing`), the way it leaves out idle subagents, and clients get no `agent` surface: no health chip, no filter and nothing to start (`surface.Listed`). A paired host's mirror reads every catalog row, so it carries identities.

## Sender and reach

`--from scout` and `AGENTHAIL_SESSION_ID=scout` resolve to the identity through the same lookup as any session. The message is labeled `[from @scout]`, and a Claude peer message names `agenthail/agent: @scout`. Failure and held notices for an identity's own sends go to its inbox.

An identity has the reach of the Agenthail CLI: the CLI does not check it as an agent session, so it is not held to one project. It only names the sender of a message the CLI could already send without one, and grants nothing the person running that CLI does not already have.

## Across paired hosts

The pairing carries an identity both ways with no new path (see [paired hosts](hosts.md)):

- From the identity's host, a send to a paired host's session goes qualified as `<host id>::agent:scout` with the name `@scout`. The receiver mirrors that row, so the message's sender is the mirror and the receiver addresses it as `server::@scout`. A reply routes to the identity's host and lands in the inbox.
- From a paired host, `agenthail send server::@scout ...` resolves through the mirror. While the identity's host is unreachable the send is held and replayed with its idempotency key, and the inbox keeps it once.
- A one-way pairing falls back to the unrepliable sender row, as for any session.

After `identity rm`, the next discovery passes retire the row and paired hosts drop their mirror; a send that still reaches the host is refused with `no such agent identity`.

## Known gaps

- The Mac and iPhone apps and the web dashboard do not show identities or their inboxes. Messages from one show its name like any agent's.
- `agenthail inbox` reads the local registry, so it runs on the identity's own host.
