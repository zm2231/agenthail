@testable import AgenthailHelpers
import Foundation

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

        notice.show("  \n ")
        check(notice.text == nil, "an empty message shows nothing")

        check(ProjectChange.done("Renamed to web.").settle(notice: notice) == nil && notice.text == "Renamed to web.", "done shows the notice and no error")
        check(ProjectChange.failed("The host refused it.").settle(notice: notice) == "The host refused it." && notice.text == nil, "a failure is an error and takes the notice down")

        let model = AgenthailModel(connecting: false)
        let pane = SessionPane(model: model, restoresSelection: false)
        model.operationError = "stale"
        pane.settle(.done("Terminal sessions in notes no longer get the Agenthail tools.")) { .done("") }
        check(model.operationError == nil && model.projectNotice.text == "Terminal sessions in notes no longer get the Agenthail tools.", "on the Mac a finished project change clears the error and shows its message: \(model.projectNotice.text ?? "nil")")
        pane.settle(.failed("Agenthail isn't connected.")) { .done("") }
        check(model.operationError == "Agenthail isn't connected." && model.projectNotice.text == nil, "on the Mac a failed change shows the error, never the notice")
        pane.settle(.confirmReplace("notes already has a lead.")) { .done("") }
        check(pane.projectConfirmation?.message == "notes already has a lead." && model.projectNotice.text == nil, "a change that needs confirming asks first")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
