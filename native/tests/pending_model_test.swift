@testable import AgenthailHelpers
import Foundation

@main
struct PendingModelTest {
    static func main() throws {
        let row = try detail(session: #","pendingModel":"gpt-sol""#, extra: #","model":"gpt-luna""#)
        check(row.nextTurnModel == "gpt-sol" && row.modelLabel == "gpt-luna · gpt-sol from next turn", "the session row's pending model shows beside the current one")

        let applied = try detail(session: #","pendingModel":"gpt-sol""#, extra: #","model":"gpt-sol""#)
        check(applied.nextTurnModel == nil && applied.modelLabel == "gpt-sol", "a pending model the conversation already runs is not shown twice")

        let none = try detail(session: "", extra: #","model":"gpt-luna""#)
        check(none.nextTurnModel == nil && none.modelLabel == "gpt-luna", "no pending model shows only the current one")

        let metadata = try JSONDecoder().decode(SessionMetadata.self, from: Data(#"{"model":"gpt-luna","pendingModel":"gpt-sol"}"#.utf8))
        var overlay = MetadataOverlay(seed: none)
        overlay.absorb(metadata)
        let merged = overlay.apply(to: none)
        check(merged.pendingModel == "gpt-sol" && merged.modelLabel == "gpt-luna · gpt-sol from next turn", "metadata carries the pending model through the overlay")

        let cleared = try JSONDecoder().decode(SessionMetadata.self, from: Data(#"{"model":"gpt-sol","pendingModel":""}"#.utf8))
        overlay.absorb(cleared)
        let after = overlay.apply(to: row)
        check(after.nextTurnModel == nil && after.modelLabel == "gpt-sol", "an empty pending model from metadata overrides a stale row")

        check(MetadataLabels.modelMenuTitle(current: "Luna", next: "Sol") == "Luna · Sol from next turn", "the model menu names both models")
        check(MetadataLabels.modelMenuTitle(current: "Luna", next: nil) == "Luna", "the model menu names the current model alone")
        check(MetadataLabels.modelMenuTitle(current: nil, next: nil) == "Choose", "the model menu without a model asks to choose")
    }

    private static func detail(session: String, extra: String) throws -> SessionDetail {
        let json = #"{"session":{"id":"s1","surface":"codex","name":"Demo","status":"idle","lastActive":"2026-10-04T12:00:00Z"\#(session)},"exchanges":[],"capabilities":{"send":false,"stream":false,"reply":false,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false},"readOnly":false,"readOnlyReason":""\#(extra)}"#
        return try JSONDecoder().decode(SessionDetail.self, from: Data(json.utf8))
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
