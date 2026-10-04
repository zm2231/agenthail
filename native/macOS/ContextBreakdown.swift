import Foundation

struct ContextBreakdownRow: Equatable {
    let label: String
    let value: String
}

enum ContextBreakdown {
    static func rows(_ context: ContextState) -> [ContextBreakdownRow] {
        let window = context.windowEstimated == true ? "Estimated window" : context.contextWindowSource == "configured" ? "Configured window" : "Context window"
        var rows = [
            ContextBreakdownRow(label: "Used tokens", value: context.usedTokens.formatted()),
            ContextBreakdownRow(label: window, value: context.contextWindow > 0 ? context.contextWindow.formatted() : "Unavailable"),
        ]
        let reported: [(String, Int64?)] = [
            ("Input", context.inputTokens),
            ("Cached input", context.cachedInputTokens),
            ("Output", context.outputTokens),
            ("Reasoning output", context.reasoningOutputTokens),
            ("Cumulative tokens", context.cumulativeTokens),
        ]
        rows += reported.compactMap { label, value in value.map { ContextBreakdownRow(label: label, value: $0.formatted()) } }
        rows.append(ContextBreakdownRow(label: "Compactions", value: context.compactionCount.formatted()))
        if let reclaimed = context.reclaimedTokens {
            rows.append(ContextBreakdownRow(label: "Tokens reclaimed", value: reclaimed.formatted()))
        }
        return rows
    }
}
