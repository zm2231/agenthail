@testable import AgenthailHelpers
import Foundation

@main
struct ComposerCompletionTest {
    static func main() {
        tokens()
        commands()
        mentions()
        insertion()
        scoping()
        print("ok")
    }

    static func tokens() {
        check(CompletionToken.at("/rev", caret: 4) == CompletionToken(trigger: .command, query: "rev", range: NSRange(location: 0, length: 4), atStart: true), "a slash at the start opens commands with the typed name")
        let mid = CompletionToken.at("please @src/ma now", caret: 14)
        check(mid?.trigger == .mention && mid?.query == "src/ma" && mid?.range == NSRange(location: 7, length: 7) && mid?.atStart == false, "an @ after a space completes the word under the caret")
        let inside = CompletionToken.at("@src/main.go", caret: 4)
        check(inside?.query == "src" && inside?.range == NSRange(location: 0, length: 12), "the caret inside a word completes from its start and replaces the whole word")
        check(CompletionToken.at("mail me@example.com", caret: 19) == nil, "an @ inside a word opens nothing")
        check(CompletionToken.at("/review ", caret: 8) == nil, "after the word's space the menu closes")
        check(CompletionToken.at("  $no", caret: 5)?.atStart == true, "leading spaces still count as the start")
        check(CompletionToken.at("🙂 /x", caret: 4)?.range == NSRange(location: 3, length: 2), "ranges count UTF-16, as the text view does")
        check(CompletionToken.at("", caret: 0) == nil && CompletionToken.at("x", caret: 9) == nil, "an empty text or a caret past the end opens nothing")
    }

    static func commands() {
        let app = [CompletionItem(id: "/compact", title: "/compact", detail: "Compact", insertion: "/compact"), CompletionItem(id: "/model", title: "/model", detail: "Model", insertion: "/model ")]
        let claude = RuntimeCommands(commands: [RuntimeCommand(name: "review", description: "Review a PR", argumentHint: "[pr]", insert: "/review"), RuntimeCommand(name: "compact", description: "Claude's compact", argumentHint: "", insert: "/compact")], unavailable: "")
        let start = ComposerCompletion.items(for: CompletionToken.at("/", caret: 1)!, appCommands: app, runtime: claude, sessions: [], files: [])
        check(start.map(\.insertion) == ["/compact", "/model ", "/review", "/compact"], "at the start the app's commands come first, then the session's")
        check(start[2].title == "/review [pr]" && start[2].detail == "Review a PR", "a runtime command shows its argument hint and description")
        let mid = ComposerCompletion.items(for: CompletionToken.at("then /re", caret: 8)!, appCommands: app, runtime: claude, sessions: [], files: [])
        check(mid.map(\.insertion) == ["/review"], "mid-message only the session's commands are offered")
        let filtered = ComposerCompletion.items(for: CompletionToken.at("/pa", caret: 3)!, appCommands: app, runtime: claude, sessions: [], files: [])
        check(filtered.map(\.insertion) == ["/compact", "/compact"], "names containing the query follow names starting with it")

        let codex = RuntimeCommands(commands: [RuntimeCommand(name: "notes", description: "Take notes", argumentHint: "", insert: "$notes")], unavailable: "")
        check(ComposerCompletion.items(for: CompletionToken.at("/", caret: 1)!, appCommands: [], runtime: codex, sessions: [], files: []).isEmpty, "a Codex skill never shows under /")
        check(ComposerCompletion.items(for: CompletionToken.at("$n", caret: 2)!, appCommands: app, runtime: codex, sessions: [], files: []).map(\.insertion) == ["$notes"], "$ lists only skills and no app commands")

        let peerOnly = RuntimeCommands(commands: [], unavailable: "Commands run only when this session has Remote Control.")
        check(ComposerCompletion.notice(for: CompletionToken.at("/", caret: 1)!, runtime: peerOnly) == peerOnly.unavailable, "a / says why the session offers no commands")
        check(ComposerCompletion.notice(for: CompletionToken.at("@", caret: 1)!, runtime: peerOnly) == nil, "the reason is only for /")
    }

