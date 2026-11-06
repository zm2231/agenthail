import SwiftUI

struct PagerLinks: View {
    let links: [SessionRequest.Link]
    let accent: AnyShapeStyle

    var body: some View {
        let openable = links.compactMap { link in
            URL(string: link.url).flatMap { ["http", "https"].contains($0.scheme?.lowercased()) ? (link.title.isEmpty ? link.url : link.title, $0) : nil }
        }
        if !openable.isEmpty {
            HStack(spacing: 12) {
                ForEach(openable.indices, id: \.self) { index in
                    Link(destination: openable[index].1) {
                        Label(openable[index].0, systemImage: "arrow.up.right.square")
                    }
                    .foregroundStyle(accent)
                }
            }
            .font(.footnote)
        }
    }
}

struct PagerNoteField: View {
    @Binding var note: String
    let disabled: Bool

    var body: some View {
        TextField("Note", text: $note, prompt: Text("Anything else, optional"), axis: .vertical)
            .lineLimit(1...4)
            .textFieldStyle(.roundedBorder)
            .disabled(disabled)
            .accessibilityIdentifier("question-pager-note")
    }
}

struct SuggestedMark: View {
    let accent: AnyShapeStyle

    var body: some View {
        Text("Suggested")
            .font(.caption.weight(.semibold))
            .foregroundStyle(accent)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .overlay(Capsule().strokeBorder(accent, lineWidth: 1))
    }
}
