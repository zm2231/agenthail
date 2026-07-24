import Foundation

@main
struct StatusRefreshPolicyTest {
    static func main() {
        guard StatusRefreshPolicy.interval(reconnecting: false) == .seconds(30) else { exit(1) }
        guard StatusRefreshPolicy.interval(reconnecting: true) == .seconds(5) else { exit(1) }
    }
}
