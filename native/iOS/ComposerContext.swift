import Foundation

struct ComposerContext: Equatable {
    let sessionID: String
    let busy: Bool
    let waiting: Bool
    let capabilities: Capabilities
    let readOnly: Bool
    let turnSettings: [TurnSetting]?

    init(_ detail: SessionDetail) {
        sessionID = detail.session.id
        busy = detail.session.status == "busy"
        waiting = detail.session.waitingOn != nil
        capabilities = detail.capabilities
        readOnly = detail.readOnly
        turnSettings = detail.turnSettings
    }
}
