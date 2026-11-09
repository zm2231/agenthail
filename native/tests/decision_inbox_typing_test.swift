@testable import AgenthailHelpers
import AppKit
import SwiftUI

final class TypingDaemon: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var posts: [[String: Any]] = []
    static func ask(_ session: String) -> String {
        #"{"group":"waiting","sessionId":"\#(session)","kind":"ask","reason":"Ready to ship","answerable":true,"requests":[{"id":"turn-end-\#(session)","kind":"ask","tool":"","title":"Ready to ship","questions":[{"question":"Deploy it now?","header":"","multiSelect":false,"options":[{"label":"Deploy","description":""},{"label":"Wait","description":""}],"suggested":0}],"alwaysAllow":false}]}"#
    }
    static let decisions = #"{"decisions":["# + ask("S1") + "," + ask("S2") + #"],"leadHandling":[]}"#

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body): (Int, String)
        switch request.url?.path {
        case "/api/v1/decisions":
            (status, body) = (200, Self.decisions)
        case "/api/v1/actions":
            Self.posts.append(["path": "actions"])
            (status, body) = (200, #"{"ok":true,"result":{"outcome":"answered","receipt":{}}}"#)
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

@main
struct DecisionInboxTypingTest {
    @MainActor
    static func main() async {
        _ = NSApplication.shared
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [TypingDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        let pane = model.openPane()
        pane.showInbox()
        model.decisionFeed.refresh()
        await until { model.decisionFeed.count == 2 }
        check(model.decisionFeed.count == 2, "the inbox loads both asks")

        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 700), styleMask: [.titled], backing: .buffered, defer: false)
        window.contentView = NSHostingView(rootView: DecisionInboxPane(model: model, pane: pane))
        window.layoutIfNeeded()
        await settle(0.5)
        let first = pane.selectedDecisionID
        type(window, "j")
        await settle(0.3)
        check(pane.selectedDecisionID != first, "j moves to the next decision when no text field has focus")
        type(window, "k")
        await settle(0.5)
        check(pane.selectedDecisionID == first, "k moves back")

        guard let note = field(in: window.contentView, placeholder: "Add a note (optional)") else { return check(false, "the ask shows its note field") }
        check(window.makeFirstResponder(note), "the note field takes focus")
        await settle(0.2)
        type(window, "jk 2")
        await settle(0.3)
        check(note.stringValue == "jk 2", "j, k and a digit typed into the answer field stay in it: \(note.stringValue.debugDescription)")
        check(pane.selectedDecisionID == first, "typing in the answer field does not move to another decision")
        press(window, "\r", keyCode: 36, shift: true)
        await settle(0.3)
        check(TypingDaemon.posts.isEmpty, "Shift-Return while typing a note does not send the recommended answer without it: \(TypingDaemon.posts)")
        print("decision inbox typing ok")
    }

    @MainActor
    static func field(in view: NSView?, placeholder: String) -> NSTextField? {
        guard let view else { return nil }
        if let field = view as? NSTextField, field.isEditable, field.placeholderString == placeholder || field.placeholderAttributedString?.string == placeholder { return field }
        for child in view.subviews {
            if let found = field(in: child, placeholder: placeholder) { return found }
        }
        return nil
    }

    @MainActor
    static func type(_ window: NSWindow, _ text: String) {
        for character in text { press(window, String(character), keyCode: 0, shift: false) }
    }

    @MainActor
    static func press(_ window: NSWindow, _ characters: String, keyCode: UInt16, shift: Bool) {
        for type in [NSEvent.EventType.keyDown, .keyUp] {
            let event = NSEvent.keyEvent(with: type, location: .zero, modifierFlags: shift ? .shift : [], timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber, context: nil, characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: keyCode)!
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
