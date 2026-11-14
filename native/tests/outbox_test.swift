@testable import AgenthailHelpers
import Foundation

@main
struct OutboxTest {
    @MainActor
    static func main() async {
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent("outbox-test-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        let store = ComposerStore(file: folder.appendingPathComponent("composer.json"))
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))

        StubDaemon.reset { message, attempt in message == "first" && attempt == 1 ? .drop : .answer }
        let model = AgenthailModel(connecting: false, api: api, composerStore: store)
        model.send("first", to: "S", busyDelivery: "queue")
        check(await wait { model.localSends["S"]?.first?.unconfirmed == true }, "a send with no answer is shown as not confirmed")
        check(model.localSends["S"]?.first?.unconfirmedTitle == "Not confirmed yet · retrying", "while it retries the row says so: \(String(describing: model.localSends["S"]))")
        let saved = store.load().saved.sends
        let firstAttempt = StubDaemon.sends(of: "first").first
        check(saved.count == 1 && saved.first?.idempotencyKey == firstAttempt?.key && saved.first?.busyDelivery == "queue", "an unconfirmed send is saved with its key: \(saved)")
        check(await wait(seconds: 5) { model.localSends["S"]?.first?.status != nil }, "the retry is answered")
        let attempts = StubDaemon.sends(of: "first")
        check(attempts.count == 2 && attempts[0].key == attempts[1].key && attempts[0].body == attempts[1].body, "an unconfirmed send is retried with the same key and the same bytes: \(attempts.map(\.key))")
        check(model.localSends["S"]?.first?.unconfirmed == false, "an answered retry is no longer unconfirmed")
        check(await wait { store.load().saved.sends.isEmpty }, "an answered send leaves the saved file")

        StubDaemon.reset { message, _ in message == "refused" ? .refuse : .answer }
        model.send("refused", to: "S")
        check(await wait { model.localSends["S"]?.first(where: { $0.text == "refused" })?.failure != nil }, "a refusal is shown on the row")
        check(await wait { store.load().saved.sends.isEmpty }, "a refused send is not kept for retry")
        check(StubDaemon.sends(of: "refused").count == 1, "a refused send is never retried by itself: \(StubDaemon.sends(of: "refused").count)")

        StubDaemon.reset { message, attempt in message == "host unsure" && attempt == 1 ? .unknown : .answer }
        model.send("host unsure", to: "S")
        check(await wait(seconds: 5) { model.localSends["S"]?.first(where: { $0.text == "host unsure" })?.status != nil }, "a host whose outcome is unknown is retried until it answers")
        let unsure = StubDaemon.sends(of: "host unsure")
        check(unsure.count == 2 && unsure[0].key == unsure[1].key && unsure[0].body == unsure[1].body, "an unknown host outcome may have arrived, so it is retried under the same key and bytes, never a new one: \(unsure.map(\.key))")
        check(model.localSends["S"]?.first(where: { $0.text == "host unsure" })?.failure == nil, "an unknown host outcome is never shown as refused")

        model.draft(for: "S").text = "half typed"
        check(await wait { store.load().saved.drafts["S"] == "half typed" }, "a typed draft is saved")

