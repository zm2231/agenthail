import SwiftUI

struct PhoneWorkingTime: View {
    let session: SessionState
    var clock = MinuteClock.shared

    var body: some View {
        Text(SessionRowTime.working(session, now: clock.now)).font(.caption).foregroundStyle(PhonePalette.muted)
            .lineLimit(1).fixedSize()
    }
}

struct PhoneWorkingNote: View {
    let activity: SessionActivity
    var clock = MinuteClock.shared

    var body: some View {
        if let note = activity.note(now: clock.now) {
            Text(note).font(.caption).foregroundStyle(PhonePalette.muted)
        }
    }
}
