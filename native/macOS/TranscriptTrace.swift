import AppKit

@MainActor
final class TranscriptTrace {
    static let shared = TranscriptTrace()

    enum Event: String, CaseIterable {
        case scrim
        case crossfade
        case composer
        case panels
        case scroll

        var fields: [String] {
            switch self {
            case .scrim: ["hidden", "mask", "rows"]
            case .crossfade: []
            case .composer: ["height"]
            case .panels: ["covers", "rows", "gap", "gapAt", "top", "bottom"]
            case .scroll: ["y", "document", "visible"]
            }
        }

        var capacity: Int {
            switch self {
            case .scrim, .crossfade, .composer: 1024
            case .panels, .scroll: 4096
            }
        }
    }

    struct Entry: Equatable {
        var at: CFTimeInterval = 0
        var event: Event = .scroll
        var values: (Double, Double, Double, Double, Double, Double) = (0, 0, 0, 0, 0, 0)

        static func == (lhs: Entry, rhs: Entry) -> Bool {
            lhs.at == rhs.at && lhs.event == rhs.event && lhs.values == rhs.values
        }

        var list: [Double] { [values.0, values.1, values.2, values.3, values.4, values.5] }
    }

    private struct Ring {
        var slots: [Entry]
        var next = 0
        var filled = false

        init(capacity: Int) { slots = Array(repeating: Entry(), count: capacity) }

        mutating func append(_ entry: Entry) {
            slots[next] = entry
            next += 1
            if next == slots.count { next = 0; filled = true }
        }

        var ordered: ArraySlice<Entry> { filled ? slots[next...] + slots[..<next] : slots[..<next] }
    }

    private var rings: [Event: Ring]
    private var lastScrim: (Double, Double, Double)?
    private var lastComposer: Double?

    init(capacity: ((Event) -> Int)? = nil) {
        rings = Dictionary(uniqueKeysWithValues: Event.allCases.map { ($0, Ring(capacity: capacity?($0) ?? $0.capacity)) })
    }

    var entries: [Entry] { rings.values.flatMap(\.ordered).sorted { $0.at < $1.at } }

    func record(_ event: Event, _ a: Double = 0, _ b: Double = 0, _ c: Double = 0, _ d: Double = 0, _ e: Double = 0, _ f: Double = 0, at: CFTimeInterval = CACurrentMediaTime()) {
        rings[event]?.append(Entry(at: at, event: event, values: (a, b, c, d, e, f)))
    }

    func scrim(hidden: Bool, mask: Bool, rows: Bool) {
        let state: (Double, Double, Double) = (hidden ? 1 : 0, mask ? 1 : 0, rows ? 1 : 0)
        if let last = lastScrim, last == state { return }
        lastScrim = state
        record(.scrim, state.0, state.1, state.2)
    }

    func composer(height: CGFloat) {
        guard lastComposer != height else { return }
        lastComposer = height
        record(.composer, height)
    }

    func panels(covers: Int, rows: [CGRect]) {
        let sorted = rows.sorted { $0.minY < $1.minY }
        var widest: CGFloat = 0, at: CGFloat = 0, bottom = sorted.first?.maxY ?? 0
        for row in sorted.dropFirst() {
            if row.minY - bottom > widest { widest = row.minY - bottom; at = bottom }
            bottom = max(bottom, row.maxY)
        }
        record(.panels, Double(covers), Double(rows.count), widest, at, Double(sorted.first?.minY ?? 0), bottom)
    }

    func lines(wall: Date = Date(), now: CFTimeInterval = CACurrentMediaTime()) -> [String] {
        let format = ISO8601DateFormatter()
        format.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return entries.compactMap { entry in
            var object: [String: Any] = ["event": entry.event.rawValue, "uptime": entry.at, "time": format.string(from: wall.addingTimeInterval(entry.at - now))]
            for (key, value) in zip(entry.event.fields, entry.list) { object[key] = value }
            guard let data = try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]) else { return nil }
            return String(decoding: data, as: UTF8.self)
        }
    }

    func dump(to url: URL = TranscriptTrace.file) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try (lines().joined(separator: "\n") + "\n").write(to: url, atomically: true, encoding: .utf8)
    }

    static func saveAndReveal() {
        do {
            try shared.dump()
            NSWorkspace.shared.activateFileViewerSelecting([file])
        } catch {
            let alert = NSAlert(error: error)
            alert.messageText = "Couldn't save the transcript trace"
            alert.runModal()
        }
    }

    nonisolated static var file: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/Agenthail/transcript-trace.jsonl")
    }
}
