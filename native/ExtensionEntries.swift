import Foundation
import Observation

struct ExtensionEntry: Identifiable, Equatable, Sendable {
    enum Kind: Equatable, Hashable, Sendable {
        case page
        case mount(String)
    }

    let hostID: String
    let hostName: String
    let extensionID: String
    let extensionLabel: String
    let kind: Kind
    let label: String
    let detail: String?
    let icon: String
    let order: Int
    let offered: Bool
    let badge: String?
    let url: URL?
    let unavailable: String?

    static let fallbackIcon = "puzzlepiece.extension"

    var id: String { Self.id(host: hostID, extension: extensionID, kind: kind) }

    static func id(host: String, extension id: String, kind: Kind) -> String {
        switch kind {
        case .page: "\(host)/\(id)/page"
        case .mount(let name): "\(host)/\(id)/mount/\(name)"
        }
    }
}

enum ExtensionEntries {
    static func build(_ list: ExtensionsList, hostID: String, hostName: String, origin: URL) -> [ExtensionEntry] {
        var entries: [ExtensionEntry] = []
        for summary in list.extensions {
            if let page = summary.page {
                let (url, unavailable) = summary.state == "loaded"
                    ? target(port: page.port, entry: page.entry, published: page.published, error: page.error ?? summary.error, origin: origin, hostName: hostName)
                    : (nil, summary.error ?? "This extension is \(summary.state).")
                entries.append(ExtensionEntry(
                    hostID: hostID, hostName: hostName, extensionID: summary.id, extensionLabel: summary.label, kind: .page,
                    label: label(summary.app, or: summary.label), detail: distinct(summary.description, from: label(summary.app, or: summary.label)),
                    icon: icon(summary.app), order: summary.app?.order ?? 0,
                    offered: summary.app != nil, badge: shown(summary.badge), url: url, unavailable: unavailable))
            }
            for mount in summary.mounts ?? [] {
                let (url, unavailable) = mountTarget(mount, origin: origin, hostName: hostName)
                entries.append(ExtensionEntry(
                    hostID: hostID, hostName: hostName, extensionID: summary.id, extensionLabel: summary.label, kind: .mount(mount.name),
                    label: label(mount.app, or: mount.label), detail: distinct(summary.label, from: label(mount.app, or: mount.label)),
                    icon: icon(mount.app), order: mount.app?.order ?? 0,
                    offered: mount.app != nil, badge: shown(mount.badge), url: url, unavailable: unavailable))
            }
        }
        return entries.sorted(by: hubOrder)
    }

    static func mountTarget(_ mount: ExtensionMount, origin: URL, hostName: String) -> (URL?, String?) {
        if mount.state == "pending" && (mount.error ?? "").isEmpty { return (nil, "Waiting for approval on \(hostName).") }
        guard mount.state == "serving" || !(mount.error ?? "").isEmpty else { return (nil, "This mount is \(mount.state).") }
        return target(port: mount.port, entry: mount.entry, published: mount.published, error: mount.error, origin: origin, hostName: hostName)
    }

    static func target(port: Int?, entry: String, published: Bool?, error: String?, origin: URL, hostName: String) -> (URL?, String?) {
        if let error, !error.isEmpty { return (nil, error) }
        guard let port, port > 0 else { return (nil, "\(hostName) isn't serving this yet.") }
        if origin.scheme == "https" && published != true { return (nil, "\(hostName) doesn't publish this on the tailnet.") }
        var parts = URLComponents()
        parts.scheme = origin.scheme
        parts.host = origin.host
        parts.port = port
        guard let base = parts.url, let url = URL(string: entry.hasPrefix("/") ? entry : "/" + entry, relativeTo: base)?.absoluteURL else {
            return (nil, "This address is not valid.")
        }
        return (url, nil)
    }

    static func pinned(_ entries: [ExtensionEntry], pins: Set<String>) -> [ExtensionEntry] {
        entries.filter { pins.contains($0.id) }.sorted { ($0.order, $0.label.localizedLowercase, $0.id) < ($1.order, $1.label.localizedLowercase, $1.id) }
    }

    private static func hubOrder(_ a: ExtensionEntry, _ b: ExtensionEntry) -> Bool {
        (a.extensionLabel.localizedLowercase, a.extensionID, a.kind == .page ? 0 : 1, a.label.localizedLowercase) < (b.extensionLabel.localizedLowercase, b.extensionID, b.kind == .page ? 0 : 1, b.label.localizedLowercase)
    }

    private static func label(_ offer: ExtensionAppOffer?, or fallback: String) -> String {
        let label = offer?.label?.trimmingCharacters(in: .whitespaces) ?? ""
        return label.isEmpty ? fallback : label
    }

