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
            SplitDividerProbe(hidden: backdrop.active && backdrop.gutter > 0)
        }
    }
}

/// NSSplitView has no invisible divider setting, and can replace its divider subview on any layout.
private struct SplitDividerProbe: NSViewRepresentable {
    let hidden: Bool

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> NSView { NSView() }

    func updateNSView(_ view: NSView, context: Context) {
        let coordinator = context.coordinator
        coordinator.hidden = hidden
        DispatchQueue.main.async { coordinator.attach(from: view) }
    }

    static func dismantleNSView(_ view: NSView, coordinator: Coordinator) {
        coordinator.hidden = false
        coordinator.apply()
        coordinator.detach()
    }

    @MainActor
    final class Coordinator {
        var hidden = false
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
                for divider in split.subviews where String(describing: type(of: divider)).hasSuffix("SplitDividerView") {
                    let alpha: CGFloat = hidden ? 0 : 1
                    if divider.alphaValue != alpha { divider.alphaValue = alpha }
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
