import AppKit

@MainActor
final class ScrimRows: TranscriptScrimSink {
    let root = CALayer()
    weak var host: BackdropLayerView?
    weak var originView: ScrimRowsOriginView? {
        didSet { position() }
    }
    private let rows = TranscriptScrimRowLayers()
    private var mask = TranscriptScrimMask(rows: [], shape: .panels)
    private var origin: CGPoint?
    private weak var waiting: TranscriptScrim?

    init() {
        root.actions = TranscriptScrimRowLayers.still
        root.isGeometryFlipped = true
        root.addSublayer(rows.content)
    }

    var active: Bool { origin != nil && !rows.isEmpty }

    private var scale: CGFloat { host?.window?.backingScaleFactor ?? 2 }

    func place(_ mask: TranscriptScrimMask, origin: CGPoint?) {
        self.mask = mask
        self.origin = origin
        changed { rows.place(mask, scale: scale) }
    }

    func placeRow(_ row: ObjectIdentifier, _ layer: CGRect?) {
        changed { rows.placeRow(row, layer, scale: scale) }
    }

    func flushSoon(_ scrim: TranscriptScrim) {
        guard let host else {
            scrim.flush()
            return
        }
        waiting = scrim
        host.needsLayout = true
    }

    func flush() {
        let scrim = waiting
        waiting = nil
        scrim?.flush()
    }

    func move(to origin: CGPoint) {
        self.origin = origin
        position()
    }

    func position() {
        guard let host, let origin, let content = host.window?.contentView else { return }
        let point: NSPoint
        if let view = originView, view.window === host.window {
            point = host.convert(view.topLeft, from: view)
        } else {
            point = host.convert(origin, from: content)
        }
        let box = host.boxFrame
        transaction {
            rows.rescale(scale)
            rows.content.frame = CGRect(x: point.x - box.minX, y: root.bounds.height - (point.y - box.minY), width: 0, height: 0)
        }
    }

    private func changed(_ body: () -> Void) {
        let was = active
        transaction(body)
        if was != active { host?.place() }
    }

    private func transaction(_ body: () -> Void) {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        body()
        CATransaction.commit()
    }
}
