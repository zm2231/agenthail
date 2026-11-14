@testable import AgenthailHelpers
import Foundation

final class NoticeActionStub: URLProtocol {
    nonisolated(unsafe) static var status = 200
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let action = request.url?.path == "/api/v1/actions"
        let status = action ? NoticeActionStub.status : 503
        let body = action && status == 200 ? #"{"ok":true,"created":true,"coordinator":{"name":"lead","projectRoot":"/srv/notes"}}"# : #"{"error":{"code":"refused","message":"The host refused it."}}"#
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@main
struct TransientNoticeTest {
    @MainActor
    static func main() async {
        let notice = TransientNotice()
        notice.show("Project removed.", for: .milliseconds(150))
        check(notice.text == "Project removed.", "a finished change shows its message")
        try? await Task.sleep(for: .milliseconds(400))
        check(notice.text == nil, "the notice goes away by itself, got \(notice.text ?? "nil")")

        notice.show("first", for: .milliseconds(150))
        try? await Task.sleep(for: .milliseconds(100))
        notice.show("second", for: .milliseconds(400))
        try? await Task.sleep(for: .milliseconds(150))
        check(notice.text == "second", "an older notice's timer leaves a newer one up, got \(notice.text ?? "nil")")
        notice.dismiss()
        check(notice.text == nil, "a notice can be dismissed")

        let before = notice.shown
        notice.show("Project removed.")
        notice.show("Project removed.")
        check(notice.shown == before + 2, "the same message shown twice counts as two showings, so each is announced")

        let shownBeforeEmpty = notice.shown
        notice.show("  \n ")
        check(notice.text == "Project removed." && notice.shown == shownBeforeEmpty, "an empty message changes nothing")

        check(ProjectChange.done("Renamed to web.").error == nil && ProjectChange.failed("No.").error == "No." && ProjectChange.confirmReplace("Replace?").error == "Replace?", "only failures and confirmations are errors")
        ProjectChange.failed("The host refused it.").announce(in: notice)
        check(notice.text == nil, "a failure takes the notice down")

        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [NoticeActionStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://daemon.invalid")!, token: "t", session: URLSession(configuration: config))
        let model = AgenthailModel(connecting: false, api: api)
        let renamed = await model.renameProject("p-1", to: "web")
        check(renamed == .done("Renamed to web.") && model.projectNotice.text == "Renamed to web.", "a change made from a form shows its message after the form closes: \(renamed) \(model.projectNotice.text ?? "nil")")
        let notes = ProjectState(id: "p-1", displayName: "notes", checkouts: [ProjectState.Checkout(host: SessionHost(id: "self", name: "self", state: "local"), projectId: "p-1", rootPath: "/srv/notes", added: true)], mcp: "on")
        let actions: [(String, () async -> ProjectChange)] = [
            ("add", { await model.addProject(path: "/srv/notes") }),
            ("rename", { await model.renameProject("p-1", to: "web") }),
            ("remove", { await model.removeProject("p-1") }),
            ("tools", { await model.setProjectTools(notes, on: false) }),
            ("clear lead", { await model.clearLead(of: notes) }),
            ("make lead", { await model.makeLead("s-1") }),
            ("start lead", { await model.startLead(of: notes, in: notes.checkouts[0], surface: "claude", name: "lead") }),
        ]
        for (name, act) in actions {
            model.projectNotice.dismiss()
            let change = await act()
            guard case .done(let message) = change, !message.isEmpty else { check(false, "\(name) finished with a message: \(change)"); return }
            check(model.projectNotice.text == message, "\(name) leaves its message on screen: \(model.projectNotice.text ?? "nil") vs \(message)")
        }
        let pane = SessionPane(model: model, restoresSelection: false)
        check(model.projectNotice.text != nil, "a notice from the last change is still up")
        NoticeActionStub.status = 409
        let refused = await model.renameProject("p-1", to: "api")
        pane.settle(refused) { .done("") }
        check(refused.error != nil && model.operationError == refused.error, "on the Mac a refused change shows its error: \(refused) \(model.operationError ?? "nil")")
        check(model.projectNotice.text == nil, "a refused change takes the earlier notice down and shows none")

        model.operationError = "stale"
        pane.settle(.done("Renamed to web.")) { .done("") }
        check(model.operationError == nil, "on the Mac a finished project change clears the error")
        pane.settle(.failed("Agenthail isn't connected.")) { .done("") }
        check(model.operationError == "Agenthail isn't connected.", "on the Mac a failed change shows the error")
        pane.settle(.confirmReplace("notes already has a lead.")) { .done("") }
        check(pane.projectConfirmation?.message == "notes already has a lead.", "a change that needs confirming asks first")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
