import SwiftUI

struct TurnNoticeCard: View {
    let sessionID: String
    let notice: String
    @AppStorage(TurnNoticePreference.hiddenKey) private var hidden = false
    @AppStorage(TurnNoticePreference.dismissedKey) private var dismissed = ""

    var body: some View {
        if !notice.isEmpty && TurnNoticePreference.shows(for: sessionID, hidden: hidden, dismissed: dismissed) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .top, spacing: 8) {
                    Label(notice, systemImage: "terminal")
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Button {
                        dismissed = TurnNoticePreference.dismissing(sessionID, in: dismissed)
                    } label: {
                        Image(systemName: "xmark").font(.footnote.weight(.semibold)).frame(width: 44, height: 44)
                    }
                    .buttonStyle(.plain)
                    .padding(.top, -12).padding(.trailing, -10)
                    .accessibilityLabel("Hide for this session")
                    .accessibilityIdentifier("turn-notice-dismiss")
                }
                Button("Don’t show again") { hidden = true }
                    .buttonStyle(.plain)
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(PhonePalette.accentText)
                    .frame(minHeight: 32)
                    .accessibilityHint("Hides this note for every terminal session. Turn it back on in Settings.")
                    .accessibilityIdentifier("turn-notice-never")
            }
            .font(.footnote).foregroundStyle(PhonePalette.muted)
            .padding(.leading, 12).padding(.trailing, 6).padding(.vertical, 7)
            .floatingGlass(in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            .padding(.horizontal).padding(.top, 6)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("turn-notice")
        }
    }
}

struct TurnNoticeSettingsSection: View {
    @AppStorage(TurnNoticePreference.hiddenKey) private var hidden = false
    @AppStorage(TurnNoticePreference.dismissedKey) private var dismissed = ""

    var body: some View {
        Section {
            Toggle("Terminal session note", isOn: Binding(get: { !hidden }, set: { shown in
                hidden = !shown
                if shown { dismissed = "" }
            }))
            .accessibilityIdentifier("turn-notice-setting")
        } header: {
            Text("Composer")
        } footer: {
            Text("Explains that a message to a terminal session arrives as a note from Agenthail. Turning it on shows it again on every session you hid it from.")
        }
    }
}
