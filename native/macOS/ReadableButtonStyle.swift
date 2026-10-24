import SwiftUI

struct ReadableButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        ReadableButtonLabel(label: configuration.label, pressed: configuration.isPressed)
    }
}

private struct ReadableButtonLabel<Label: View>: View {
    let label: Label
    let pressed: Bool
    @Environment(\.isEnabled) private var enabled

    var body: some View {
        label
            .foregroundStyle(enabled ? AnyShapeStyle(.foreground) : AnyShapeStyle(DesktopPalette.muted))
            .opacity(pressed ? 0.7 : 1)
    }
}

struct SendButtonFace: View {
    let symbol: String
    var size: CGFloat = 14
    var diameter: CGFloat = 32
    var fill = AnyShapeStyle(DesktopPalette.accent)
    let enabled: Bool

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: size, weight: .semibold))
            .foregroundStyle(enabled ? AnyShapeStyle(DesktopPalette.onAccent) : AnyShapeStyle(DesktopPalette.muted))
            .frame(width: diameter, height: diameter)
            .background(enabled ? fill : AnyShapeStyle(Color.clear), in: Circle())
            .overlay(Circle().strokeBorder(enabled ? AnyShapeStyle(Color.clear) : AnyShapeStyle(DesktopPalette.muted), lineWidth: 1))
    }
}
