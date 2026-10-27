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
        let pick = ClaudeRootPick(root: work.root, name: "work", account: "work@example.com", reason: "most headroom, session 10%")
        check(NewSessionLabels.account([primary, work], choice: "", pick: nil) == nil, "automatic with no preview yet names nothing")
        check(NewSessionLabels.account([primary, work], choice: "", pick: pick) == "auto: work", "automatic names the account it would pick")
        check(NewSessionLabels.account([primary, work], choice: primary.root, pick: pick) == "default", "a chosen primary is named")
        check(NewSessionLabels.account([primary, work], choice: work.root, pick: pick) == "work", "another account is named")
        check(NewSessionLabels.account([work], choice: work.root, pick: pick) == nil, "a single account is never named")
        check(pick.menuLabel == "Automatic · work (work@example.com): most headroom, session 10%", pick.menuLabel)

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
