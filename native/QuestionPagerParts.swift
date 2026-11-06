import SwiftUI

struct PagerLinks: View {
    let links: [SessionRequest.Link]
    let accent: AnyShapeStyle

    var body: some View {
        let openable = links.compactMap { link in
            URL(string: link.url).flatMap { ["http", "https"].contains($0.scheme?.lowercased()) ? (link.title.isEmpty ? link.url : link.title, $0) : nil }
        }
        if !openable.isEmpty {
            ChipFlow(spacing: 12) {
                ForEach(openable.indices, id: \.self) { index in
                    Link(destination: openable[index].1) {
                        Label(openable[index].0, systemImage: "arrow.up.right.square")
                    }
                    .foregroundStyle(accent)
                }
            }
            .font(.caption)
        }
    }
}

struct PagerNoteField: View {
    @Binding var note: String
    let disabled: Bool

    var body: some View {
        VStack(spacing: 2) {
            TextField("Note", text: $note, prompt: Text("Add a note (optional)"), axis: .vertical)
                .lineLimit(1...3)
                .textFieldStyle(.plain)
                .font(.callout)
                .disabled(disabled)
                .accessibilityIdentifier("question-pager-note")
            Rectangle().fill(.separator).frame(height: 1)
        }
        .padding(.top, 4)
    }
}

struct PagerOtherRow: View {
    let picked: Bool
    @Binding var text: String
    let disabled: Bool
    let choose: () -> Void
    let submit: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Button {
                choose()
                focused = true
            } label: {
                HStack(alignment: .firstTextBaseline) {
                    #if os(macOS)
                    Text("").frame(width: 10)
                    #endif
                    Image(systemName: picked ? "largecircle.fill.circle" : "circle")
                    Text("Other…")
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(disabled)
            if picked {
                TextField("Other answer", text: $text, prompt: Text("Your answer"))
                    .textFieldStyle(.roundedBorder)
                    .focused($focused)
                    .onAppear { focused = true }
                    .onSubmit(submit)
                    .disabled(disabled)
            }
        }
    }
}

struct AnswerReceiptRow: View {
    let receipt: AnswerReceipt

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Image(systemName: "checkmark.circle")
            Text(receipt.text).frame(maxWidth: .infinity, alignment: .leading)
            Text(receipt.at, style: .time)
        }
        .font(.footnote)
        .foregroundStyle(RoleStyle(.muted))
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("ask-answer-receipt")
    }
}

struct SuggestedMark: View {
    var body: some View {
        Text("· Suggested")
            .font(.caption)
            .foregroundStyle(RoleStyle(.muted))
    }
}

struct PagerActionButton: View {
    let title: String
    let prominent: Bool
    let enabled: Bool
    var tint: AnyShapeStyle? = nil
    let action: () -> Void

    var body: some View {
        if prominent {
            Button(title, action: action)
                .buttonStyle(.borderedProminent)
                .buttonBorderShape(.capsule)
                .tint(tint)
                .disabled(!enabled)
                .help("⏎ \(title)")
        } else {
            Button(title, action: action)
                .buttonStyle(.plain)
                .foregroundStyle(RoleStyle(.muted))
                .padding(.horizontal, 12)
                .padding(.vertical, 5)
                .overlay(Capsule().strokeBorder(.separator))
                .contentShape(Capsule())
                .disabled(!enabled)
        }
    }
}
