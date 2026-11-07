import AppKit
import SwiftUI

struct WorkingSpinner: View {
    @Environment(\.self) private var environment
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        WorkingSymbol(tint: NSColor(DesktopPalette.working.resolve(in: environment)), spinning: !reduceMotion)
            .frame(width: 12, height: 12)
            .accessibilityLabel("Working")
    }
}

private struct WorkingSymbol: NSViewRepresentable {
    let tint: NSColor
    let spinning: Bool

    func makeNSView(context: Context) -> WorkingSymbolView { WorkingSymbolView() }

    func updateNSView(_ view: WorkingSymbolView, context: Context) {
        view.tint = tint
        view.spinning = spinning
    }
}

final class WorkingSymbolView: NSImageView {
    var tint: NSColor? {
        didSet { if tint != oldValue { contentTintColor = tint } }
    }

    var spinning = false {
        didSet { if spinning != oldValue { applyEffect() } }
    }

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        image = NSImage(systemSymbolName: "progress.indicator", accessibilityDescription: nil)?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 10, weight: .semibold))
        setAccessibilityElement(false)
    }

    required init?(coder: NSCoder) { nil }

    private func applyEffect() {
        if spinning {
            addSymbolEffect(.variableColor.iterative, options: .repeating)
        } else {
            removeAllSymbolEffects()
        }
    }
}
