import SwiftUI
import UIKit
import XCTest
@testable import Agenthail

@MainActor
final class ComposerCompletionTests: XCTestCase {
    private var window: UIWindow?
    private var draft = ""
    private var carets: [Int] = []

    private func composer() -> (ComposerUITextView, IOSComposerTextView.Coordinator) {
        let view = ComposerUITextView.make()
        view.frame = CGRect(x: 0, y: 0, width: 320, height: 44)
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first!
        let window = UIWindow(windowScene: scene)
        window.addSubview(view)
        window.makeKeyAndVisible()
        self.window = window
        XCTAssertTrue(view.becomeFirstResponder())
        let coordinator = IOSComposerTextView.Coordinator(text: Binding(get: { [unowned self] in draft }, set: { [unowned self] in draft = $0 }))
        coordinator.onCaret = { [unowned self] in carets.append($0) }
        view.delegate = coordinator
        return (view, coordinator)
    }

    override func tearDown() {
        window?.isHidden = true
        window = nil
        super.tearDown()
    }

    private func settle() async {
        for _ in 0 ..< 5 { await Task.yield() }
        try? await Task.sleep(for: .milliseconds(20))
    }

    func testTheCaretReachesTheCompletionAfterTypingAndMoving() async {
        let (view, coordinator) = composer()
        _ = coordinator
        view.insertText("ask @co")
        await settle()
        XCTAssertEqual(carets.last, 7)
        view.selectedRange = NSRange(location: 3, length: 0)
        await settle()
        XCTAssertEqual(carets.last, 3)
    }

    func testAcceptingASuggestionReplacesTheWordThroughTheField() async throws {
        let (view, coordinator) = composer()
        _ = coordinator
        view.insertText("ask @co then")
        view.selectedRange = NSRange(location: 7, length: 0)
        await settle()
        let token = try XCTUnwrap(CompletionToken.at(draft, caret: 7))
        let item = CompletionItem(id: "file:composer.swift", title: "composer.swift", detail: "", insertion: "@composer.swift")
        let change = ComposerCompletion.edit(item, in: draft, at: token)
        let editor = IOSComposerEditor()
        editor.textView = view
        editor.replace(change.range, with: change.replacement, caret: change.caret)
        XCTAssertEqual(view.text, "ask @composer.swift then")
        XCTAssertEqual(draft, "ask @composer.swift then")
        XCTAssertEqual(view.selectedRange, NSRange(location: change.caret, length: 0))
        await settle()
        XCTAssertEqual(carets.last, change.caret)
        view.undoManager?.undo()
        XCTAssertEqual(draft, "ask @co then")
    }
}
