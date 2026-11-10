@testable import AgenthailHelpers
import AppKit
import SwiftUI

final class InboxDaemon: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var posts: [[String: Any]] = []
    static let decisions = #"{"decisions":[{"group":"waiting","sessionId":"S","kind":"ask","reason":"Ready to ship","answerable":true,"requests":[{"id":"ask-000000000001","kind":"ask","tool":"","title":"Ready to ship","questions":[{"question":"Deploy it now?","header":"","multiSelect":false,"options":[{"id":"o0","label":"Deploy","description":""},{"id":"o1","label":"Wait","description":""}],"suggested":0}],"alwaysAllow":false}]}],"leadHandling":[]}"#

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body): (Int, String)
        switch request.url?.path {
        case "/api/v1/decisions":
            (status, body) = (200, Self.posts.isEmpty ? Self.decisions : #"{"decisions":[],"leadHandling":[]}"#)
        case "/api/v1/actions":
            let data = request.httpBody ?? request.httpBodyStream.map(AskStream.read) ?? Data()
            Self.posts.append((try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:])
            (status, body) = (200, #"{"ok":true,"result":{"requestId":"ask-000000000001","outcome":"answered","receipt":{}}}"#)
        default:
            (status, body) = (404, #"{"error":{"code":"unknown_route","message":"not in this test"}}"#)
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

enum AskStream {
    static func read(_ stream: InputStream) -> Data {
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }
            data.append(buffer, count: count)
        }
        return data
    }
}

@main
struct DecisionInboxKeysTest {
    @MainActor
    static func main() async {
        _ = NSApplication.shared
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [InboxDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        let pane = model.openPane()
        pane.showInbox()
        model.decisionFeed.refresh()
        await until { model.decisionFeed.count == 1 }
        check(model.decisionFeed.count == 1, "the inbox loads the waiting ask")

        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 700), styleMask: [.titled], backing: .buffered, defer: false)
        window.contentView = NSHostingView(rootView: DecisionInboxPane(model: model, pane: pane))
        window.layoutIfNeeded()
        await settle(0.5)

        press(window, shift: false)
        await settle(0.5)
        check(InboxDaemon.posts.isEmpty, "a bare Return with nothing picked sends nothing: \(InboxDaemon.posts)")

        press(window, shift: true)
        await until { !InboxDaemon.posts.isEmpty }
        await settle(0.3)
        check(InboxDaemon.posts.count == 1, "Shift-Return sends exactly one answer: \(InboxDaemon.posts.count)")
        let decision = InboxDaemon.posts.first?["decision"] as? [String: Any] ?? [:]
        check(InboxDaemon.posts.first?["action"] as? String == "respond" && decision["choices"] as? [String] == ["o0"], "it sends the recommended answer through respond: \(InboxDaemon.posts)")

        press(window, shift: true)
        await settle(0.5)
        check(InboxDaemon.posts.count == 1, "nothing is left to accept once it is answered")
        print("decision inbox keys ok")
    }

    @MainActor
    static func press(_ window: NSWindow, shift: Bool) {
        for type in [NSEvent.EventType.keyDown, .keyUp] {
            let event = NSEvent.keyEvent(with: type, location: .zero, modifierFlags: shift ? .shift : [], timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber, context: nil, characters: "\r", charactersIgnoringModifiers: "\r", isARepeat: false, keyCode: 36)!
            if type == .keyDown && window.performKeyEquivalent(with: event) { continue }
            window.sendEvent(event)
        }
    }

    @MainActor
    static func settle(_ seconds: Double) async {
        let end = Date().addingTimeInterval(seconds)
        while Date() < end {
            RunLoop.main.run(until: Date().addingTimeInterval(0.02))
            await Task.yield()
        }
    }

    @MainActor
    static func until(_ condition: () -> Bool) async {
        for _ in 0..<150 where !condition() { await settle(0.02) }
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
