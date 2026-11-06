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
        let reply = CGRect(x: 0, y: 100, width: 300, height: 40)
        let bubble = CGRect(x: 500, y: 160, width: 140, height: 30)
        let tool = CGRect(x: 0, y: 202, width: 680, height: 24)
        let panels = TranscriptScrimMask(rows: [tool, reply, bubble], shape: .panels, end: 260)
        let cores = panels.layers.map { $0.insetBy(dx: reach, dy: reach) }
        check(cores.contains(CGRect(x: 0, y: -far, width: 680, height: far + 100)), "everything above the first row is covered, rows laid out while scrolling up land under it")
        check(cores.contains(CGRect(x: 0, y: 100, width: 680, height: 40)), "a turn's panel spans the transcript's full width, so edges line up")
        check(cores.contains(CGRect(x: 0, y: 160, width: 680, height: 100)), "rows less than a turn gap apart share one panel, which runs down to the content end")
        check(cores.count == 3, "the gap above a new turn stays open, got \(cores)")

        let late = TranscriptScrimMask(rows: [reply, CGRect(x: 0, y: 400, width: 300, height: 20)], shape: .panels)
        check(late.layers.map { $0.insetBy(dx: reach, dy: reach) }.contains(CGRect(x: 0, y: 100, width: 300, height: 320)), "a space wider than any laid-out gap is covered as rows not reported yet")
        check(TranscriptScrimMask(rows: [reply], shape: .panels, end: 120).layers.count == 2, "an end inside the last row adds nothing below it")
        check(TranscriptScrimMask(rows: [], shape: .panels, end: 500).layers.isEmpty, "with no rows nothing is covered, so a switch does not darken the pane")
        check(TranscriptScrimMask.layer(row: CGRect(x: 0, y: 10, width: 100, height: 0)) == nil, "an empty row has no layer")

        let band = TranscriptScrimMask(rows: [reply, bubble])
        check(band == TranscriptScrimMask(rows: [reply, bubble], shape: .band, end: 900), "the band shape ignores the content end")
        check(band.layers.count == 3, "the band shape still merges close rows, got \(band.layers.count)")

        let scrim = TranscriptScrim(shape: .panels)
        let sink = RecordingSink()
        scrim.attach(sink)
        let a = Key(), b = Key()
        scrim.update(ObjectIdentifier(a), reply)
        check(sink.rows[ObjectIdentifier(a)] == reply.insetBy(dx: -reach, dy: -reach), "a row is covered as soon as it reports, before covers are rebuilt")
        scrim.update(ObjectIdentifier(b), bubble)
        check(sink.flushes.count == 1, "row reports in one pass ask for one cover rebuild, got \(sink.flushes.count)")
        check(sink.covers.isEmpty, "covers wait for the flush")
        scrim.flush()
        check(sink.covers.count == 3, "the flush builds the panels for every reported row, got \(sink.covers)")
        scrim.update(ObjectIdentifier(a), nil)
        check(sink.rows[ObjectIdentifier(a)] == nil && sink.rows[ObjectIdentifier(b)] != nil, "a row that disappears drops its layer")
        check(scrim.rowLayers == [bubble.insetBy(dx: -reach, dy: -reach)], "only reported rows keep layers")

        if failures > 0 { fatalError("\(failures) failures") }
        print("ok")
    }
}
