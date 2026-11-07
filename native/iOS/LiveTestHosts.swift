#if DEBUG
import Foundation

enum LiveTestHosts {
    static func loopbackHostIDs(arguments: [String] = ProcessInfo.processInfo.arguments) -> [String] {
        guard arguments.contains(PairingLink.loopbackArgument) else { return [] }
        return PairedCredential.all().filter { $0.endpoint.host == "127.0.0.1" }.map(\.hostID)
    }
}
#endif
