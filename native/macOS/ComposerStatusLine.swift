import Foundation

enum ComposerStatusLine {
    struct State {
        var waitingOnYou = false
        var turnNotice = ""
        var working = false
        var hasMessage = false
        var isCommand = false
        var steerRefusal: String?
        var followUpHint = ""
        var stopShortcut: String?
        var unreachableHost: String?
    }

    static func text(_ state: State) -> String {
        if let host = state.unreachableHost { return "Will send when \(host) is back." }
        if state.waitingOnYou { return "This agent is waiting on you. Messages queue until you answer it." }
        if !state.turnNotice.isEmpty { return state.turnNotice }
        guard state.working else { return "Ready to send." }
        if state.hasMessage, let refusal = state.steerRefusal { return refusal }
        if state.hasMessage && !state.isCommand { return state.followUpHint }
        guard let stop = state.stopShortcut else { return "This agent is working. Stop ends the current turn." }
        return "This agent is working. Stop (\(stop)) ends the current turn."
    }
}
