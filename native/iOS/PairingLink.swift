import Foundation

struct PairingLink {
    static let loopbackArgument = "--live-test-loopback"

    let endpoint: URL
    let secret: String
    let hostID: String
    let hostName: String?

    init(url: URL, arguments: [String] = ProcessInfo.processInfo.arguments) throws {
        guard url.scheme?.lowercased() == "agenthail", url.host?.lowercased() == "pair", url.fragment == nil,
              let components = URLComponents(url: url, resolvingAgainstBaseURL: false),
              let endpointValue = components.queryItems?.first(where: { $0.name == "endpoint" })?.value,
              let endpoint = URL(string: endpointValue),
              let secret = components.queryItems?.first(where: { $0.name == "secret" })?.value,
              let hostID = components.queryItems?.first(where: { $0.name == "host" })?.value,
              !hostID.isEmpty, hostID.count <= 64 else {
            throw PairingLinkError.invalid
        }
        guard endpoint.user == nil, endpoint.password == nil,
              endpoint.fragment == nil, endpoint.query == nil, endpoint.path.isEmpty || endpoint.path == "/",
              endpoint.port.map({ (1...65535).contains($0) }) ?? true,
              Self.isTailscale(endpoint) || Self.isLiveTestLoopback(endpoint, arguments: arguments),
              secret.count >= 20, secret.count <= 256 else {
            throw PairingLinkError.invalid
        }
        self.endpoint = endpoint
        self.secret = secret
        self.hostID = hostID
        hostName = components.queryItems?.first(where: { $0.name == "name" })?.value.flatMap { $0.isEmpty ? nil : $0 }
    }

    private static func isTailscale(_ endpoint: URL) -> Bool {
        let host = endpoint.host?.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: ".")) ?? ""
        return endpoint.scheme?.lowercased() == "https" && host.hasSuffix(".ts.net") && host.count > ".ts.net".count
    }

    private static func isLiveTestLoopback(_ endpoint: URL, arguments: [String]) -> Bool {
        #if DEBUG
        return arguments.contains(loopbackArgument) && endpoint.scheme?.lowercased() == "http"
            && endpoint.host == "127.0.0.1" && endpoint.port != nil
        #else
        return false
        #endif
    }
}

enum PairingLinkError: LocalizedError {
    case invalid

    var errorDescription: String? {
        "This pairing code is not a secure Agenthail Tailscale link."
    }
}
