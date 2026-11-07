import Foundation

struct LaunchSettings: Equatable, Sendable {
    enum Mode: String, CaseIterable, Sendable {
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
}
