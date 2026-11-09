#if DEBUG
import Foundation

enum LiveTestHosts {
    static let alertOnConnectArgument = "--live-test-alert-on-connect"
    static let connectAlert = "Live test alert on connect."

    static func alertsOnConnect(arguments: [String] = ProcessInfo.processInfo.arguments) -> Bool {
        arguments.contains(alertOnConnectArgument)
    }

    static func loopbackHostIDs(arguments: [String] = ProcessInfo.processInfo.arguments) -> [String] {
        guard arguments.contains(PairingLink.loopbackArgument) else { return [] }
        return PairedCredential.all().filter { $0.endpoint.host == "127.0.0.1" }.map(\.hostID)
    }
}
#endif
