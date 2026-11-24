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
        let scene = try XCTUnwrap(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let window = UIWindow(windowScene: scene)
        window.frame = CGRect(x: 0, y: 0, width: 390, height: 480)
        window.rootViewController = host
        window.makeKeyAndVisible()
        defer {
            host.dismiss(animated: false)
            _ = poll { PresentedSheets.holdsDraft ? nil : true }
            window.isHidden = true
        }

        let list = try XCTUnwrap(poll { self.collectionView(in: host.view) }, "the details are a List")
        var field = poll(timeout: 1) { self.textInput(in: host.presentedViewController?.view) }
        for offset in stride(from: lowest(list), through: highest(list), by: list.bounds.height / 3) where field == nil {
            scroll(list, to: offset)
            field = poll(timeout: 1) { self.textInput(in: host.presentedViewController?.view) }
        }
        let typing = try XCTUnwrap(field, "the rename sheet opens over the details (presented: \(String(describing: host.presentedViewController)), list height \(list.contentSize.height))")
        _ = typing.becomeFirstResponder()
        typing.insertText("keepme")
        XCTAssertEqual(text(of: typing), "keepme")
        XCTAssertNotNil(poll { PresentedSheets.holdsDraft ? true : nil }, "a typed name holds routes")

        let before = list.contentSize.height
        model.selectedDetail = grown
        XCTAssertNotNil(poll { list.contentSize.height > before ? true : nil }, "the details gain their context rows under the open sheet (height \(before) -> \(list.contentSize.height), list in window: \(list.window != nil))")
        for _ in 0..<10 {
            let height = list.contentSize.height
            RunLoop.main.run(until: Date().addingTimeInterval(0.3))
            list.layoutIfNeeded()
            if list.contentSize.height == height { break }
        }
        scroll(list, to: lowest(list))
        let top = Set(list.indexPathsForVisibleItems)
        scroll(list, to: highest(list))
        let bottom = Set(list.indexPathsForVisibleItems)
        XCTAssertFalse(top.isEmpty || bottom.isEmpty, "rows show at both ends")
        XCTAssertTrue(top.isDisjoint(with: bottom), "no row shows at both ends, so each one is off screen at one of them: top \(top.sorted()), bottom \(bottom.sorted())")
        for (pass, upward) in [("going down", false), ("going back up", true), ("going down again", false)] {
            let seen = sweep(list, upward: upward)
            let rows = allRows(list)
            XCTAssertTrue(seen == rows, "every row, Name conversation included, comes on screen \(pass); differ: \(seen.symmetricDifference(rows).sorted()) of \(rows.count)")
        }

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

    private func allRows(_ list: UICollectionView) -> Set<IndexPath> {
        Set((0..<list.numberOfSections).flatMap { section in
            (0..<list.numberOfItems(inSection: section)).map { IndexPath(item: $0, section: section) }
        })
    }

    private func sweep(_ list: UICollectionView, upward: Bool) -> Set<IndexPath> {
        let down = Array(stride(from: lowest(list), to: highest(list), by: list.bounds.height / 3)) + [highest(list)]
        return (upward ? Array(down.reversed()) : down).reduce(into: Set<IndexPath>()) { seen, offset in
            scroll(list, to: offset)
            seen.formUnion(list.indexPathsForVisibleItems)
        }
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
