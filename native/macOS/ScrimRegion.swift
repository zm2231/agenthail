import AppKit
import SwiftUI

final class ScrimRegionView: NSView {
    weak var anchor: ScrimAnchor? {
        didSet {
            guard anchor !== oldValue else { return }
            oldValue?.remove(self)
            if window != nil { anchor?.add(self) }
        }
    }

    private let frames = AncestorFrames()

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        frames.watch(self) { [weak self] in self?.anchor?.invalidate() }
        if window == nil { anchor?.remove(self) } else { anchor?.add(self) }
    }

    override func setFrameOrigin(_ newOrigin: NSPoint) {
        super.setFrameOrigin(newOrigin)
        anchor?.invalidate()
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        anchor?.invalidate()
    }
}

struct ScrimRegion: NSViewRepresentable {
    let anchor: ScrimAnchor

    func makeNSView(context: Context) -> ScrimRegionView { ScrimRegionView() }

    func updateNSView(_ view: ScrimRegionView, context: Context) {
        view.anchor = anchor
    }

    static func dismantleNSView(_ view: ScrimRegionView, coordinator: ()) {
        view.anchor = nil
    }
}
