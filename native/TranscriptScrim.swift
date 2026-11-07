import CoreGraphics

@MainActor
protocol TranscriptScrimSink: AnyObject {
    func place(_ mask: TranscriptScrimMask, origin: CGPoint?)
    func placeRow(_ row: ObjectIdentifier, _ layer: CGRect?)
    func move(to origin: CGPoint)
    func flushSoon(_ scrim: TranscriptScrim)
}

@MainActor
final class TranscriptScrim {
    let shape: TranscriptScrimMask.Shape
    private weak var sink: TranscriptScrimSink?
    private(set) var mask: TranscriptScrimMask
    private(set) var origin: CGPoint?
    private var keyed: [ObjectIdentifier: TranscriptScrimMask.Row] = [:]
    private var end: CGFloat?
    private var column: ClosedRange<CGFloat>?
    private var pending = false

    init(shape: TranscriptScrimMask.Shape = .band) {
        self.shape = shape
        mask = TranscriptScrimMask(rows: [], shape: shape)
    }

    var rowLayers: [CGRect] { keyed.values.compactMap { TranscriptScrimMask.layer(row: $0.rect) } }

    func attach(_ sink: TranscriptScrimSink) {
        guard self.sink !== sink else { return }
        self.sink = sink
        sink.place(mask, origin: origin)
        for (row, entry) in keyed { sink.placeRow(row, TranscriptScrimMask.layer(row: entry.rect)) }
    }

    func report(_ rows: [CGRect]) {
        rebuild(rows.map { TranscriptScrimMask.Row(rect: $0) })
    }

    func update(_ row: ObjectIdentifier, _ rect: CGRect?, turn: String? = nil) {
        let next = rect.map { TranscriptScrimMask.Row(rect: $0, turn: turn) }
        guard keyed[row] != next else { return }
        let moved = keyed[row]?.rect != rect
        keyed[row] = next
        if moved { sink?.placeRow(row, rect.flatMap { TranscriptScrimMask.layer(row: $0) }) }
        schedule()
    }

    func retag(_ row: ObjectIdentifier, turn: String?) {
        guard let entry = keyed[row], entry.turn != turn else { return }
        update(row, entry.rect, turn: turn)
    }

    func reportEnd(_ end: CGFloat) {
        guard shape != .band, end != self.end else { return }
        self.end = end
        schedule()
    }

    // Off-screen lazy rows keep the frames of an older width, so the column sets the panels' sides.
    func reportColumn(_ column: ClosedRange<CGFloat>) {
        guard shape != .band, column != self.column else { return }
        self.column = column
        schedule()
    }

    func flush() {
        guard pending else { return }
        pending = false
        rebuild(Array(keyed.values))
    }

    func move(to origin: CGPoint) {
        guard origin != self.origin else { return }
        let first = self.origin == nil
        self.origin = origin
        if first { sink?.place(mask, origin: origin) } else { sink?.move(to: origin) }
    }

    private func schedule() {
        guard !pending else { return }
        pending = true
        if let sink { sink.flushSoon(self) } else { flush() }
    }

    private func rebuild(_ rows: [TranscriptScrimMask.Row]) {
        let next = TranscriptScrimMask(turns: rows, shape: shape, end: end, column: column)
        guard next != mask else { return }
        mask = next
        sink?.place(mask, origin: origin)
    }

    private static var images: [CGFloat: CGImage] = [:]

    static func image(scale: CGFloat) -> CGImage? {
        if let image = images[scale] { return image }
        let image = TranscriptScrimMask.image(scale: scale)
        images[scale] = image
        return image
    }
}
