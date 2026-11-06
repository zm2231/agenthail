import AppKit
import SwiftUI

extension View {
    /// Hides the split view dividers around this column while the backdrop shows.
    func hidesSplitDividers() -> some View {
        modifier(SplitDividerHider())
    }
}

private struct SplitDividerHider: ViewModifier {
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        content.background {
            if let backdrop { Probe(backdrop: backdrop) }
        }
    }

    private struct Probe: View {
        @ObservedObject var backdrop: WindowBackdrop

        var body: some View {
            SplitDividerProbe(hidden: backdrop.active && backdrop.gutter > 0, shadowless: backdrop.active)
        }
    }
}

/// NSSplitView has no invisible divider setting, and can replace its divider subview on any layout.
private struct SplitDividerProbe: NSViewRepresentable {
    let hidden: Bool
    let shadowless: Bool

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> NSView { NSView() }

    func updateNSView(_ view: NSView, context: Context) {
        let coordinator = context.coordinator
        coordinator.hidden = hidden
        coordinator.shadowless = shadowless
        DispatchQueue.main.async { coordinator.attach(from: view) }
    }

    static func dismantleNSView(_ view: NSView, coordinator: Coordinator) {
        coordinator.hidden = false
        coordinator.shadowless = false
        coordinator.apply()
        coordinator.detach()
    }

    @MainActor
    final class Coordinator {
        var hidden = false
        var shadowless = false
        private var splits: [WeakSplit] = []
        private var observers: [NSObjectProtocol] = []

        func attach(from view: NSView) {
            var found: [NSSplitView] = []
            var current = view.superview
            while let next = current {
                if let split = next as? NSSplitView { found.append(split) }
                current = next.superview
            }
            if found.map(ObjectIdentifier.init) != splits.compactMap({ $0.split.map(ObjectIdentifier.init) }) {
                detach()
                splits = found.map(WeakSplit.init)
                observers = found.map { split in
                    NotificationCenter.default.addObserver(forName: NSSplitView.didResizeSubviewsNotification, object: split, queue: .main) { [weak self] _ in
                        MainActor.assumeIsolated { self?.apply() }
                    }
                }
            }
            apply()
        }

        func apply() {
            for split in splits.compactMap(\.split) {
                for view in split.subviews {
                    let name = String(describing: type(of: view))
                    let off = name.hasSuffix("SplitDividerView") ? hidden : name.hasSuffix("SplitViewShadowView") ? hidden || shadowless : nil
                    guard let off else { continue }
                    let alpha: CGFloat = off ? 0 : 1
                    if view.alphaValue != alpha { view.alphaValue = alpha }
                }
            }
        }

        func detach() {
            observers.forEach(NotificationCenter.default.removeObserver)
            observers = []
            splits = []
        }
    }

    struct WeakSplit {
        weak var split: NSSplitView?
    }
}
