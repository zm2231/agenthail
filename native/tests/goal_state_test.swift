@testable import AgenthailHelpers
import Foundation

@main
struct GoalStateTest {
    static func main() throws {
        let blocked = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"tests pass","status":"blocked","timeUsedSeconds":3,"tokensUsed":90,"tokenBudget":null,"createdAt":"2026-10-20T16:07:30Z","updatedAt":"2026-10-20T16:08:30Z","reason":"the test binary cannot be built here"}"#.utf8))
        check(blocked.reason == "the test binary cannot be built here" && blocked.needsAttention && blocked.displayStatus == "Needs you", "an impossible Claude goal reads as needing you, with its reason")

        let complete = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"tests pass","status":"complete","timeUsedSeconds":60,"tokensUsed":835,"tokenBudget":null,"createdAt":"2026-10-20T16:07:30Z","updatedAt":"2026-10-20T16:08:30Z","reason":""}"#.utf8))
        check(complete.reason == nil && !complete.needsAttention && complete.displayStatus == "Complete", "an empty reason is no reason")

        let codex = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"Ship","status":"active","timeUsedSeconds":0,"tokensUsed":0,"tokenBudget":500,"createdAt":"2026-10-20T16:07:30Z","updatedAt":"2026-10-20T16:07:30Z"}"#.utf8))
        check(codex.reason == nil && codex.tokenBudget == 500, "a goal without a reason still decodes")

        let claude = try JSONDecoder().decode(Capabilities.self, from: Data(#"{"send":true,"stream":true,"reply":true,"goal":false,"goalSet":true,"goalStatus":true,"compact":true,"model":true,"interrupt":true,"steer":true}"#.utf8))
        check(claude.goalSet && !claude.goal, "a Claude session sets and clears its goal but has no pause or budget")

        let peer = try JSONDecoder().decode(Capabilities.self, from: Data(#"{"send":true,"stream":true,"reply":true,"goal":false,"goalStatus":true,"compact":false,"model":false,"interrupt":false,"steer":false}"#.utf8))
        check(!peer.goalSet && !peer.goal, "a session that only reports its goal offers no goal controls")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