    static func mentions() {
        let current = session("hsrv::a", alias: nil)
        let all = [current, session("hsrv::b", alias: "worker"), session("hsrv::c", alias: nil), session("hsrv::f", alias: nil, open: false), session("other::d", alias: "elsewhere"), session("e", alias: "local")]
        let named = ComposerCompletion.sessions(all, from: current)
        check(named.map(\.insertion) == ["@worker", "@c"], "@ names open or named sessions on the same computer by alias or that computer's id, never the current one")
        check(named[0].title == "@worker" && named[0].detail == "claude", "a session reads as the handle it inserts")
        let crowd = (0..<20).map { session("hsrv::w\($0)", alias: "worker\($0)") }
        check(ComposerCompletion.items(for: CompletionToken.at("@work", caret: 5)!, appCommands: [], runtime: nil, sessions: ComposerCompletion.sessions(crowd, from: current), files: []).count == ComposerCompletion.mentionedSessionLimit, "a few sessions show, not every match")
        let token = CompletionToken.at("@wor", caret: 4)!
        let files = [SessionFile(path: "work.go", dir: false), SessionFile(path: "workers", dir: true)]
        let items = ComposerCompletion.items(for: token, appCommands: [], runtime: nil, sessions: named, files: files)
        check(items.map(\.insertion) == ["@worker", "@work.go", "@workers/"], "matching sessions come before the folder's files")
        check(items[2].title == "workers/" && items[2].detail == "Folder", "a folder reads as one")
    }

    static func insertion() {
        let text = "look at @sr please"
        let token = CompletionToken.at(text, caret: 11)!
        let file = ComposerCompletion.apply(CompletionItem(id: "f", title: "", detail: "", insertion: "@src/main.go"), to: text, at: token)
        check(file.text == "look at @src/main.go please" && file.caret == 21, "a file replaces the word and the caret steps past the space already there")
        let last = ComposerCompletion.apply(CompletionItem(id: "f", title: "", detail: "", insertion: "@src/main.go"), to: "see @sr", at: CompletionToken.at("see @sr", caret: 7)!)
        check(last.text == "see @src/main.go " && last.caret == 17, "a word at the end gets its own space")
        let folder = ComposerCompletion.apply(CompletionItem(id: "d", title: "", detail: "", insertion: "@src/"), to: "@s", at: CompletionToken.at("@s", caret: 2)!)
        check(folder.text == "@src/" && folder.caret == 5, "a folder leaves the word open")
        let model = ComposerCompletion.apply(CompletionItem(id: "m", title: "", detail: "", insertion: "/model "), to: "/mo", at: CompletionToken.at("/mo", caret: 3)!)
        check(model.text == "/model ", "an insertion that ends in a space gets no second one")
    }

    static func scoping() {
        var a = CompletionState(session: "hsrv::a", caret: 3)
        a.dismissed = "/x"
        a.highlighted = 2
        a.files = ("", [SessionFile(path: "a.go", dir: false)])
        check(a.scoped(to: "hsrv::a", draft: "/x").caret == 3 && a.scoped(to: "hsrv::a", draft: "/x").files?.files.count == 1, "the same session keeps its caret and files")
        let b = a.scoped(to: "hsrv::b", draft: "hi 🙂")
        check(b.session == "hsrv::b" && b.caret == 5 && b.dismissed == nil && b.highlighted == 0 && b.files == nil, "another session sees none of the first one's files, dismissal or highlight, and its caret is at the end of its draft")
    }

    static func session(_ id: String, alias: String?, open: Bool = true) -> SessionState {
        SessionState(id: id, surface: "claude", name: "", alias: alias, status: "idle", lastActive: nil, queueCount: 0, open: open, current: false, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil)
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
