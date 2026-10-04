import Foundation

@main
struct MetadataOverlayTest {
    static func main() throws {
        let bare = try detail(extra: "")
        let legacy = try detail(extra: #","context":{"usedTokens":900,"contextWindow":1000,"compacting":false,"compactionCount":0},"goal":{"objective":"Old","status":"active"},"model":"gpt-old""#)

        var overlay = MetadataOverlay()
        overlay.absorb(context: try context(400), goal: try goal("Ship it"), model: "gpt-5", models: nil)
        let first = overlay.apply(to: bare)
        check(first.context?.usedTokens == 400 && first.goal?.objective == "Ship it" && first.model == "gpt-5", "metadata arriving before detail is kept")

        overlay.stream(context: try context(700))
        overlay.absorb(context: try context(100), goal: nil, model: nil, models: nil)
        check(overlay.apply(to: bare).context?.usedTokens == 700, "streamed context wins over later metadata")

        overlay.stream(goal: try goal("Streamed"))
        overlay.absorb(context: nil, goal: try goal("Stale"), model: nil, models: nil)
        check(overlay.apply(to: bare).goal?.objective == "Streamed", "streamed goal wins over later metadata")

        let reloaded = overlay.apply(to: bare)
        check(reloaded.context?.usedTokens == 700 && reloaded.goal?.objective == "Streamed" && reloaded.model == "gpt-5", "a reload without metadata keeps every field")

        let observed = try JSONDecoder().decode(SessionMetadata.self, from: Data(#"{"claudeRuns":[{"recordPath":"/runs/1.json","jobId":"job-1","providerState":"running"}],"claudeSubagents":[{"parentSessionId":"s","agentId":"helper","transcriptPath":"/t/helper.jsonl"}],"errors":{"goal":"unavailable"}}"#.utf8))
        overlay.absorb(observed)
        let withRuns = overlay.apply(to: bare)
        check(withRuns.claudeRuns?.first?.jobId == "job-1" && withRuns.claudeSubagents?.first?.agentId == "helper" && withRuns.metadataErrors?["goal"] == "unavailable" && withRuns.model == "gpt-5", "Claude runs, subagents, and warnings come from metadata")
        check(overlay.apply(to: bare).goal?.objective == "Streamed", "metadata observations keep streamed fields")
        overlay.metadataFailed()
        let failed = overlay.apply(to: bare)
        check(failed.claudeRuns == nil && failed.claudeSubagents == nil && failed.metadataErrors == nil && failed.model == "gpt-5", "a failed metadata load clears observations only")

        var older = MetadataOverlay()
        older.absorb(context: legacy.context, goal: legacy.goal, model: legacy.model, models: legacy.models)
        let fromLegacy = older.apply(to: legacy)
        check(fromLegacy.context?.usedTokens == 900 && fromLegacy.goal?.objective == "Old" && fromLegacy.model == "gpt-old", "older daemons that send metadata in detail keep working")

        let options = try JSONDecoder().decode([ModelOption].self, from: Data(#"[{"id":"gpt-5","displayName":"GPT-5"}]"#.utf8))
        overlay.absorb(context: nil, goal: nil, model: nil, models: options)
        check(overlay.apply(to: bare).models?.map(\.id) == ["gpt-5"] && overlay.apply(to: bare).model == "gpt-5", "model options arrive without clearing the current model")

        let seeded = MetadataOverlay(seed: first).apply(to: bare)
        check(seeded.context?.usedTokens == 400 && seeded.model == "gpt-5", "a cached detail seeds the overlay")
    }

    private static func detail(extra: String) throws -> SessionDetail {
        let json = #"{"session":{"id":"s1","surface":"codex","name":"Demo","status":"idle","lastActive":"2026-10-04T12:00:00Z"},"exchanges":[],"capabilities":{"send":false,"stream":false,"reply":false,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false},"readOnly":false,"readOnlyReason":""\#(extra)}"#
        return try JSONDecoder().decode(SessionDetail.self, from: Data(json.utf8))
    }

    private static func context(_ used: Int) throws -> ContextState {
        try JSONDecoder().decode(ContextState.self, from: Data(#"{"usedTokens":\#(used),"contextWindow":1000,"compacting":false,"compactionCount":0}"#.utf8))
    }

    private static func goal(_ objective: String) throws -> GoalState {
        try JSONDecoder().decode(GoalState.self, from: Data(#"{"objective":"\#(objective)","status":"active"}"#.utf8))
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
