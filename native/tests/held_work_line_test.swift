@testable import AgenthailHelpers
import Foundation

@main
struct HeldWorkLineTest {
    static func row(_ heldWork: String?) throws -> SessionState {
        var object: [String: Any] = ["id": "s1", "surface": "claude", "name": "s1", "status": "idle", "lastActive": "2026-11-22T01:00:00Z", "queueCount": 0, "open": true, "current": true,
                                     "capabilities": ["send": true, "stream": true, "reply": true, "goal": false, "compact": false, "model": false, "interrupt": false, "steer": false, "respond": true]]
        if let heldWork { object["heldWork"] = try JSONSerialization.jsonObject(with: Data(heldWork.utf8)) }
        return try JSONDecoder().decode(SessionState.self, from: JSONSerialization.data(withJSONObject: object))
    }

    static func main() throws {
        let bare = try row(nil)
        check(bare.heldWork == nil && HeldWorkLine.of(bare) == nil, "a row without heldWork decodes and shows no line")
        check(HeldWorkLine.of(try row("[]")) == nil, "an empty list shows no line")

        let logged = try row(#"[{"itemId":"app-W7","text":"fix the login","state":"in_progress","projectKey":"repo:github.com/acme/app","log":{"type":"decision","text":"kept the old token","at":"2026-11-22T01:02:03Z"}}]"#)
        check(HeldWorkLine.of(logged) == HeldWorkLine(id: "app-W7", text: "decision: kept the old token", help: "app-W7 fix the login\ndecision: kept the old token"), "the line is the item and its newest log line: \(String(describing: HeldWorkLine.of(logged)))")
        check(HeldWorkLine.of(logged)?.spoken == "Holds app-W7: decision: kept the old token", "the line reads aloud with what the session holds")

        let several = try row(#"[{"itemId":"app-W2","text":"tidy the build","state":"blocked"},{"itemId":"app-W7","text":"fix the login","state":"in_review","log":{"type":"result","text":"merged"}},{"itemId":"app-W9","text":"docs","state":"in_progress"}]"#)
        check(HeldWorkLine.of(several) == HeldWorkLine(id: "app-W2 +2", text: "tidy the build", help: "app-W2 tidy the build"), "without a log the title shows, and more items read +N: \(String(describing: HeldWorkLine.of(several)))")

        let encoded = try JSONDecoder().decode(SessionState.self, from: JSONEncoder().encode(logged))
        check(encoded == logged && encoded.heldWork?.first?.log?.at == "2026-11-22T01:02:03Z", "held work survives a round trip")
        var moved = logged
        moved.heldWork = [HeldWork(itemId: "app-W7", text: "fix the login", state: "in_progress", log: HeldWorkLog(type: "progress", text: "tests pass", at: nil))]
        check(moved != logged, "a new log line makes the row unequal, so it redraws")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
