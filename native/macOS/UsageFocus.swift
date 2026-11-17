import Foundation

struct UsageFocus: Equatable {
    let chips: [UsageAccount]
    let others: Int

    static func pick(_ accounts: [UsageAccount], shown: SessionState?, sessions: [SessionState]) -> UsageFocus {
        guard !accounts.isEmpty else { return UsageFocus(chips: [], others: 0) }
        let owner = shown.flatMap { session in accounts.first { $0.owns(session) } }
        var busiest: UsageAccount?
        var busiestCount = 0
        for account in accounts {
            let working = sessions.filter { account.owns($0) && $0.isWorking }.count
            let percent = account.worst?.percent ?? 0
            if working > busiestCount || working == busiestCount && working > 0 && percent > (busiest?.worst?.percent ?? 0) {
                busiest = account
                busiestCount = working
            }
        }
        let fullest = accounts.max { ($0.worst?.percent ?? -1) < ($1.worst?.percent ?? -1) }
        let lead = owner ?? busiest ?? fullest ?? accounts[0]
        let alarming = accounts.filter { $0.id != lead.id && $0.critical && !$0.activeSessions(sessions).isEmpty }
        let chips = [lead] + alarming
        return UsageFocus(chips: chips, others: accounts.count - chips.count)
    }

    static func name(_ account: UsageAccount) -> String {
        switch account.provider {
        case "claude" where !account.title.localizedCaseInsensitiveContains("claude"): "Claude " + account.title
        default: account.title
        }
    }

    static func line(_ account: UsageAccount, now: Date) -> String {
        let meters = account.meters.map { UsageMeterText.shortLabel($0) + " " + UsageMeterText.text($0, now: now) }
        let state = meters.isEmpty ? [account.problem ?? "no reading"] : meters
        return name(account) + ": " + state.joined(separator: ", ")
    }
}