        let restoredWire = try! api.instructionWire(sessionID: "R", message: "restored", author: "person", attachments: [], idempotencyKey: "k-restored")
        let overdueWire = try! api.instructionWire(sessionID: "T", message: "overdue", author: "person", attachments: [], idempotencyKey: "k-overdue")
        var previous = ComposerStore.Saved(drafts: ["R": "kept draft"], stashes: ["R": "kept stash"])
        previous.attachmentNames = ["R": ["notes.txt"]]
        previous.sends = [
            .init(id: UUID(), sessionID: "R", message: "restored", idempotencyKey: "k-restored", uploaded: [], wire: restoredWire, hadAttachments: false, sentAt: Date()),
            .init(id: UUID(), sessionID: "T", message: "overdue", idempotencyKey: "k-overdue", uploaded: [], wire: overdueWire, hadAttachments: false, sentAt: Date(timeIntervalSinceNow: -2 * SendQueue.retryWindow)),
            .init(id: UUID(), sessionID: "U", message: "lost upload", idempotencyKey: "k-lost", uploaded: nil, wire: nil, hadAttachments: true, sentAt: Date()),
            .init(id: UUID(), sessionID: "F", message: "with file", idempotencyKey: "k-file", uploaded: ["up-1"], wire: nil, hadAttachments: true, sentAt: Date(), files: [UploadedAttachment(id: "up-1", name: "notes.txt", mediaType: "text/plain", bytes: 3)])
        ]
        let relaunchStore = ComposerStore(file: folder.appendingPathComponent("relaunch.json"))
        relaunchStore.save(previous)
        StubDaemon.reset { _, _ in .answer }
        let relaunched = AgenthailModel(connecting: false, api: api, composerStore: relaunchStore)
        check(relaunchStore.load().saved.drafts["U"] == "lost upload", "the text of a send that cannot resume is in the first save after a relaunch, so a kill then loses nothing: \(relaunchStore.load().saved.drafts)")
        check(relaunched.draft(for: "R").text == "kept draft" && relaunched.draft(for: "R").stash?.text == "kept stash", "drafts and stashes come back after a relaunch")
        check(await wait(seconds: 3) { relaunched.localSends["R"]?.first?.status != nil }, "a saved send is delivered after a relaunch: \(String(describing: relaunched.localSends["R"]))")
        let resent = StubDaemon.sends(of: "restored")
        check(resent.count == 1 && resent.first?.key == "k-restored" && resent.first?.body == restoredWire.body, "a saved send goes out under its saved key and bytes: \(resent.map(\.key))")
        check(relaunched.localSends["F"]?.first?.files.map(\.name) == ["notes.txt"], "a saved send whose file uploaded comes back with its file chip: \(String(describing: relaunched.localSends["F"]))")
        check(await wait(seconds: 3) { relaunched.localSends["F"]?.first?.status != nil }, "a saved send whose file uploaded is delivered")
        let withFile = StubDaemon.sends(of: "with file").first
        let withFileBody = withFile.flatMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
        check(withFile?.key == "k-file" && withFileBody?["attachments"] as? [String] == ["up-1"], "it goes out under its saved key with the uploaded file, not uploaded again: \(String(describing: withFileBody))")
        check(relaunched.draft(for: "U").text == "lost upload", "a send whose attachment never uploaded comes back to the composer")
        check(relaunched.operationError?.contains("Agenthail closed before its attachment uploaded") == true && relaunched.operationError?.contains("notes.txt") == true, "the person is told what to attach again: \(String(describing: relaunched.operationError))")
        check(StubDaemon.sends(of: "lost upload").isEmpty, "a send whose attachment never uploaded is not sent without it")

        let overdue = relaunched.localSends["T"]?.first
        check(overdue?.retryStopped == true && overdue?.unconfirmedTitle == "Not confirmed. It may already have arrived.", "a send past the retry window stops and says it may have arrived: \(String(describing: overdue))")
        check(StubDaemon.sends(of: "overdue").isEmpty, "a send past the retry window is not sent again by itself")
        if let overdue { relaunched.putBackSend(overdue, in: "T") }
        check(relaunched.localSends["T"] == nil && relaunched.draft(for: "T").text == "overdue", "Put back returns the text to the composer and drops the row")
        check(await wait { !relaunchStore.load().saved.sends.contains { $0.sessionID == "T" } }, "a put-back send leaves the saved file")
        check(StubDaemon.sends(of: "overdue").isEmpty, "Put back sends nothing")

