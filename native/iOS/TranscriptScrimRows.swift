import SwiftUI

extension EnvironmentValues {
    @Entry var transcriptScrim: TranscriptScrim?
}

private struct TranscriptScrimRowsKey: PreferenceKey {
    static let defaultValue: [CGRect] = []
    static func reduce(value: inout [CGRect], nextValue: () -> [CGRect]) { value.append(contentsOf: nextValue()) }
}

extension View {
    func transcriptScrimRow() -> some View {
        modifier(TranscriptScrimRow())
    }

    func transcriptScrimContent() -> some View {
        modifier(TranscriptScrimContent())
    }
}

private struct TranscriptScrimRow: ViewModifier {
    @Environment(\.transcriptScrim) private var scrim

    func body(content: Content) -> some View {
        if scrim != nil {
            content.background {
                GeometryReader { proxy in
                    Color.clear.preference(key: TranscriptScrimRowsKey.self, value: [proxy.frame(in: .named(TranscriptRows.contentSpace))])
                }
            }
        } else {
            content
        }
    }
}

private struct TranscriptScrimContent: ViewModifier {
    @Environment(\.transcriptScrim) private var scrim

    func body(content: Content) -> some View {
        if let scrim {
            content
                .onPreferenceChange(TranscriptScrimRowsKey.self) { rows in
                    MainActor.assumeIsolated { scrim.report(rows) }
                }
                .onGeometryChange(for: CGPoint.self) { $0.frame(in: .global).origin } action: { scrim.move(to: $0) }
        } else {
            content
        }
    }
}
