import Foundation

enum SlashCommand: String, CaseIterable, Identifiable {
    case name, compact, stop, steer, model, goal

    var id: String { rawValue }
    var token: String { "/" + rawValue }
    var needsArgument: Bool { [.name, .steer, .model, .goal].contains(self) }

    var summary: String {
        switch self {
        case .name: return "Name this conversation"
        case .compact: return "Compact this conversation"
        case .stop: return "Stop the current turn"
        case .steer: return "Redirect the current turn"
        case .model: return "Choose the active model"
        case .goal: return "Set the session goal"
        }
    }

    static func available(capabilities: Capabilities, working: Bool) -> [SlashCommand] {
        allCases.filter { command in
            switch command {
            case .name: return true
            case .compact: return capabilities.compact
            case .stop: return working && capabilities.interrupt
            case .steer: return working && capabilities.steer
            case .model: return capabilities.model
            case .goal: return capabilities.goal
            }
        }
    }

    static func suggestions(for text: String, available: [SlashCommand]) -> [SlashCommand] {
        let value = text.drop { $0.isWhitespace }
        guard value.hasPrefix("/"), !value.contains(where: \.isWhitespace) else { return [] }
        let query = value.lowercased()
        return available.filter { $0.token.hasPrefix(query) }
    }

    static func modelQuery(for text: String) -> String? {
        let value = String(text.drop { $0.isWhitespace })
        guard value.lowercased().hasPrefix("/model ") else { return nil }
        return value.dropFirst(7).trimmingCharacters(in: .whitespaces)
    }
}

enum SlashInput: Equatable {
    case message
    case command(SlashCommand, argument: String)
    case missingArgument(SlashCommand)
    case unavailable(SlashCommand)

    init(_ text: String, available: [SlashCommand]) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.hasPrefix("/") else { self = .message; return }
        let head = trimmed.prefix { !$0.isWhitespace }
        guard let command = SlashCommand(rawValue: head.dropFirst().lowercased()) else { self = .message; return }
        let argument = trimmed.dropFirst(head.count).trimmingCharacters(in: .whitespacesAndNewlines)
        if !available.contains(command) {
            self = .unavailable(command)
        } else if command.needsArgument && argument.isEmpty {
            self = .missingArgument(command)
        } else {
            self = .command(command, argument: argument)
        }
    }

    var problem: String? {
        switch self {
        case .missingArgument(let command): return "\(command.token) needs a value."
        case .unavailable(let command): return "\(command.token) isn't available for this session right now."
        case .message, .command: return nil
        }
    }
}
