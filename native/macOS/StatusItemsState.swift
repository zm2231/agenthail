import Foundation

struct StatusItemsState: Equatable {
    let items: [String]
    let short: Bool
    let working: Int
    let needsYou: Int
    let hosts: String

    static func hosts(_ hosts: [HostHealth]) -> String {
        (["this Mac"] + hosts.filter { $0.state == "connected" }.map(\.name)).joined(separator: " · ")
    }
}
