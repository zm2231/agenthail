import Darwin
import Foundation
import Network

struct InterfaceAddress: Equatable {
    let name: String
    let address: String
}

enum Tailnet {
    static func isUp(_ interfaces: [InterfaceAddress] = interfaces()) -> Bool {
        interfaces.contains { $0.name.hasPrefix("utun") && isTailnetAddress($0.address) }
    }

    static func isTailnetAddress(_ text: String) -> Bool {
        let bare = String(text.split(separator: "%", maxSplits: 1).first ?? "")
        if let v4 = IPv4Address(bare) {
            let bytes = [UInt8](v4.rawValue)
            return bytes[0] == 100 && bytes[1] & 0xC0 == 64
        }
        if let v6 = IPv6Address(bare) {
            return [UInt8](v6.rawValue).prefix(6).elementsEqual([0xfd, 0x7a, 0x11, 0x5c, 0xa1, 0xe0])
        }
        return false
    }

    static func names(_ endpoint: URL) -> Bool {
        guard let host = endpoint.host?.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: ".[]")), !host.isEmpty else { return false }
        return host.hasSuffix(".ts.net") || isTailnetAddress(host)
    }

    static func interfaces() -> [InterfaceAddress] {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return [] }
        defer { freeifaddrs(head) }
        var found: [InterfaceAddress] = []
        for entry in sequence(first: first, next: { $0.pointee.ifa_next }).map(\.pointee) {
            guard let address = entry.ifa_addr, entry.ifa_flags & UInt32(IFF_UP) != 0 else { continue }
            let family = Int32(address.pointee.sa_family)
            guard family == AF_INET || family == AF_INET6 else { continue }
            let length = socklen_t(family == AF_INET ? MemoryLayout<sockaddr_in>.size : MemoryLayout<sockaddr_in6>.size)
            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            guard getnameinfo(address, length, &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else { continue }
            let text = String(decoding: host.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }, as: UTF8.self)
            found.append(InterfaceAddress(name: String(cString: entry.ifa_name), address: text))
        }
        return found
    }
}

enum HostProblem: Equatable {
    case offline
    case timedOut(TimeInterval?)
    case notFound
    case dropped
    case refused

    init?(_ error: Error, waited: TimeInterval? = nil) {
        guard let url = error as? URLError else { return nil }
        switch url.code {
        case .notConnectedToInternet, .dataNotAllowed, .internationalRoamingOff, .callIsActive: self = .offline
        case .timedOut: self = .timedOut(waited)
        case .cannotFindHost, .dnsLookupFailed: self = .notFound
        case .networkConnectionLost: self = .dropped
        case .cannotConnectToHost: self = .refused
        default: return nil
        }
    }

    var message: String {
        switch self {
        case .offline: "This iPhone isn't connected to the internet."
        case .timedOut(let waited): waited.map { "Didn't answer within \(max(1, Int($0.rounded()))) s." } ?? "Didn't answer in time."
        case .notFound: "Its address can't be found from this network."
        case .dropped: "The connection dropped."
        case .refused: "Refused the connection."
        }
    }

    static let tailnetOff = "Tailscale is off on this iPhone, so it can't reach your computers."

    struct Failing {
        let name: String
        let error: String
        let problem: HostProblem?
        let endpoint: URL
    }

    static func summary(_ failing: [Failing], hostCount: Int, tailnetUp: () -> Bool) -> String? {
        guard !failing.isEmpty else { return nil }
        if failing.allSatisfy({ $0.problem == .offline }) { return HostProblem.offline.message }
        if failing.allSatisfy({ $0.problem != nil && Tailnet.names($0.endpoint) }), !tailnetUp() { return tailnetOff }
        if hostCount == 1 { return failing[0].error }
        return failing.map { "\($0.name): \($0.error)" }.joined(separator: "\n")
    }
}
