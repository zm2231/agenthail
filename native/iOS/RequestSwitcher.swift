import SwiftUI

struct RequestSwitcher: View {
    let index: Int
    let count: Int
    let show: (Int) -> Void

    var body: some View {
        HStack(spacing: 12) {
            Text("\(index + 1) of \(count) waiting on you")
                .font(.footnote.weight(.medium))
                .foregroundStyle(PhonePalette.muted)
            Spacer()
            Button { show(index - 1) } label: { Image(systemName: "chevron.left").frame(width: 44, height: 32) }
                .disabled(index == 0)
                .accessibilityLabel("Previous request")
                .accessibilityIdentifier("request-previous")
            Button { show(index + 1) } label: { Image(systemName: "chevron.right").frame(width: 44, height: 32) }
                .disabled(index + 1 >= count)
                .accessibilityLabel("Next request")
                .accessibilityIdentifier("request-next")
        }
        .buttonStyle(.borderless)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("request-switcher")
    }
}
