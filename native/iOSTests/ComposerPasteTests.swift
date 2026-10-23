import SwiftUI
import UIKit
import UniformTypeIdentifiers
import XCTest
@testable import Agenthail

@MainActor
final class ComposerPasteTests: XCTestCase {
    private var pasted: [PastedAttachments] = []

    private var window: UIWindow?

    private func makeTextView() -> ComposerUITextView {
        let view = ComposerUITextView.make()
        view.frame = CGRect(x: 0, y: 0, width: 320, height: 44)
        view.pasteAttachments = { [unowned self] in pasted.append($0) }
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first!
        let window = UIWindow(windowScene: scene)
        window.frame = CGRect(x: 0, y: 0, width: 320, height: 200)
        window.addSubview(view)
        window.makeKeyAndVisible()
        self.window = window
        XCTAssertTrue(view.becomeFirstResponder())
        return view
    }

    override func tearDown() {
        window?.isHidden = true
        window = nil
        super.tearDown()
    }

    private func uikitHeight(_ view: ComposerUITextView) -> CGFloat {
        ceil(view.sizeThatFits(CGSize(width: 320, height: CGFloat.greatestFiniteMagnitude)).height)
    }

    private var png: Data {
        UIGraphicsImageRenderer(size: CGSize(width: 8, height: 8)).pngData { context in
            UIColor.systemGreen.setFill()
            context.fill(CGRect(x: 0, y: 0, width: 8, height: 8))
        }
    }

    func testPastingACopiedImageAttachesItAndLeavesTheTextAlone() {
        UIPasteboard.general.setData(png, forPasteboardType: UTType.png.identifier)
        let view = makeTextView()
        view.text = "look at"
        XCTAssertTrue(view.canPerformAction(#selector(UIResponderStandardEditActions.paste(_:)), withSender: nil))
        view.paste(nil)
        XCTAssertEqual(pasted.count, 1)
        XCTAssertEqual(pasted.first?.attachments.map(\.name), ["Pasted image.png"])
        XCTAssertEqual(view.text, "look at")
    }

    private func waitForText(_ view: UITextView, _ expected: String) async {
        for _ in 0 ..< 100 where view.text != expected { try? await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(view.text, expected)
    }

    func testPastingTextStaysText() async {
        UIPasteboard.general.string = "plain words"
        let view = makeTextView()
        view.paste(nil)
        await waitForText(view, "plain words")
        XCTAssertTrue(pasted.isEmpty)
    }

    func testSpreadsheetCellsThatAlsoOfferAPicturePasteAsText() async {
        UIPasteboard.general.items = [[UTType.utf8PlainText.identifier: "a\tb", UTType.png.identifier: png]]
        let view = makeTextView()
        view.paste(nil)
        await waitForText(view, "a\tb")
        XCTAssertTrue(pasted.isEmpty)
    }

    func testABrowserImageWithItsAddressPastesTheImage() {
        UIPasteboard.general.items = [[UTType.utf8PlainText.identifier: "https://example.com/a.png", UTType.png.identifier: png]]
        makeTextView().paste(nil)
        XCTAssertEqual(pasted.first?.attachments.count, 1)
    }

    func testPastingACopiedFileAttachesTheFile() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("notes-\(UUID().uuidString).txt")
        try Data("file body".utf8).write(to: url)
        defer { try? FileManager.default.removeItem(at: url) }
        UIPasteboard.general.items = [[UTType.fileURL.identifier: url.dataRepresentation, UTType.utf8PlainText.identifier: url.lastPathComponent]]
        let view = makeTextView()
        XCTAssertTrue(view.canPerformAction(#selector(UIResponderStandardEditActions.paste(_:)), withSender: nil))
        view.paste(nil)
        XCTAssertEqual(pasted.first?.attachments.map(\.name), [url.lastPathComponent])
        XCTAssertEqual(pasted.first?.attachments.first?.data, Data("file body".utf8))
        XCTAssertEqual(view.text, "")
    }

    private final class Draft {
        var text = ""
        var duringWrite: ((String) -> Void)?
    }

    private func composer(_ draft: Draft) -> (ComposerUITextView, IOSComposerTextView.Coordinator) {
        let view = makeTextView()
        let coordinator = IOSComposerTextView.Coordinator(text: Binding(get: { draft.text }, set: { text in
            draft.duringWrite?(text)
            draft.text = text
        }))
        view.delegate = coordinator
        return (view, coordinator)
    }

    func testAnUpdateBeforeTheBindingCatchesUpKeepsTheTypedText() {
        let draft = Draft()
        let (view, coordinator) = composer(draft)
        view.insertText("@c")
        draft.duringWrite = { _ in view.show(bound: draft.text, sync: coordinator.sync) }
        view.insertText("o")
        XCTAssertEqual(view.text, "@co")
        XCTAssertEqual(draft.text, "@co")
        view.show(bound: draft.text, sync: coordinator.sync)
        XCTAssertEqual(view.text, "@co")
    }

    func testOutsideTextEndsTheKeyboardsComposition() {
        let draft = Draft()
        let (view, coordinator) = composer(draft)
        view.insertText("draft ")
        view.setMarkedText("ka", selectedRange: NSRange(location: 2, length: 0))
        XCTAssertEqual(draft.text, "draft ka")
        draft.text = "shared from another app"
        view.show(bound: draft.text, sync: coordinator.sync)
        XCTAssertNil(view.markedTextRange)
        XCTAssertEqual(view.text, "shared from another app")
        XCTAssertEqual(draft.text, "shared from another app")
        XCTAssertEqual(view.selectedRange, NSRange(location: 23, length: 0))
        view.insertText("!")
        XCTAssertEqual(draft.text, "shared from another app!")
    }

    func testWithoutAttachSupportAnImagePasteIsNotOffered() {
        UIPasteboard.general.setData(png, forPasteboardType: UTType.png.identifier)
        let view = makeTextView()
        view.pasteAttachments = nil
        view.paste(nil)
        XCTAssertTrue(pasted.isEmpty)
    }

    func testTheFieldGrowsWithItsTextUpToSixLines() {
        let view = makeTextView()
        view.text = "one"
        let one = uikitHeight(view)
        view.text = ""
        XCTAssertEqual(view.fittingHeight(width: 320, lines: 1 ... 6), one, accuracy: 1)
        view.text = "one\ntwo\nthree"
        XCTAssertEqual(view.fittingHeight(width: 320, lines: 1 ... 6), uikitHeight(view), accuracy: 1)
        view.text = (1 ... 6).map(String.init).joined(separator: "\n")
        let six = uikitHeight(view)
        XCTAssertEqual(view.fittingHeight(width: 320, lines: 1 ... 6), six, accuracy: 1)
        view.text = (1 ... 20).map(String.init).joined(separator: "\n")
        XCTAssertEqual(view.fittingHeight(width: 320, lines: 1 ... 6), six, accuracy: 1)
        view.text = "ends with a return\n"
        XCTAssertGreaterThan(view.fittingHeight(width: 320, lines: 1 ... 6), one + 1)
    }
}
