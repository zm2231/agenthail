import SwiftUI
import Textual

enum SessionStyle {
    static let accent = Color(red: 1, green: 0.37, blue: 0.16)
    static let surface = Color(uiColor: .secondarySystemBackground)

    static func agentName(_ surface: String) -> String {
        switch surface {
        case "claude": return "Claude"
        case "codex": return "Codex"
        default: return surface.capitalized
        }
    }
}

struct SessionMarkdown: View {
    let text: String
    var readingStyle = false

    var body: some View {
        StructuredText(markdown: text)
            .font(.system(.body, design: readingStyle ? .serif : .default))
            .textual.structuredTextStyle(.gitHub)
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct TranscriptCode: View {
    let text: String

    var body: some View {
        ScrollView(.horizontal) {
            Text(text)
                .font(.system(.callout, design: .monospaced))
                .textSelection(.enabled)
                .fixedSize(horizontal: true, vertical: false)
                .padding(12)
        }
        .background(SessionStyle.surface, in: RoundedRectangle(cornerRadius: 10))
    }
}

struct SessionTimestamp: View {
    let value: String?

    var body: some View {
        if let value, let date = ISO8601DateFormatter.sessionDate(value) {
            Text(date, format: Calendar.current.isDateInToday(date) ? .dateTime.hour().minute() : .dateTime.month(.abbreviated).day().hour().minute())
                .font(.caption)
                .foregroundStyle(.secondary)
                .accessibilityLabel(date.formatted(date: .abbreviated, time: .shortened))
        }
    }
}
