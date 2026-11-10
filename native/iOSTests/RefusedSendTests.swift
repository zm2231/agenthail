import XCTest
@testable import Agenthail

final class RefusedSendTests: XCTestCase {
    private let shot = ComposerAttachment(name: "shot.png", data: Data([1, 2, 3]), mediaType: "image/png")
    private let notes = ComposerAttachment(name: "notes.txt", data: Data([4]), mediaType: "text/plain")

    func testDeletingTakesOutTheMessageThatCameBack() {
        let refused = RefusedSend(message: "Run the tests", attachments: [shot])
        let left = refused.removed(from: "Run the tests", attachments: [shot])
        XCTAssertEqual(left.text, "")
        XCTAssertTrue(left.attachments.isEmpty)
    }

    func testDeletingKeepsWhatThePersonTypedAfterIt() {
        let refused = RefusedSend(message: "Run the tests", attachments: [shot])
        let left = refused.removed(from: "Run the tests\n\nAnd the lint", attachments: [shot, notes])
        XCTAssertEqual(left.text, "And the lint")
        XCTAssertEqual(left.attachments.map(\.name), ["notes.txt"])
    }

    func testAnEditedMessageStaysInTheComposer() {
        let refused = RefusedSend(message: "Run the tests", attachments: [])
        XCTAssertEqual(refused.removed(from: "Run the tests twice", attachments: []).text, "Run the tests twice")
        XCTAssertEqual(refused.removed(from: "Please run the tests", attachments: []).text, "Please run the tests")
    }

    func testDeleteShowsOnlyWhenNothingIsKeptOrTheAskOffersIt() {
        func requests(_ choices: [String]) -> SessionRequests {
            let options = choices.map { SessionRequest.Option(id: $0, label: $0.capitalized, description: "") }
            return SessionRequests(permissionMode: "", requests: [SessionRequest(id: "ask-1", kind: "ask", tool: "", questions: [SessionRequest.Question(question: "q", header: "", multiSelect: false, options: options)])])
        }
        XCTAssertTrue(RefusedSend(message: "a", attachments: []).canDelete, "no ask: nothing is kept on the daemon, so the draft is removed locally")
        XCTAssertFalse(RefusedSend(message: "a", attachments: [], ask: FailedSendAsk(id: "ask-1")).canDelete, "Delete waits until the ask is read")
        XCTAssertTrue(RefusedSend(message: "a", attachments: [], ask: FailedSendAsk(id: "ask-1").reading(requests(["delete", "dismiss"]))).canDelete)
        XCTAssertFalse(RefusedSend(message: "a", attachments: [], ask: FailedSendAsk(id: "ask-1").reading(requests(["dismiss"]))).canDelete, "an ask without delete means it may have arrived")
    }

    func testAnAttachmentMatchesByContentNotByCopy() {
        let refused = RefusedSend(message: "", attachments: [shot])
        let copy = ComposerAttachment(name: "shot.png", data: Data([1, 2, 3]), mediaType: "image/png")
        let left = refused.removed(from: "", attachments: [copy, copy])
        XCTAssertEqual(left.attachments.count, 1, "only the one that came back is removed")
    }
}
