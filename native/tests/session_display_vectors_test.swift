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
            let kind = SessionDisplayKind(rawValue: want)!
            if !SessionRowTime.status(session, now: now).hasPrefix(kind.label) {
                failures.append("\(name): the row says \(SessionRowTime.status(session, now: now)), not \(want)")
            }
            let header = SessionDisplayKind.label(session, now: now)
            let agrees = switch kind {
            case .unavailable, .queued, .offline: header == kind.label
            case .working: header.hasPrefix("Working")
            case .waiting: header == (session.waitingPlace?.label ?? "Waiting on you")
            case .limited: header == (session.limitLabel ?? "Usage limit")
            case .finishing: header == "Waiting on background work"
            case .idle: header == "Idle" || header == "Closed"
            }
            if !agrees { failures.append("\(name): the header says \(header), not \(want)") }
            if kind != .idle && kind != .queued && SessionFacts.presenceLabel(session) != SessionDisplayKind.label(session) {
                failures.append("\(name): the row detail says \(SessionFacts.presenceLabel(session)), not \(want)")
            }
            let patch = vector["patch"] as! [String: Any]
            if patch["host"] == nil, patch["queueCount"] == nil {
                let detailFields: [String: Any] = ["session": fields, "exchanges": [], "capabilities": fields["capabilities"]!, "readOnly": false, "readOnlyReason": "", "limitResume": fields["limitResume"] ?? NSNull()]
                let detail = try JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: detailFields))
                let fromDetail = SessionDisplayKind.of(detail.sessionState(open: true), now: now)
                if fromDetail.rawValue != want { failures.append("\(name): a session known only by its detail says \(fromDetail.rawValue), not \(want)") }
            }
        }
        guard failures.isEmpty else {
            failures.forEach { print($0) }
            exit(1)
        }
        print("ok   session_display_vectors_test")
    }
}
