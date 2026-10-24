import AppKit

struct WeakView<V: NSView> {
    weak var view: V?
}

@MainActor
final class ScrimAnchor {
    var feather: CGFloat = CGFloat(BackdropRenderer.paneFeather)
    var inset: CGFloat = 0
    var radius: CGFloat = 0
    weak var host: NSView?
    private var regions: [ObjectIdentifier: WeakView<ScrimRegionView>] = [:]
    private var consumers: [ObjectIdentifier: WeakView<BackdropLayerView>] = [:]
    private(set) var mask: (image: CGImage, rect: CGRect)?
    private var built: (area: CGRect, regions: [CGRect], feather: CGFloat)?
    private var updating = false

    var area: CGRect? {
        guard let host, host.window != nil else { return nil }
        return host.convert(host.bounds, to: nil).insetBy(dx: inset, dy: inset)
    }

    func add(_ region: ScrimRegionView) {
        regions[ObjectIdentifier(region)] = WeakView(view: region)
        invalidate()
    }

    func remove(_ region: ScrimRegionView) {
        guard regions.removeValue(forKey: ObjectIdentifier(region)) != nil else { return }
        invalidate()
    }

    func add(_ consumer: BackdropLayerView) {
        consumers[ObjectIdentifier(consumer)] = WeakView(view: consumer)
    }

    func remove(_ consumer: BackdropLayerView) {
        consumers[ObjectIdentifier(consumer)] = nil
    }

    func invalidate() {
        guard !updating else { return }
        updating = true
        defer { updating = false }
        guard let area else {
            mask = nil
            built = nil
            return
        }
        let rects = regions.values.compactMap { entry -> CGRect? in
            guard let view = entry.view, view.window === host?.window else { return nil }
            return view.convert(view.bounds, to: nil)
        }.sorted { ($0.minX, $0.minY, $0.width, $0.height) < ($1.minX, $1.minY, $1.width, $1.height) }
        if let built, built.area == area, built.regions == rects, built.feather == feather { return }
        built = (area, rects, feather)
        let local = rects.map { CGRect(x: $0.minX - area.minX, y: area.maxY - $0.maxY, width: $0.width, height: $0.height) }
        let grid = ScrimMask.grid(size: area.size, regions: local, feather: feather)
        mask = ScrimMask.image(grid).map { image in
            let size = CGSize(width: CGFloat(grid.columns) * ScrimMask.cell, height: CGFloat(grid.rows) * ScrimMask.cell)
            return (image, CGRect(x: area.minX, y: area.maxY - size.height, width: size.width, height: size.height))
        }
        consumers = consumers.filter { $0.value.view != nil }
        for consumer in consumers.values { consumer.view?.place() }
    }
}
