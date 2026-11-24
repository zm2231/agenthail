import SwiftUI
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

@MainActor
final class SessionEditSheetTests: XCTestCase {
    func testATypedRenameSurvivesTheDetailsGrowingAndScrollingUnderIt() throws {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as? [String: Any])
        let context = object.removeValue(forKey: "context")
        let quiet = try JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: object))
        object["context"] = context
        let grown = try JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: object))
        let model = AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: .ephemeral)))
        model.selectedDetail = quiet
        let host = UIHostingController(rootView: SessionInspector(model: model, session: quiet.sessionState(open: true), initialDetail: quiet, editing: .name))
        let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 390, height: 360))
        window.rootViewController = host
        window.makeKeyAndVisible()
        defer {
            host.dismiss(animated: false)
            _ = poll { PresentedSheets.holdsDraft ? nil : true }
            window.isHidden = true
        }

        let field = try XCTUnwrap(poll { self.textInput(in: host.presentedViewController?.view) }, "the rename sheet opens over the details")
        _ = field.becomeFirstResponder()
        field.insertText("keepme")
        XCTAssertNotNil(poll { PresentedSheets.holdsDraft ? true : nil }, "a typed name holds routes")

        let list = try XCTUnwrap(collectionView(in: host.view), "the details are a List")
        let before = list.contentSize.height
        model.selectedDetail = grown
        XCTAssertNotNil(poll { list.contentSize.height > before ? true : nil }, "the details gain their context rows under the open sheet")
        XCTAssertGreaterThan(list.contentSize.height, 2 * list.bounds.height, "the details are taller than two screens")
        let shown = try XCTUnwrap(stride(from: lowest(list), through: highest(list), by: list.bounds.height / 3).first { offset in
            scroll(list, to: offset)
            return showsEditingControls(list)
        }, "the Name conversation row comes on screen")
        let away = shown - lowest(list) > highest(list) - shown ? lowest(list) : highest(list)
        scroll(list, to: away)
        XCTAssertFalse(showsEditingControls(list), "the Name conversation row leaves the screen")
        scroll(list, to: shown)
        XCTAssertTrue(showsEditingControls(list), "the Name conversation row comes back")

        XCTAssertNotNil(host.presentedViewController, "the rename sheet stays open")
        XCTAssertEqual(text(of: textInput(in: host.presentedViewController?.view)), "keepme", "the typed name survives")
        XCTAssertTrue(PresentedSheets.holdsDraft, "the typed name still holds routes")
    }

    private func poll<T>(timeout: TimeInterval = 2, _ read: () -> T?) -> T? {
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            if let value = read() { return value }
            RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        } while Date() < deadline
        return read()
    }

    private func lowest(_ list: UICollectionView) -> CGFloat { -list.adjustedContentInset.top }

    private func highest(_ list: UICollectionView) -> CGFloat {
        list.contentSize.height - list.bounds.height + list.adjustedContentInset.bottom
    }

    private func scroll(_ list: UICollectionView, to offset: CGFloat) {
        list.setContentOffset(CGPoint(x: 0, y: offset), animated: false)
        for _ in 0..<5 {
            RunLoop.main.run(until: Date().addingTimeInterval(0.05))
            list.layoutIfNeeded()
        }
    }

    private func showsEditingControls(_ list: UICollectionView) -> Bool {
        list.visibleCells.contains { cell in
            cell.frame.intersects(list.bounds) && labels(cell).contains { $0.contains("Name conversation") }
        }
    }

    private func labels(_ object: NSObject) -> [String] {
        var found = [object.accessibilityLabel].compactMap { $0 }
        for case let child as NSObject in object.accessibilityElements ?? [] { found += labels(child) }
        if let view = object as? UIView { for child in view.subviews { found += labels(child) } }
        return found
    }

    private func textInput(in view: UIView?) -> (UIView & UIKeyInput)? {
        guard let view else { return nil }
        if let input = view as? UITextView { return input }
        if let input = view as? UITextField { return input }
        for child in view.subviews { if let input = textInput(in: child) { return input } }
        return nil
    }

    private func collectionView(in view: UIView) -> UICollectionView? {
        if let list = view as? UICollectionView { return list }
        for child in view.subviews { if let list = collectionView(in: child) { return list } }
        return nil
    }

    private func text(of input: (UIView & UIKeyInput)?) -> String? {
        (input as? UITextView)?.text ?? (input as? UITextField)?.text
    }
}