    private static func icon(_ offer: ExtensionAppOffer?) -> String {
        let icon = offer?.icon ?? ""
        return icon.isEmpty ? ExtensionEntry.fallbackIcon : icon
    }

    private static func distinct(_ detail: String?, from label: String) -> String? {
        let detail = detail?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return detail.isEmpty || detail.caseInsensitiveCompare(label) == .orderedSame ? nil : detail
    }

    private static func shown(_ badge: String?) -> String? {
        guard let badge, !badge.isEmpty else { return nil }
        return badge
    }
}

@MainActor
@Observable
final class ExtensionPins {
    static let shared = ExtensionPins()
    private static let key = "extensionPins"
    private let defaults: UserDefaults
    private(set) var ids: Set<String>

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        ids = Set(defaults.stringArray(forKey: Self.key) ?? [])
    }

    func isPinned(_ entry: ExtensionEntry) -> Bool { ids.contains(entry.id) }

    func pins(host hostID: String) -> Bool {
        ids.contains { $0.hasPrefix(hostID + "/") }
    }

    func toggle(_ entry: ExtensionEntry) {
        if ids.contains(entry.id) { ids.remove(entry.id) } else { ids.insert(entry.id) }
        defaults.set(ids.sorted(), forKey: Self.key)
    }
}

@MainActor
@Observable
final class ExtensionCatalog {
    private(set) var hostID: String
    private(set) var hostName: String
    private(set) var list: ExtensionsList?
    private(set) var entries: [ExtensionEntry] = []
    private(set) var error: String?
    private(set) var loading = false
    private(set) var signIn: ExtensionSignIn?
    @ObservationIgnored private(set) var api: AgenthailAPI?
    @ObservationIgnored private var refreshTask: Task<Void, Never>?
    @ObservationIgnored private var again = false
    @ObservationIgnored private(set) var wanted = false

    init(hostID: String = "", hostName: String = "", api: AgenthailAPI? = nil) {
        self.hostID = hostID
        self.hostName = hostName
        self.api = api
        signIn = api.map(ExtensionSignIn.init)
    }

    @discardableResult
    func attach(_ api: AgenthailAPI?, hostID: String, hostName: String) -> Bool {
        let renamed = hostID != self.hostID || hostName != self.hostName
        self.hostID = hostID
        self.hostName = hostName
        guard self.api !== api else {
            if renamed, let list, let api {
                entries = ExtensionEntries.build(list, hostID: hostID, hostName: hostName, origin: api.origin)
            }
            return false
        }
        self.api = api
        signIn = api.map(ExtensionSignIn.init)
        list = nil
        entries = []
        error = nil
        if wanted { refresh() }
        return true
    }

    func entry(_ id: String) -> ExtensionEntry? { entries.first { $0.id == id } }

    func refresh() {
        wanted = true
        guard refreshTask == nil else {
            again = true
            return
        }
        guard let api else { return }
        loading = true
        refreshTask = Task { [weak self] in
            let result: Result<ExtensionsList, Error>
            do { result = .success(try await api.extensions()) } catch { result = .failure(error) }
            self?.apply(result, from: api)
        }
    }

    private func apply(_ result: Result<ExtensionsList, Error>, from source: AgenthailAPI) {
        loading = false
        refreshTask = nil
        if source === api {
            switch result {
            case .success(let list):
                self.list = list
                entries = ExtensionEntries.build(list, hostID: hostID, hostName: hostName, origin: source.origin)
                error = list.host == "running" ? nil : (list.hostError ?? (list.host == "off" ? "Extensions are off on \(hostName)." : "The extension host on \(hostName) is \(list.host)."))
            case .failure(let failure):
                error = failure.localizedDescription
            }
        }
        if again {
            again = false
            refresh()
        }
    }

    static func refreshes(for eventType: String) -> Bool {
        eventType == "extension.changed" || eventType == "extension.badge" || eventType == "stream.reset"
    }
}

enum ExtensionHistory {
    static func describe(_ change: ExtensionChange) -> String {
        let parts = (change.extensions ?? []).map { files -> String in
            if files.created == true { return "created \(files.id)" }
            if files.removed == true { return "removed \(files.id)" }
            let count = (files.added?.count ?? 0) + (files.updated?.count ?? 0) + (files.deleted?.count ?? 0)
            return "changed \(count) \(count == 1 ? "file" : "files") in \(files.id)"
        }
        return parts.isEmpty ? (change.message ?? "no extension files") : parts.joined(separator: ", ")
    }
}
