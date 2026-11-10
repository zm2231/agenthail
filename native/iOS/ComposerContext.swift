import Foundation

struct ComposerContext: Equatable, ModelChoices {
    let sessionID: String
    let busy: Bool
    let waiting: Bool
    let capabilities: Capabilities
    let readOnly: Bool
    let turnSettings: [TurnSetting]?
    let models: [ModelOption]?
    let model: String?

    init(_ detail: SessionDetail) {
        sessionID = detail.session.id
        busy = detail.session.status == "busy"
        waiting = detail.session.waitingOn != nil
        capabilities = detail.capabilities
        readOnly = detail.readOnly
        turnSettings = detail.turnSettings
        models = detail.models
        model = detail.model
    }
}
