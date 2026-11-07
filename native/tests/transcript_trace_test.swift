@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptTraceTest {
    @MainActor static func main() {
        let trace = TranscriptTrace(capacity: { $0 == .scroll ? 4 : 8 })
        trace.scrim(hidden: false, mask: false, rows: true)
        trace.scrim(hidden: false, mask: false, rows: true)
        for y in 0..<10 { trace.record(.scroll, Double(y), 900, 600, at: 10 + Double(y)) }
        trace.scrim(hidden: true, mask: false, rows: false)
        let scrolls = trace.entries.filter { $0.event == .scroll }.map(\.values.0)
        expect(scrolls == [6, 7, 8, 9], "a full ring keeps the newest entries in order, got \(scrolls)")
        let hides = trace.entries.filter { $0.event == .scrim }.map(\.values.0)
        expect(hides == [0, 1], "scroll entries never evict scrim changes, and an unchanged scrim state is recorded once, got \(hides)")

        trace.composer(height: 44)
        trace.composer(height: 44)
        trace.composer(height: 72)
        expect(trace.entries.filter { $0.event == .composer }.map(\.values.0) == [44, 72], "the composer height is recorded when it changes")

        trace.panels(covers: 3, rows: [CGRect(x: 0, y: 400, width: 10, height: 50), CGRect(x: 0, y: 100, width: 10, height: 40), CGRect(x: 0, y: 150, width: 10, height: 30)])
        let panels = trace.entries.last { $0.event == .panels }
        expect(panels?.values.2 == 220 && panels?.values.3 == 180, "panels record the widest gap between reported rows and where it starts, got \(String(describing: panels))")

        let ordered = trace.entries.map(\.at)
        expect(ordered == ordered.sorted(), "entries from every ring come back in time order")

        let now = CFTimeInterval(100)
        let line = TranscriptTrace(capacity: { _ in 2 })
        line.record(.scroll, 12, 900, 600, at: 99)
        let wall = Date(timeIntervalSince1970: 1_800_000_000)
        guard let text = line.lines(wall: wall, now: now).first,
              let object = try? JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any] else {
            expect(false, "a line is one JSON object")
            return
        }
        expect(object["event"] as? String == "scroll" && object["y"] as? Double == 12 && object["document"] as? Double == 900, "a line names the event and its fields, got \(object)")
        expect(object["time"] as? String == "2027-01-15T07:59:59.000Z", "a line carries the wall time of the entry, got \(String(describing: object["time"]))")

        let file = FileManager.default.temporaryDirectory.appendingPathComponent("transcript-trace-\(UUID().uuidString)/trace.jsonl")
        defer { try? FileManager.default.removeItem(at: file.deletingLastPathComponent()) }
        expect((try? line.dump(to: file)) != nil, "the dump writes its file")
        expect((try? String(contentsOf: file, encoding: .utf8))?.split(separator: "\n").count == 1, "the dump holds one line per entry")
        print("ok")
    }

    static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
