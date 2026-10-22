import Foundation

struct PairingLink {
    let endpoint: URL
    let secret: String
    let hostID: String
    let hostName: String?

    init(url: URL) throws {
        guard url.scheme?.lowercased() == "agenthail", url.host?.lowercased() == "pair", url.fragment == nil,
              let components = URLComponents(url: url, resolvingAgainstBaseURL: false),
              let endpointValue = components.queryItems?.first(where: { $0.name == "endpoint" })?.value,
              let endpoint = URL(string: endpointValue),
              let secret = components.queryItems?.first(where: { $0.name == "secret" })?.value,
              let hostID = components.queryItems?.first(where: { $0.name == "host" })?.value,
              !hostID.isEmpty, hostID.count <= 64 else {
            throw PairingLinkError.invalid
        }
        let host = endpoint.host?.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: ".")) ?? ""
        guard endpoint.scheme?.lowercased() == "https", endpoint.user == nil, endpoint.password == nil,
              endpoint.fragment == nil, endpoint.query == nil, endpoint.path.isEmpty || endpoint.path == "/",
              host.hasSuffix(".ts.net"), host.count > ".ts.net".count,
              endpoint.port.map({ (1...65535).contains($0) }) ?? true,
              secret.count >= 20, secret.count <= 256 else {
            throw PairingLinkError.invalid
        }
        self.endpoint = endpoint
        self.secret = secret
        self.hostID = hostID
        hostName = components.queryItems?.first(where: { $0.name == "name" })?.value.flatMap { $0.isEmpty ? nil : $0 }
    }
}

enum PairingLinkError: LocalizedError {
    case invalid

    var errorDescription: String? {
        "This pairing code is not a secure Agenthail Tailscale link."
    }
}
