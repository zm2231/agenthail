@testable import AgenthailHelpers
import Foundation

@main
struct SessionTreeTest {
    static let caps = Capabilities(send: true, stream: true, reply: true, goal: false, compact: false, model: true, interrupt: true, steer: true)
    static let now = SessionTree.parseTimestamp("2026-10-04T12:00:00Z")!

    static func session(_ id: String, status: String = "idle", lastActive: String = "2026-10-04T11:00:00Z", project: String? = "agenthail", checkout: String = "main", branch: String? = "main", detached: String? = nil, name: String? = nil, alias: String? = nil) -> SessionState {
        SessionState(id: id, surface: "codex", name: name ?? id, alias: alias, status: status, lastActive: lastActive, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/repo/\(checkout)", project: project.map { ProjectIdentity(id: $0, displayName: $0, rootPath: "/repo", kind: "git") }, checkout: CheckoutIdentity(id: checkout, path: "/repo/\(checkout)", branch: branch, detachedHead: detached, isMain: checkout == "main", dirty: false))
    }

    static func family(_ id: String, project: String, checkout: String = "", members: [(String, Int)] = [], pinned: Bool = false, working: Int = 0) -> ViewFamily {
        ViewFamily(id: id, rootId: id, projectId: project, checkoutId: checkout, members: [ViewMember(id: id, depth: 0, contextOnly: false, pinned: pinned)] + members.map { ViewMember(id: $0.0, depth: $0.1, contextOnly: false, pinned: false) }, pinned: pinned, workingSubagents: working)
    }

    static func view(_ families: [ViewFamily], projects: [(String, [String])], counts: [String: Int] = [:]) -> ScopePage.View {
        ScopePage.View(counts: counts.mapValues { ScopePage.Count(families: $0, sessions: $0) }, surfaces: [], projects: projects.map { project in
            ViewProject(id: project.0, name: project.0, counts: ScopePage.Count(families: families.filter { $0.projectId == project.0 }.count, sessions: families.filter { $0.projectId == project.0 }.flatMap(\.members).count), collapsedFamilyIds: project.1)
        }, families: families)
    }

    static func rows(_ sessions: [SessionState]) -> [String: SessionState] {
        Dictionary(sessions.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
    }

    static func ids(_ tree: SessionTree) -> [String] { tree.projects.flatMap { $0.checkouts.flatMap(\.families) }.map(\.id) }

    static func main() {
        expect(SessionTree.parseTimestamp("2026-10-04T12:00:00.000000000Z") == now, "fractional RFC3339 timestamps parse as UTC")
        rendersTheDaemonOrder()
        groupsCheckoutsAndHosts()
        joinsComputers()
        detectsNews()
        holdsOrder()
        familiesAndAttention()
        print("session tree tests passed")
    }

    static func rendersTheDaemonOrder() {
        let sessions = [session("zeta", project: "zeta"), session("alpha", project: "alpha"), session("old", lastActive: "2026-10-01T12:00:00Z", project: "alpha"), session("busy", status: "busy", lastActive: "2026-10-04T11:59:00Z", project: "alpha", checkout: "feat", branch: "feat/x")]
        let order = view([family("zeta", project: "zeta", pinned: true), family("old", project: "alpha", checkout: "main"), family("busy", project: "alpha", checkout: "feat"), family("alpha", project: "alpha", checkout: "main"), family("ghost", project: "alpha")], projects: [("zeta", ["zeta"]), ("alpha", ["old"])], counts: ["running": 1, "current": 3, "all": 4])
        let tree = SessionTree.ordered(list: order, current: order, rows: rows(sessions), attention: [:])
        expect(tree.projects.map(\.id) == ["zeta", "alpha"], "projects come in the daemon's order, not by name: \(tree.projects.map(\.id))")
        expect(ids(tree) == ["zeta", "old", "alpha", "busy"], "families keep daemon order inside each checkout, a family without a row is skipped: \(ids(tree))")
        expect(tree.projects[1].checkouts.map(\.label) == ["main", "feat/x"], "checkouts appear in the order their first family does")
        expect(tree.counts[.running] == 1 && tree.counts[.all] == 4 && tree.counts[.archived] == nil, "counts are the daemon's: \(tree.counts)")
        expect(tree.projects[0].pinned == ["zeta"], "pinned comes from the view")
        let collapsed = tree.projects[1].collapsed(keeping: nil)
        expect(collapsed.checkouts.flatMap(\.families).map(\.id) == ["old"], "collapse shows exactly the daemon's collapsed families")
        expect(tree.projects[1].collapsed(keeping: "busy").checkouts.flatMap(\.families).map(\.id) == ["old", "busy"], "the selected family stays visible in its daemon place")
        expect(tree.projects[1].familyTotal == 4 && tree.projects[1].sessionCountLabel == "4 sessions", "a project's totals are the daemon's")
        expect(SessionTree.ordered(list: nil, current: order, rows: rows(sessions), attention: [:]).projects.isEmpty, "a list whose view has not arrived shows nothing rather than another order")
        let detached = SessionTree.ordered(list: view([family("wt", project: "fable", checkout: "wt")], projects: [("fable", [])]), current: nil, rows: rows([session("wt", project: "fable", checkout: "wt", branch: nil, detached: "de2fda1f716")]), attention: [:])
        expect(detached.projects[0].checkouts[0].label == "detached at de2fda1", "detached checkout label")
    }

    static func groupsCheckoutsAndHosts() {
        func onHost(_ id: String, _ host: SessionHost?, checkout: String) -> SessionState {
            var row = session(id, project: "repo:github.com/example/widget", checkout: checkout)
            row.host = host
            return row
        }
        let vps = SessionHost(id: "h1", name: "vps.example.ts.net", state: "connected")
        let laptop = SessionHost(id: "h2", name: "laptop.example.ts.net", state: "connected")
        let project = "repo:github.com/example/widget"
        let spreadRows = [onHost("here", nil, checkout: "checkout-1"), onHost("h1::there", vps, checkout: "h1::checkout-1"), onHost("h2::elsewhere", laptop, checkout: "h2::checkout-1")]
        let spreadView = view(spreadRows.map { family($0.id, project: project, checkout: $0.checkout!.id!) }, projects: [(project, [])])
        let spread = SessionTree.ordered(list: spreadView, current: nil, rows: rows(spreadRows), attention: [:]).projects
        expect(spread.count == 1 && spread[0].checkouts.count == 3, "one repository on three hosts is one project with a checkout per host")
        expect(Set(spread[0].hosts) == ["vps", "laptop"] && spread[0].spansHosts, "the project header names every other host: \(spread[0].hosts)")
        expect(Set(spread[0].checkouts.map { spread[0].hostLabel(of: $0) }) == [nil, "vps", "laptop"], "each checkout names its own host")
        let unnamedRows = [onHost("here", nil, checkout: "x"), onHost("h1::there", vps, checkout: "y")]
        let unnamed = SessionTree.ordered(list: view(unnamedRows.map { family($0.id, project: project) }, projects: [(project, [])]), current: nil, rows: rows(unnamedRows), attention: [:]).projects
        expect(unnamed.first?.checkouts.count == 2, "the same path on two hosts stays two checkouts without a checkout id")
    }

    static func joinsComputers() {
        let mac = view([family("a", project: "repo:x"), family("b", project: "mac-only")], projects: [("repo:x", ["a"]), ("mac-only", ["b"])], counts: ["current": 2])
        let forge = view([family("c", project: "repo:x"), family("d", project: "forge-only")], projects: [("repo:x", ["c"]), ("forge-only", [])], counts: ["current": 2])
        let renamed = forge.renamed { $0.hasPrefix("repo:") || $0.contains("::") ? $0 : "forge::" + $0 }
        expect(renamed.families.map(\.id) == ["forge::c", "forge::d"] && renamed.projects.map(\.id) == ["repo:x", "forge::forge-only"], "a computer's view takes the merged ids; a repository id is shared: \(renamed.projects.map(\.id))")
        let joined = ScopePage.View.joined([mac, renamed])!
        expect(joined.projects.map(\.id) == ["repo:x", "mac-only", "forge::forge-only"], "the first computer's projects come first, others follow in their own order")
        expect(joined.families.map(\.id) == ["a", "b", "forge::c", "forge::d"], "families another computer adds follow the first one's")
        expect(joined.projects[0].collapsedFamilyIds == ["a", "forge::c"] && joined.counts["current"]?.families == 4, "a shared project collapses both; counts add for a computer the first does not mirror")
        let mirror = view([family("a", project: "repo:x"), family("forge::c", project: "repo:x")], projects: [("repo:x", [])], counts: ["current": 2])
        expect(ScopePage.View.joined([mirror, renamed])?.counts["current"]?.families == 2, "a computer the first already mirrors adds no counts")
        let merged = mac.merging(view([family("b", project: "mac-only"), family("e", project: "mac-only")], projects: []))
        expect(merged.families.map(\.id) == ["a", "b", "e"] && merged.projects.count == 2, "a later page adds its new families and keeps the first page's projects")
    }

    static func detectsNews() {
        func event(_ type: String, _ data: String, seq: UInt64 = 1) -> CatalogStreamEvent {
            try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"\#(type)","data":\#(data)}"#.utf8))
        }
        func row(_ id: String, placement: String?) -> String {
            #"{"session":{"id":"\#(id)","surface":"claude","name":"\#(id)","status":"busy","lastActive":"2026-10-04T11:00:00Z","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":true,"steer":true}\#(placement.map { #","placement":"\#($0)""# } ?? "")}}"#
        }
        var held = session("a")
        held.placement = "p1"
        let holding = ["a": held]
        expect(!CatalogNews.moves([event("session.upserted", row("a", placement: "p1"))], held: holding), "a progress write with the same placement is not news")
        expect(CatalogNews.moves([event("session.upserted", row("a", placement: "p2"))], held: holding), "a changed placement is news")
        expect(CatalogNews.moves([event("session.upserted", row("new", placement: "p1"))], held: holding), "a row the list does not hold is news")
        expect(CatalogNews.moves([event("session.upserted", row("a", placement: nil))], held: holding), "a row without a placement is treated as news")
        expect(CatalogNews.moves([event("session.removed", #"{"sessionId":"a","reason":"unlisted"}"#)], held: holding), "removing a held row is news")
        expect(!CatalogNews.moves([event("session.removed", #"{"sessionId":"elsewhere"}"#)], held: holding), "removing a row the list never held is not")
        expect(CatalogNews.moves([event("session.queue", #"{"sessionId":"a","queueCount":1}"#)], held: holding), "a queue change is news")
        expect(CatalogNews.moves([event("delivery.problem", #"{"deliveryId":1,"sessionId":"a"}"#)], held: holding) && CatalogNews.moves([event("coordinator.changed", #"{}"#)], held: holding), "attention and lead changes are news")
        expect(!CatalogNews.moves([event("surface.health", #"{"surface":"claude","health":"healthy"}"#)], held: holding), "a surface's health does not move rows")
    }

    static func holdsOrder() {
        let first = view([family("a", project: "p")], projects: [("p", ["a"])])
        let second = view([family("b", project: "p"), family("a", project: "p")], projects: [("p", ["b", "a"])])
        var hold = OrderHold()
        hold.hold(list: first, current: first, rows: rows([session("a"), session("gone")]))
        let shown = hold.shown(list: second, current: second, rows: rows([session("a", status: "busy")]))
        expect(shown.list == first && shown.rows["a"]?.status == "busy" && shown.rows["gone"] != nil, "a held list keeps its order and removed rows, and takes fresh data for the rows it has")
        hold.release()
        expect(hold.shown(list: second, current: second, rows: [:]).list == second, "a released list takes the newest order")
    }

    static func familiesAndAttention() {
        func member(_ id: String, parent: String, root: String, depth: Int, nickname: String, role: String? = nil, status: String) -> SessionState {
            var state = session(id, status: status)
            state.subagent = SubagentIdentity(parentId: parent, rootId: root, depth: depth, nickname: nickname, role: role)
            return state
        }
        let lead = session("lead", name: "Build the parser")
        let family = [lead, member("ada", parent: "lead", root: "lead", depth: 1, nickname: "Ada", status: "busy"), member("euclid", parent: "ada", root: "lead", depth: 2, nickname: "Euclid", role: "reviewer", status: "busy"), member("bo", parent: "lead", root: "lead", depth: 1, nickname: "Bo", status: "idle")]
        expect(SessionFamilies.title(family[2], in: family) == "Build the parser > Ada > Euclid (reviewer)", "a standalone subagent reads as its parent chain")
        expect(SessionFamilies.label(family[1]) == "Ada", "subagent labels use the nickname")
        let tree = SessionTree.ordered(list: view([Self.family("lead", project: "agenthail", members: [("ada", 1), ("euclid", 2), ("bo", 1)], working: 2)], projects: [("agenthail", [])]), current: nil, rows: rows(family), attention: [:])
        let built = tree.projects[0].checkouts[0].families[0]
        expect(built.members.map(\.id) == ["ada", "euclid", "bo"] && built.members.map(\.depth) == [1, 2, 1], "members and depths come from the view")
        expect(built.workingSubagents == 2 && SessionFamilies.subagentSummary(built) == "2 subagents working", "working subagents come from the view")

        func waiting(_ id: String, kind: String) -> SessionState {
            var state = session(id)
            state.waitingOn = WaitingState(kind: kind, id: "req-\(id)")
            return state
        }
        let asker = waiting("asker", kind: "question")
        let approver = waiting("approver", kind: "approval")
        var oldLead = waiting("old-lead", kind: "question")
        oldLead.replacedLead = [CoordinatorTenure(name: "release-lead", from: "2026-10-04T09:00:00Z", until: "2026-10-04T11:00:00Z")]
        let reasons = SessionAttention.build(sessions: [asker, approver, oldLead], attention: [], deliveryProblems: [])
        expect(reasons["asker"] == SessionAttention(kind: .request, reason: "Asks you a question") && reasons["approver"]?.reason == "Needs your approval", "requests read as what they ask")
        expect(reasons["old-lead"] == SessionAttention(kind: .replacedRequest, reason: "From a replaced lead. Asks you a question"), "a replaced lead's request says so")
        let current = view([Self.family("asker", project: "agenthail"), Self.family("old-lead", project: "agenthail"), Self.family("missing", project: "agenthail"), Self.family("approver", project: "agenthail")], projects: [("agenthail", [])])
        let needs = SessionTree.needsYou(in: current, rows: rows([asker, approver, oldLead]), attention: reasons.merging(["missing": SessionAttention(kind: .request, reason: "x")]) { $1 })
        expect(needs.map(\.id) == ["asker", "approver"], "Needs you follows the daemon's order, without a replaced lead's request or a row the client lacks: \(needs.map(\.id))")

        var terminal = waiting("terminal", kind: "approval")
        terminal.runtimeKind = "claude-cli"
        var limited = session("limited")
        limited.usageLimit = UsageLimitState(resetsAt: "2026-10-04T18:20:00Z")
        let places = SessionAttention.build(sessions: [terminal, limited], attention: [], deliveryProblems: [])
        expect(places["terminal"]?.reason == "Waiting in its terminal", "a terminal prompt Agenthail does not hold is answered in its terminal")
        let resets = TimestampParser.date("2026-10-04T18:20:00Z")!.formatted(date: .omitted, time: .shortened)
        expect(places["limited"] == SessionAttention(kind: .usageLimit, reason: "Usage limit, resets \(resets)"), "a usage limit names when it resets")
        expect(SessionRefinement.surfaceLabel("codex") == "Codex", "surface labels are capitalized")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
