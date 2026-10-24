@testable import AgenthailHelpers
import Foundation

@main
struct SessionTreeTest {
    static func main() {
        let now = SessionTree.parseTimestamp("2026-10-04T12:00:00Z")!
        expect(SessionTree.parseTimestamp("2026-10-04 12:00:00") == now, "SQLite UTC timestamps parse as UTC")
        func flags(_ ids: [String]) -> [String: SessionAttention] {
            Dictionary(uniqueKeysWithValues: ids.map { ($0, SessionAttention(kind: .request, reason: "Asks you a question")) })
        }
        let caps = Capabilities(send: true, stream: true, reply: true, goal: false, compact: false, model: true, interrupt: true, steer: true)
        func session(_ id: String, status: String, lastActive: String, project: String, checkout: String, branch: String?, detached: String? = nil, name: String? = nil, alias: String? = nil) -> SessionState {
            SessionState(id: id, surface: "codex", name: name ?? id, alias: alias, status: status, lastActive: lastActive, queueCount: 0, open: true, current: status == "busy" || now.timeIntervalSince(SessionTree.parseTimestamp(lastActive) ?? .distantPast) <= 24 * 60 * 60, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/repo/\(checkout)", project: ProjectIdentity(id: project, displayName: project, rootPath: "/repo", kind: "git"), checkout: CheckoutIdentity(id: checkout, path: "/repo/\(checkout)", branch: branch, detachedHead: detached, isMain: checkout == "main", dirty: false))
        }
        let sessions = [
            session("old", status: "idle", lastActive: "2026-10-01T12:00:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("busy", status: "busy", lastActive: "2026-10-04T11:59:00.250-04:00", project: "agenthail", checkout: "feat", branch: "feat/x"),
            session("idle", status: "idle", lastActive: "2026-10-04T11:00:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("other", status: "idle", lastActive: "2026-10-04T10:00:00Z", project: "fable", checkout: "wt", branch: nil, detached: "de2fda1f716")
        ]

        let recent = SessionTree.build(sessions, filter: .recent, attention: flags(["other"]))
        expect(recent.counts == [.running: 1, .recent: 3, .all: 4, .archived: 0], "counts per filter: \(recent.counts)")
        expect(recent.projects.map(\.name) == ["agenthail", "fable"], "projects ordered by latest activity")
        expect(recent.projects[0].checkouts.map(\.label) == ["feat/x", "main"], "checkouts ordered by latest activity")
        expect(recent.projects[0].checkouts[1].sessions.map(\.id) == ["idle"], "sessions the daemon does not mark current leave Recent")
        expect(recent.projects[1].checkouts[0].label == "detached at de2fda1", "detached checkout label")
        expect(recent.needsYou.map(\.id) == ["other"], "attention sessions appear under Needs you")
        expect(SessionTree.needsYou(sessions, attention: flags(["other", "gone"])).map(\.id) == ["other"], "attention for a session missing from the catalog is not counted")

        let running = SessionTree.build(sessions, filter: .running, attention: [:])
        expect(running.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id) == ["busy"], "Running shows only working sessions")

        let all = SessionTree.build(sessions, filter: .all, attention: [:])
        expect(all.projects[0].checkouts.first { $0.label == "main" }?.sessions.map(\.id) == ["idle", "old"], "All keeps older sessions, newest first")
        let project = all.projects[0]
        let capped = project.limited(to: 2, keeping: nil)
        expect(capped.sessionCount == 2 && capped.checkouts.flatMap(\.sessions).map(\.id) == ["busy", "idle"], "a capped workspace keeps its most recent sessions")
        expect(capped.checkouts.map(\.label) == ["feat/x", "main"], "checkouts with no visible sessions are hidden")
        let keepSelected = project.limited(to: 1, keeping: "old")
        expect(Set(keepSelected.checkouts.flatMap(\.sessions).map(\.id)) == ["busy", "old"], "the selected session stays visible past the cap")
        func member(_ id: String, parent: String, root: String, depth: Int, nickname: String, role: String? = nil, status: String, lastActive: String) -> SessionState {
            var state = session(id, status: status, lastActive: lastActive, project: "agenthail", checkout: "main", branch: "main")
            state.subagent = SubagentIdentity(parentId: parent, rootId: root, depth: depth, nickname: nickname, role: role)
            state.current = status == "busy"
            return state
        }
        let lead = session("lead", status: "idle", lastActive: "2026-10-04T09:00:00Z", project: "agenthail", checkout: "main", branch: "main", name: "Build the parser")
        let family = [
            lead,
            member("ada", parent: "lead", root: "lead", depth: 1, nickname: "Ada", status: "busy", lastActive: "2026-10-04T11:58:00Z"),
            member("euclid", parent: "ada", root: "lead", depth: 2, nickname: "Euclid", role: "reviewer", status: "busy", lastActive: "2026-10-04T11:57:00Z"),
            member("bo", parent: "lead", root: "lead", depth: 1, nickname: "Bo", status: "idle", lastActive: "2026-10-04T08:00:00Z"),
            member("orphan", parent: "gone", root: "gone", depth: 1, nickname: "Dee", status: "idle", lastActive: "2026-10-04T07:00:00Z")
        ]
        let families = SessionFamilies.build(family)
        expect(families.map(\.id) == ["lead", "orphan"], "subagents nest under a listed parent; an unlisted parent leaves a root: \(families.map(\.id))")
        expect(families[0].members.map(\.id) == ["ada", "euclid", "bo"] && families[0].members.map(\.depth) == [1, 2, 1], "members follow their parent depth-first")
        expect(families[0].workingSubagents == 2 && families[0].isWorking && SessionFamilies.subagentSummary(families[0]) == "2 subagents working", "the root rolls up its working descendants")
        expect(SessionFamilies.title(family[2], in: family) == "Build the parser > Ada > Euclid (reviewer)", "a standalone subagent reads as its parent chain: \(SessionFamilies.title(family[2], in: family))")
        expect(SessionFamilies.title(family[4], in: family) == "Dee" && SessionFamilies.label(family[1]) == "Ada", "subagent labels use the nickname")

        var claudeLead = session("claude-lead", status: "idle", lastActive: "2026-10-04T11:00:00Z", project: "fable", checkout: "wt", branch: "main")
        claudeLead.subagents = SubagentRollup(count: 4, working: 1)
        let claude = SessionFamilies.build([claudeLead])[0]
        expect(claude.workingSubagents == 1 && claude.isWorking && SessionFamilies.subagentSummary(claude) == "1 subagent working", "only working observed Claude subagents roll into the parent row")

        let familyTree = SessionTree.build(family + [claudeLead], filter: .running, attention: [:])
        expect(families[1].isCurrent == false && families[0].isCurrent, "a family is current while any member works")
        expect(familyTree.counts[.running] == 2, "Running counts working families, not subagent threads: \(familyTree.counts)")
        expect(familyTree.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id).sorted() == ["claude-lead", "lead"], "a family with a working subagent is running and shows its root")
        let allFamilies = SessionTree.build(family, filter: .all, attention: [:])
        expect(allFamilies.counts[.all] == 2, "All counts families: \(allFamilies.counts)")
        expect(allFamilies.projects.map(\.listedSessionCount) == [5] && allFamilies.projects[0].sessionCountLabel == "5 sessions", "a project header counts every session it lists, subagents included: \(allFamilies.projects.map(\.listedSessionCount))")
        let capFamily = allFamilies.projects[0].limited(to: 1, keeping: "euclid")
        expect(capFamily.checkouts.flatMap(\.families).map(\.id) == ["lead"], "selecting a subagent keeps its family visible past the cap")

        var offline = session("gone", status: "offline", lastActive: "2026-10-04T11:30:00Z", project: "agenthail", checkout: "main", branch: "main")
        offline = SessionState(id: offline.id, surface: "claude", name: "gone", alias: nil, status: "offline", lastActive: offline.lastActive, queueCount: 0, open: false, current: true, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: offline.cwd, project: offline.project, checkout: offline.checkout)
        let notLoaded = session("cold", status: "notLoaded", lastActive: "2026-10-04T11:40:00Z", project: "agenthail", checkout: "main", branch: "main")
        let unknown = session("lost", status: "unknown", lastActive: "2026-10-04T11:41:00Z", project: "agenthail", checkout: "main", branch: "main")
        let refined = sessions + [offline, notLoaded, unknown]
        func ids(_ tree: SessionTree) -> [String] { tree.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id).sorted() }
        let idleOnly = SessionTree.build(refined, filter: .all, refinement: SessionRefinement(status: .idle), attention: flags(["busy"]))
        expect(ids(idleOnly) == ["idle", "old", "other"], "the idle filter keeps idle sessions: \(ids(idleOnly))")
        expect(idleOnly.counts == [.running: 0, .recent: 2, .all: 3, .archived: 0], "scope counts follow the refinement: \(idleOnly.counts)")
        expect(idleOnly.needsYou.map(\.id) == ["busy"], "Needs you is not narrowed by the refinement")
        let unavailable = SessionTree.build(refined, filter: .all, refinement: SessionRefinement(status: .unavailable), attention: [:])
        expect(ids(unavailable) == ["cold", "lost"], "unavailable covers unknown and not loaded: \(ids(unavailable))")
        let claudeOnly = SessionTree.build(refined, filter: .all, refinement: SessionRefinement(surface: "claude"), attention: [:])
        expect(ids(claudeOnly) == ["gone"], "the surface filter keeps one surface: \(ids(claudeOnly))")
        let offlineCodex = SessionTree.build(refined, filter: .all, refinement: SessionRefinement(status: .offline, surface: "codex"), attention: [:])
        expect(offlineCodex.projects.isEmpty, "status and surface combine")
        let busyRunning = SessionTree.build(refined, filter: .running, refinement: SessionRefinement(status: .busy), attention: [:])
        expect(ids(busyRunning) == ["busy"], "Running with Working shows the working session")
        expect(SessionTree.build(refined, filter: .all, refinement: SessionRefinement(), attention: [:]).counts[.all] == 7, "no refinement keeps every family")
        let workingMember = SessionTree.build(family, filter: .all, refinement: SessionRefinement(status: .busy), attention: [:])
        expect(workingMember.projects.flatMap { $0.checkouts.flatMap(\.families) }.map(\.id) == ["lead"], "a family matches when any member matches")
        expect(SessionRefinement.surfaces(refined, configured: [SurfaceState(name: "notion", connected: false, error: nil, health: "unavailable", healthDetail: nil, capabilities: caps)]) == ["claude", "codex", "notion"], "surface choices merge configured surfaces and sessions")
        expect(SessionRefinement.surfaceLabel("codex") == "Codex", "surface labels are capitalized")

        var hidden = session("hidden", status: "idle", lastActive: "2026-10-04T11:50:00Z", project: "agenthail", checkout: "main", branch: "main")
        hidden.archived = true
        var loose = session("loose", status: "idle", lastActive: "2026-10-04T11:55:00Z", project: "agenthail", checkout: "main", branch: "main")
        loose.project = nil
        loose.cwd = "/tmp/scratch"
        let archiveTree = SessionTree.build(sessions + [hidden, loose], filter: .all, attention: [:])
        expect(archiveTree.counts == [.running: 1, .recent: 4, .all: 5, .archived: 1], "archived sessions leave every scope but Archived: \(archiveTree.counts)")
        expect(!ids(archiveTree).contains("hidden"), "All hides archived sessions")
        expect(archiveTree.projects.last?.identity == nil && archiveTree.projects.last?.name == "No project", "sessions without a project group last")
        expect(ids(SessionTree.build(sessions + [hidden], filter: .archived, attention: [:])) == ["hidden"], "Archived lists archived sessions")

        var remote = session("h1::remote", status: "idle", lastActive: "2026-10-04T11:58:00Z", project: "h1::agenthail", checkout: "wt", branch: "history-truth")
        remote.host = SessionHost(id: "h1", name: "vps.example.ts.net", state: "connected")
        var remoteLoose = SessionState(id: "h1::loose", surface: "codex", name: "remote loose", alias: nil, status: "idle", lastActive: "2026-10-04T11:57:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/home/demo")
        remoteLoose.host = remote.host
        let hosted = SessionTree.build(sessions + [remote, loose, remoteLoose], filter: .all, attention: [:])
        expect(hosted.projects.first { $0.id == "h1::agenthail" }?.hosts == ["vps"], "a remote project names its host without the domain")
        expect(hosted.projects.first { $0.id == "agenthail" }?.hosts == [], "a project on this host carries no host")
        expect(Set(hosted.projects.suffix(2).map(\.hostLabel)) == [nil, "vps"] && hosted.projects.suffix(2).allSatisfy { $0.identity == nil }, "sessions without a project group per host, last: \(hosted.projects.map(\.id))")

        func onHost(_ id: String, _ host: SessionHost?, checkout: String?) -> SessionState {
            var row = session(id, status: "idle", lastActive: "2026-10-04T11:5\(id.count % 10):00Z", project: "repo:github.com/example/widget", checkout: "main", branch: "main")
            row.checkout = CheckoutIdentity(id: checkout, path: "/srv/widget", branch: "main", detachedHead: nil, isMain: true, dirty: false)
            row.host = host
            return row
        }
        let vps = SessionHost(id: "h1", name: "vps.example.ts.net", state: "connected")
        let laptop = SessionHost(id: "h2", name: "laptop.example.ts.net", state: "connected")
        let spread = SessionTree.build([onHost("here", nil, checkout: "checkout-1"), onHost("h1::there", vps, checkout: "h1::checkout-1"), onHost("h2::elsewhere", laptop, checkout: "h2::checkout-1")], filter: .all, attention: [:]).projects
        expect(spread.count == 1 && spread[0].checkouts.count == 3, "one repository on three hosts is one project with a checkout per host: \(spread.map { $0.checkouts.map(\.id) })")
        expect(Set(spread[0].hosts) == ["vps", "laptop"] && spread[0].spansHosts, "the project header names every other host: \(spread[0].hosts)")
        expect(Set(spread[0].checkouts.map(\.host)) == [nil, "vps", "laptop"], "each checkout names its own host")
        let unnamed = SessionTree.build([onHost("here", nil, checkout: nil), onHost("h1::there", vps, checkout: nil)], filter: .all, attention: [:]).projects
        expect(unnamed.first?.checkouts.count == 2, "the same path on two hosts stays two checkouts without a checkout id")
        let remoteOnly = SessionTree.build([onHost("h1::there", vps, checkout: "h1::checkout-1")], filter: .all, attention: [:]).projects
        expect(remoteOnly.first?.spansHosts == false && remoteOnly.first?.hostLabel == "vps", "a project on one other host does not span hosts")

        var queued = session("queued", status: "idle", lastActive: "2026-09-01T00:00:00Z", project: "agenthail", checkout: "main", branch: "main")
        queued.queueCount = 1
        let flagged = session("flagged", status: "idle", lastActive: "2026-09-02T00:00:00Z", project: "agenthail", checkout: "main", branch: "main")
        var rolled = session("rolled", status: "idle", lastActive: "2026-09-03T00:00:00Z", project: "agenthail", checkout: "main", branch: "main")
        rolled.subagents = SubagentRollup(count: 2, working: 1)
        let crowded = sessions + [queued, flagged, rolled] + (0..<6).map { session("filler\($0)", status: "idle", lastActive: "2026-10-04T11:3\($0):00Z", project: "agenthail", checkout: "main", branch: "main") }
        let collapsed = SessionTree.build(crowded, filter: .all, attention: flags(["flagged"])).projects[0].limited(to: SessionTree.collapsedSessionLimit, keeping: nil)
        let shownIDs = Set(collapsed.checkouts.flatMap(\.sessions).map(\.id))
        expect(shownIDs.isSuperset(of: ["busy", "queued", "flagged", "rolled"]) && !shownIDs.contains("old"), "collapsing a project keeps working, working-subagent, queued and attention sessions: \(shownIDs.sorted())")

        let mixed = [
            session("whole", status: "idle", lastActive: "2026-10-04T15:58:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("short-fraction", status: "idle", lastActive: "2026-10-04T11:59:58.03-04:00", project: "agenthail", checkout: "main", branch: "main"),
            session("sqlite", status: "idle", lastActive: "2026-10-04 15:59:00", project: "agenthail", checkout: "main", branch: "main"),
            session("unparsed", status: "idle", lastActive: "yesterday", project: "agenthail", checkout: "main", branch: "main")
        ]
        expect(SessionTree.newestFirst(mixed).map(\.id) == ["short-fraction", "sqlite", "whole", "unparsed"], "newest first across timestamp formats, unparsed last")
        func waiting(_ id: String, kind: String, lastActive: String = "2026-10-04T11:00:00Z") -> SessionState {
            var state = session(id, status: "idle", lastActive: lastActive, project: "agenthail", checkout: "main", branch: "main")
            state.waitingOn = WaitingState(kind: kind, id: "req-\(id)")
            return state
        }
        func item(_ id: Int64, _ sessionID: String, _ reason: String) -> AttentionState {
            AttentionState(id: id, sessionId: sessionID, target: sessionID, queueId: id, reason: reason, requestedAction: "Retry or cancel this message", createdAt: "2026-10-04 11:00:00")
        }
        func problem(_ id: Int64, to target: String, from sender: String?) -> DeliveryProblem {
            DeliveryProblem(deliveryId: id, sessionId: target, sourceSessionId: sender, attentionSessionId: sender, message: "Run the tests", reason: "expired", status: "failed", at: "2026-10-04 11:00:00")
        }
        let asker = waiting("asker", kind: "question", lastActive: "2026-10-01T00:00:00Z")
        let approver = waiting("approver", kind: "approval")
        let reviewer = session("reviewer", status: "idle", lastActive: "2026-10-04T11:30:00Z", project: "agenthail", checkout: "main", branch: "main", alias: "reviewer")
        let sender = session("sender", status: "idle", lastActive: "2026-10-04T11:59:00Z", project: "agenthail", checkout: "main", branch: "main")
        let fanout = session("fanout", status: "idle", lastActive: "2026-10-04T11:58:00Z", project: "agenthail", checkout: "main", branch: "main")
        let target = session("target", status: "idle", lastActive: "2026-10-04T11:57:00Z", project: "agenthail", checkout: "main", branch: "main")
        let mixedTarget = session("mixed", status: "idle", lastActive: "2026-10-04T11:56:00Z", project: "agenthail", checkout: "main", branch: "main")
        var archivedAsker = waiting("archived-asker", kind: "question")
        archivedAsker.archived = true
        var coldTarget = session("cold-target", status: "notLoaded", lastActive: "2026-09-12T00:00:00Z", project: "agenthail", checkout: "main", branch: "main")
        coldTarget.waitingOn = WaitingState(kind: "question", id: "stale")
        var lostRemote = waiting("h1::lost", kind: "question")
        lostRemote.host = SessionHost(id: "h1", name: "vps.example.ts.net", state: "disconnected")
        lostRemote.unavailableReason = "The host is offline"
        let both = waiting("both", kind: "input")
        let pool = [asker, approver, reviewer, sender, fanout, target, mixedTarget, archivedAsker, coldTarget, lostRemote, both]
        let reasons = SessionAttention.build(
            sessions: pool,
            attention: [item(1, "target", "Delivery outcome could not be confirmed"), item(2, "target", "Delivery outcome could not be confirmed"), item(3, "mixed", "Claude needs to reconnect"), item(4, "mixed", "The target refused to start"), item(5, "cold-target", "Delivery target is unavailable"), item(6, "gone", "Delivery target is unavailable")],
            deliveryProblems: [problem(1, to: "reviewer", from: "sender"), problem(2, to: "reviewer", from: "sender"), problem(3, to: "reviewer", from: "fanout"), problem(4, to: "target", from: "fanout"), problem(5, to: "reviewer", from: nil), problem(6, to: "reviewer", from: "both")]
        )
        expect(reasons["asker"] == SessionAttention(kind: .request, reason: "Asks you a question"), "a session waiting on a question needs you even with no queue attention: \(String(describing: reasons["asker"]))")
        expect(reasons["approver"]?.reason == "Needs your approval", "an approval reads as one")
        expect(reasons["target"] == SessionAttention(kind: .incoming, reason: "2 messages: Delivery outcome could not be confirmed"), "failed messages to a session share their reason: \(String(describing: reasons["target"]))")
        expect(reasons["mixed"]?.reason == "2 messages need a decision", "different reasons are counted, not listed")
        expect(reasons["sender"] == SessionAttention(kind: .outgoing, reason: "2 messages to @reviewer not delivered"), "an agent's undelivered messages name their target: \(String(describing: reasons["sender"]))")
        expect(reasons["fanout"]?.reason == "2 messages not delivered", "messages to several targets are counted")
        expect(reasons["both"]?.kind == .request && reasons["both"]?.reason == "Needs your input", "a request outranks a delivery problem")
        expect(reasons["archived-asker"] == nil && reasons["cold-target"] == nil && reasons["h1::lost"] == nil && reasons["gone"] == nil, "archived, unavailable, unreachable and missing sessions never need you")
        expect(reasons["reviewer"] == nil, "the target of an agent's failed message is not flagged; a person's own failed send flags no one")
        expect(Set(reasons.keys) == ["asker", "approver", "target", "mixed", "sender", "fanout", "both"], "only sessions that can act are flagged: \(reasons.keys.sorted())")
        let ordered = SessionTree.needsYou(pool, attention: reasons).map(\.id)
        expect(ordered == ["approver", "both", "asker", "target", "mixed", "sender", "fanout"], "requests come first, then decisions on failed messages, then undelivered sends, newest first within each: \(ordered)")
        expect(SessionTree.build(pool, filter: .all, attention: reasons).needsYou.map(\.id) == ordered, "the tree shows the same Needs you list")

        var terminal = waiting("terminal", kind: "approval")
        terminal.runtimeKind = "claude-cli"
        func hostedSession(_ id: String, waiting: WaitingState) -> SessionState {
            var state = SessionState(id: id, surface: "claude", name: id, alias: nil, status: "busy", lastActive: "2026-10-04T11:00:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(respond: true, lifecycle: SessionLifecycle(stop: true, resume: true)), readOnly: nil, readOnlyReason: nil)
            state.runtimeKind = "claude-host"
            state.waitingOn = waiting
            return state
        }
        let hostedWait = hostedSession("hosted", waiting: WaitingState(kind: "approval", id: "r0"))
        let held = hostedSession("held", waiting: WaitingState(kind: "approval", id: "r1", answerable: true))
        var limited = session("limited", status: "idle", lastActive: "2026-10-04T11:40:00Z", project: "agenthail", checkout: "main", branch: "main")
        limited.usageLimit = UsageLimitState(resetsAt: "2026-10-04T18:20:00Z")
        var unknownReset = session("unknown-reset", status: "idle", lastActive: "2026-10-04T11:39:00Z", project: "agenthail", checkout: "main", branch: "main")
        unknownReset.usageLimit = UsageLimitState()
        var resuming = session("resuming", status: "idle", lastActive: "2026-10-04T11:38:00Z", project: "agenthail", checkout: "main", branch: "main")
        resuming.usageLimit = UsageLimitState(resetsAt: "2026-10-04T18:20:00Z")
        resuming.limitResume = LimitResumeState(state: "waiting", resetsAt: "2026-10-04T18:20:00Z")
        var probing = session("probing", status: "idle", lastActive: "2026-10-04T11:37:00Z", project: "agenthail", checkout: "main", branch: "main")
        probing.limitResume = LimitResumeState(state: "waiting")
        var kept = session("kept", status: "idle", lastActive: "2026-10-04T11:36:00Z", project: "agenthail", checkout: "main", branch: "main")
        kept.limitResume = LimitResumeState(state: "canceled", resetsAt: "2026-10-04T18:20:00Z")
        let places = SessionAttention.build(sessions: [terminal, hostedWait, held, limited, unknownReset, resuming, probing, kept], attention: [], deliveryProblems: [])
        expect(places["resuming"]?.kind == .usageLimit && places["resuming"]?.reason.hasPrefix("Usage limit, resumes ") == true, "a stop its computer resumes says when: \(String(describing: places["resuming"]))")
        expect(places["probing"]?.reason == "Usage limit, resumes when it lifts", "a stop with no reset time resumes when the limit lifts")
        expect(places["kept"]?.reason == "Stopped by usage limit", "a stop kept stopped says so")
        expect(places["terminal"]?.reason == "Waiting in its terminal", "a terminal prompt Agenthail does not hold is answered in its terminal: \(String(describing: places["terminal"]))")
        expect(places["hosted"]?.reason == "Waiting on a prompt Agenthail can't reach. Stop and resume to clear it", "a hosted prompt no one holds says how to clear it: \(String(describing: places["hosted"]))")
        expect(places["held"]?.reason == "Needs your approval", "a request Agenthail holds asks for the answer")
        let resets = ISO8601DateFormatter().date(from: "2026-10-04T18:20:00Z")!.formatted(date: .omitted, time: .shortened)
        expect(places["limited"] == SessionAttention(kind: .usageLimit, reason: "Usage limit, resets \(resets)"), "a usage limit names when it resets: \(String(describing: places["limited"]))")
        expect(places["unknown-reset"]?.reason == "Usage limit reached", "a limit with no reset time says it was reached")
        expect(SessionTree.needsYou([limited, terminal], attention: places).map(\.id) == ["terminal", "limited"], "a usage limit comes after waits on the person")
        print("session tree tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