        let spoolStore = ComposerStore(file: folder.appendingPathComponent("spooled.json"))
        let sendFile = ComposerAttachment(name: "spooled.txt", data: Data("kept".utf8), mediaType: "text/plain")
        let draftFile = ComposerAttachment(name: "draft.txt", data: Data("draft".utf8), mediaType: "text/plain")
        await spoolStore.write([sendFile, draftFile]) {}
        let pending = PendingSend(sessionID: "P", message: "spooled", attachments: [sendFile], idempotencyKey: "k-spooled")
        var spooledSave = ComposerStore.Saved(sends: [ComposerStore.Saved.Send(pending, spool: spoolStore.spool)])
        spooledSave.spooledDrafts = ["D": [spoolStore.spool(draftFile)!]]
        spoolStore.save(spooledSave)
        StubDaemon.reset { _, _ in .answer }
        let respooled = AgenthailModel(connecting: false, api: api, composerStore: spoolStore)
        check(respooled.draft(for: "D").attachments.map(\.data) == [Data("draft".utf8)], "a draft's attachment comes back with its bytes after a relaunch")
        check(await wait(seconds: 3) { respooled.localSends["P"]?.first?.status != nil }, "a send killed before its upload finished is delivered after a relaunch: \(String(describing: respooled.localSends["P"]))")
        let spooledSends = StubDaemon.sends(of: "spooled")
        let spooledBody = spooledSends.first.flatMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
        check(spooledSends.count == 1 && spooledSends.first?.key == "k-spooled" && spooledBody?["attachments"] as? [String] == ["up-spooled"], "it goes once, under its saved key, with its file uploaded from the spool: \(String(describing: spooledBody))")
        respooled.draft(for: "D").attachments = []
        respooled.saveComposer()
        check(((try? FileManager.default.contentsOfDirectory(atPath: spoolStore.spoolFolder.path)) ?? []).isEmpty, "a delivered message's and a removed draft attachment's bytes leave the spool")
        print("ok")
    }

    @MainActor
    private static func wait(seconds: Double = 2, _ condition: () -> Bool) async -> Bool {
        let deadline = Date(timeIntervalSinceNow: seconds)
        while Date() < deadline {
            if condition() { return true }
            try? await Task.sleep(for: .milliseconds(25))
        }
        return condition()
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}

final class StubDaemon: URLProtocol {
    enum Answer { case answer, refuse, drop, unknown }
    struct Send {
        let message: String
        let key: String?
        let body: Data
    }

    private static let lock = NSLock()
    nonisolated(unsafe) private static var recorded: [Send] = []
    nonisolated(unsafe) private static var rule: @Sendable (String, Int) -> Answer = { _, _ in .answer }

    static func reset(_ answer: @escaping @Sendable (String, Int) -> Answer) {
        lock.withLock {
            recorded = []
            rule = answer
        }
    }

    static func sends(of message: String) -> [Send] {
        lock.withLock { recorded.filter { $0.message == message } }
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if body.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                body.append(buffer, count: count)
            }
            stream.close()
        }
        if request.url?.path == "/api/v1/uploads" {
            reply(200, #"{"upload":{"id":"up-spooled","name":"spooled.txt","mediaType":"text/plain","bytes":4}}"#)
            return
        }
        let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] ?? [:]
        guard request.url?.path == "/api/v1/actions", let message = object["message"] as? String else {
            reply(200, #"{"ok":true}"#)
            return
        }
        let answer = Self.lock.withLock {
            Self.recorded.append(Send(message: message, key: request.value(forHTTPHeaderField: "Idempotency-Key"), body: body))
            return Self.rule(message, Self.recorded.filter { $0.message == message }.count)
        }
        switch answer {
        case .answer: reply(200, #"{"ok":true,"result":{"status":"sent"}}"#)
        case .refuse: reply(409, "no running turn")
        case .unknown: reply(504, #"{"error":{"code":"host_outcome_unknown","message":"Host lab did not answer; the outcome is unknown"},"host":"lab"}"#)
        case .drop: client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
        }
    }

    override func stopLoading() {}

    private func reply(_ status: Int, _ text: String) {
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": status == 200 ? "application/json" : "text/plain"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
