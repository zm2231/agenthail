@testable import AgenthailHelpers
import Foundation

@main
struct SessionPlanTest {
    static func main() throws {
        let working = try session(status: "busy", plan: #"{"done":3,"total":5,"current":"Running gates"}"#)
        check(working.livePlan?.badge == "3/5", "an open session part way through shows its progress")
        check(working.livePlan?.spokenLabel == "3 of 5 tasks done", "VoiceOver reads the count in words")
        check(working.livePlan?.detail == "3 of 5 tasks done · Running gates", "the detail names the step in progress")
        check(SessionFacts.rowDetail(working).contains("3 of 5 tasks done · Running gates"), "the row's tooltip carries the plan: \(SessionFacts.rowDetail(working))")

        check(try session(status: "idle", plan: #"{"done":2,"total":4}"#).livePlan?.detail == "2 of 4 tasks done", "an idle open session between turns keeps its badge")
        check(try session(status: "idle", open: false, plan: #"{"done":3,"total":5}"#).livePlan == nil, "a closed session that stopped part way shows no badge")
        check(try session(status: "offline", plan: #"{"done":3,"total":5}"#).livePlan == nil, "an offline session shows no badge")
        check(try session(status: "busy", plan: #"{"done":5,"total":5}"#).livePlan == nil, "a finished list shows no badge")
        check(try session(status: "busy", plan: #"{"done":0,"total":0}"#).livePlan == nil, "an empty list shows no badge")
        check(try session(status: "busy", plan: nil).livePlan == nil, "a row from a daemon that sends no plan shows no badge")
        check(try session(status: "busy", plan: nil).plan == nil, "a row without the field decodes")
        check(SessionTree.sessionCountLabel(1) == "1 session" && SessionTree.sessionCountLabel(4) == "4 sessions", "a project header reads its session count")
        print("ok")
    }

    static func session(status: String, open: Bool = true, plan: String?) throws -> SessionState {
        let field = plan.map { #","plan":\#($0)"# } ?? ""
        return try JSONDecoder().decode(SessionState.self, from: Data(#"{"id":"s","surface":"claude","name":"builder","status":"\#(status)","queueCount":0,"open":\#(open),"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}\#(field)}"#.utf8))
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
