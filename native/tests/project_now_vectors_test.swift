@testable import AgenthailHelpers
import Foundation

@main
struct ProjectNowVectorsTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/project-now-vectors.json")

    static func main() throws {
        let vectors = try JSONSerialization.jsonObject(with: Data(contentsOf: fixture)) as! [String: Any]
        let base = vectors["base"] as! [String: Any]
        var failures: [String] = []
        for vector in vectors["cases"] as! [[String: Any]] {
            let name = vector["name"] as! String
            let rows = (vector["sessions"] as! [[String: Any]]).map { base.merging($0) { $1 } }
            let sessions = try JSONDecoder().decode([SessionState].self, from: JSONSerialization.data(withJSONObject: rows))
            let now = ProjectNow(sessions, computers: vector["computers"] as! Int)
            if now.showsGroups != vector["showsGroups"] as! Bool { failures.append("\(name): showsGroups is \(now.showsGroups)") }
            if now.count != sessions.count { failures.append("\(name): counts \(now.count) of \(sessions.count)") }
            let want = (vector["groups"] as! [[String: Any]]).map { "\($0["id"]!)|\($0["title"]!)|\(($0["sessions"] as! [String]).joined(separator: ","))" }
            let got = now.groups.map { "\($0.id)|\($0.title)|\($0.sessions.map(\.id).joined(separator: ","))" }
            if got != want { failures.append("\(name):\n  Swift says \(got)\n  vectors say \(want)") }
        }
        guard failures.isEmpty else {
            print(failures.joined(separator: "\n"))
            exit(1)
        }
        print("ok")
    }
}
