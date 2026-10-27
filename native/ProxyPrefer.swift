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

    struct Line: Equatable {
        let text: String
        var help: String? = nil
    }

    var changeLines: [Line] {
        if let nothing { return [Line(text: nothing)] }
        var lines = changes.map { Line(text: "\($0.label): \(Self.word($0.before)) → \(Self.word($0.after))", help: "quota_preference \(Self.number($0.before)) → \(Self.number($0.after))") }
        if set != ProxyPreferSet.only.rawValue { lines.append(Line(text: "Other accounts are not changed.")) }
        if !release.isEmpty { lines.append(Line(text: "Releases the sessions on " + release.map(\.label).joined(separator: ", ") + ", so they move on their next request.")) }
        return lines
    }

    var affectLines: [String] {
        guard nothing == nil else { return [] }
        return hosts.map { host in
            let running = host.sessions.map(\.name)
            return host.host + ": " + host.roots.joined(separator: ", ") + (running.isEmpty ? " · no running sessions" : " · running: " + running.joined(separator: ", "))
        }
    }

    var notes: [String] { nothing == nil ? warnings ?? [] : [] }

    static func word(_ value: Double) -> String {
        value == 0 ? "not preferred" : value >= 10 ? "preferred" : "preference " + number(value)
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
