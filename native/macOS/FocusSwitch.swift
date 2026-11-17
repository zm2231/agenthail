import SwiftUI

struct FocusSwitch: View {
    @AppStorage(TranscriptFocus.defaultsKey) private var focus = false

    var body: some View {
        ViewThatFits(in: .horizontal) {
            Picker("Transcript", selection: $focus) {
                Text("Focus").tag(true)
                Text("Show work").tag(false)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .controlSize(.small)
            .fixedSize()
            Button { focus.toggle() } label: {
                Image(systemName: focus ? "text.bubble.fill" : "text.bubble")
                    .frame(width: 28, height: 28)
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .help(focus ? "Show work" : "Focus")
        }
        .accessibilityLabel("Focus")
        .accessibilityValue(focus ? "On" : "Off")
        .accessibilityIdentifier("focus-switch")
    }
}
