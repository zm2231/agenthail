import AppKit
import SwiftUI

@MainActor
final class SnapshotProbe {
    let pane: SessionPane
    weak var backdrop: WindowBackdrop?

    init(pane: SessionPane) {
        self.pane = pane
    }
}

struct BackdropProbe: View {
    let snapshot: SnapshotProbe
    @Environment(\.windowBackdrop) private var backdrop

    var body: some View {
        Color.clear.onAppear { snapshot.backdrop = backdrop }
    }
}

struct MainWindowContent: View {
    let model: AgenthailModel
    let voice: VoiceController
    var snapshot: SnapshotProbe?

    var body: some View {
        DesktopWindow(model: model, snapshot: snapshot)
            .frame(minHeight: 560)
            .environmentObject(voice)
    }
}

@MainActor
enum WindowSnapshot {
    static let defaultSize = CGSize(width: 1440, height: 900)
    static let loadDeadline: Duration = .seconds(10)
    static let settle: Duration = .milliseconds(300)

    static func answer(_ request: SnapshotRequest, model: AgenthailModel, voice: VoiceController, api: AgenthailAPI) {
        if let device = request.device, !device.isEmpty, device != Host.current().localizedName { return }
        Task { @MainActor in
            guard let png = await capture(request, model: model, voice: voice) else { return }
            try? await api.uploadSnapshot(id: request.id, png: png)
        }
    }

    static func capture(_ request: SnapshotRequest, model: AgenthailModel, voice: VoiceController) async -> Data? {
        let size = request.width.flatMap { width in request.height.map { CGSize(width: width, height: $0) } }
        if request.session == nil, size == nil, let window = model.mainWindow, window.isVisible, !window.isMiniaturized, let png = png(of: window) {
            return png
        }
        let session = request.session ?? model.mainPane.selectedSessionID
        return await offscreen(session: session, size: size ?? model.mainWindow?.frame.size ?? defaultSize, model: model, voice: voice)
    }

    static func offscreen(session: String?, size: CGSize, model: AgenthailModel, voice: VoiceController) async -> Data? {
        let pane = model.openPane()
        defer { model.closePane(pane) }
        let resolved = session.map(model.resolveSessionID)
        if let resolved { pane.select(resolved) }
        let probe = SnapshotProbe(pane: pane)
        let window = SnapshotWindow(contentRect: CGRect(origin: SnapshotWindow.hidden, size: size), styleMask: [.titled, .closable, .resizable, .fullSizeContentView], backing: .buffered, defer: false)
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.isReleasedWhenClosed = false
        window.isExcludedFromWindowsMenu = true
        window.ignoresMouseEvents = true
        let hosting = NSHostingView(rootView: MainWindowContent(model: model, voice: voice, snapshot: probe))
        hosting.sizingOptions = [.minSize]
        window.contentView = hosting
        window.setFrame(CGRect(origin: SnapshotWindow.hidden, size: size), display: false)
        window.orderFrontRegardless()
        defer {
            window.orderOut(nil)
            window.close()
        }
        let deadline = ContinuousClock.now + loadDeadline
        func loaded() -> Bool {
            let detail = resolved == nil || pane.detail?.session.id == resolved || pane.detailLoadError != nil
            return detail && probe.backdrop?.current == true
        }
        while !loaded(), ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(50))
        }
        try? await Task.sleep(for: settle)
        return png(of: window)
    }

    /// The window server's copy keeps glass that cacheDisplay drops, and reads occluded windows whole.
    static func png(of window: NSWindow) -> Data? {
        guard let image = windowImage(window) else { return nil }
        return NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:])
    }

    private typealias CreateImage = @convention(c) (CGRect, UInt32, UInt32, UInt32) -> Unmanaged<CGImage>?

    // CGWindowListCreateImage is unavailable to Swift from the macOS 15 SDK on, but remains in CoreGraphics.
    private static let createImage: CreateImage? = {
        guard let handle = dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", RTLD_NOW),
              let symbol = dlsym(handle, "CGWindowListCreateImage") else { return nil }
        return unsafeBitCast(symbol, to: CreateImage.self)
    }()

    private static func windowImage(_ window: NSWindow) -> CGImage? {
        let includingWindow: UInt32 = 1 << 3
        let boundsIgnoreFraming: UInt32 = 1 << 0
        let bestResolution: UInt32 = 1 << 3
        guard let image = createImage?(.null, includingWindow, UInt32(window.windowNumber), boundsIgnoreFraming | bestResolution)?.takeRetainedValue(),
              image.width > 1, image.height > 1 else { return nil }
        return image
    }
}

final class SnapshotWindow: NSWindow {
    static let hidden = CGPoint(x: -20000, y: -20000)
    override var isKeyWindow: Bool { true }
    override var isMainWindow: Bool { true }
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }

    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
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
