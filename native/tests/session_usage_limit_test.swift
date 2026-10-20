@testable import AgenthailHelpers
import Foundation

@main
struct SessionUsageLimitTest {
    static func main() {
        let json = #"{"message":"You've hit your session limit","resetsAt":"2026-10-21T11:00:00Z","account":{"name":"default","root":"/h/.claude","email":"me@example.com","label":"me@example.com"},"alternatives":[{"name":"spare","root":"/h/.spare","label":"spare@example.com","primary":false,"signedIn":true}]}"#
        guard let limit = try? JSONDecoder().decode(SessionUsageLimit.self, from: Data(json.utf8)) else {
            fail("the usage limit offer did not decode")
        }
        expect(limit.resetsDate == ISO8601DateFormatter().date(from: "2026-10-21T11:00:00Z"), "the reset time parses")
        expect(limit.summary.hasPrefix("me@example.com is out of usage.") && limit.summary.contains("It resets"), "the summary names the account and the reset")
        expect(limit.alternatives?.first?.root == "/h/.spare", "the alternative account decodes")
        let bare = try? JSONDecoder().decode(SessionUsageLimit.self, from: Data(#"{"message":"limit"}"#.utf8))
        expect(bare?.summary == "This Claude account is out of usage.", "an offer without account or reset still reads")
        let streamed = try? JSONDecoder().decode(SessionStreamItem.self, from: Data(#"{"itemId":"n","version":1,"op":"upsert","kind":"notice","ts":"t","status":"usage_limit","resetsAt":"2026-10-21T11:00:00Z","truncated":false}"#.utf8))
        expect(streamed?.resetsAt == "2026-10-21T11:00:00Z", "a streamed notice keeps its reset time")
        let item = try? JSONDecoder().decode(TimelineItem.self, from: Data(#"{"id":"n","kind":"notice","title":"Usage limit reached","text":"limit","status":"usage_limit","resetsAt":"2026-10-21T11:00:00Z","truncated":false}"#.utf8))
        expect(item?.resetsAt == "2026-10-21T11:00:00Z", "a timeline notice keeps its reset time")
        print("session usage limit tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition { fail(message) }
    }

    static func fail(_ message: String) -> Never {
        FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
        exit(1)
    }
}
