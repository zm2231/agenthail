@testable import AgenthailHelpers
import Foundation

@main
struct TurnSettingModelOptionsTest {
    static func main() throws {
        let settings = try JSONDecoder().decode([TurnSetting].self, from: Data(#"[{"key":"model","value":"sonnet","options":[{"id":"sonnet","label":"Claude Sonnet"},{"id":"opus","label":"Claude Opus"}],"applies":"now"}]"#.utf8))
        let catalog = [
            ModelOption(id: "opus", displayName: "Opus 5.5", description: "Deepest", default: nil, allowsCustom: true, supportedReasoningEfforts: ["high"], defaultReasoningEffort: "high"),
            ModelOption(id: "haiku", displayName: "Haiku 5.5", description: nil, default: nil, allowsCustom: true, supportedReasoningEfforts: nil, defaultReasoningEffort: nil),
        ]
        guard let options = settings.modelOptions(catalog: catalog) else { return check(false, "a model setting gives model options") }
        check(options.map(\.id) == ["sonnet", "opus"], "the descriptor decides the offered models, in its order: \(options.map(\.id))")
        check(options.contains { $0.id == settings.setting(TurnSetting.model)?.value }, "the selected model is always one of the options")
        check(options[0].displayName == "Claude Sonnet" && options[0].allowsCustom == true, "a model the catalog lacks keeps its label and the catalog's custom IDs")
        check(options[1].displayName == "Opus 5.5" && options[1].description == "Deepest", "a model the catalog knows keeps the catalog's detail")
        check([TurnSetting]().modelOptions(catalog: catalog) == nil, "without a model setting the catalog stands")
        let plain = [ModelOption(id: "gpt-sol", displayName: "Sol", description: nil, default: nil, allowsCustom: nil, supportedReasoningEfforts: nil, defaultReasoningEffort: nil)]
        check(settings.modelOptions(catalog: plain)?.first?.allowsCustom == nil, "a catalog without custom IDs grants none")
        print("turn setting model options tests passed")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
