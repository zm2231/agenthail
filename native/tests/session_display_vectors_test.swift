@testable import AgenthailHelpers
import Foundation

@main
struct SessionDisplayVectorsTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/session-display-vectors.json")

    static func main() throws {
        let vectors = try JSONSerialization.jsonObject(with: Data(contentsOf: fixture)) as! [String: Any]
        let now = TimestampParser.date(vectors["now"] as! String)!
        let base = vectors["base"] as! [String: Any]
        var failures: [String] = []
        for vector in vectors["cases"] as! [[String: Any]] {
            let name = vector["name"] as! String
            let fields = base.merging(vector["patch"] as! [String: Any]) { $1 }
            let session = try JSONDecoder().decode(SessionState.self, from: JSONSerialization.data(withJSONObject: fields))
            let want = vector["kind"] as! String
            let got = SessionDisplayKind.of(session, now: now)
            if got.rawValue != want { failures.append("\(name): Swift says \(got.rawValue), vectors say \(want)") }
            if !SessionStatusFilter.allCases.filter({ $0.matches(session, now: now) }).elementsEqual([got]) {
                failures.append("\(name): the status filter does not pick exactly its kind")
            }
            if !SessionRowTime.status(session, now: now).hasPrefix(SessionDisplayKind(rawValue: want)!.label) {
                failures.append("\(name): the row says \(SessionRowTime.status(session, now: now)), not \(want)")
            }
        }
        guard failures.isEmpty else {
            failures.forEach { print($0) }
            exit(1)
        }
        print("ok   session_display_vectors_test")
    }
}
