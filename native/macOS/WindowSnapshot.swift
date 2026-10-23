import AppKit
import SwiftUI

@MainActor
enum WindowSnapshot {
    static let defaultSize = CGSize(width: 1440, height: 900)
    static let loadDeadline: Duration = .seconds(10)
    static let settle: Duration = .milliseconds(600)

    static func answer(_ request: SnapshotRequest, model: AgenthailModel, api: AgenthailAPI) {
        if let device = request.device, !device.isEmpty, device != Host.current().localizedName { return }
        Task { @MainActor in
            guard let png = await capture(request, model: model) else { return }
            try? await api.uploadSnapshot(id: request.id, png: png)
        }
    }

    static func capture(_ request: SnapshotRequest, model: AgenthailModel) async -> Data? {
        let size = request.width.flatMap { width in request.height.map { CGSize(width: width, height: $0) } }
        if request.session == nil, size == nil, let window = model.mainWindow, window.isVisible {
            return png(of: window)
        }
        let session = request.session ?? model.mainPane.selectedSessionID
        return await offscreen(session: session, size: size ?? model.mainWindow?.contentLayoutRect.size ?? defaultSize, model: model)
    }

    static func offscreen(session: String?, size: CGSize, model: AgenthailModel) async -> Data? {
        let pane = model.openPane()
        defer { model.closePane(pane) }
        let resolved = session.map(model.resolveSessionID)
        if let resolved { pane.select(resolved) }
        let window = SnapshotWindow(contentRect: CGRect(origin: SnapshotWindow.hidden, size: size), styleMask: [.titled, .closable, .resizable, .fullSizeContentView], backing: .buffered, defer: false)
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.isReleasedWhenClosed = false
        window.isExcludedFromWindowsMenu = true
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = NSHostingView(rootView: DesktopWindow(model: model, snapshotPane: pane).frame(width: size.width, height: size.height))
        window.orderFrontRegardless()
        window.setFrameOrigin(SnapshotWindow.hidden)
        defer {
            window.orderOut(nil)
            window.close()
        }
        let deadline = ContinuousClock.now + loadDeadline
        while let resolved, pane.detail?.session.id != resolved, pane.detailLoadError == nil, ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(50))
        }
        try? await Task.sleep(for: settle)
        return png(of: window)
    }

    static func png(of window: NSWindow) -> Data? {
        guard let view = window.contentView else { return nil }
        view.layoutSubtreeIfNeeded()
        guard let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return nil }
        view.cacheDisplay(in: view.bounds, to: rep)
        return rep.representation(using: .png, properties: [:])
    }
}

final class SnapshotWindow: NSWindow {
    static let hidden = CGPoint(x: -20000, y: -20000)
    override var isKeyWindow: Bool { true }
    override var isMainWindow: Bool { true }
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

struct MainWindowReader: NSViewRepresentable {
    let model: AgenthailModel

    func makeNSView(context: Context) -> ReaderView {
        let view = ReaderView()
        view.onWindow = { [weak model] in model?.mainWindow = $0 }
        return view
    }

    func updateNSView(_ view: ReaderView, context: Context) {}

    final class ReaderView: NSView {
        var onWindow: ((NSWindow?) -> Void)?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            onWindow?(window)
        }
    }
}
