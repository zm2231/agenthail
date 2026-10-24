@testable import AgenthailHelpers
import Foundation

@main
struct NewSessionLabelsTest {
    static func main() {
        check(NewSessionLabels.agent("claude", model: "Opus", effort: "", account: nil) == "Claude · Opus", "the agent chip names the provider and model")
        check(NewSessionLabels.agent("codex", model: "GPT-5", effort: "high", account: nil) == "Codex · GPT-5 · High", "a chosen effort joins the agent chip")
        check(NewSessionLabels.agent("claude", model: "Opus", effort: "", account: "work") == "Claude · work · Opus", "a non-default account shows before the model")

        let primary = ClaudeConfigRootOption(name: "default", root: "/home/a/.claude", primary: true)
        let work = ClaudeConfigRootOption(name: "work", root: "/home/a/.claude-work", primary: false)
        check(NewSessionLabels.account([primary, work], choice: "") == nil, "the default account stays out of the label")
        check(NewSessionLabels.account([primary, work], choice: work.choice) == "work", "another account is named")
        check(NewSessionLabels.account([work], choice: work.choice) == nil, "a single account is never named")

        check(NewSessionLabels.place("agenthail", host: nil) == "agenthail", "on this Mac the place is just the project")
        check(NewSessionLabels.place("agenthail", host: "build-box") == "build-box · agenthail", "another computer is named before the project")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
