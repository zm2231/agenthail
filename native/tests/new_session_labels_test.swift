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
        let pick = ClaudeRootPick(root: "", name: "default", account: "dev@example.com", reason: "your default root for new sessions")
        let roots = [primary, work, keys, spare]
        check(NewSessionLabels.account(roots, choice: "", pick: nil, host: "studio") == nil, "the default with no preview yet names nothing")
        check(NewSessionLabels.account(roots, choice: "", pick: pick, host: "studio") == "dev (default)", "the default names the account the way the usage chips do")
        check(NewSessionLabels.account(roots, choice: work.root, pick: pick, host: "studio") == "team", "a chosen account is named by its login")
        check(NewSessionLabels.account([work], choice: work.root, pick: pick, host: "studio") == nil, "a single account is never named")

        let titles = ClaudeRootLabels.titles(roots, host: "studio")
        check(titles == ["dev", "team", "studio (API key)", "studio (signed out)"], "\(titles)")
        check(ClaudeRootLabels.titles([primary, ClaudeConfigRootOption(name: "other", root: "/o", primary: false, signedInAs: .init(email: "dev@example.org"))], host: "studio") == ["dev · default", "dev · other"], "the root tells apart two logins that would read alike")
        check(ClaudeRootLabels.byDefault(pick, roots: roots, titles: titles) == "Default (dev, your default root for new sessions)", ClaudeRootLabels.byDefault(pick, roots: roots, titles: titles))
        check(ClaudeRootLabels.byDefault(nil, roots: roots, titles: titles) == "Default", "before the preview arrives")
        check(ClaudeRootLabels.option(primary, title: "dev") == "dev · session 34%, week 58%", "each account shows its usage")
        check(ClaudeRootLabels.option(work, title: "team") == "team · session 18%, week 92%", "usage is shown, never judged against a ceiling")
        check(ClaudeRootLabels.reason(pick, roots: roots, titles: titles) == "Default is dev: your default root for new sessions", "the full reason")
        check(keys.selectable && !spare.selectable, "an API-key root can be chosen; a signed-out one cannot")
        let bedrock = ClaudeConfigRootOption(name: "cloud", root: "/home/a/.cloud", primary: false, login: "api_key", loginSource: "bedrock")
        let token = ClaudeConfigRootOption(name: "token", root: "/home/a/.token", primary: false, login: "api_key", loginSource: "oauth_token")
        let sourced = ClaudeRootLabels.titles([bedrock, token], host: "studio")
        check(sourced == ["studio (Bedrock)", "studio (OAuth token)"] && bedrock.selectable && token.selectable, "a root without an account is named for what it signs in with, and can be chosen: \(sourced)")

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
