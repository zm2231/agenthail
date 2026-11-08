@testable import AgenthailHelpers
import Foundation

@main
struct SessionRowLabelsTest {
    static func main() {
        func session(name: String = "builder", status: String = "idle", queued: Int = 0) -> SessionState {
            SessionState(id: "s1", surface: "claude", name: name, alias: nil, status: status, lastActive: nil, queueCount: queued, open: true, current: false, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        }

        expect(SessionNames.display("https://github.com/acme/widgets/pull/12 fix the build") == "acme / widgets", "a repository link reads as owner / repo, as on the dashboard")
        expect(SessionNames.display("http://git.example.com/team/site") == "team / site", "any host and plain http count")
        expect(SessionNames.display("https://github.com/acme") == "https://github.com/acme", "a link without a repository stays as it is")
        expect(SessionNames.display("fix https://github.com/acme/widgets") == "fix https://github.com/acme/widgets", "only a name that starts with the link changes")
        expect(SessionNames.display("Write the install guide") == "Write the install guide", "a plain name stays")
        expect(session(name: "https://github.com/acme/widgets/issues/4").title == "acme / widgets", "the session title uses the short name")

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

        expect(SessionFacts.configFolderLabel(nil) == nil, "a session without a config folder shows none")
        expect(SessionFacts.configFolderLabel(SessionConfigRoot(name: "default", root: "/home/a/.claude")) == nil, "the default folder without a login says nothing")
        expect(SessionFacts.configFolderLabel(SessionConfigRoot(name: "default", root: "/home/a/.claude", signedInAs: "dev@example.com")) == "config folder default · currently signed in as dev@example.com", "a known login shows even for the default folder")
        expect(SessionFacts.configFolderLabel(SessionConfigRoot(name: "team", root: "/home/a/.team/.claude")) == "config folder team", "another folder is named")
        print("ok")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
