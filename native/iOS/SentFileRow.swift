import PDFKit
import SwiftUI
import UIKit

struct SentFileContext: Sendable {
    var hostName: String?
    var load: (@MainActor @Sendable (TimelineItem) async throws -> URL)?
}

private struct SentFileContextKey: EnvironmentKey {
    static let defaultValue = SentFileContext()
}

extension EnvironmentValues {
    var sentFiles: SentFileContext {
        get { self[SentFileContextKey.self] }
        set { self[SentFileContextKey.self] = newValue }
    }
}

private struct OpenedFile: Identifiable {
    let url: URL
    let pdf: Bool
    var id: String { url.path }
}

struct IOSSentFileCard: View {
    let item: TimelineItem
    @Environment(\.sentFiles) private var context
    @State private var opened: OpenedFile?
    @State private var loading = false
    @State private var failure: String?
    @State private var copied = false

    private var path: String { item.files?.first?.path ?? "" }

    var body: some View {
        let name = SentFileState.name(item)
        let reason = SentFileState.reason(item, hostName: context.hostName)
        VStack(alignment: .leading, spacing: 8) {
            if let caption = SentFileState.caption(item) {
                Text(caption).font(.body).textSelection(.enabled)
            }
            Button {
                if reason == nil { Task { await open() } } else { copyPath() }
            } label: {
                HStack(spacing: 12) {
                    Image(systemName: SentFileState.icon(item))
                        .font(.title3)
                        .foregroundStyle(PhonePalette.muted)
                        .frame(width: 28)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(name).font(.subheadline.weight(.medium)).lineLimit(1).truncationMode(.middle)
                        Text(copied ? "Path copied" : reason ?? failure ?? SentFileState.detail(item))
                            .font(.caption).foregroundStyle(PhonePalette.muted).lineLimit(2)
                    }
                    Spacer(minLength: 0)
                    if loading {
                        ProgressView()
                    } else {
                        Image(systemName: reason == nil ? (SentFileState.isPDF(item) ? "doc.text.magnifyingglass" : "square.and.arrow.up") : "doc.on.doc")
                            .font(.footnote).foregroundStyle(PhonePalette.muted)
                    }
                }
                .padding(.horizontal, 12)
                .frame(minHeight: 52)
                .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 12))
            }
            .buttonStyle(.plain)
            .disabled(loading || (reason != nil && path.isEmpty))
            .accessibilityLabel(reason == nil ? "\(name), \(SentFileState.detail(item))" : "\(name), \(reason ?? "")")
            .accessibilityHint(reason == nil ? (SentFileState.isPDF(item) ? "Opens the PDF" : "Shares the file") : "Copies the path on the computer that sent it")
            .accessibilityIdentifier("sent-file")
        }
        .sheet(item: $opened) { file in
            if file.pdf {
                SentPDFSheet(url: file.url, name: name)
            } else {
                ShareSheet(items: [file.url]).ignoresSafeArea()
            }
        }
        .task(id: copied) {
            guard copied else { return }
            try? await Task.sleep(for: .seconds(2))
            copied = false
        }
    }

    private func copyPath() {
        UIPasteboard.general.string = path
        copied = true
    }

    private func open() async {
        guard let load = context.load else { return }
        loading = true
        defer { loading = false }
        do {
            let url = try await load(item)
            failure = nil
            opened = OpenedFile(url: url, pdf: SentFileState.isPDF(item))
        } catch {
            if !error.isCancellation { failure = error.localizedDescription }
        }
    }
}

private struct SentPDFSheet: View {
    let url: URL
    let name: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            PDFKitView(url: url)
                .ignoresSafeArea(edges: .bottom)
                .navigationTitle(name)
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Done") { dismiss() } }
                    ToolbarItem(placement: .primaryAction) { ShareLink(item: url) }
                }
        }
    }
}

private struct PDFKitView: UIViewRepresentable {
    let url: URL

    func makeUIView(context: Context) -> PDFView {
        let view = PDFView()
        view.autoScales = true
        view.document = PDFDocument(url: url)
        view.accessibilityIdentifier = "sent-file-pdf"
        return view
    }

    func updateUIView(_ view: PDFView, context: Context) {
        if view.document?.documentURL != url { view.document = PDFDocument(url: url) }
    }
}

private struct ShareSheet: UIViewControllerRepresentable {
    let items: [Any]

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: items, applicationActivities: nil)
    }

    func updateUIViewController(_ controller: UIActivityViewController, context: Context) {}
}
