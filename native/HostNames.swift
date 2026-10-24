/// One distinct label per host id: short name, else full name, else full name and id.
enum HostNames {
    static func short(_ name: String) -> String {
        String(name.split(separator: ".", maxSplits: 1).first ?? Substring(name))
    }

    static func labels(_ hosts: [SessionHost]) -> [String: String] {
        let names = Dictionary(hosts.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
        let shortCounts = Dictionary(grouping: names.values, by: short).mapValues(\.count)
        let fullCounts = Dictionary(grouping: names.values, by: { $0 }).mapValues(\.count)
        return Dictionary(uniqueKeysWithValues: names.map { id, name in
            if fullCounts[name, default: 0] > 1 { return (id, "\(name) (\(id))") }
            return (id, shortCounts[short(name), default: 0] > 1 ? name : short(name))
        })
    }

    static func ordered(_ hosts: [SessionHost]) -> [String] {
        let labels = labels(hosts)
        var seen = Set<String>()
        return hosts.compactMap { seen.insert($0.id).inserted ? labels[$0.id] : nil }
    }
}
