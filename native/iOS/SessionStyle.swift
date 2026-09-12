import SwiftUI
import Textual

enum SessionStyle {
    static let accent = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark
            ? UIColor(red: 1, green: 0.37, blue: 0.16, alpha: 1)
            : UIColor(red: 0.72, green: 0.20, blue: 0.06, alpha: 1)
    })
    static let readingWidth: CGFloat = 600
    static let surface = Color(uiColor: .secondarySystemBackground)

    static func title(_ session: SessionState) -> String {
        if session.displayName != session.id { return session.displayName }
        let agent = agentName(session.surface)
        if let cwd = session.cwd, !cwd.isEmpty {
            return "\(agent) · \(URL(fileURLWithPath: cwd).lastPathComponent)"
        }
        return "\(agent) session"
    }

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
            .textual.inlineStyle(.default.code(.monospaced, .fontScale(0.85)))
            .textual.headingStyle(TranscriptHeadingStyle())
            .textual.paragraphStyle(TranscriptParagraphStyle())
            .textual.structuredTextStyle(.gitHub)
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct TranscriptHeadingStyle: StructuredText.HeadingStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .textual.fontScale(configuration.headingLevel == 1 ? 1.4 : configuration.headingLevel == 2 ? 1.2 : 1.05)
            .fontWeight(.semibold)
            .textual.lineSpacing(.fontScaled(0.2))
            .textual.blockSpacing(.init(top: 12, bottom: 8))
    }
}

private struct TranscriptParagraphStyle: StructuredText.ParagraphStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .textual.lineSpacing(.fontScaled(0.3))
            .textual.blockSpacing(.init(top: 0, bottom: 12))
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
