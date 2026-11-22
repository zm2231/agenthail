import Foundation
import UIKit
import XCTest
@testable import Agenthail

@MainActor
private final class ProtectedData {
    var locked = true
}

@MainActor
final class ComposerSendTests: XCTestCase {
    private let queuedReceipt = #"{"ok":true,"result":{"evidence":"queued","status":"queued","queueId":7,"detail":"Queued for fixture."}}"#
    private let sentReceipt = #"{"ok":true,"result":{"evidence":"transport_accepted","status":"sent"}}"#

    override func setUp() async throws {
        ComposerSendProtocol.state.reset()
    }

    func testASentMessageClearsTheDraftAtOnceAndStaysClear() async throws {
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composerSession.composer = "Sent"
        model.composerSession.send(to: session)
        XCTAssertEqual(model.composerSession.composer, "")
        try await waitUntil { model.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composerSession.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Sent")
        XCTAssertNil(model.composerSession.failedSends[session.id])
    }

    func testAQueuedMessageClearsTheDraftAndSaysItIsQueued() async throws {
        ComposerSendProtocol.state.script([.answer(200, queuedReceipt)])
        let (model, session) = try makeModel()
        model.composerSession.composer = "Queued"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composerSession.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Queued for fixture.")
        XCTAssertNil(model.composerSession.failedSends[session.id])
    }

    func testARefusedMessageReturnsWithAReasonAndRetryUsesANewKey() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"Session ended"}}"#), .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composerSession.composer = "Refused"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composerSession.composer, "Refused")
        XCTAssertNotNil(model.composerSession.failedSends[session.id])
        XCTAssertNil(model.operationError, "the reason stays on the composer of the session on screen")

        model.composerSession.send(to: session)
        XCTAssertNil(model.composerSession.failedSends[session.id])
        try await waitUntil { model.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 2 }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertNotEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(model.composerSession.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Sent")
    }

    func testARefusalForASessionOffScreenRaisesAnAlert() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"Session ended"}}"#)])
        let (model, session) = try makeModel()
        model.composerSession.composer = "Refused"
        model.composerSession.send(to: session)
        model.selectedSessionID = "computer::other"
        try await waitUntil { model.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertNotNil(model.operationError)
        XCTAssertNotNil(model.composerSession.failedSends[session.id])
    }

    func testALostResponseKeepsTheDraftClearAndRetriesWithTheSameKey() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, queuedReceipt)])
        let (model, session) = try makeModel()
        model.composerSession.composer = "Arrived, answer lost"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends[session.id]?.first?.unconfirmed == true }
        XCTAssertEqual(model.composerSession.composer, "", "the message may have arrived, so it does not come back")
        XCTAssertEqual(model.deliveryStatus[session.id], "Not confirmed yet · retrying")
        XCTAssertNil(model.composerSession.failedSends[session.id])
        try await waitUntil(seconds: 5) { model.composerSession.pendingSends.isEmpty }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(attempts[0].body["message"] as? String, attempts[1].body["message"] as? String)
        XCTAssertEqual(model.composerSession.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Queued for fixture.")
    }

    func testOfflineMessagesWaitInOrderWhileTheComposerStaysUsable() async throws {
        ComposerSendProtocol.state.script([.offline, .answer(200, sentReceipt), .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composerSession.composer = "First"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends[session.id]?.first?.unconfirmed == true }
        model.composerSession.composer = "Second"
        model.composerSession.send(to: session)
        XCTAssertEqual(model.composerSession.composer, "")
        XCTAssertEqual(model.composerSession.pendingSends[session.id]?.map(\.message), ["First", "Second"])
        try await waitUntil(seconds: 5) { model.composerSession.pendingSends.isEmpty }
        XCTAssertEqual(ComposerSendProtocol.state.attempts.map { $0.body["message"] as? String }, ["First", "First", "Second"])
    }

    func testAWaitingMessageCanBePutBack() async throws {
        ComposerSendProtocol.state.script([.offline, .offline, .offline, .offline])
        let (model, session) = try makeModel()
        model.composerSession.composer = "Take it back"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends[session.id]?.first?.unconfirmed == true }
        model.composerSession.composer = "newer"
        model.composerSession.putBack(try XCTUnwrap(model.composerSession.pendingSends[session.id]?.first))
        XCTAssertEqual(model.composerSession.composer, "Take it back\n\nnewer")
        XCTAssertTrue(model.composerSession.pendingSends.isEmpty)
        XCTAssertEqual(model.deliveryStatus[session.id], "Put back. It may already have arrived.")
    }

    func testASendThatCannotStartSaysSoAndKeepsTheDraft() throws {
        let (model, session) = try makeModel()
        model.selectedDetail = nil
        model.composerSession.composer = "Keep me"
        model.composerSession.send(to: session)
        XCTAssertEqual(model.composerSession.composer, "Keep me")
        XCTAssertNotNil(model.composerSession.failedSends[session.id])
        XCTAssertTrue(model.composerSession.pendingSends.isEmpty)
    }

    func testAPendingMessageResumesAfterTheAppIsKilledWithItsKey() async throws {
        let store = temporaryStore()
        ComposerSendProtocol.state.script([.offline, .offline, .offline, .offline])
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        first?.composerSession.composer = "Survives"
        first?.composerSession.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.attempts.count >= 1 }
        let key = ComposerSendProtocol.state.attempts[0].key
        let raw = ComposerSendProtocol.state.attempts[0].raw
        first = nil
        XCTAssertEqual(store.load().saved.sends.map(\.message), ["Survives"])
        XCTAssertEqual(store.load().saved.drafts, [:], "a message that left the composer is not also a draft")

        ComposerSendProtocol.state.reset()
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let second = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        second.selectedSessionID = session.id
        try await waitUntil(seconds: 5) { second.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[0].key, key)
        XCTAssertEqual(ComposerSendProtocol.state.attempts[0].raw, raw, "the resumed message is the same request, byte for byte")
        XCTAssertEqual(store.load().saved.sends, [])
    }

    private func queuedItem(_ session: SessionState, attachments: String = "") throws -> QueueState {
        var item = try JSONDecoder().decode(QueueState.self, from: Data(#"{"id":9,"sessionId":"\#(session.id)","target":"t","message":"from the row","status":"pending","attempts":0,"queuedAt":"2026-11-04T10:00:00.000000000Z","evidence":"queued"\#(attachments)}"#.utf8))
        item.hostID = "computer"
        return item
    }

    private let cancelledWithAttachments = #"{"ok":true,"result":{"message":"from the reply","attachments":[{"id":"up-1","name":"shot.png","mediaType":"image/png","bytes":2048,"width":10,"height":10},{"id":"up-2","name":"gone.pdf","mediaType":"application/pdf","bytes":9,"missing":true}]}}"#

    func testTakingBackRestoresAttachmentsAndNamesAMissingOne() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, cancelledWithAttachments), .answer(200, sentReceipt)])
        try await model.takeBack(queuedItem(session))
        XCTAssertEqual(model.composerSession.composer, "from the reply")
        XCTAssertEqual(model.composerSession.composerAttachments.map(\.uploaded?.id), ["up-1"])
        XCTAssertEqual(model.composerSession.composerAttachments.first?.byteCount, 2048)
        XCTAssertTrue(model.composerSession.attachmentProblem?.contains("gone.pdf") == true, String(describing: model.composerSession.attachmentProblem))

        model.composerSession.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.attempts.count == 2 && model.composerSession.pendingSends.isEmpty }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[1].body["attachments"] as? [String], ["up-1"], "a taken-back attachment goes by its id, with no upload")
    }

    func testTakingBackFromAnOlderComputerKeepsTheRowsMessageAndAttachments() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, #"{"ok":true}"#)])
        try await model.takeBack(queuedItem(session, attachments: #","attachments":[{"id":"up-3","name":"row.png","mediaType":"image/png","bytes":1}]"#))
        XCTAssertEqual(model.composerSession.composer, "from the row")
        XCTAssertEqual(model.composerSession.composerAttachments.map(\.uploaded?.id), ["up-3"])
    }

    private var takesImages: Capabilities {
        var capabilities = Capabilities(steer: true)
        capabilities.steerImages = true
        return capabilities
    }

    private let rowImage = #","attachments":[{"id":"up-3","name":"row.png","mediaType":"image/png","bytes":1}]"#

    func testARowActionAlreadyRunningReportsThatNothingWasDone() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        let item = try queuedItem(session)
        model.pendingControls.insert("queue:\(item.listID)")
        ComposerSendProtocol.state.script([])
        let steered = try await model.steerQueued(item, capabilities: takesImages)
        let edited = try await model.takeBack(item)
        let cancelled = try await model.cancelQueue(item)
        XCTAssertFalse(steered)
        XCTAssertFalse(edited)
        XCTAssertFalse(cancelled)
        XCTAssertTrue(ComposerSendProtocol.state.attempts.isEmpty)
    }

    func testARowActionThatRanReportsIt() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, #"{"ok":true}"#), .answer(200, sentReceipt)])
        let steered = try await model.steerQueued(queuedItem(session), capabilities: takesImages)
        XCTAssertTrue(steered)
    }

    func testSteeringAQueuedImageSendsItIntoTheTurnByItsUpload() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, #"{"ok":true}"#), .answer(200, sentReceipt)])
        try await model.steerQueued(queuedItem(session, attachments: rowImage), capabilities: takesImages)
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.compactMap { $0.body["action"] as? String }.prefix(2), ["queue-cancel", "send"])
        XCTAssertEqual(attempts[1].body["busyDelivery"] as? String, "steer")
        XCTAssertEqual(attempts[1].body["attachments"] as? [String], ["up-3"])
        XCTAssertEqual(attempts[1].body["message"] as? String, "from the row")
        XCTAssertEqual(model.composerSession.composer, "")
    }

    func testAQueuedRowTheSessionCantSteerIsNotCancelled() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, #"{"ok":true}"#)])
        try await model.steerQueued(queuedItem(session, attachments: rowImage), capabilities: Capabilities(steer: true))
        XCTAssertEqual(ComposerSendProtocol.state.attempts.count, 0)
    }

    func testARowAnotherSessionSentIsNotCancelledToSteerIt() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, #"{"ok":true}"#)])
        try await model.steerQueued(queuedItem(session, attachments: #","author":"""# + rowImage), capabilities: takesImages)
        XCTAssertEqual(ComposerSendProtocol.state.attempts.count, 0)
    }

    func testACancelThatFailsSendsNothing() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"already delivering"}}"#)])
        do {
            try await model.steerQueued(queuedItem(session, attachments: rowImage), capabilities: takesImages)
            XCTFail("the failure must reach the row")
        } catch {
            XCTAssertTrue(AgenthailAPIError.userMessage(error).contains("already delivering"), String(describing: error))
        }
        XCTAssertEqual(ComposerSendProtocol.state.attempts.count, 1)
        XCTAssertEqual(model.composerSession.composer, "")
    }

    func testASteerThatFailsReturnsTheMessageAndAttachmentsToTheComposer() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, #"{"ok":true}"#), .answer(500, #"{"error":{"message":"no route"}}"#)])
        do {
            try await model.steerQueued(queuedItem(session, attachments: rowImage), capabilities: takesImages)
            XCTFail("the failure must reach the row")
        } catch {
            XCTAssertTrue(AgenthailAPIError.userMessage(error).contains("may not have reached"), String(describing: error))
        }
        XCTAssertEqual(model.composerSession.composer, "from the row")
        XCTAssertEqual(model.composerSession.composerAttachments.map(\.uploaded?.id), ["up-3"])
    }

    func testTheInboxOffersSteerOnlyWhileTheSessionRuns() throws {
        let (model, session) = try makeModel(store: temporaryStore())
        XCTAssertNil(model.queueSteer(try queuedItem(session)), "an idle session has no turn to steer")
        model.selectedDetail = try makeDetail(status: "busy")
        let offer = try XCTUnwrap(model.queueSteer(try queuedItem(session)))
        XCTAssertNil(offer.refusal)
    }

    func testTheInboxGivesTheReasonARowCantSteer() throws {
        let (model, session) = try makeModel(store: temporaryStore())
        model.selectedDetail = try makeDetail(status: "busy")
        let image = try queuedItem(session, attachments: rowImage)
        let offer = try XCTUnwrap(model.queueSteer(image))
        XCTAssertNotNil(offer.refusal, "this computer's capabilities don't say it takes images")
    }

    func testOnlyAWaitingMessageRowOffersSteer() throws {
        let (model, session) = try makeModel(store: temporaryStore())
        model.selectedDetail = try makeDetail(status: "busy")
        let goal = try queuedItem(session, attachments: #","operation":"goal-set""#)
        XCTAssertNil(model.queueSteer(goal))
        XCTAssertNotNil(model.queueSteer(try queuedItem(session)))
    }

    func testATakenBackAttachmentSurvivesTheAppBeingKilled() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        ComposerSendProtocol.state.script([.answer(200, cancelledWithAttachments)])
        try await model.takeBack(queuedItem(session))
        model.composerSession.saveComposerState()
        XCTAssertEqual(store.load().saved.attachmentNames, [:], "it isn't reported lost")

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composerSession.composerAttachments.map(\.uploaded?.id), ["up-1"])
        XCTAssertEqual(reopened.composerSession.composerAttachments.first?.name, "shot.png")
        XCTAssertEqual(reopened.composerSession.composerAttachments.first?.mediaType, "image/png")
    }

    func testAStashedTakenBackAttachmentSurvivesTheAppBeingKilled() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        ComposerSendProtocol.state.script([.answer(200, cancelledWithAttachments)])
        try await model.takeBack(queuedItem(session))
        model.composerSession.toggleStash()
        XCTAssertTrue(model.composerSession.composerAttachments.isEmpty)
        model.composerSession.saveComposerState()

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertTrue(reopened.composerSession.composerAttachments.isEmpty)
        reopened.composerSession.toggleStash()
        XCTAssertEqual(reopened.composerSession.composerAttachments.map(\.uploaded?.id), ["up-1"])
    }

    func testAKilledSendOfATakenBackAttachmentResumesWithItsID() async throws {
        let store = temporaryStore()
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        ComposerSendProtocol.state.script([.answer(200, cancelledWithAttachments), .offline, .offline])
        try await first?.takeBack(queuedItem(session))
        first?.composerSession.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.attempts.count >= 2 }
        first?.composerSession.saveComposerState()
        first = nil
        XCTAssertEqual(store.load().saved.sends.first?.uploaded, ["up-1"])

        ComposerSendProtocol.state.reset()
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let second = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        second.selectedSessionID = session.id
        try await waitUntil(seconds: 5) { second.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[0].body["attachments"] as? [String], ["up-1"])
        XCTAssertNil(second.composerSession.failedSends[session.id])
    }

    func testADraftSurvivesTheAppBeingKilled() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composerSession.composer = "Typed, not sent"
        model.composerSession.saveComposerState()
        XCTAssertEqual(store.load().saved.drafts[session.id], "Typed, not sent")

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composerSession.composer, "Typed, not sent")
    }

    func testTypingReachesTheStoreWithoutAnExplicitSave() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composerSession.composer = "Typed"
        try await waitUntil(seconds: 3) { store.load().saved.drafts[session.id] == "Typed" }
    }

    func testAKilledSendWhoseAttachmentNeverUploadedComesBackAsADraft() async throws {
        let store = temporaryStore()
        store.save(.init(sends: [.init(id: UUID(), sessionID: "computer::codex-flow", message: "With a file",idempotencyKey: "k", uploaded: nil, wire: nil, hadAttachments: true)]))
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertTrue(model.composerSession.pendingSends.isEmpty)
        XCTAssertNotNil(model.composerSession.failedSends["computer::codex-flow"])
        await model.loadSession("computer::codex-flow")
        XCTAssertEqual(model.composerSession.composer, "With a file")
    }

    func testASendKilledWhileItsAttachmentUploadsResumesWithItsBytesAndKey() async throws {
        let store = temporaryStore()
        ComposerSendProtocol.state.scriptUploads([.offline, .offline, .offline])
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        first?.composerSession.composerAttachments = [ComposerAttachment(name: "notes.txt", data: Data("hello".utf8), mediaType: "text/plain")]
        first?.composerSession.composer = "With a file"
        first?.composerSession.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.uploads.count >= 1 }
        first = nil
        let saved = try XCTUnwrap(store.load().saved.sends.first)
        let record = try XCTUnwrap(saved.attachments?.first)
        XCTAssertEqual(store.unspool(record)?.data, Data("hello".utf8), "the attachment's bytes outlive the app")

        ComposerSendProtocol.state.reset()
        ComposerSendProtocol.state.scriptUploads([.stored("up-a")])
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let second = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        second.selectedSessionID = session.id
        try await waitUntil(seconds: 5) { second.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.uploads, ["notes.txt"])
        XCTAssertEqual(ComposerSendProtocol.state.attempts.first?.key, saved.idempotencyKey)
        XCTAssertEqual(ComposerSendProtocol.state.attempts.first?.body["attachments"] as? [String], ["up-a"])
        XCTAssertNil(second.composerSession.failedSends[session.id])
        XCTAssertEqual(try spooledFiles(store), [], "a sent message's attachment leaves the spool")
    }

    func testAKilledSendUploadsOnlyTheAttachmentsItHadNotUploaded() async throws {
        let store = temporaryStore()
        ComposerSendProtocol.state.scriptUploads([.stored("up-1"), .offline, .offline, .offline])
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        first?.composerSession.composerAttachments = [ComposerAttachment(name: "one.txt", data: Data("one".utf8), mediaType: "text/plain"), ComposerAttachment(name: "two.txt", data: Data("two".utf8), mediaType: "text/plain")]
        first?.composerSession.composer = "Two files"
        first?.composerSession.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.uploads.count >= 2 }
        first = nil

        ComposerSendProtocol.state.reset()
        ComposerSendProtocol.state.scriptUploads([.stored("up-2")])
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let second = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        try await waitUntil(seconds: 5) { second.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.uploads, ["two.txt"], "an attachment already on the computer is not uploaded again")
        XCTAssertEqual(ComposerSendProtocol.state.attempts.first?.body["attachments"] as? [String], ["up-1", "up-2"])
    }

    func testASendKilledAfterItWentOutIsResentOnceAsTheSameRequest() async throws {
        let store = temporaryStore()
        ComposerSendProtocol.state.scriptUploads([.stored("up-a")])
        ComposerSendProtocol.state.script([.lostResponse, .offline, .offline, .offline])
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        first?.composerSession.composerAttachments = [ComposerAttachment(name: "notes.txt", data: Data("hello".utf8), mediaType: "text/plain")]
        first?.composerSession.composer = "Went out"
        first?.composerSession.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.attempts.count >= 1 }
        let raw = try XCTUnwrap(ComposerSendProtocol.state.attempts.first?.raw)
        first = nil

        ComposerSendProtocol.state.reset()
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let second = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        try await waitUntil(seconds: 5) { second.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.uploads, [], "an uploaded attachment is not uploaded again")
        XCTAssertEqual(ComposerSendProtocol.state.attempts.first?.raw, raw, "the resent message is the same request, so the computer keeps one")
    }

    func testAKilledSendWhoseSpooledAttachmentIsMissingComesBackAsADraft() async throws {
        let store = temporaryStore()
        let missing = SpooledAttachment(id: UUID(), name: "gone.txt", mediaType: "text/plain")
        store.save(.init(sends: [.init(id: UUID(), sessionID: "computer::codex-flow", message: "With a file", idempotencyKey: "k", uploaded: nil, wire: nil, hadAttachments: true, attachments: [missing])]))
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertTrue(model.composerSession.pendingSends.isEmpty)
        XCTAssertNotNil(model.composerSession.failedSends["computer::codex-flow"])
        await model.loadSession("computer::codex-flow")
        XCTAssertEqual(model.composerSession.composer, "With a file")
    }

    func testADraftAttachmentSurvivesTheAppBeingKilledAndLeavesTheSpoolWhenRemoved() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composerSession.composerAttachments = [ComposerAttachment(name: "draft.txt", data: Data("draft".utf8), mediaType: "text/plain")]
        model.composerSession.saveComposerState()
        try await waitUntil { store.load().saved.spooledDrafts?[session.id] != nil }

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composerSession.composerAttachments.map(\.data), [Data("draft".utf8)])
        XCTAssertNil(reopened.composerSession.attachmentProblem)
        reopened.composerSession.composerAttachments = []
        reopened.composerSession.saveComposerState()
        XCTAssertEqual(try spooledFiles(store), [])
    }

    func testAnUploadStartsOnlyOnceItsAttachmentIsKeptOnDisk() async throws {
        var store = temporaryStore()
        let write = store.writeSpool
        store.writeSpool = { data, target in
            Thread.sleep(forTimeInterval: 0.5)
            try write(data, target)
        }
        ComposerSendProtocol.state.scriptUploads([.offline, .offline, .offline])
        let (model, session) = try makeModel(store: store)
        model.composerSession.composerAttachments = [ComposerAttachment(name: "slow.txt", data: Data("slow".utf8), mediaType: "text/plain")]
        model.composerSession.composer = "Slow disk"
        model.composerSession.send(to: session)
        try await waitUntil(seconds: 5) { ComposerSendProtocol.state.uploads.count >= 1 }
        XCTAssertNotNil(store.load().saved.sends.first?.attachments, "the upload began before a kill could have resumed it")
    }

    func testADraftAttachmentKilledBeforeItsBytesLandIsNamedNotSilentlyLost() async throws {
        var store = temporaryStore()
        let write = store.writeSpool
        store.writeSpool = { data, target in
            Thread.sleep(forTimeInterval: 1)
            try write(data, target)
        }
        let (model, session) = try makeModel(store: store)
        model.composerSession.composerAttachments = [ComposerAttachment(name: "late.txt", data: Data("late".utf8), mediaType: "text/plain")]
        model.composerSession.saveComposerState()

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: ComposerStore(file: store.file))
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composerSession.composerAttachments, [])
        XCTAssertTrue(reopened.composerSession.attachmentProblem?.contains("late.txt") == true, "\(String(describing: reopened.composerSession.attachmentProblem))")
        withExtendedLifetime(model) {}
    }

    func testAFileThatLandsDuringAnotherSaveIsNotPruned() async throws {
        var store = temporaryStore()
        let write = store.writeSpool
        let other = ComposerStore(file: store.file)
        store.writeSpool = { data, target in
            try write(data, target)
            other.save(ComposerStore.Saved())
        }
        let attachment = ComposerAttachment(name: "raced.txt", data: Data("raced".utf8), mediaType: "text/plain")
        var recorded: SpooledAttachment?
        await store.write([attachment]) { recorded = store.spool(attachment) }
        XCTAssertEqual(recorded.flatMap(store.unspool)?.data, Data("raced".utf8), "a save that ran while the file landed deleted it")
    }

    func testRestoringKeepsTheTextOfASendThatCannotResumeInTheFirstSave() async throws {
        let store = temporaryStore()
        ComposerSendProtocol.state.script([.offline, .offline, .offline])
        store.save(.init(sends: [
            .init(id: UUID(), sessionID: "computer::codex-flow", message: "Resumes", idempotencyKey: "k-1", uploaded: [], wire: nil, hadAttachments: false),
            .init(id: UUID(), sessionID: "computer::other", message: "Cannot resume", idempotencyKey: "k-2", uploaded: nil, wire: nil, hadAttachments: true),
        ]))
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        let saved = store.load().saved
        XCTAssertEqual(saved.drafts["computer::other"], "Cannot resume", "a kill right after restoring would lose this text")
        XCTAssertEqual(saved.sends.map(\.message), ["Resumes"])
        withExtendedLifetime(model) {}
    }

    func testFilesNoSavedMessageOrDraftNamesAreRemoved() async throws {
        let store = temporaryStore()
        try FileManager.default.createDirectory(at: store.spoolFolder, withIntermediateDirectories: true)
        try Data("left over".utf8).write(to: store.spoolFolder.appendingPathComponent(UUID().uuidString))
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        model.composerSession.saveComposerState()
        XCTAssertEqual(try spooledFiles(store), [])
    }

    func testASendStillGoesWhenItsAttachmentCannotBeKeptOnDisk() async throws {
        var store = temporaryStore()
        store.writeSpool = { _, _ in throw CocoaError(.fileWriteOutOfSpace) }
        ComposerSendProtocol.state.scriptUploads([.stored("up-a")])
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let (model, session) = try makeModel(store: store)
        model.composerSession.composerAttachments = [ComposerAttachment(name: "notes.txt", data: Data("hello".utf8), mediaType: "text/plain")]
        model.composerSession.composer = "Disk full"
        model.composerSession.send(to: session)
        try await waitUntil(seconds: 5) { model.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.attempts.first?.body["attachments"] as? [String], ["up-a"])
    }

    func testASendWhoseAttachmentCouldNotBeKeptComesBackAsADraftAfterAKill() async throws {
        var store = temporaryStore()
        store.writeSpool = { _, _ in throw CocoaError(.fileWriteOutOfSpace) }
        ComposerSendProtocol.state.scriptUploads([.offline, .offline, .offline])
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        first?.composerSession.composerAttachments = [ComposerAttachment(name: "notes.txt", data: Data("hello".utf8), mediaType: "text/plain")]
        first?.composerSession.composer = "Disk full"
        first?.composerSession.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.uploads.count >= 1 }
        first = nil

        ComposerSendProtocol.state.reset()
        let second = AgenthailIOSModel(api: makeAPI(), composerStore: ComposerStore(file: store.file))
        XCTAssertTrue(second.composerSession.pendingSends.isEmpty)
        XCTAssertNotNil(second.composerSession.failedSends[session.id])
        await second.loadSession(session.id)
        XCTAssertEqual(second.composerSession.composer, "Disk full")
        XCTAssertEqual(ComposerSendProtocol.state.attempts.count, 0)
    }

    func testARekeyWhileWaitingRetriesTheSameRequest() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composerSession.composer = "Rekeyed while waiting"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends[session.id]?.first?.unconfirmed == true }
        model.host(try XCTUnwrap(model.connection("computer")), rekeyed: ["codex-flow": "codex-moved"])
        XCTAssertNotNil(model.composerSession.pendingSends["computer::codex-moved"])
        try await waitUntil(seconds: 5) { model.composerSession.pendingSends.isEmpty }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(attempts[0].raw, attempts[1].raw, "a changed body under the same key is refused as a mismatch")
    }

    func testARekeyCarriesDraftsStashesRefusalsAndLostAttachmentsToTheNewID() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"Refused for fixture"}}"#)])
        let store = temporaryStore()
        var saved = ComposerStore.Saved()
        saved.attachmentNames = ["computer::codex-flow": ["plan.png"]]
        store.save(saved)
        let (model, session) = try makeModel(store: store)
        model.composerSession.composer = "Refused"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.refusedSends[session.id] != nil }
        model.composerSession.toggleStash()
        model.composerSession.composer = "Typed later"
        model.composerSession.leave(session.id)
        model.selectedSessionID = nil

        model.host(try XCTUnwrap(model.connection("computer")), rekeyed: ["codex-flow": "codex-moved"])

        let moved = "computer::codex-moved"
        XCTAssertNotNil(model.composerSession.refusedSends[moved], "a refused send follows the new id")
        XCTAssertNotNil(model.composerSession.failedSends[moved], "its reason follows too")
        XCTAssertEqual(model.composerSession.stashes[moved]?.text, "Refused", "the stash follows")
        XCTAssertNil(model.composerSession.refusedSends[session.id])
        XCTAssertNil(model.composerSession.stashes[session.id])
        model.composerSession.enter(moved)
        XCTAssertEqual(model.composerSession.composer, "Typed later", "the draft follows")
        XCTAssertTrue(model.composerSession.attachmentProblem?.contains("plan.png") == true, "a lost attachment is still named under the new id")
    }

    func testASendStillWaitingWhenItsModelGoesAwayDoesNotCrash() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, sentReceipt)])
        weak var gone: AgenthailIOSModel?
        do {
            let (model, session) = try makeModel()
            model.composerSession.composer = "Outlives its model"
            model.composerSession.send(to: session)
            try await waitUntil { model.composerSession.pendingSends[session.id]?.first?.unconfirmed == true }
            gone = model
        }
        try await Task.sleep(for: .seconds(3))
        XCTAssertNil(gone, "nothing in the composer keeps the model alive")
    }

    func testAModelClosedWhileItsUploadIsInFlightStillSavesTheNewerDraft() async throws {
        ComposerSendProtocol.state.scriptUploads([.held("up-1")])
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let store = temporaryStore()
        weak var gone: AgenthailIOSModel?
        var sessionID = ""
        do {
            let (model, session) = try makeModel(store: store)
            sessionID = session.id
            model.composerSession.composerAttachments = [ComposerAttachment(name: "notes.txt", data: Data("hello".utf8), mediaType: "text/plain")]
            model.composerSession.composer = "Went out"
            model.composerSession.send(to: session)
            try await waitUntil { ComposerSendProtocol.state.holding }
            model.composerSession.composer = "Typed while it was sending"
            model.composerSession.saveComposerState()
            gone = model
        }
        XCTAssertNotNil(gone, "a send in flight keeps its model until it finishes")
        ComposerSendProtocol.state.releaseHeld()
        try await waitUntil(seconds: 3) { gone == nil && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(store.load().saved.drafts[sessionID], "Typed while it was sending")
    }

    func testChangingTheTerminalWhileWaitingRetriesTheFirstRequest() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        let route = "codex-flow"
        defer { HolderPins.shared.pick(nil, for: route) }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 11, status: "idle")), for: route)
        model.composerSession.composer = "To the first terminal"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends[session.id]?.first?.unconfirmed == true }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 22, status: "idle")), for: route)
        try await waitUntil(seconds: 5) { model.composerSession.pendingSends.isEmpty }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts[0].body["pid"] as? Int, 11)
        XCTAssertEqual(attempts[1].body["pid"] as? Int, 11, "a retry never moves to another terminal: the first may already have it")
        XCTAssertEqual(attempts[0].key, attempts[1].key)
    }

    func testARetryAfterARefusalPicksUpTheNewTerminal() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"That terminal is gone"}}"#), .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        let route = "codex-flow"
        defer { HolderPins.shared.pick(nil, for: route) }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 11, status: "idle")), for: route)
        model.composerSession.composer = "Refused"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 22, status: "idle")), for: route)
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 2 }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[1].body["pid"] as? Int, 22)
    }

    func testAFileThatCannotBeReadIsKeptAndAnnouncedOnce() throws {
        let store = temporaryStore()
        let directory = store.file.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let unreadable = Data("not the format".utf8)
        try unreadable.write(to: store.file)

        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNotNil(model.operationError)
        let names = try FileManager.default.contentsOfDirectory(atPath: directory.path)
        let aside = try XCTUnwrap(names.first { $0.hasPrefix("composer.json.unreadable-") })
        XCTAssertFalse(names.contains("composer.json"))

        model.selectedSessionID = "computer::codex-flow"
        model.composerSession.composer = "Typed after"
        model.composerSession.saveComposerState()
        XCTAssertEqual(store.load().saved.drafts["computer::codex-flow"], "Typed after")
        XCTAssertEqual(try Data(contentsOf: directory.appendingPathComponent(aside)), unreadable, "a later save does not touch the kept file")

        let next = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(next.operationError, "the notice appears once")
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).filter { $0.hasPrefix("composer.json.unreadable-") }, [aside])
    }

    func testAFileThatCannotBeReadOnAnUnlockedPhoneMovesAsideSoNewSendsAreSaved() async throws {
        ComposerSendProtocol.state.script(Array(repeating: .offline, count: 40))
        let store = temporaryStore()
        store.save(.init(drafts: ["computer::other": "Kept in the file"]))
        try FileManager.default.setAttributes([.posixPermissions: 0o000], ofItemAtPath: store.file.path)
        try XCTSkipIf(FileManager.default.isReadableFile(atPath: store.file.path), "this runner can read any file")
        let directory = store.file.deletingLastPathComponent()

        var first: AgenthailIOSModel? = try makeModel(store: store).model
        XCTAssertEqual(first?.operationError, "Saved drafts couldn’t be loaded. Their file was kept on this iPhone.")
        let names = try FileManager.default.contentsOfDirectory(atPath: directory.path)
        let aside = try XCTUnwrap(names.first { $0.hasPrefix("composer.json.unreadable-") })
        XCTAssertFalse(names.contains("composer.json"))

        let session = try makeSession()
        first?.composerSession.composer = "Sent after"
        first?.composerSession.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.attempts.count >= 1 }
        let key = ComposerSendProtocol.state.attempts[0].key
        first = nil

        let saved = store.load().saved
        XCTAssertEqual(saved.sends.map(\.message), ["Sent after"], "an unconfirmed send outlives the app")
        XCTAssertEqual(saved.sends.first?.idempotencyKey, key)
        let keptAside = directory.appendingPathComponent(aside)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: keptAside.path)
        XCTAssertEqual(ComposerStore(file: keptAside).load().saved.drafts["computer::other"], "Kept in the file")
    }

    func testAFileThatCannotBeReadYetIsLeftAloneAndRestoredLater() async throws {
        let lock = ProtectedData()
        var store = temporaryStore()
        store.protectedDataIsAvailable = { !lock.locked }
        lock.locked = false
        let first = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        first.selectedSessionID = "computer::codex-flow"
        first.composerSession.composer = "Typed before the lock"
        first.composerSession.saveComposerState()
        let directory = store.file.deletingLastPathComponent()
        try FileManager.default.setAttributes([.posixPermissions: 0o000], ofItemAtPath: store.file.path)
        addTeardownBlock { try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path) }
        try XCTSkipIf(FileManager.default.isReadableFile(atPath: store.file.path), "this runner can read any file")

        lock.locked = true
        let locked = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(locked.operationError, "a locked phone heals itself, so there is nothing to tell the person")
        locked.selectedSessionID = "computer::codex-flow"
        locked.composerSession.composer = "Typed while locked"
        locked.composerSession.saveComposerState()
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path), ["composer.json"], "a file that may be fine is not moved")

        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path)
        lock.locked = false
        XCTAssertEqual(store.load().saved.drafts["computer::codex-flow"], "Typed before the lock", "nothing overwrote it")
        let unlocked = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(unlocked.operationError)
        await unlocked.loadSession("computer::codex-flow")
        XCTAssertEqual(unlocked.composerSession.composer, "Typed before the lock", "the restored draft reaches the composer")
    }

    func testUnlockingMidLaunchMergesWhatThisLaunchHeldOverWhatWasSaved() async throws {
        ComposerSendProtocol.state.script(Array(repeating: .offline, count: 40))
        let lock = ProtectedData()
        var store = temporaryStore()
        store.protectedDataIsAvailable = { !lock.locked }
        let session = try makeSession()
        store.save(.init(
            drafts: [session.id: "Saved draft", "computer::other": "Saved elsewhere"],
            stashes: ["computer::stashed": "Saved stash"],
            sends: [.init(id: UUID(), sessionID: session.id, message: "Saved send",idempotencyKey: "saved-key", uploaded: [], wire: nil, hadAttachments: false)]
        ))
        try FileManager.default.setAttributes([.posixPermissions: 0o000], ofItemAtPath: store.file.path)
        addTeardownBlock { try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path) }
        try XCTSkipIf(FileManager.default.isReadableFile(atPath: store.file.path), "this runner can read any file")

        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(model.operationError)
        XCTAssertTrue(model.composerSession.pendingSends.isEmpty)
        model.selectedSessionID = session.id
        model.selectedDetail = try makeDetail()
        model.composerSession.composer = "Sent while locked"
        model.composerSession.send(to: session)
        model.composerSession.composer = "Typed while locked"

        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path)
        lock.locked = false
        NotificationCenter.default.post(name: UIApplication.protectedDataDidBecomeAvailableNotification, object: nil)

        XCTAssertNil(model.operationError)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: store.file.deletingLastPathComponent().path), ["composer.json"], "the file did not move")
        XCTAssertEqual(model.composerSession.composer, "Saved draft\n\nTyped while locked")
        XCTAssertEqual(model.composerSession.pendingSends[session.id]?.map(\.message).sorted(), ["Saved send", "Sent while locked"])
        XCTAssertEqual(model.composerSession.stashes["computer::stashed"]?.text, "Saved stash")
        let saved = store.load().saved
        XCTAssertEqual(saved.drafts[session.id], "Saved draft\n\nTyped while locked", "what was held in memory is on disk now")
        XCTAssertEqual(saved.drafts["computer::other"], "Saved elsewhere")
        XCTAssertEqual(Set(saved.sends.map(\.message)), ["Saved send", "Sent while locked"])
        model.composerSession.composer = "Typed after"
        model.composerSession.saveComposerState()
        XCTAssertEqual(store.load().saved.drafts[session.id], "Typed after", "saving is on again")
    }

    func testSavedMessagesGoAheadOfOnesQueuedThisLaunch() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        var release = false
        queue.deliver = { send in
            delivered.append(send.message)
            return release ? .answered : .unconfirmed
        }
        queue.add(PendingSend(sessionID: "a", message: "New", attachments: []))
        try await waitUntil { delivered.count == 1 }
        queue.restore([PendingSend(sessionID: "a", message: "Saved", attachments: [])])
        XCTAssertEqual(queue.pending["a"]?.map(\.message), ["Saved", "New"])
        release = true
        queue.wake()
        try await waitUntil(seconds: 5) { queue.pending.isEmpty }
        XCTAssertEqual(delivered, ["New", "Saved", "New"])
    }

    func testAFileThatCannotBeMovedAsideIsNeverOverwrittenAndThePersonIsTold() throws {
        let store = temporaryStore()
        let directory = store.file.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let unreadable = Data("not the format".utf8)
        try unreadable.write(to: store.file)
        try FileManager.default.setAttributes([.posixPermissions: 0o500], ofItemAtPath: directory.path)
        addTeardownBlock { try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path) }
        try XCTSkipIf(FileManager.default.isWritableFile(atPath: directory.path), "this runner can write any directory")

        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertEqual(model.operationError, "Saved drafts couldn’t be read. Until they can, drafts and unconfirmed messages are lost if Agenthail closes.")
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
        model.selectedSessionID = "computer::codex-flow"
        model.composerSession.composer = "Typed after"
        model.composerSession.saveComposerState()
        XCTAssertEqual(try Data(contentsOf: store.file), unreadable)
    }

    func testAMissingFileIsNotAProblem() {
        let store = temporaryStore()
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(model.operationError)
    }

    func testAnAttachmentThatCouldNotBeKeptIsNamedOnReturn() async throws {
        var store = temporaryStore()
        store.writeSpool = { _, _ in throw CocoaError(.fileWriteOutOfSpace) }
        let (model, session) = try makeModel(store: store)
        model.composerSession.composer = "With a file"
        model.composerSession.composerAttachments = [ComposerAttachment(name: "plan.pdf", data: Data([1]), mediaType: "application/pdf")]
        model.composerSession.saveComposerState()

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composerSession.composer, "With a file")
        XCTAssertEqual(reopened.composerSession.composerAttachments, [])
        XCTAssertTrue(reopened.composerSession.attachmentProblem?.contains("plan.pdf") == true, "\(String(describing: reopened.composerSession.attachmentProblem))")
    }

    func testAStashedAttachmentSurvivesTheAppOrIsNamedWhenItCouldNotBeKept() async throws {
        for keeps in [true, false] {
            var store = temporaryStore()
            if !keeps { store.writeSpool = { _, _ in throw CocoaError(.fileWriteOutOfSpace) } }
            let (model, session) = try makeModel(store: store)
            model.composerSession.composer = "Stashed"
            model.composerSession.composerAttachments = [ComposerAttachment(name: "stashed.png", data: Data([1]), mediaType: "image/png")]
            model.composerSession.toggleStash()
            model.composerSession.saveComposerState()
            if keeps { try await waitUntil { store.load().saved.spooledStashes?[session.id] != nil } }

            let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
            XCTAssertEqual(reopened.composerSession.stashes[session.id]?.text, "Stashed")
            XCTAssertEqual(reopened.composerSession.stashes[session.id]?.attachments.map(\.data), keeps ? [Data([1])] : [])
            await reopened.loadSession(session.id)
            XCTAssertEqual(reopened.composerSession.attachmentProblem?.contains("stashed.png") == true, !keeps, "\(String(describing: reopened.composerSession.attachmentProblem))")
        }
    }

    func testAMessageFollowsItsSessionToANewID() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        var online = false
        queue.deliver = { send in
            delivered.append(send.sessionID)
            return online ? .answered : .unconfirmed
        }
        queue.add(PendingSend(sessionID: "old", message: "Moves", attachments: []))
        try await waitUntil { queue.pending["old"]?.first?.unconfirmed == true }
        var rekeys = SessionRekeys()
        rekeys.record(["old": "new"])
        online = true
        queue.carry(rekeys)
        XCTAssertNil(queue.pending["old"])
        XCTAssertEqual(queue.pending["new"]?.first?.sessionID, "new")
        queue.wake()
        try await waitUntil(seconds: 5) { queue.pending.isEmpty }
        XCTAssertEqual(delivered, ["old", "new"], "one worker keeps delivering, under the new id")
    }

    func testASendStopsRetryingAnHourAfterItWasSentAndATapTriesOnce() async throws {
        let queue = SendQueue()
        var clock = Date()
        queue.now = { clock }
        var attempts = 0
        var online = false
        queue.deliver = { _ in
            attempts += 1
            return online ? .answered : .unconfirmed
        }
        queue.add(PendingSend(sessionID: "a", message: "m", attachments: [],sentAt: clock))
        try await waitUntil { queue.pending["a"]?.first?.unconfirmed == true }
        queue.wake()
        try await waitUntil { attempts == 2 }
        let retrying = try XCTUnwrap(queue.pending["a"]?.first)
        XCTAssertEqual(retrying.unconfirmedTitle, "Not confirmed yet · retrying")
        XCTAssertFalse(retrying.offersRetry, "within the hour it retries by itself")

        clock = clock.addingTimeInterval(SendQueue.retryWindow)
        queue.wake()
        try await waitUntil { queue.pending["a"]?.first?.retryStopped == true }
        let stopped = attempts
        queue.wake()
        try await Task.sleep(for: .milliseconds(1200))
        XCTAssertEqual(attempts, stopped, "past the hour only a tap sends it")

        let send = try XCTUnwrap(queue.pending["a"]?.first)
        XCTAssertEqual(send.unconfirmedTitle, "Not confirmed")
        XCTAssertTrue(send.offersRetry, "past the hour the row offers Retry")
        queue.retry(send)
        try await waitUntil { attempts == stopped + 1 && queue.pending["a"]?.first?.retryStopped == true }
        try await Task.sleep(for: .milliseconds(1200))
        XCTAssertEqual(attempts, stopped + 1, "a tap is one attempt")

        online = true
        queue.retry(try XCTUnwrap(queue.pending["a"]?.first))
        try await waitUntil { queue.pending.isEmpty }
    }

    func testAWaitingSendIsNamedByItsFirstLine() {
        let send = PendingSend(sessionID: "a", message: "  Check the signing step\nthen rerun", attachments: [])
        XCTAssertEqual(send.previewLine, "Check the signing step")
        XCTAssertEqual(PendingSend(sessionID: "a", message: "", attachments: []).previewLine, "Message with attachments")
    }

    private func stoppedSend(_ session: String = "a", _ message: String = "old") -> PendingSend {
        PendingSend(sessionID: session, message: message, attachments: [],unconfirmed: true, sentAt: Date(timeIntervalSinceNow: -2 * SendQueue.retryWindow))
    }

    private func recentSend(_ session: String = "a", _ message: String = "new") -> PendingSend {
        PendingSend(sessionID: session, message: message, attachments: [])
    }

    func testATapDuringAnotherSendsBackoffGoesAtOnce() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        queue.deliver = { send in
            delivered.append(send.message)
            return send.message == "old" ? .answered : .unconfirmed
        }
        queue.restore([stoppedSend()])
        try await waitUntil { queue.pending["a"]?.first?.retryStopped == true }
        queue.add(recentSend())
        queue.wake()
        try await waitUntil { delivered.count >= 2 }
        delivered = []

        queue.retry(try XCTUnwrap(queue.pending["a"]?.first { $0.message == "old" }))
        try await waitUntil(seconds: 1.5) { delivered.first == "old" }
        try await waitUntil { queue.pending["a"]?.map(\.message) == ["new"] }
    }

    func testATapDuringAnotherSendsAttemptWaitsForItsTurn() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        var release = false
        queue.deliver = { send in
            delivered.append(send.message)
            while send.message == "new" && !release { try? await Task.sleep(for: .milliseconds(10)) }
            return .answered
        }
        queue.restore([stoppedSend()])
        try await waitUntil { queue.pending["a"]?.first?.retryStopped == true }
        queue.add(recentSend())
        try await waitUntil { delivered == ["new"] }

        queue.retry(try XCTUnwrap(queue.pending["a"]?.first { $0.message == "old" }))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(delivered, ["new"], "one attempt at a time in a session")
        release = true
        try await waitUntil { queue.pending.isEmpty }
        XCTAssertEqual(delivered, ["new", "old"])
    }

    func testARefusedTapIsReportedOnce() async throws {
        let queue = SendQueue()
        var refusals: [String] = []
        queue.refused = { send, reason in refusals.append("\(send.message): \(reason)") }
        queue.deliver = { _ in .refused("Ended") }
        queue.restore([stoppedSend()])
        try await waitUntil { queue.pending["a"]?.first?.retryStopped == true }
        queue.retry(try XCTUnwrap(queue.pending["a"]?.first))
        try await waitUntil { queue.pending.isEmpty }
        XCTAssertEqual(refusals, ["old: Ended"])
    }

    func testAQueueWithOnlyStoppedSendsMakesNoAttemptsAndRestartsWhenAsked() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        queue.deliver = { send in
            delivered.append(send.message)
            return .answered
        }
        queue.restore([stoppedSend()])
        try await waitUntil { queue.pending["a"]?.first?.retryStopped == true }
        queue.wake()
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(delivered, [], "nothing to send and nothing spinning")

        queue.add(recentSend())
        try await waitUntil { delivered == ["new"] }
        queue.restore([recentSend("a", "restored")])
        try await waitUntil { delivered == ["new", "restored"] }
        queue.retry(try XCTUnwrap(queue.pending["a"]?.first))
        try await waitUntil { queue.pending.isEmpty }
        XCTAssertEqual(delivered, ["new", "restored", "old"])
    }

    func testAStoppedSendStaysStoppedWhenItsSessionMovesAndCanStillBeRetried() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        queue.deliver = { send in
            delivered.append(send.sessionID)
            return .answered
        }
        queue.restore([stoppedSend("old-id")])
        try await waitUntil { queue.pending["old-id"]?.first?.retryStopped == true }
        var rekeys = SessionRekeys()
        rekeys.record(["old-id": "new-id"])
        queue.carry(rekeys)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(queue.pending["new-id"]?.first?.retryStopped, true)
        XCTAssertEqual(delivered, [])

        var stale = try XCTUnwrap(queue.pending["new-id"]?.first)
        stale.sessionID = "old-id"
        queue.retry(stale)
        try await waitUntil { queue.pending.isEmpty }
        XCTAssertEqual(delivered, ["new-id"], "a tap from a row drawn before the move reaches the moved send")
    }

    func testASessionMovingDuringATapStopsTheSendUnderItsNewID() async throws {
        let queue = SendQueue()
        var release = false
        var attempts = 0
        queue.deliver = { _ in
            attempts += 1
            while !release { try? await Task.sleep(for: .milliseconds(10)) }
            return .unconfirmed
        }
        queue.restore([stoppedSend("old-id")])
        try await waitUntil { queue.pending["old-id"]?.first?.retryStopped == true }
        queue.retry(try XCTUnwrap(queue.pending["old-id"]?.first))
        try await waitUntil { attempts == 1 }
        var rekeys = SessionRekeys()
        rekeys.record(["old-id": "new-id"])
        queue.carry(rekeys)
        release = true
        try await waitUntil { queue.pending["new-id"]?.first?.retryStopped == true }
        XCTAssertEqual(queue.pending["new-id"]?.first?.retryingOnTap, false)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(attempts, 1)
    }

    func testAPendingSendPreviewsItsFirstLine() {
        func preview(_ message: String) -> String {
            PendingSend(sessionID: "a", message: message, attachments: []).previewLine
        }
        XCTAssertEqual(preview("\n  First line  \nSecond"), "First line")
        XCTAssertEqual(preview(""), "Message with attachments")
    }

    func testAStoppedSendDoesNotHoldANewerOne() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        queue.deliver = { send in
            delivered.append(send.message)
            return .answered
        }
        queue.restore([PendingSend(sessionID: "a", message: "old", attachments: [],unconfirmed: true, sentAt: Date(timeIntervalSinceNow: -2 * SendQueue.retryWindow))])
        try await waitUntil { queue.pending["a"]?.first?.retryStopped == true }
        queue.add(PendingSend(sessionID: "a", message: "new", attachments: []))
        try await waitUntil { delivered == ["new"] }
        XCTAssertEqual(queue.pending["a"]?.map(\.message), ["old"])
    }

    func testAnOldSavedSendComesBackStoppedAndKeepsItsSentTime() async throws {
        let store = temporaryStore()
        let sent = Date(timeIntervalSinceNow: -2 * SendQueue.retryWindow)
        store.save(ComposerStore.Saved(sends: [.init(PendingSend(sessionID: "computer::demo", message: "Days old", attachments: [],sentAt: sent), spool: { _ in nil })]))
        XCTAssertEqual(store.load().saved.sends.first?.sentAt?.timeIntervalSince1970 ?? 0, sent.timeIntervalSince1970, accuracy: 0.01)

        let before = ComposerSendProtocol.state.attempts.count
        let model = try makeModel(store: store).model
        try await waitUntil { model.composerSession.pendingSends["computer::demo"]?.first?.retryStopped == true }
        XCTAssertEqual(model.deliveryStatus["computer::demo"], "Not confirmed")
        XCTAssertEqual(ComposerSendProtocol.state.attempts.count, before, "opening the app does not send it")

        let older = ComposerStore.Saved.Send(id: UUID(), sessionID: "s", message: "m",idempotencyKey: "k", uploaded: nil, wire: nil, hadAttachments: false)
        let decoded = try JSONDecoder().decode(ComposerStore.Saved.Send.self, from: JSONEncoder().encode(older))
        XCTAssertEqual(decoded.resumable(unspool: { _ in nil })?.sentAt, .distantPast, "a send saved with no time never arrives late by itself")
    }

    func testAReleasedQueueStopsRetrying() async throws {
        var attempts = 0
        var queue: SendQueue? = SendQueue()
        queue?.deliver = { _ in
            attempts += 1
            return .unconfirmed
        }
        queue?.add(PendingSend(sessionID: "a", message: "m", attachments: []))
        try await waitUntil { attempts == 1 }
        queue = nil
        try await Task.sleep(for: .milliseconds(2500))
        XCTAssertLessThanOrEqual(attempts, 1, "a queue nobody holds makes no further attempts")
    }

    func testAnAnswerIsDefiniteAndNoAnswerIsNot() {
        XCTAssertEqual(SendOutcome.of(URLError(.networkConnectionLost)), .unconfirmed)
        XCTAssertEqual(SendOutcome.of(URLError(.timedOut)), .unconfirmed)
        XCTAssertEqual(SendOutcome.of(URLError(.notConnectedToInternet)), .unconfirmed)
        XCTAssertEqual(SendOutcome.of(AgenthailAPIError.invalidResponse), .unconfirmed)
        XCTAssertEqual(SendOutcome.of(AgenthailAPIError.request(409, "ended")), .refused("ended"))
        XCTAssertEqual(SendOutcome.of(AgenthailAPIError.request(502, "down")), .refused("down"))
    }

    private func spooledFiles(_ store: ComposerStore) throws -> [String] {
        guard FileManager.default.fileExists(atPath: store.spoolFolder.path) else { return [] }
        return try FileManager.default.contentsOfDirectory(atPath: store.spoolFolder.path)
    }

    private func temporaryStore() -> ComposerStore {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        return ComposerStore(file: directory.appendingPathComponent("composer.json"))
    }

    private func makeAPI() -> AgenthailAPI {
        AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: FixtureScope.open(self).session([ComposerSendProtocol.self]))
    }

    private func makeDetail(status: String = "idle") throws -> SessionDetail {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as? [String: Any])
        var session = try XCTUnwrap(object["session"] as? [String: Any])
        session["id"] = "codex-flow"
        session["surface"] = "codex"
        session["status"] = status
        object["session"] = session
        var capabilities = try XCTUnwrap(object["capabilities"] as? [String: Any])
        capabilities["send"] = true
        object["capabilities"] = capabilities
        var detail = try JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: object))
        detail.session.id = "computer::codex-flow"
        return detail
    }

    private func makeSession() throws -> SessionState {
        let detail = try makeDetail()
        return SessionState(id: detail.session.id, surface: detail.session.surface, name: detail.session.name, alias: detail.alias,
                            status: detail.session.status, lastActive: nil, queueCount: 0, open: true, current: true,
                            currentReason: nil, capabilities: detail.capabilities, readOnly: false,
                            readOnlyReason: "", cwd: detail.session.cwd)
    }

    private let keptRefusal = #"{"error":{"code":"conflict","message":"steer requires a message, not a slash command"},"askId":"ask-1"}"#
    private let deleteOffered = #"{"permissionMode":"","requests":[{"id":"ask-1","kind":"ask","tool":"","questions":[{"question":"Message not delivered","header":"","multiSelect":false,"options":[{"id":"delete","label":"Delete","description":""},{"id":"dismiss","label":"Dismiss","description":""}]}]}]}"#

    func testDeleteOnARefusedSendsPinnedAskRemovesTheRefusedSend() async throws {
        ComposerSendProtocol.state.script([.answer(409, keptRefusal), .answer(200, #"{"ok":true,"result":{"requestId":"ask-1","outcome":"answered","choice":"delete"}}"#)])
        ComposerSendProtocol.state.listRequests(deleteOffered)
        let (model, session) = try makeModel()
        model.composerSession.composer = "/srv/footage holds 680 GiB"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.refusedSends[session.id]?.ask?.offersDelete == true }
        let request = SessionRequest(id: "ask-1", kind: "ask", tool: "", questions: [SessionRequest.Question(question: "Message not delivered", header: "", multiSelect: false, options: [SessionRequest.Option(id: "delete", label: "Delete", description: "")])])
        await model.respond(to: request, sessionID: session.id, decision: .choose(FailedSendAsk.delete))
        XCTAssertNil(model.composerSession.refusedSends[session.id], "Delete on the pinned ask removes the refused send it named")
    }

    private func refusedSend(settledReply: String) async throws -> (AgenthailIOSModel, SessionState) {
        ComposerSendProtocol.state.script([.answer(409, keptRefusal), .answer(409, settledReply)])
        ComposerSendProtocol.state.listRequests(deleteOffered)
        let (model, session) = try makeModel()
        model.composerSession.composer = "/srv/footage holds 680 GiB"
        model.composerSession.send(to: session)
        try await waitUntil { model.composerSession.refusedSends[session.id]?.canDelete == true }
        return (model, session)
    }

    private let staleFields = #""ok":false,"error":{"code":"stale","message":"this request was already answered or has expired"}"#

    func testDeleteOnARefusedSendWhoseAskWasDeletedElsewhereRemovesIt() async throws {
        let (model, session) = try await refusedSend(settledReply: "{\(staleFields),\"settled\":\"delete\"}")
        model.composerSession.deleteRefusedSend(in: session.id)
        try await waitUntil { model.composerSession.refusedSends[session.id] == nil }
        XCTAssertNil(model.operationError, "an ask deleted elsewhere is not reported as a failure")
    }

    func testDeleteOnARefusedSendThatWasRetriedElsewhereSaysItWasSentAgain() async throws {
        let (model, session) = try await refusedSend(settledReply: "{\(staleFields),\"settled\":\"retry\"}")
        model.composerSession.deleteRefusedSend(in: session.id)
        try await waitUntil { model.composerSession.refusedSends[session.id] == nil }
        XCTAssertEqual(model.operationError, SettledAsk.resentNotice)
    }

    func testDeleteFromANotificationRemovesTheRefusedSend() async throws {
        let (model, session) = try await refusedSend(settledReply: "{\(staleFields)}")
        NotificationCenter.default.post(name: .agenthailRequestAnswered, object: AnsweredRequest(requestID: "ask-1", choice: FailedSendAsk.delete))
        try await waitUntil { model.composerSession.refusedSends[session.id] == nil }
    }

    private func makeModel(store: ComposerStore? = nil) throws -> (model: AgenthailIOSModel, session: SessionState) {
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        let session = try makeSession()
        model.selectedSessionID = session.id
        model.selectedDetail = try makeDetail()
        return (model, session)
    }

    private func waitUntil(seconds: Double = 2, _ condition: () -> Bool) async throws {
        let deadline = Date().addingTimeInterval(seconds)
        while !condition(), Date() < deadline { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(condition(), "timed out")
    }
}

private final class ComposerSendProtocol: URLProtocol, @unchecked Sendable {
    enum Step {
        case answer(Int, String)
        case lostResponse
        case offline
    }

    enum UploadStep {
        case stored(String)
        case offline
        case held(String)
    }

    struct Attempt {
        let key: String?
        let raw: Data
        var body: [String: Any] { (try? JSONSerialization.jsonObject(with: raw) as? [String: Any]) ?? [:] }
    }

    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var steps: [Step] = []
        private var recorded: [Attempt] = []
        private var uploadSteps: [UploadStep] = []
        private var uploadNames: [String] = []
        private var requestsBody = "{}"
        private var parked: (() -> Void)?

        var attempts: [Attempt] { lock.withLock { recorded } }
        var uploads: [String] { lock.withLock { uploadNames } }

        func reset() { lock.withLock { steps = []; recorded = []; uploadSteps = []; uploadNames = []; requestsBody = "{}"; parked = nil } }
        func listRequests(_ body: String) { lock.withLock { requestsBody = body } }
        var requests: String { lock.withLock { requestsBody } }
        func script(_ steps: [Step]) { lock.withLock { self.steps = steps } }
        func scriptUploads(_ steps: [UploadStep]) { lock.withLock { uploadSteps = steps } }
        var holding: Bool { lock.withLock { parked != nil } }
        func park(_ answer: @escaping () -> Void) { lock.withLock { parked = answer } }
        func releaseHeld() { lock.withLock { defer { parked = nil }; return parked }?() }

        func nextUpload(_ name: String) -> UploadStep {
            lock.withLock {
                uploadNames.append(name)
                return uploadSteps.isEmpty ? .offline : uploadSteps.removeFirst()
            }
        }

        func next(_ attempt: Attempt) -> Step {
            lock.withLock {
                recorded.append(attempt)
                return steps.isEmpty ? .offline : steps.removeFirst()
            }
        }
    }

    static let state = State()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() { served(answer) }

    private func answer() {
        if request.url?.path == "/api/v1/uploads" { return answerUpload() }
        if request.url?.path == "/api/v1/requests" { return respond(200, Self.state.requests) }
        guard request.url?.path == "/api/v1/actions" else { return respond(200, "{}") }
        switch Self.state.next(Attempt(key: request.value(forHTTPHeaderField: "Idempotency-Key"), raw: requestBody() ?? Data())) {
        case .answer(let status, let text): respond(status, text)
        case .lostResponse: client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
        case .offline: client?.urlProtocol(self, didFailWithError: URLError(.notConnectedToInternet))
        }
    }

    private func answerUpload() {
        let name = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "name" }?.value ?? ""
        switch Self.state.nextUpload(name) {
        case .stored(let id): respond(200, #"{"upload":{"id":"\#(id)","name":"\#(name)","mediaType":"text/plain","bytes":5}}"#)
        case .held(let id): Self.state.park { [self] in respond(200, #"{"upload":{"id":"\#(id)","name":"\#(name)","mediaType":"text/plain","bytes":5}}"#) }
        case .offline: client?.urlProtocol(self, didFailWithError: URLError(.notConnectedToInternet))
        }
    }

    private func respond(_ status: Int, _ text: String) {
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    private func requestBody() -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            data.append(buffer, count: count)
        }
        return data
    }
}
