/// One label per host id: the name without its DNS domain, or the full name
/// when another host shares that short name.
enum HostNames {
    static func short(_ name: String) -> String {
        String(name.split(separator: ".", maxSplits: 1).first ?? Substring(name))
    }

    static func labels(_ hosts: [SessionHost]) -> [String: String] {
        let names = Dictionary(hosts.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
        let shortCounts = Dictionary(grouping: Set(names.values), by: short).mapValues(\.count)
        return names.mapValues { shortCounts[short($0), default: 0] > 1 ? $0 : short($0) }
    }

    static func ordered(_ hosts: [SessionHost]) -> [String] {
        let labels = labels(hosts)
        var seen = Set<String>()
        return hosts.compactMap { seen.insert($0.id).inserted ? labels[$0.id] : nil }
    }
}
