@testable import AgenthailHelpers
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

        let compacted = try context(#"{"usedTokens":0,"contextWindow":200000,"compacting":true,"compactionCount":1,"reclaimedTokens":150000}"#)
        check(ContextBreakdown.usage(compacted) == 0.0.formatted(.percent.precision(.fractionLength(0))), "a reported zero usage still has a usage label")
        check(ContextBreakdown.rows(compacted).map(\.label) == ["Used tokens", "Context window", "Compactions", "Tokens reclaimed"], "a compacted context keeps its figures")
        let unknownWindow = try context(#"{"usedTokens":404000,"contextWindow":200000,"compacting":false,"compactionCount":0,"windowEstimated":true}"#)
        check(ContextBreakdown.usage(unknownWindow) == "\(Int64(404000).formatted(.number.notation(.compactName))) tokens used", "usage past an estimated window is shown as tokens")

        let estimated = try context(#"{"usedTokens":10,"contextWindow":100,"compacting":false,"compactionCount":0,"windowEstimated":true}"#)
        check(ContextBreakdown.rows(estimated)[1].label == "Estimated window", "an estimated window is labeled")
        let configured = try context(#"{"usedTokens":10,"contextWindow":100,"compacting":false,"compactionCount":0,"contextWindowSource":"configured"}"#)
        check(ContextBreakdown.rows(configured)[1].label == "Configured window", "a configured window is labeled")

        func used(_ tokens: Int, estimated: Bool = false) throws -> ContextState {
            try context(#"{"usedTokens":\#(tokens),"contextWindow":1000,"compacting":false,"compactionCount":0,"windowEstimated":\#(estimated)}"#)
        }
        let levels = try [799, 800, 949, 950].map { try used($0).level }
        check(levels == [.normal, .high, .high, .full], "the ring turns high at 80 percent and full at 95: \(levels)")
        check(try used(1200, estimated: true).level == .full, "an overrun estimated window reads as full")
        let spoken = try used(620).spokenLabel
        check(spoken == "Context 62 percent used", "VoiceOver reads the percent: \(spoken)")
        check(compacted.spokenLabel == "Compacting context", "a compaction in progress says so")
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
