import Foundation

struct UsageProxyAccountRef: Equatable, Hashable {
    let proxyId: String
    let id: String
    let preferred: Bool
    let setOn: String?
    let setAt: Date?

    var setNote: String? {
        guard let setOn, let setAt else { return nil }
        return "set on " + setOn + " at " + setAt.formatted(date: .omitted, time: .shortened)
    }
}

enum ProxyPreferSet: String {
    case on, off, only
}

struct ProxyPreferPlan: Decodable, Equatable {
    struct Change: Decodable, Equatable {
        let id: String
        let label: String
        let before: Double
        let after: Double
    }
    struct Account: Decodable, Equatable {
        let id: String
        let label: String
    }
    struct Session: Decodable, Equatable {
        let id: String
        let name: String
    }
    struct Host: Decodable, Equatable {
        let host: String
        let roots: [String]
        let sessions: [Session]
    }
    let set: String
    var now: Bool? = nil
    let changes: [Change]
    let release: [Account]
    let hosts: [Host]
    var nothing: String? = nil
    var confirm: String? = nil
    var warnings: [String]? = nil
    let planId: String

    var lines: [String] {
        if let nothing { return [nothing] }
        var lines = changes.map { "\($0.label): preference \(Self.number($0.before)) → \(Self.number($0.after))" }
        if set != ProxyPreferSet.only.rawValue { lines.append("Other accounts are not changed.") }
        if !release.isEmpty { lines.append("Releases the sessions on " + release.map(\.label).joined(separator: ", ") + ".") }
        for host in hosts {
            let running = host.sessions.map(\.name)
            lines.append(host.host + ": " + host.roots.joined(separator: ", ") + (running.isEmpty ? " · no running sessions" : " · running: " + running.joined(separator: ", ")))
        }
        return lines + (warnings ?? [])
    }

    private static func number(_ value: Double) -> String {
        value.rounded() == value ? String(Int(value)) : String(value)
    }
}

struct ProxyPreferResult: Decodable {
    var message: String? = nil
}

extension UsageReport.ProxyAccount {
    func reference(proxy: String) -> UsageProxyAccountRef {
        UsageProxyAccountRef(proxyId: proxy, id: id, preferred: preferred == true, setOn: setOn, setAt: setAt.flatMap(UsageMeterText.date))
    }
}
