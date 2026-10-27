package cli

import "fmt"

func (a *App) usage() {
	fmt.Print(`agenthail - hail an agent

Usage:
  agenthail <command> [target] [args] [options]

Session commands:
  codex [args]                  Start a writable Codex terminal session
  codex --repair-managed-runtime Restart a stale managed Codex runtime
  thread create <codex|claude> "msg" [--cwd <folder>]  Create a conversation
  thread fork <target>         Fork a Codex conversation
  thread <status|stop|resume|logs> <target>  Manage a Claude session (logs: claude-bg jobs)
  thread <archive|unarchive> <target>  Hide a session until it is active again
  thread adopt <target>        Open a terminal Claude session in Agenthail
  thread queue <target> <list|add|update|delete|reorder|start>  Manage Codex native input
  list [--all|--archived|--recent|--running] [--project <name|id>|--cwd <path>] [--wide]
                                 List daemon catalog conversations; --recent adds the last 24h;
                                 --cwd lists the project that holds that folder
  project list|add <path>|remove <name>|rename <name> <new>|archive <name>|unarchive <name>
                                 List, add, rename or remove project folders, or hide a project
  runs [--json]                  Read verified Claude job and subagent records
  whoami [--json]                Who the calling session is, who it reports to, how it reaches the person
  help agent                    What the calling session can do through Agenthail
  roots [--json]                 List each Claude config directory and the login it currently holds
  usage [--json]                 Show every account's usage limits, resets and running sessions on each host
  search codex <query>           Search older Codex conversation history on demand
  send <target> "msg"|-       Send now when idle; queue when busy (use --no-queue to refuse delay)
  stream <target>               Tail live activity
  reply <target> [--before cursor] [--json] [--max-bytes N | --report] [--timeout 30s]  Fetch last assistant reply, capped or as its completion report
  last <target> [count] [--before cursor] [--full] [--json] [--max-bytes N | --report] [--timeout 30s]  Show a bounded exchange page
  goal <target> [set|edit|pause|resume|budget|clear] [value] [--json]
                                 Read goal usage or control its objective, status and budget
  compact <target>              Compress context (typed control; unsupported for socket-only Claude peers)
  model <target> [name] [--json] [--timeout 30s]
                                 Get or set model; Claude waits for its own control response
  permission-mode <target> [mode] [--json]
                                 Get or set the permission mode of a session the Claude host runs
  requests <target> [--json]     List the tool approvals and questions a session waits on you for
  answer <target> <allow|always|deny|"answer"> [--request id] [--reason text] [--answers json]
                                 Answer the oldest open request, or the one --request names
  interrupt <target> [--json] [--timeout 30s]
                                 Stop current turn; Claude waits for the turn to end
  steer <target> "message" [--json] [--timeout 30s]
                                 Inject guidance into a running turn; use send when idle
  control <target> <context|usage|status|effort|rename> [value] [--json] [--timeout 30s]
                                 Run a Claude Remote Control command and show its output
  queue <target> "msg"|-        Hold until target is ready; start daemon to deliver queued work
  queue list [--json] [--all] [--target <target>] [--mine] [--project <name|id>|--cwd <path>]
                                 Inspect queued work scoped to a session or project
  queue retry <id>              Retry a failed message
  queue rm <id>                 Cancel a pending queued message
  queue clear <target>          Cancel all pending messages for a target
  history [target] [count]       Show delivery history (default 50)
  limit-resume [list] [--json]  Show sessions a usage limit stopped and when they resume
  limit-resume resume <target>  Resume a stopped session now
  limit-resume cancel <target>  Do not resume a stopped session for its current stop
  limit-resume on|off           Resume stopped sessions once their limit resets (default on)

Identity:
  identify <target> <name>      Name a session (henceforth @name resolves to it)
  identify rm <name>             Remove an alias
  identify list                 Show all names

Coordinator (one per project; a message to it always lands):
  coordinator [--cwd <path>] [--json]  Show the coordinator of this folder's project
  coordinator list [--json]     Show every project's coordinator
  coordinator set <target> [--name <name>] [--instructions <file>] [--permission-mode <mode>]
                                 Make an existing session its project's coordinator
  coordinator new <claude|codex> [--cwd <path>] [--name <name>] [--model <model>] [--instructions <file>] [--permission-mode <mode>]
                                 Define this folder's project coordinator (default @<project>-lead); the first message starts it
  coordinator clear [--cwd <path>]  Stop treating any session as this project's coordinator
  @lead addresses the coordinator of the sender's project. A send to a coordinator
  steers it when busy, resumes it when ended, and starts a new conversation from
  its saved instructions when its conversation is gone.

Channels:
  channel create <name>         Create a channel
  channel add <name> <target>   Add a session to a channel
  channel rm <name> <target>   Remove a session from a channel (--all deletes)
  channel list                  List channels + members (shows @alias names)
  channel rename <name> <new>   Rename a channel
  channel send <name> "msg"     Broadcast to all members (--from <name>)
  channel send '#<project>' "msg"  Message a project's active conversations
                                on every host and its coordinator

Automatic handoffs:
  relay add <from> <to> [regex] [--once] [--when turn_finished|goal_completed]
  relay list [--json]           Show routing rules and derived firing evidence
  relay rm <id>                 Remove a rule

Hosts:
  hosts [--json]                 List this host and paired hosts with their health
  hosts invite                  Print a pairing link for another host's daemon
  hosts add <link> [--address <host-tailscale-ip>] [--name <name>]
                                 Pair a host so its sessions appear here
  hosts remove <host>            Unpair a host and drop its sessions from this host
  hosts name [name]              Show or set this host's name
  move <target> --to <host> [--wait <duration>] [--stop-terminal] [--json]
                                 Move an idle Claude session to a paired host
  move list [--unfinished] [--json]
                                 Show moves to and from this host

Phones:
  devices [--json]               List the phones paired with this host
  devices pair [--name <label>] [--json]
                                 Show a code that pairs the iPhone app with this host
  devices remove <device>        Disconnect a phone from this host

Browser sign-in (Notion and Claude Remote Control only):
  signin [list] [--json]        Show consumers, sign-in profiles and this computer's browser profiles
  signin add <browser> <profile> [--label <label>]
                                Add a browser profile to read sign-in cookies from
  signin use <notion|claude> <profile|none>
                                Choose the profile a consumer reads cookies from
  signin rm <profile>           Remove a sign-in profile

Background service:
  daemon start                  Start Agenthail in the background
  daemon stop                   Stop Agenthail in the background
  daemon restart                Restart Agenthail in the background
  daemon status                 Check whether Agenthail is running
  daemon notify on|off|status|test|settings
                                Native macOS completion notifications
  daemon install                Keep Agenthail running after login and restarts
  daemon uninstall              Stop Agenthail from starting automatically
  debug heap <dir>              Write the daemon's memory statistics and heap, allocation and goroutine profiles to <dir>

Claude hooks (answer terminal Claude prompts from Agenthail; tell each session who it is):
  hooks install                 Add Agenthail's hooks to every Claude config root
  hooks install --remote-control on|off
                                On an install with Claude Remote Control, also start it in each new Claude session,
                                so what you write in Agenthail is your own turn there
  hooks uninstall               Remove them
  hooks status                  Show where they are installed
  hooks install codex           Tell Codex sessions who they are (AGENTS.md and trusted hooks)
  hooks uninstall codex         Remove them
  hooks status codex            Show whether they are installed

Dashboard (optional):
  dashboard enable              Enable the local dashboard and open it
  dashboard disable             Disable the dashboard listener
  dashboard status              Show dashboard state and address
  dashboard config --codex-recent-hours <hours>
                                Set the Codex current-session window
  dashboard remote              Enable private phone access with Tailscale and
                                print a one-time phone sign-in link + QR
  dashboard remote status       Show remote access state and phone address
  dashboard remote off          Remove Agenthail's Tailscale Serve route
  dashboard                     Open the dashboard with a one-time sign-in link
  dashboard token rotate        Replace the token the CLI and Mac app use

Interface preferences (ui.conf, versioned; every change is a commit):
  ui get [--resolved] [--device <class>] [--json]
                                Print ui.conf, or every value one device uses
  ui set 'key = value' ...      Change keys; images are copied in and versioned
  ui unset <key>[@device] ...   Go back to the default
  ui write <file|->             Replace the whole file after checking it
  ui check <file|->             Report problems with line numbers
  ui log | diff [<rev>] | undo [<rev>]
                                History, what a change did, and revert
  ui show-defaults [--docs]     Every key with its default and documentation

Other:
  launch <surface>              Launch a surface app with debug settings
  doctor [--json]               Health check (nonzero when any surface is unhealthy)
  permissions [--json]          The daemon's macOS privacy grants and where to change them
  permissions request           Have macOS ask for the grants the daemon lacks
  person name [<name>|--reset]  The name sessions see on your messages
  update [--check] [--json]     Install the latest Agenthail release
  upgrade [--check] [--json]    Alias for update
  version [--json]              Build and revision information

Targets: @name, PID, session id prefix, cwd/name fragment, surface:target, or host::target.
        A target that matches on more than one host is an error; qualify it as host::target.
        send, steer, interrupt, reply, last, stream and thread create --host reach paired hosts.
Sender: --from resolves a session; otherwise identity comes from AGENTHAIL_SESSION_ID,
        CLAUDE_CODE_SESSION_ID, then CODEX_THREAD_ID.
        Native Claude peers require the daemon and register automatically before sending.
`)
}
