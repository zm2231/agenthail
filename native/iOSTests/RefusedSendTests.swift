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

    func testOnlyADeletableDeliveryOrAnAppSideFailureCanBeDeleted() {
        XCTAssertTrue(RefusedSend(message: "a", attachments: []).canDelete, "no delivery id: the app refused it, nothing reached the daemon")
        XCTAssertFalse(RefusedSend(message: "a", attachments: [], deliveryID: 7).canDelete, "a delivery the daemon does not call deletable may have arrived")
        XCTAssertTrue(RefusedSend(message: "a", attachments: [], deliveryID: 7, deletable: true).canDelete)
    }

    func testAnAttachmentMatchesByContentNotByCopy() {
        let refused = RefusedSend(message: "", attachments: [shot])
        let copy = ComposerAttachment(name: "shot.png", data: Data([1, 2, 3]), mediaType: "image/png")
        let left = refused.removed(from: "", attachments: [copy, copy])
        XCTAssertEqual(left.attachments.count, 1, "only the one that came back is removed")
    }
}
