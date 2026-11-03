import SwiftUI

struct DesktopTurnNotice: View {
    let sessionID: String
    let notice: String
    @AppStorage(TurnNoticePreference.hiddenKey) private var hidden = false
    @AppStorage(TurnNoticePreference.dismissedKey) private var dismissed = ""

    var body: some View {
        if !notice.isEmpty && TurnNoticePreference.shows(for: sessionID, hidden: hidden, dismissed: dismissed) {
            HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                Text(notice)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Button("Don’t show again") { hidden = true }
                    .buttonStyle(.link)
                    .help("Hide this note for every terminal session. Turn it back on in Settings > General.")
                Button {
                    dismissed = TurnNoticePreference.dismissing(sessionID, in: dismissed)
                } label: {
                    Image(systemName: "xmark")
                }
                .buttonStyle(.borderless)
                .help("Hide for this session")
                .accessibilityLabel("Hide for this session")
            }
            .font(.system(size: DesktopType.caption))
            .foregroundStyle(DesktopPalette.text2)
            .padding(.horizontal, 15)
            .padding(.bottom, DesktopSpace.s)
        }
    }
}

struct DesktopTurnNoticeSetting: View {
    @AppStorage(TurnNoticePreference.hiddenKey) private var hidden = false
    @AppStorage(TurnNoticePreference.dismissedKey) private var dismissed = ""

    var body: some View {
        Toggle("Show the terminal session note above the composer", isOn: Binding(get: { !hidden }, set: { shown in
            (hidden, dismissed) = TurnNoticePreference.setting(shown: shown, dismissed: dismissed)
        }))
    }
}
