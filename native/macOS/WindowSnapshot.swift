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
            .textColors(model.interface)
    }
}

@MainActor
enum WindowSnapshot {
    static let defaultSize = CGSize(width: 1440, height: 900)
    static let settle: Duration = .milliseconds(300)
    private static var running: [String: Task<Void, Never>] = [:]

    static func answer(_ request: SnapshotRequest, model: AgenthailModel, voice: VoiceController, api: AgenthailAPI) {
        if let device = request.device, !device.isEmpty, device != Host.current().localizedName { return }
        let budget = SnapshotBudget(deadlineMs: request.deadlineMs)
        running[request.id] = Task { @MainActor in
            defer { running[request.id] = nil }
            await catchUp(to: request.rev, model: model, api: api, budget: budget)
            let result = await capture(request, model: model, voice: voice, budget: budget)
            if Task.isCancelled { return }
            switch result {
            case .success(let png): try? await api.uploadSnapshot(id: request.id, png: png)
            case .failure(let failure): try? await api.failSnapshot(id: request.id, reason: failure.message)
            }
        }
    }

    static func cancel(_ id: String) {
        running.removeValue(forKey: id)?.cancel()
    }

    /// A snapshot taken right after a ui.conf change must show it, background image included.
    static func catchUp(to rev: String?, model: AgenthailModel, api: AgenthailAPI, budget: SnapshotBudget) async {
        guard let rev, !rev.isEmpty, model.interface.config.rev != rev else { return }
        model.interface.load(from: api)
        await budget.wait { model.interface.config.rev == rev }
    }

    static func capture(_ request: SnapshotRequest, model: AgenthailModel, voice: VoiceController, budget: SnapshotBudget) async -> Result<Data, WindowImage.Failure> {
        let size = request.width.flatMap { width in request.height.map { CGSize(width: width, height: $0) } }
        if request.session == nil, size == nil, let window = model.mainWindow, window.isVisible, !window.isMiniaturized {
            let live = WindowImage.png(of: window)
            if live != .failure(.empty) { return live }
        }
        let session = request.session ?? model.mainPane.selectedSessionID
        return await offscreen(session: session, size: size ?? model.mainWindow?.frame.size ?? defaultSize, model: model, voice: voice, budget: budget)
    }

    static func offscreen(session: String?, size: CGSize, model: AgenthailModel, voice: VoiceController, budget: SnapshotBudget) async -> Result<Data, WindowImage.Failure> {
        guard WindowImage.system != nil else { return .failure(.unavailable) }
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
        await budget.wait {
            let detail = resolved == nil || pane.detail?.session.id == resolved || pane.detailLoadError != nil
            return detail && probe.backdrop?.current == true
        }
        await budget.pause(settle)
        return WindowImage.png(of: window)
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
