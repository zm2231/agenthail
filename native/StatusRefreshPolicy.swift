import Foundation

enum StatusRefreshPolicy {
    static func interval(reconnecting: Bool) -> Duration {
        reconnecting ? .seconds(5) : .seconds(30)
    }
}
