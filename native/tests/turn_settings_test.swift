@testable import AgenthailHelpers
import Foundation

final class TurnSettingStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var actions: [[String: Any]] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var data = request.httpBody ?? Data()
        if data.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                data.append(buffer, count: count)
            }
            stream.close()
        }
        if request.url?.path == "/api/v1/actions" {
            Self.actions.append((try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:])
        }
        let body = Data(#"{"ok":true,"result":{"status":"delivered"}}"#.utf8)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct TurnSettingsTest {
    @MainActor
    static func main() async throws {
        let metadata = try JSONDecoder().decode(SessionMetadata.self, from: Data(#"{"sessionId":"S","turnSettings":[{"key":"model","value":"gpt-sol","options":[{"id":"gpt-sol","label":"GPT Sol"}],"applies":"nextTurn"},{"key":"effort","value":"medium","options":[{"id":"medium","label":"Medium"},{"id":"high","label":"High"}],"applies":"nextTurn"},{"key":"mode","value":"manual","options":[{"id":"manual","label":"Manual"},{"id":"bypassPermissions","label":"Bypass permissions","confirm":true}],"applies":"now"}]}"#.utf8))
        var overlay = MetadataOverlay()
        overlay.absorb(metadata)
        let detail = overlay.apply(to: try JSONDecoder().decode(SessionDetail.self, from: Data(#"{"session":{"id":"S","surface":"codex","name":"Demo","status":"idle","lastActive":"2026-10-04T12:00:00Z"},"exchanges":[],"capabilities":{"send":true,"stream":false,"reply":false,"goal":false,"compact":false,"model":true,"interrupt":false,"steer":false},"readOnly":false,"readOnlyReason":""}"#.utf8)))
        let settings = detail.turnSettings ?? []
        check(settings.map(\.key) == ["model", "effort", "mode"], "session metadata carries the turn settings to the detail: \(settings.map(\.key))")
        guard let effort = settings.setting(TurnSetting.effort), let mode = settings.setting(TurnSetting.mode) else { return check(false, "effort and mode decode") }
        check(effort.appliesNextTurn && !mode.appliesNextTurn && effort.valueLabel == "Medium", "applies and labels decode")
        check(settings.map(\.chipAccessibilityLabel) == ["Model, GPT Sol", "Effort, Medium", "Mode, Manual"], "each chip names its setting and value: \(settings.map(\.chipAccessibilityLabel))")
        overlay.absorb(context: nil, goal: nil, model: nil, models: [ModelOption(id: "gpt-luna", displayName: "GPT Luna", description: nil, default: nil, allowsCustom: nil, supportedReasoningEfforts: nil, defaultReasoningEffort: nil)])
        let catalogued = overlay.apply(to: detail)
        guard let modelSetting = settings.setting(TurnSetting.model) else { return check(false, "model decodes") }
        check(catalogued.menuOptions(modelSetting).map(\.id) == ["gpt-sol", "gpt-luna"], "the model chip menu offers catalog models too: \(catalogued.menuOptions(modelSetting).map(\.id))")
        check(catalogued.menuOptions(effort) == effort.options, "other chips offer their own options")
        check(mode.options[1].needsConfirmation && mode.quietOptions.map(\.id) == ["manual"], "a confirm option is kept out of the quiet options")

        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [TurnSettingStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)

        var pending: TurnSettingChoice?
        var applied: [(String, String)] = []
        TurnSettingChoice.choose(mode.options[1], for: mode, pending: &pending, apply: { applied.append(($0, $1)) })
        check(applied.isEmpty && pending?.option.id == "bypassPermissions", "an option that lets the agent act without asking waits for confirmation")
        TurnSettingChoice.choose(effort.options[0], for: effort, pending: &pending, apply: { applied.append(($0, $1)) })
        check(applied.isEmpty, "choosing the current value changes nothing")
        TurnSettingChoice.choose(effort.options[1], for: effort, pending: &pending, apply: { applied.append(($0, $1)) })
        check(applied.count == 1 && applied[0] == ("effort", "high"), "a plain option applies at once: \(applied)")

        TurnSettingStub.actions = []
        let changed = await model.setTurnSetting(applied[0].0, to: applied[0].1, sessionID: "S")
        let change = TurnSettingStub.actions.first ?? [:]
        check(changed && change["action"] as? String == "turn-setting" && change["sessionId"] as? String == "S" && change["setting"] as? String == "effort" && change["value"] as? String == "high", "a chip change posts turn-setting: \(change)")

        var sent = false
        model.send("next turn", to: "S") { sent = $0 }
        let deadline = Date().addingTimeInterval(5)
        while !sent, Date() < deadline { try await Task.sleep(for: .milliseconds(20)) }
        let send = TurnSettingStub.actions.last ?? [:]
        check(sent && TurnSettingStub.actions.count == 2 && send["action"] as? String == "send", "the next message is sent: \(TurnSettingStub.actions)")
        check(send["effort"] == nil && send["mode"] == nil, "the send carries no effort or mode: \(send)")
        modelOptions()
        try compactSettings()
        print("turn settings: ok")
    }

    private static func compactSettings() throws {
        let metadata = try JSONDecoder().decode(SessionMetadata.self, from: Data(#"{"sessionId":"S","compactSettings":[{"key":"autoCompact","value":"on","options":[{"id":"on","label":"On"},{"id":"off","label":"Off"}],"applies":"now"},{"key":"compactWindow","value":"","options":[{"id":"auto","label":"Auto"},{"id":"400000","label":"400k"}],"applies":"newSessions","scope":"work"}]}"#.utf8))
        var overlay = MetadataOverlay()
        overlay.absorb(metadata)
        let detail = overlay.apply(to: try JSONDecoder().decode(SessionDetail.self, from: Data(#"{"session":{"id":"S","surface":"claude","name":"Demo","status":"idle","lastActive":"2026-10-04T12:00:00Z"},"exchanges":[],"capabilities":{"send":true,"stream":false,"reply":false,"goal":false,"compact":true,"model":true,"interrupt":false,"steer":false},"readOnly":false,"readOnlyReason":""}"#.utf8)))
        let compact = detail.compactSettings ?? []
        check(compact.map(\.chipName) == ["Auto-compact", "Compact at"], "session metadata carries compact settings to the detail: \(compact.map(\.chipName))")
        check(compact.map(\.appliesNote) == [nil, "For new sessions on work"], "a root window says where it applies: \(compact.map(\.appliesNote))")
        check(compact.last?.valueLabel == "Default" && compact.first?.chipAccessibilityLabel == "Auto-compact, On", "an unset window reads as the default")
        let own = try JSONDecoder().decode(TurnSetting.self, from: Data(#"{"key":"sessionCompactWindow","value":"default","options":[{"id":"default","label":"Root default"},{"id":"150000","label":"150k"}],"applies":"nextMessage","pending":"150000"}"#.utf8))
        check(own.chipName == "This session" && own.valueLabel == "Root default" && own.appliesNote == "150k from the next message", "a pending window shows as pending: \(String(describing: own.appliesNote))")
        let later = try JSONDecoder().decode(SessionMetadata.self, from: Data(#"{"sessionId":"S"}"#.utf8))
        overlay.absorb(later)
        check(overlay.apply(to: detail).compactSettings?.count == 2, "metadata without compact settings keeps the last ones")
    }

    private static func modelOptions() {
        guard let settings = try? JSONDecoder().decode([TurnSetting].self, from: Data(#"[{"key":"model","value":"sonnet","options":[{"id":"sonnet","label":"Claude Sonnet"},{"id":"opus","label":"Claude Opus"}],"applies":"now"}]"#.utf8)) else { return check(false, "a model setting decodes") }
        let catalog = [
            ModelOption(id: "opus", displayName: "Opus 5.5", description: "Deepest", default: nil, allowsCustom: true, supportedReasoningEfforts: ["high"], defaultReasoningEffort: "high"),
            ModelOption(id: "haiku", displayName: "Haiku 5.5", description: nil, default: nil, allowsCustom: true, supportedReasoningEfforts: nil, defaultReasoningEffort: nil),
        ]
        guard let options = settings.modelOptions(catalog: catalog) else { return check(false, "a model setting gives model options") }
        check(options.map(\.id) == ["sonnet", "opus", "haiku"], "descriptor models lead and catalog-only models follow: \(options.map(\.id))")
        check(options[0].displayName == "Claude Sonnet", "a model the catalog lacks keeps its label")
        check(options.contains { $0.allowsCustom == true }, "the catalog's custom IDs still reach the picker")
        check(options[1].displayName == "Opus 5.5" && options[1].description == "Deepest", "a model the catalog knows keeps the catalog's detail")
        check([TurnSetting]().modelOptions(catalog: catalog) == nil, "without a model setting the catalog stands")
        let plain = [ModelOption(id: "gpt-sol", displayName: "Sol", description: nil, default: nil, allowsCustom: nil, supportedReasoningEfforts: nil, defaultReasoningEffort: nil)]
        check(settings.modelOptions(catalog: plain)?.allSatisfy { $0.allowsCustom == nil } == true, "a catalog without custom IDs grants none")
        check(settings.modelOptions(catalog: [])?.map(\.id) == ["sonnet", "opus"], "an empty catalog still lists the descriptor's models")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
