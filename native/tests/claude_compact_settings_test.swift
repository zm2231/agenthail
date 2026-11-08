@testable import AgenthailHelpers
import Foundation
import Network

@main
@MainActor
struct ClaudeCompactSettingsTest {
    static let settings = #"{"roots":[{"root":"/u/.claude","settingsPath":"/u/.claude/settings.json","autoCompactWindow":{"tokens":400000},"modelWindows":{"claude-opus-5-5":{"auto":true},"claude-haiku-5-5":{"ignored":"5000"}},"autoCompactEnabled":true,"agenthailIdleCompaction":{"enabled":true,"minContextTokens":200000},"overrides":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"300000"}},{"root":"/u/.claude-work","settingsPath":"/u/.claude-work/settings.json","idleCompaction":false,"agenthailIdleCompaction":{"enabled":false,"minContextTokens":200000}}],"idleCompactionHolds":[{"sessionId":"s1","session":"@lead","root":"/u/.claude","requestAt":"2026-11-08T11:00:00Z","contextTokens":250000,"reason":"the account is at its limit","at":"2026-11-08T11:00:00.123456789Z"},{"sessionId":"s2","session":"","root":"/u/.claude","requestAt":"2026-11-08T11:30:00Z","contextTokens":210000,"reason":"busy","at":"2026-11-08T11:30:00Z"},{"sessionId":"s3","session":"@w","root":"/u/.claude-work","requestAt":"2026-11-08T11:40:00Z","contextTokens":1,"reason":"x","at":"2026-11-08T11:40:00Z"}]}"#

    static func main() async throws {
        let response = try JSONDecoder().decode(ClaudeCompactResponse.self, from: Data(settings.utf8))
        let main = response.roots[0], work = response.roots[1]
        let holds = response.idleCompactionHolds ?? []

        check(ClaudeCompactFacts.window(main.autoCompactWindow) == "\(Int64(400000).formatted()) tokens", "a window reads in tokens")
        check(ClaudeCompactFacts.window(nil) == "Claude's default", "no window is Claude's default")
        check(ClaudeCompactFacts.modelLines(main) == ["claude-haiku-5-5: 5000 (Claude ignores it)", "claude-opus-5-5: auto"], "per-model windows list in order: \(ClaudeCompactFacts.modelLines(main))")
        check(ClaudeCompactFacts.toggle(main.autoCompactEnabled) == "on" && ClaudeCompactFacts.toggle(work.idleCompaction) == "off" && ClaudeCompactFacts.toggle(main.idleCompaction) == "not set", "toggles read on, off and not set")
        check(ClaudeCompactFacts.agenthailIdle(main.agenthailIdleCompaction) == "on, from \(Int64(200000).formatted()) tokens" && ClaudeCompactFacts.agenthailIdle(work.agenthailIdleCompaction) == "off", "Agenthail idle shows its minimum when on")
        let now = TurnClock.date("2026-11-08T12:00:00Z")!
        let lines = ClaudeCompactFacts.holdLines(main, holds: holds, now: now)
        check(lines.count == 2 && lines[0].hasPrefix("Not compacted: s2 at \(Int64(210000).formatted()) tokens, ") && lines[0].hasSuffix(": busy") && lines[1].contains("@lead"), "holds are the folder's own, newest first, named by session: \(lines)")
        check(ClaudeCompactFacts.overrideLines(main) == ["CLAUDE_CODE_AUTO_COMPACT_WINDOW=300000 is set where Agenthail runs and takes precedence over this file."], "env overrides are named")

        var draft = ClaudeCompactDraft(root: "/u/.claude")
        check(!draft.changes && draft.problem == nil, "an untouched draft has nothing to save")
        draft.model = "claude-opus-5-5"
        check(draft.problem != nil, "a model without a window is refused")
        draft.window = " auto "
        draft.enabled = false
        draft.minTokens = "12x"
        check(draft.problem == "From context size must be a whole number of tokens.", "a bad minimum is refused")
        draft.minTokens = "150000"
        let body = draft.body
        check(draft.problem == nil && draft.changes && body["window"] as? String == "auto" && body["model"] as? String == "claude-opus-5-5" && body["enabled"] as? Bool == false && body["idleCompaction"] == nil && body["agenthailIdleMinTokens"] as? Int64 == 150000, "the body sends only what changed: \(body)")

        try await saves()
        print("ok")
    }

