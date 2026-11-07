import XCTest
@testable import Agenthail

final class GoalControlsTests: XCTestCase {
    private func detail(goal: [String: Any]?, goalStatus: Bool, readOnly: Bool = false, goalSet: Bool = true) throws -> SessionDetail {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as? [String: Any])
        var capabilities = try XCTUnwrap(object["capabilities"] as? [String: Any])
        capabilities["goal"] = goalStatus
        capabilities["goalSet"] = goalSet
        object["capabilities"] = capabilities
        object["goal"] = goal ?? NSNull()
        object["readOnly"] = readOnly
        return try JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: object))
    }

    private func goal(_ status: String, budget: Int? = nil) -> [String: Any] {
        ["objective": "Make the release build pass", "status": status, "tokenBudget": budget.map { $0 as Any } ?? NSNull()]
    }

    func testAClaudeGoalOffersSetAndClearButNoPauseOrBudget() throws {
        let controls = GoalControls(try detail(goal: goal("blocked"), goalStatus: false))
        XCTAssertEqual(controls.controls, [.editGoal, .clearGoal])
    }

    func testACodexGoalPausesResumesAndBudgets() throws {
        XCTAssertEqual(GoalControls(try detail(goal: goal("active"), goalStatus: true)).controls, [.editGoal, .clearGoal, .pause, .setBudget])
        XCTAssertEqual(GoalControls(try detail(goal: goal("paused", budget: 5000), goalStatus: true)).controls, [.editGoal, .clearGoal, .resume, .editBudget, .clearBudget])
    }

    func testNoGoalOffersOnlySet() throws {
        XCTAssertEqual(GoalControls(try detail(goal: nil, goalStatus: true)).controls, [.setGoal])
    }

    func testABlockedClaudeGoalShowsItsObjectiveStatusAndReason() throws {
        let goal = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"Make the release build pass on a clean checkout","status":"blocked","timeUsedSeconds":540,"tokensUsed":1078,"tokenBudget":null,"reason":"The signing certificate is not installed on this machine.","createdAt":"2026-09-07T12:00:00Z","updatedAt":"2026-09-07T12:09:00Z"}"#.utf8))
        let facts = GoalFacts(goal)
        XCTAssertEqual(facts.objective, "Make the release build pass on a clean checkout")
        XCTAssertEqual(facts.status, "Needs you")
        XCTAssertTrue(facts.needsAttention)
        XCTAssertEqual(facts.reason, "The signing certificate is not installed on this machine.")
        XCTAssertEqual(facts.rows.map(\.label), ["Elapsed", "Tokens used", "Created", "Updated"])
        XCTAssertEqual(facts.rows.first?.value, "9m 0s")
    }

    func testAGoalWithoutAnObjectiveShowsNone() throws {
        let goal = try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"","status":"active","tokenBudget":5000}"#.utf8))
        XCTAssertNil(GoalFacts(goal).objective)
        XCTAssertNil(GoalFacts(goal).reason)
    }

    func testAReadOnlySessionOrOneWithoutGoalsOffersNothing() throws {
        XCTAssertEqual(GoalControls(try detail(goal: goal("active"), goalStatus: true, readOnly: true)).controls, [])
        XCTAssertEqual(GoalControls(try detail(goal: goal("active"), goalStatus: true, goalSet: false)).controls, [])
    }
}
