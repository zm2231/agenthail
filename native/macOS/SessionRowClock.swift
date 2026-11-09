import SwiftUI

struct RowAge: View {
    let session: SessionState
    var clock = MinuteClock.shared

    var body: some View {
        let now = clock.now
        Text(SessionDisplayKind.of(session) == .working ? session.activity?.busySince.map { relativeAge($0, now: now) } ?? "now" : relativeAge(session.lastActive, now: now))
            .font(.system(size: DesktopType.caption))
            .foregroundStyle(DesktopPalette.text2)
    }
}

struct WorkingRowNote: View {
    let session: SessionState
    var clock = MinuteClock.shared

    var body: some View {
        if let note = SessionRowNote.text(session, now: clock.now) {
            SessionRowNoteText(note: note, kind: .working)
        }
    }
}

struct SessionRowNoteText: View {
    let note: String
    let kind: SessionDisplayKind

    var body: some View {
        Text(note)
            .font(.system(size: DesktopType.meta))
            .foregroundStyle(kind == .waiting ? DesktopPalette.attention : DesktopPalette.text2)
            .lineLimit(1)
            .truncationMode(.tail)
    }
}