    static func saves() async throws {
        var posted: [String] = []
        let server = try StubServer { request in
            if request.hasPrefix("POST /api/v1/claude/compact-settings") {
                posted.append(request)
                if request.contains("\"window\":\"huge\"") {
                    return reply("400 Bad Request", #"{"error":{"code":"invalid_request","message":"autoCompactWindow must be a token count"}}"#)
                }
                return reply("200 OK", #"{"root":"/u/.claude-work","settingsPath":"/u/.claude-work/settings.json","agenthailIdleCompaction":{"enabled":true,"minContextTokens":200000}}"#)
            }
            return reply("200 OK", settings)
        }
        let port = await server.ready()
        let api = AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(port)")!, token: "t", session: URLSession(configuration: .ephemeral))
        let store = ClaudeCompactStore()
        await store.load(api)
        check(store.roots?.count == 2 && store.holds.count == 3 && store.draft.root == "/u/.claude", "loading picks the first folder")

        store.draft.root = "/u/.claude-work"
        store.draft.agenthailIdle = true
        await store.save(api)
        check(posted.count == 1 && (posted[0].contains(#""root":"\/u\/.claude-work""#) || posted[0].contains(#""root":"/u/.claude-work""#)),"saving posts to the chosen folder: \(posted.last ?? "")")
        check(posted[0].contains("\"agenthailIdleCompaction\":true") && !posted[0].contains("\"enabled\""), "saving sends only the changed setting")
        check(store.notice == "Saved." && store.saveError == nil && store.draft == ClaudeCompactDraft(root: "/u/.claude-work"), "a save clears the form but keeps the folder")

        store.draft.window = "huge"
        await store.save(api)
        check(store.saveError?.contains("autoCompactWindow must be a token count") == true && store.notice == nil && store.draft.window == "huge", "a refused save says why and keeps the form: \(store.saveError ?? "nil")")

        let untouched = posted.count
        store.draft = ClaudeCompactDraft(root: "/u/.claude-work")
        await store.save(api)
        check(posted.count == untouched, "nothing to change sends nothing")
        server.stop()
    }

    static func reply(_ status: String, _ body: String) -> String {
        "HTTP/1.1 \(status)\r\nContent-Type: application/json\r\nContent-Length: \(body.utf8.count)\r\nConnection: close\r\n\r\n\(body)"
    }

    final class StubServer: @unchecked Sendable {
        private let listener: NWListener
        private var connections: [NWConnection] = []

        init(respond: @escaping (String) -> String) throws {
            listener = try NWListener(using: .tcp, on: .any)
            listener.newConnectionHandler = { [self] connection in
                connections.append(connection)
                connection.start(queue: .main)
                read(connection, Data()) { request in
                    connection.send(content: Data(respond(request).utf8), completion: .contentProcessed { _ in })
                }
            }
        }

        private func read(_ connection: NWConnection, _ received: Data, then handle: @escaping (String) -> Void) {
            connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [self] data, _, complete, error in
                let all = received + (data ?? Data())
                let text = String(decoding: all, as: UTF8.self)
                if let end = text.range(of: "\r\n\r\n") {
                    let length = text[..<end.lowerBound].components(separatedBy: "\r\n")
                        .first { $0.lowercased().hasPrefix("content-length:") }
                        .flatMap { Int($0.split(separator: ":")[1].trimmingCharacters(in: .whitespaces)) } ?? 0
                    if text[end.upperBound...].utf8.count >= length || complete || error != nil { return handle(text) }
                } else if complete || error != nil {
                    return handle(text)
                }
                read(connection, all, then: handle)
            }
        }

        func ready() async -> UInt16 {
            await withCheckedContinuation { continuation in
                listener.stateUpdateHandler = { [listener] state in
                    if case .ready = state { continuation.resume(returning: listener.port!.rawValue) }
                }
                listener.start(queue: .main)
            }
        }

        func stop() {
            connections.forEach { $0.cancel() }
            listener.cancel()
        }
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
