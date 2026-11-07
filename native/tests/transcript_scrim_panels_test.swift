@testable import AgenthailHelpers
import CoreGraphics

@MainActor
final class RecordingSink: TranscriptScrimSink {
    var rows: [ObjectIdentifier: CGRect] = [:]
    var covers: [CGRect] = []
    var flushes: [TranscriptScrim] = []

    func place(_ mask: TranscriptScrimMask, origin: CGPoint?) { covers = mask.layers }
    func placeRow(_ row: ObjectIdentifier, _ layer: CGRect?) { rows[row] = layer }
    func move(to origin: CGPoint) {}
    func flushSoon(_ scrim: TranscriptScrim) { flushes.append(scrim) }
}

final class Key {}

@main
struct TranscriptScrimPanelsTest {
    @MainActor
    static func main() {
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }
        let reach = TranscriptScrimMask.reach
        let far = TranscriptScrimMask.far
        typealias Row = TranscriptScrimMask.Row
        func cores(_ mask: TranscriptScrimMask) -> [CGRect] { mask.layers.map { $0.insetBy(dx: reach, dy: reach) } }
        let ask = Row(rect: CGRect(x: 500, y: 100, width: 140, height: 30), turn: "u1")
        let reply = Row(rect: CGRect(x: 0, y: 142, width: 300, height: 40), turn: "u1")
        let tool = Row(rect: CGRect(x: 0, y: 202, width: 680, height: 24), turn: "u1")
        let next = Row(rect: CGRect(x: 500, y: 246, width: 140, height: 30), turn: "u2")
        let answer = Row(rect: CGRect(x: 0, y: 288, width: 300, height: 40), turn: "u2")
        let panels = cores(TranscriptScrimMask(turns: [tool, answer, ask, next, reply], shape: .panels, end: 360))
        check(panels.contains(CGRect(x: 0, y: -far, width: 680, height: far + 100)), "everything above the first row is covered, rows laid out while scrolling up land under it")
        check(panels.contains(CGRect(x: 0, y: 100, width: 680, height: 126)), "a turn's rows share one full-width panel across the gaps inside it, got \(panels)")
        check(panels.contains(CGRect(x: 0, y: 246, width: 680, height: 114)), "the next turn opens its own panel, which runs down to the content end, got \(panels)")
        check(panels.count == 3, "only the gap above a new turn stays open, got \(panels)")

        let untagged = cores(TranscriptScrimMask(rows: [ask.rect, reply.rect, tool.rect, next.rect], shape: .panels))
        check(untagged.count == 2, "rows without a turn never open a gap, got \(untagged)")

        let late = cores(TranscriptScrimMask(turns: [reply, Row(rect: CGRect(x: 0, y: 400, width: 300, height: 20), turn: "u2")], shape: .panels))
        check(late.contains(CGRect(x: 0, y: 142, width: 300, height: 278)), "a space wider than any laid-out gap is covered as rows not reported yet, even across turns")
        let unturned = Row(rect: CGRect(x: 0, y: 232, width: 300, height: 10))
        let carried = cores(TranscriptScrimMask(turns: [reply, unturned, next], shape: .panels))
        check(carried.count == 3, "a row without a turn joins the turn above it, so the next turn still opens a gap, got \(carried)")
        check(TranscriptScrimMask(turns: [reply], shape: .panels, end: 160).layers.count == 2, "an end inside the last row adds nothing below it")
        check(TranscriptScrimMask(rows: [], shape: .panels, end: 500).layers.isEmpty, "with no rows nothing is covered, so a switch does not darken the pane")
        check(TranscriptScrimMask.layer(row: CGRect(x: 0, y: 10, width: 100, height: 0)) == nil, "an empty row has no layer")

        let band = TranscriptScrimMask(rows: [ask.rect, reply.rect])
        check(band == TranscriptScrimMask(turns: [ask, Row(rect: reply.rect, turn: "u2")], shape: .band, end: 900), "the band shape ignores turns and the content end")
        check(band.layers.count == 3, "the band shape still merges close rows, got \(band.layers.count)")

        let scrim = TranscriptScrim(shape: .panels)
        let sink = RecordingSink()
        scrim.attach(sink)
        let a = Key(), b = Key()
        scrim.update(ObjectIdentifier(a), reply.rect, turn: "u1")
        check(sink.rows[ObjectIdentifier(a)] == reply.rect.insetBy(dx: -reach, dy: -reach), "a row is covered as soon as it reports, before covers are rebuilt")
        scrim.update(ObjectIdentifier(b), tool.rect, turn: "u1")
        check(sink.flushes.count == 1, "row reports in one pass ask for one cover rebuild, got \(sink.flushes.count)")
        check(sink.covers.isEmpty, "covers wait for the flush")
        scrim.flush()
        check(sink.covers.count == 2, "the flush builds one panel for a turn's rows, got \(sink.covers)")
        sink.rows[ObjectIdentifier(b)] = nil
        scrim.retag(ObjectIdentifier(b), turn: "u2")
        scrim.flush()
        check(sink.covers.count == 3, "a row moved to another turn opens a gap above it, got \(sink.covers)")
        check(sink.rows[ObjectIdentifier(b)] == nil, "a turn change alone does not re-place the row's layer")
        scrim.update(ObjectIdentifier(a), nil)
        check(sink.rows[ObjectIdentifier(a)] == nil && scrim.rowLayers == [tool.rect.insetBy(dx: -reach, dy: -reach)], "a row that disappears drops its layer")

        if failures > 0 { fatalError("\(failures) failures") }
        print("ok")
    }
}
