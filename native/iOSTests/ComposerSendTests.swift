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
        model.composer = "Sent"
        model.send(to: session)
        XCTAssertEqual(model.composer, "")
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Sent")
        XCTAssertNil(model.failedSends[session.id])
    }

    func testAQueuedMessageClearsTheDraftAndSaysItIsQueued() async throws {
        ComposerSendProtocol.state.script([.answer(200, queuedReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Queued"
        model.send(to: session)
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Queued for fixture.")
        XCTAssertNil(model.failedSends[session.id])
    }

    func testARefusedMessageReturnsWithAReasonAndRetryUsesANewKey() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"Session ended"}}"#), .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Refused"
        model.send(to: session)
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(model.composer, "Refused")
        XCTAssertNotNil(model.failedSends[session.id])
        XCTAssertNil(model.operationError, "the reason stays on the composer of the session on screen")

        model.send(to: session)
        XCTAssertNil(model.failedSends[session.id])
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 2 }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertNotEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Sent")
    }

    func testARefusalForASessionOffScreenRaisesAnAlert() async throws {
        ComposerSendProtocol.state.script([.answer(409, #"{"error":{"message":"Session ended"}}"#)])
        let (model, session) = try makeModel()
        model.composer = "Refused"
        model.send(to: session)
        model.selectedSessionID = "computer::other"
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertNotNil(model.operationError)
        XCTAssertNotNil(model.failedSends[session.id])
    }

    func testALostResponseKeepsTheDraftClearAndRetriesWithTheSameKey() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, queuedReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Arrived, answer lost"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        XCTAssertEqual(model.composer, "", "the message may have arrived, so it does not come back")
        XCTAssertEqual(model.deliveryStatus[session.id], "Not confirmed yet · retrying")
        XCTAssertNil(model.failedSends[session.id])
        try await waitUntil(seconds: 5) { model.pendingSends.isEmpty }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(attempts[0].body["message"] as? String, attempts[1].body["message"] as? String)
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.deliveryStatus[session.id], "Queued for fixture.")
    }

    func testOfflineMessagesWaitInOrderWhileTheComposerStaysUsable() async throws {
        ComposerSendProtocol.state.script([.offline, .answer(200, sentReceipt), .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composer = "First"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        model.composer = "Second"
        model.send(to: session)
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.pendingSends[session.id]?.map(\.message), ["First", "Second"])
        try await waitUntil(seconds: 5) { model.pendingSends.isEmpty }
        XCTAssertEqual(ComposerSendProtocol.state.attempts.map { $0.body["message"] as? String }, ["First", "First", "Second"])
    }

    func testAWaitingMessageCanBePutBack() async throws {
        ComposerSendProtocol.state.script([.offline, .offline, .offline, .offline])
        let (model, session) = try makeModel()
        model.composer = "Take it back"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        model.composer = "newer"
        model.putBack(try XCTUnwrap(model.pendingSends[session.id]?.first))
        XCTAssertEqual(model.composer, "Take it back\n\nnewer")
        XCTAssertTrue(model.pendingSends.isEmpty)
        XCTAssertEqual(model.deliveryStatus[session.id], "Put back. It may already have arrived.")
    }

    func testASendThatCannotStartSaysSoAndKeepsTheDraft() throws {
        let (model, session) = try makeModel()
        model.selectedDetail = nil
        model.composer = "Keep me"
        model.send(to: session)
        XCTAssertEqual(model.composer, "Keep me")
        XCTAssertNotNil(model.failedSends[session.id])
        XCTAssertTrue(model.pendingSends.isEmpty)
    }

    func testAPendingMessageResumesAfterTheAppIsKilledWithItsKey() async throws {
        let store = temporaryStore()
        ComposerSendProtocol.state.script([.offline, .offline, .offline, .offline])
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        first?.composer = "Survives"
        first?.send(to: session)
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
        try await waitUntil(seconds: 5) { second.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[0].key, key)
        XCTAssertEqual(ComposerSendProtocol.state.attempts[0].raw, raw, "the resumed message is the same request, byte for byte")
        XCTAssertEqual(store.load().saved.sends, [])
    }

    private func queuedItem(_ session: SessionState, attachments: String = "") throws -> QueueState {
        var item = try JSONDecoder().decode(QueueState.self, from: Data(#"{"id":9,"sessionId":"\#(session.id)","target":"t","message":"from the row","status":"pending","attempts":0,"queuedAt":"2026-11-04 10:00:00","evidence":"queued"\#(attachments)}"#.utf8))
        item.hostID = "computer"
        return item
    }

    private let cancelledWithAttachments = #"{"ok":true,"result":{"message":"from the reply","attachments":[{"id":"up-1","name":"shot.png","mediaType":"image/png","bytes":2048,"width":10,"height":10},{"id":"up-2","name":"gone.pdf","mediaType":"application/pdf","bytes":9,"missing":true}]}}"#

    func testTakingBackRestoresAttachmentsAndNamesAMissingOne() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, cancelledWithAttachments), .answer(200, sentReceipt)])
        try await model.takeBack(queuedItem(session))
        XCTAssertEqual(model.composer, "from the reply")
        XCTAssertEqual(model.composerAttachments.map(\.uploaded?.id), ["up-1"])
        XCTAssertEqual(model.composerAttachments.first?.byteCount, 2048)
        XCTAssertTrue(model.attachmentProblem?.contains("gone.pdf") == true, String(describing: model.attachmentProblem))

        model.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.attempts.count == 2 && model.pendingSends.isEmpty }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[1].body["attachments"] as? [String], ["up-1"], "a taken-back attachment goes by its id, with no upload")
    }

    func testTakingBackFromAnOlderComputerKeepsTheRowsMessageAndAttachments() async throws {
        let (model, session) = try makeModel(store: temporaryStore())
        ComposerSendProtocol.state.script([.answer(200, #"{"ok":true}"#)])
        try await model.takeBack(queuedItem(session, attachments: #","attachments":[{"id":"up-3","name":"row.png","mediaType":"image/png","bytes":1}]"#))
        XCTAssertEqual(model.composer, "from the row")
        XCTAssertEqual(model.composerAttachments.map(\.uploaded?.id), ["up-3"])
    }

    func testATakenBackAttachmentSurvivesTheAppBeingKilled() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        ComposerSendProtocol.state.script([.answer(200, cancelledWithAttachments)])
        try await model.takeBack(queuedItem(session))
        model.saveComposerState()
        XCTAssertEqual(store.load().saved.attachmentNames, [:], "it isn't reported lost")

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composerAttachments.map(\.uploaded?.id), ["up-1"])
        XCTAssertEqual(reopened.composerAttachments.first?.name, "shot.png")
        XCTAssertEqual(reopened.composerAttachments.first?.mediaType, "image/png")
    }

    func testAStashedTakenBackAttachmentSurvivesTheAppBeingKilled() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        ComposerSendProtocol.state.script([.answer(200, cancelledWithAttachments)])
        try await model.takeBack(queuedItem(session))
        model.toggleStash()
        XCTAssertTrue(model.composerAttachments.isEmpty)
        model.saveComposerState()

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertTrue(reopened.composerAttachments.isEmpty)
        reopened.toggleStash()
        XCTAssertEqual(reopened.composerAttachments.map(\.uploaded?.id), ["up-1"])
    }

    func testAKilledSendOfATakenBackAttachmentResumesWithItsID() async throws {
        let store = temporaryStore()
        var first: AgenthailIOSModel? = try makeModel(store: store).model
        let session = try makeSession()
        ComposerSendProtocol.state.script([.answer(200, cancelledWithAttachments), .offline, .offline])
        try await first?.takeBack(queuedItem(session))
        first?.send(to: session)
        try await waitUntil { ComposerSendProtocol.state.attempts.count >= 2 }
        first?.saveComposerState()
        first = nil
        XCTAssertEqual(store.load().saved.sends.first?.uploaded, ["up-1"])

        ComposerSendProtocol.state.reset()
        ComposerSendProtocol.state.script([.answer(200, sentReceipt)])
        let second = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        second.selectedSessionID = session.id
        try await waitUntil(seconds: 5) { second.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        XCTAssertEqual(ComposerSendProtocol.state.attempts[0].body["attachments"] as? [String], ["up-1"])
        XCTAssertNil(second.failedSends[session.id])
    }

    func testADraftSurvivesTheAppBeingKilled() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composer = "Typed, not sent"
        model.saveComposerState()
        XCTAssertEqual(store.load().saved.drafts[session.id], "Typed, not sent")

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composer, "Typed, not sent")
    }

    func testTypingReachesTheStoreWithoutAnExplicitSave() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composer = "Typed"
        try await waitUntil(seconds: 3) { store.load().saved.drafts[session.id] == "Typed" }
    }

    func testAKilledSendWhoseAttachmentNeverUploadedComesBackAsADraft() async throws {
        let store = temporaryStore()
        store.save(.init(sends: [.init(id: UUID(), sessionID: "computer::codex-flow", message: "With a file", turnSettings: TurnSettings(), idempotencyKey: "k", uploaded: nil, wire: nil, hadAttachments: true)]))
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertTrue(model.pendingSends.isEmpty)
        XCTAssertNotNil(model.failedSends["computer::codex-flow"])
        await model.loadSession("computer::codex-flow")
        XCTAssertEqual(model.composer, "With a file")
    }

    func testARekeyWhileWaitingRetriesTheSameRequest() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        model.composer = "Rekeyed while waiting"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        model.host(try XCTUnwrap(model.connection("computer")), rekeyed: ["codex-flow": "codex-moved"])
        XCTAssertNotNil(model.pendingSends["computer::codex-moved"])
        try await waitUntil(seconds: 5) { model.pendingSends.isEmpty }
        let attempts = ComposerSendProtocol.state.attempts
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts[0].key, attempts[1].key)
        XCTAssertEqual(attempts[0].raw, attempts[1].raw, "a changed body under the same key is refused as a mismatch")
    }

    func testChangingTheTerminalWhileWaitingRetriesTheFirstRequest() async throws {
        ComposerSendProtocol.state.script([.lostResponse, .answer(200, sentReceipt)])
        let (model, session) = try makeModel()
        let route = "codex-flow"
        defer { HolderPins.shared.pick(nil, for: route) }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 11, status: "idle")), for: route)
        model.composer = "To the first terminal"
        model.send(to: session)
        try await waitUntil { model.pendingSends[session.id]?.first?.unconfirmed == true }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 22, status: "idle")), for: route)
        try await waitUntil(seconds: 5) { model.pendingSends.isEmpty }
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
        model.composer = "Refused"
        model.send(to: session)
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 1 }
        HolderPins.shared.pick(HolderPin(SessionHolder(pid: 22, status: "idle")), for: route)
        model.send(to: session)
        try await waitUntil { model.pendingSends.isEmpty && ComposerSendProtocol.state.attempts.count == 2 }
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
        model.composer = "Typed after"
        model.saveComposerState()
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
        first?.composer = "Sent after"
        first?.send(to: session)
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
        first.composer = "Typed before the lock"
        first.saveComposerState()
        let directory = store.file.deletingLastPathComponent()
        try FileManager.default.setAttributes([.posixPermissions: 0o000], ofItemAtPath: store.file.path)
        addTeardownBlock { try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path) }
        try XCTSkipIf(FileManager.default.isReadableFile(atPath: store.file.path), "this runner can read any file")

        lock.locked = true
        let locked = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(locked.operationError, "a locked phone heals itself, so there is nothing to tell the person")
        locked.selectedSessionID = "computer::codex-flow"
        locked.composer = "Typed while locked"
        locked.saveComposerState()
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path), ["composer.json"], "a file that may be fine is not moved")

        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path)
        lock.locked = false
        XCTAssertEqual(store.load().saved.drafts["computer::codex-flow"], "Typed before the lock", "nothing overwrote it")
        let unlocked = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(unlocked.operationError)
        await unlocked.loadSession("computer::codex-flow")
        XCTAssertEqual(unlocked.composer, "Typed before the lock", "the restored draft reaches the composer")
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
            sends: [.init(id: UUID(), sessionID: session.id, message: "Saved send", turnSettings: TurnSettings(), idempotencyKey: "saved-key", uploaded: [], wire: nil, hadAttachments: false)]
        ))
        try FileManager.default.setAttributes([.posixPermissions: 0o000], ofItemAtPath: store.file.path)
        addTeardownBlock { try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path) }
        try XCTSkipIf(FileManager.default.isReadableFile(atPath: store.file.path), "this runner can read any file")

        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(model.operationError)
        XCTAssertTrue(model.pendingSends.isEmpty)
        model.selectedSessionID = session.id
        model.selectedDetail = try makeDetail()
        model.composer = "Sent while locked"
        model.send(to: session)
        model.composer = "Typed while locked"

        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: store.file.path)
        lock.locked = false
        NotificationCenter.default.post(name: UIApplication.protectedDataDidBecomeAvailableNotification, object: nil)

        XCTAssertNil(model.operationError)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: store.file.deletingLastPathComponent().path), ["composer.json"], "the file did not move")
        XCTAssertEqual(model.composer, "Saved draft\n\nTyped while locked")
        XCTAssertEqual(model.pendingSends[session.id]?.map(\.message).sorted(), ["Saved send", "Sent while locked"])
        XCTAssertEqual(model.stashes["computer::stashed"]?.text, "Saved stash")
        let saved = store.load().saved
        XCTAssertEqual(saved.drafts[session.id], "Saved draft\n\nTyped while locked", "what was held in memory is on disk now")
        XCTAssertEqual(saved.drafts["computer::other"], "Saved elsewhere")
        XCTAssertEqual(Set(saved.sends.map(\.message)), ["Saved send", "Sent while locked"])
        model.composer = "Typed after"
        model.saveComposerState()
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
        queue.add(PendingSend(sessionID: "a", message: "New", attachments: [], turnSettings: TurnSettings()))
        try await waitUntil { delivered.count == 1 }
        queue.restore([PendingSend(sessionID: "a", message: "Saved", attachments: [], turnSettings: TurnSettings())])
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
        model.composer = "Typed after"
        model.saveComposerState()
        XCTAssertEqual(try Data(contentsOf: store.file), unreadable)
    }

    func testAMissingFileIsNotAProblem() {
        let store = temporaryStore()
        let model = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertNil(model.operationError)
    }

    func testAttachmentsLostWithTheAppAreNamedOnReturn() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composer = "With a file"
        model.composerAttachments = [ComposerAttachment(name: "plan.pdf", data: Data([1]), mediaType: "application/pdf")]
        model.saveComposerState()

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        await reopened.loadSession(session.id)
        XCTAssertEqual(reopened.composer, "With a file")
        XCTAssertEqual(reopened.composerAttachments, [])
        XCTAssertTrue(reopened.attachmentProblem?.contains("plan.pdf") == true, "\(String(describing: reopened.attachmentProblem))")
    }

    func testAStashedAttachmentLostWithTheAppIsNamedOnReturn() async throws {
        let store = temporaryStore()
        let (model, session) = try makeModel(store: store)
        model.composer = "Stashed"
        model.composerAttachments = [ComposerAttachment(name: "stashed.png", data: Data([1]), mediaType: "image/png")]
        model.toggleStash()
        model.saveComposerState()

        let reopened = AgenthailIOSModel(api: makeAPI(), composerStore: store)
        XCTAssertEqual(reopened.stashes[session.id]?.text, "Stashed")
        await reopened.loadSession(session.id)
        XCTAssertTrue(reopened.attachmentProblem?.contains("stashed.png") == true, "\(String(describing: reopened.attachmentProblem))")
    }

    func testAMessageFollowsItsSessionToANewID() async throws {
        let queue = SendQueue()
        var delivered: [String] = []
        var online = false
        queue.deliver = { send in
            delivered.append(send.sessionID)
            return online ? .answered : .unconfirmed
        }
        queue.add(PendingSend(sessionID: "old", message: "Moves", attachments: [], turnSettings: TurnSettings()))
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
        queue.add(PendingSend(sessionID: "a", message: "m", attachments: [], turnSettings: TurnSettings(), sentAt: clock))
        try await waitUntil { queue.pending["a"]?.first?.unconfirmed == true }
        queue.wake()
        try await waitUntil { attempts == 2 }

        clock = clock.addingTimeInterval(SendQueue.retryWindow)
        queue.wake()
        try await waitUntil { queue.pending["a"]?.first?.retryStopped == true }
        let stopped = attempts
        queue.wake()
        try await Task.sleep(for: .milliseconds(1200))
        XCTAssertEqual(attempts, stopped, "past the hour only a tap sends it")

        let send = try XCTUnwrap(queue.pending["a"]?.first)
        queue.retry(send)
        try await waitUntil { attempts == stopped + 1 && queue.pending["a"]?.first?.retryStopped == true }
        try await Task.sleep(for: .milliseconds(1200))
        XCTAssertEqual(attempts, stopped + 1, "a tap is one attempt")

        online = true
        queue.retry(try XCTUnwrap(queue.pending["a"]?.first))
        try await waitUntil { queue.pending.isEmpty }
    }

    private func stoppedSend(_ session: String = "a", _ message: String = "old") -> PendingSend {
        PendingSend(sessionID: session, message: message, attachments: [], turnSettings: TurnSettings(), unconfirmed: true, sentAt: Date(timeIntervalSinceNow: -2 * SendQueue.retryWindow))
    }

    private func recentSend(_ session: String = "a", _ message: String = "new") -> PendingSend {
        PendingSend(sessionID: session, message: message, attachments: [], turnSettings: TurnSettings())
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
            PendingSend(sessionID: "a", message: message, attachments: [], turnSettings: TurnSettings()).previewLine
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
        queue.restore([PendingSend(sessionID: "a", message: "old", attachments: [], turnSettings: TurnSettings(), unconfirmed: true, sentAt: Date(timeIntervalSinceNow: -2 * SendQueue.retryWindow))])
        try await waitUntil { queue.pending["a"]?.first?.retryStopped == true }
        queue.add(PendingSend(sessionID: "a", message: "new", attachments: [], turnSettings: TurnSettings()))
        try await waitUntil { delivered == ["new"] }
        XCTAssertEqual(queue.pending["a"]?.map(\.message), ["old"])
    }

    func testAnOldSavedSendComesBackStoppedAndKeepsItsSentTime() async throws {
        let store = temporaryStore()
        let sent = Date(timeIntervalSinceNow: -2 * SendQueue.retryWindow)
        store.save(ComposerStore.Saved(sends: [.init(PendingSend(sessionID: "computer::demo", message: "Days old", attachments: [], turnSettings: TurnSettings(), sentAt: sent))]))
        XCTAssertEqual(store.load().saved.sends.first?.sentAt?.timeIntervalSince1970 ?? 0, sent.timeIntervalSince1970, accuracy: 0.01)

        let before = ComposerSendProtocol.state.attempts.count
        let model = try makeModel(store: store).model
        try await waitUntil { model.pendingSends["computer::demo"]?.first?.retryStopped == true }
        XCTAssertEqual(model.deliveryStatus["computer::demo"], "Not confirmed")
        XCTAssertEqual(ComposerSendProtocol.state.attempts.count, before, "opening the app does not send it")

        let older = ComposerStore.Saved.Send(id: UUID(), sessionID: "s", message: "m", turnSettings: TurnSettings(), idempotencyKey: "k", uploaded: nil, wire: nil, hadAttachments: false)
        let decoded = try JSONDecoder().decode(ComposerStore.Saved.Send.self, from: JSONEncoder().encode(older))
        XCTAssertEqual(decoded.resumable?.sentAt, .distantPast, "a send saved with no time never arrives late by itself")
    }

    func testAReleasedQueueStopsRetrying() async throws {
        var attempts = 0
        var queue: SendQueue? = SendQueue()
        queue?.deliver = { _ in
            attempts += 1
            return .unconfirmed
        }
        queue?.add(PendingSend(sessionID: "a", message: "m", attachments: [], turnSettings: TurnSettings()))
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

    private func temporaryStore() -> ComposerStore {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        return ComposerStore(file: directory.appendingPathComponent("composer.json"))
    }

    private func makeAPI() -> AgenthailAPI {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ComposerSendProtocol.self]
        return AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: configuration))
    }

    private func makeDetail() throws -> SessionDetail {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as? [String: Any])
        var session = try XCTUnwrap(object["session"] as? [String: Any])
        session["id"] = "codex-flow"
        session["surface"] = "codex"
        session["status"] = "idle"
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

    struct Attempt {
        let key: String?
        let raw: Data
        var body: [String: Any] { (try? JSONSerialization.jsonObject(with: raw) as? [String: Any]) ?? [:] }
    }

    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var steps: [Step] = []
        private var recorded: [Attempt] = []

        var attempts: [Attempt] { lock.withLock { recorded } }

        func reset() { lock.withLock { steps = []; recorded = [] } }
        func script(_ steps: [Step]) { lock.withLock { self.steps = steps } }

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

    override func startLoading() {
        guard request.url?.path == "/api/v1/actions" else { return respond(200, "{}") }
        switch Self.state.next(Attempt(key: request.value(forHTTPHeaderField: "Idempotency-Key"), raw: requestBody() ?? Data())) {
        case .answer(let status, let text): respond(status, text)
        case .lostResponse: client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
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
