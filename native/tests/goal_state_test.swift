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

        check(GoalFacts.attention(blocked) == "Goal needs you: Blocked. Review it in the inspector.", "a blocked goal raises the transcript alert with its cause")
        let limited = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"x","status":"budgetLimited"}"#.utf8))
        check(GoalFacts.attention(limited)?.contains("Budget limited") == true, "a budget-limited goal names the budget")
        let usageLimited = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"x","status":"usageLimited"}"#.utf8))
        check(GoalFacts.attention(usageLimited) == "Goal needs you: Usage limited. Review it in the inspector.", "a usage-limited goal names the usage limit")
        let paused = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"x","status":"paused"}"#.utf8))
        check(GoalFacts.attention(paused) == nil, "a paused goal doesn't raise the alert")
        check(GoalFacts.attention(complete) == nil && GoalFacts.attention(nil) == nil, "a finished goal or no goal raises nothing")
        check(GoalFacts.duration(3) == "3s" && GoalFacts.duration(125) == "2m 5s" && GoalFacts.duration(3_725) == "1h 2m", "elapsed time keeps its seconds under an hour")
        check(GoalFacts.usage(complete) == "1m 0s elapsed · \(835.formatted(.number.notation(.compactName))) tokens", "usage reads elapsed then tokens: \(GoalFacts.usage(complete))")
        let times = GoalFacts.times(complete)
        check(times.hasPrefix("Created ") && times.contains(", updated "), "a goal shows when it was created and updated: \(times)")
        let undated = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"x","status":"active"}"#.utf8))
        check(GoalFacts.times(undated).isEmpty, "a goal without times shows none")

        let run = try JSONDecoder().decode(ClaudeRunObservation.self, from: Data(#"{"recordPath":"/runs/job-1.json","jobId":"job-1","sessionId":"s1","resumeSessionId":"s0","runType":"bg","providerState":"working","createdAt":"2026-10-04T00:00:00Z","updatedAt":"2026-10-04T00:01:00Z"}"#.utf8))
        let facts = ClaudeRunFacts.lines(run)
        check(facts.first == "Type: bg" && facts.contains("Provider state: working") && facts.contains("Session: s1") && facts.contains("Resume session: s0") && facts.contains("Record: /runs/job-1.json") && facts.filter { $0.hasPrefix("Created ") || $0.hasPrefix("Updated ") }.count == 2, "an observed run shows every field the dashboard does: \(facts)")
        let bare = try JSONDecoder().decode(ClaudeRunObservation.self, from: Data(#"{"recordPath":"","jobId":"job-2","runType":""}"#.utf8))
        check(ClaudeRunFacts.lines(bare).isEmpty, "a run with nothing recorded shows no empty fields: \(ClaudeRunFacts.lines(bare))")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
