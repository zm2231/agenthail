@testable import AgenthailHelpers
import Foundation

@main
struct NewSessionLabelsTest {
    static func main() {
        check(NewSessionLabels.agent("claude", model: "Opus", effort: "", account: nil) == "Claude · Opus", "the agent chip names the provider and model")
        check(NewSessionLabels.agent("codex", model: "GPT-5", effort: "high", account: nil) == "Codex · GPT-5 · High", "a chosen effort joins the agent chip")
        check(NewSessionLabels.agent("claude", model: "Opus", effort: "", account: "work") == "Claude · work · Opus", "a non-default account shows before the model")

        let usage = { (session: Double, week: Double) in ProviderUsage(limits: [.init(kind: "session", percent: session), .init(kind: "weekly_all", percent: week)]) }
        let primary = ClaudeConfigRootOption(name: "default", root: "/home/a/.claude", primary: true, signedInAs: .init(email: "dev@example.com"), usage: usage(34, 58))
        let work = ClaudeConfigRootOption(name: "work", root: "/home/a/.claude-work", primary: false, signedInAs: .init(email: "team@example.com"), usage: usage(18, 92))
        let keys = ClaudeConfigRootOption(name: "keys", root: "/home/a/.keys", primary: false, login: "api_key")
        let spare = ClaudeConfigRootOption(name: "spare", root: "/home/a/.spare", primary: false)
        let pick = ClaudeRootPick(root: "", name: "default", account: "dev@example.com", reason: "most headroom, session 34%, week 58%", sessionCeiling: 90, weeklyCeiling: 90)
        let roots = [primary, work, keys, spare]
        check(NewSessionLabels.account(roots, choice: "", pick: nil, host: "studio") == nil, "automatic with no preview yet names nothing")
        check(NewSessionLabels.account(roots, choice: "", pick: pick, host: "studio") == "dev (auto)", "automatic names the account the way the usage chips do")
        check(NewSessionLabels.account(roots, choice: work.root, pick: pick, host: "studio") == "team", "a chosen account is named by its login")
        check(NewSessionLabels.account([work], choice: work.root, pick: pick, host: "studio") == nil, "a single account is never named")

        let titles = ClaudeRootLabels.titles(roots, host: "studio")
        check(titles == ["dev", "team", "studio (API key)", "studio (signed out)"], "\(titles)")
        check(ClaudeRootLabels.titles([primary, ClaudeConfigRootOption(name: "other", root: "/o", primary: false, signedInAs: .init(email: "dev@example.org"))], host: "studio") == ["dev · default", "dev · other"], "the root tells apart two logins that would read alike")
        check(ClaudeRootLabels.automatic(pick, roots: roots, titles: titles) == "Automatic (dev, most headroom)", ClaudeRootLabels.automatic(pick, roots: roots, titles: titles))
        check(ClaudeRootLabels.option(primary, title: "dev", pick: pick) == "dev · session 34%, week 58%", "each account shows its usage")
        check(ClaudeRootLabels.option(work, title: "team", pick: pick) == "team · session 18%, week 92%, over limit", "an account over a ceiling says so")
        check(ClaudeRootLabels.usage(work, pick: pick)?.overLimit == true, "over limit is flagged for the warning tone")
        check(ClaudeRootLabels.reason(pick, roots: roots, titles: titles) == "Automatic picks dev: most headroom, session 34%, week 58%", "the full reason")
        check(keys.selectable && !spare.selectable, "an API-key root can be chosen; a signed-out one cannot")

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
