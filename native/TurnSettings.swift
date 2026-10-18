import Foundation

struct TurnSettings: Codable, Equatable, Sendable {
    enum Mode: String, Codable, CaseIterable, Sendable {
        case `default`
        case plan
    }

    var effort: String?
    var mode: Mode?

    init(effort: String? = nil, mode: Mode? = nil) {
        self.effort = effort
        self.mode = mode
    }

    var isEmpty: Bool { effort == nil && mode == nil }

    enum CodingKeys: String, CodingKey {
        case effort
        case mode
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encodeIfPresent(effort, forKey: .effort)
        try container.encodeIfPresent(mode, forKey: .mode)
    }
}
