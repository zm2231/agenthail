import Foundation

@main
struct ContextBreakdownTest {
    static func main() throws {
        let full = try context(#"{"usedTokens":86400,"contextWindow":200000,"cumulativeTokens":312000,"compacting":false,"compactionCount":1,"reclaimedTokens":42000,"inputTokens":83000,"cachedInputTokens":60000,"outputTokens":3400,"reasoningOutputTokens":900}"#)
        check(ContextBreakdown.rows(full).map(\.label) == ["Used tokens", "Context window", "Input", "Cached input", "Output", "Reasoning output", "Cumulative tokens", "Compactions", "Tokens reclaimed"], "every reported figure appears in order")
        check(ContextBreakdown.rows(full)[2].value == Int64(83000).formatted(), "values use the locale's number format")

        let sparse = try context(#"{"usedTokens":1200,"contextWindow":0,"compacting":false,"compactionCount":0}"#)
        check(ContextBreakdown.rows(sparse) == [
            ContextBreakdownRow(label: "Used tokens", value: Int64(1200).formatted()),
            ContextBreakdownRow(label: "Context window", value: "Unavailable"),
            ContextBreakdownRow(label: "Compactions", value: 0.formatted()),
        ], "unreported figures are left out and an unknown window says so")

        let estimated = try context(#"{"usedTokens":10,"contextWindow":100,"compacting":false,"compactionCount":0,"windowEstimated":true}"#)
        check(ContextBreakdown.rows(estimated)[1].label == "Estimated window", "an estimated window is labeled")
        let configured = try context(#"{"usedTokens":10,"contextWindow":100,"compacting":false,"compactionCount":0,"contextWindowSource":"configured"}"#)
        check(ContextBreakdown.rows(configured)[1].label == "Configured window", "a configured window is labeled")
    }

    static func context(_ json: String) throws -> ContextState {
        try JSONDecoder().decode(ContextState.self, from: Data(json.utf8))
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
