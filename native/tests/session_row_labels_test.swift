@testable import AgenthailHelpers
import Foundation

@main
struct SessionRowLabelsTest {
    static func main() {
        func session(name: String = "builder", alias: String? = nil, status: String = "idle", queued: Int = 0) -> SessionState {
            SessionState(id: "s1", surface: "claude", name: name, alias: alias, status: status, lastActive: nil, queueCount: queued, open: true, current: false, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        }

        expect(SessionNames.display("https://github.com/acme/widgets/pull/12 fix the build") == "acme / widgets", "a repository link reads as owner / repo, as on the dashboard")
        expect(SessionNames.display("http://git.example.com/team/site") == "team / site", "any host and plain http count")
        expect(SessionNames.display("https://github.com/acme") == "https://github.com/acme", "a link without a repository stays as it is")
        expect(SessionNames.display("fix https://github.com/acme/widgets") == "fix https://github.com/acme/widgets", "only a name that starts with the link changes")
        expect(SessionNames.display("Write the install guide") == "Write the install guide", "a plain name stays")
        expect(session(name: "https://github.com/acme/widgets/issues/4").title == "acme / widgets", "the session title uses the short name")
        expect(session(name: "https://github.com/acme/widgets/issues/4").displayName == "acme / widgets", "the iPhone's name for a session uses the short name too")
        expect(session(name: "").displayName == "s1", "a session without a name still falls back to its id")

        expect(SessionRowNote.text(session()) == nil, "an idle row has one line")
        expect(SessionRowNote.text(session(queued: 2)) == nil, "the queued count already shows on the row")
        expect(SessionRowNote.text(session(status: "busy")) == nil, "a working row with nothing to add has one line")

        var answerable = session(status: "busy")
        answerable.waitingOn = WaitingState(kind: "question", id: "q1", answerable: true)
        expect(SessionRowNote.text(answerable) == nil, "a wait answered here is already marked by the dot and highlight")
        var terminal = session(status: "busy")
        terminal.waitingOn = WaitingState(kind: "question", id: "q1")
        terminal.runtimeKind = "claude-cli"
        expect(SessionRowNote.text(terminal) == "Waiting in its terminal", "the row says where a wait has to be answered")

        var limited = session()
        limited.limitResume = LimitResumeState(state: "waiting")
        expect(SessionRowNote.text(limited) == "Usage limit, resumes when it lifts", "a usage limit says when work resumes")
        var stopped = session()
        stopped.turnStop = TurnStopState(turnId: "t1", reason: "the model returned an error")
        expect(SessionRowNote.text(stopped) == "Stopped: the model returned an error", "a stopped turn shows its reason")
        stopped.turnStop?.refused = true
        expect(SessionRowNote.text(stopped) == "Stopped: Safeguards refused this conversation; start a new one", "a refused conversation says to start a new one")
        var background = session()
        background.backgroundTasks = BackgroundTasks(running: true, tasks: [], source: "host")
        expect(SessionRowNote.text(background) == "Waiting on background work", "background work between turns shows")

        var down = session()
        down.host = SessionHost(id: "h1", name: "server", state: "unreachable")
        expect(SessionRowNote.text(down) == nil, "unavailable without a reason adds nothing to the dot")
        down.unavailableReason = "host server is unreachable"
        expect(SessionRowNote.text(down) == "host server is unreachable", "unavailable shows why")

        var aliased = session(name: "Rework the search index", alias: "search-worker")
        expect(SessionFacts.headerDetail(aliased) == "Rework the search index", "an aliased session's header names what it is working on: \(String(describing: SessionFacts.headerDetail(aliased)))")
        aliased.project = ProjectIdentity(id: "p1", displayName: "widgets", rootPath: "/src/widgets", kind: "repo")
        expect(SessionFacts.headerDetail(aliased) == "Rework the search index · widgets", "and its project after it")
        for name in ["search-worker", "s1", ""] {
            var plain = session(name: name, alias: "search-worker")
            plain.project = aliased.project
            expect(SessionFacts.headerDetail(plain) == "widgets", "a name that repeats the alias, an id or no name is left out: \(name)")
        }
        expect(SessionFacts.loadFailure("not found") == "Agenthail hasn't found its history on this computer. Trying again.", "a missing session reads in plain words")
        expect(SessionFacts.loadFailure("the daemon is not running.") == "The daemon is not running. Trying again.", "another reason keeps its words: \(SessionFacts.loadFailure("the daemon is not running."))")
        expect(SessionFacts.loadFailure(" ") == "Agenthail couldn't read it. Trying again.", "no reason still says what happens")
        expect(SessionFacts.headerDetail(session()) == nil, "a session titled by its name, outside a project, has no second line")
        print("ok")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
