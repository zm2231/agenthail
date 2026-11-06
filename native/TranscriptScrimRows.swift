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

private final class TranscriptScrimRowKey {}

private struct TranscriptScrimRow: ViewModifier {
    @Environment(\.transcriptScrim) private var scrim
    @State private var key = TranscriptScrimRowKey()

    func body(content: Content) -> some View {
        content.background {
            if let scrim, scrim.shape != .band {
                let id = ObjectIdentifier(key)
                Color.clear
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(TranscriptRows.contentSpace)) } action: { scrim.update(id, $0) }
                    .onDisappear { scrim.update(id, nil) }
            } else if scrim != nil {
                GeometryReader { proxy in
                    Color.clear.preference(key: TranscriptScrimRowsKey.self, value: [proxy.frame(in: .named(TranscriptRows.contentSpace))])
                }
            }
        }
    }
}

private struct TranscriptScrimContent: ViewModifier {
    @Environment(\.transcriptScrim) private var scrim

    func body(content: Content) -> some View {
        content
            .onPreferenceChange(TranscriptScrimRowsKey.self) { rows in
                MainActor.assumeIsolated { if let scrim, scrim.shape == .band { scrim.report(rows) } }
            }
            .background {
                if let scrim {
                    Color.clear
                        .onGeometryChange(for: CGPoint.self) { $0.frame(in: .global).origin } action: { scrim.move(to: $0) }
                        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { scrim.reportEnd($0) }
                }
            }
    }
}
