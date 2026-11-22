import SwiftUI
import UIKit
import Textual

struct IOSMessage: View {
    let label: String
    let text: String
    var timestamp: String? = nil
    var files: [AttachedFile] = []
    /// How long the turn this reply ended took, from the host.
    var finished: String? = nil
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.editMessage) private var editMessage
    @State private var showingFullMessage = false
    private var isUser: Bool { label == "You" }
    private var isLongMessage: Bool { isUser && (text.count > 700 || text.components(separatedBy: .newlines).count > 10) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if isUser {
                if !text.isEmpty {
                    InlineText(isLongMessage ? String(text.prefix(700)) : text, parser: .linkedPlain)
                        .font(.body)
                        .lineSpacing(4)
                        .lineLimit(isLongMessage ? 6 : nil)
                        .phoneInlineStyle()
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .textual.textSelection(.enabled)
                }
                ForEach(Array(files.enumerated()), id: \.offset) { _, file in
                    AttachedFileChip(file: file)
                }
                if isLongMessage {
                    Button("Read full message") { showingFullMessage = true }
                        .font(.subheadline.weight(.medium))
                        .frame(minHeight: 44)
                }
            } else {
                SessionMarkdown(text: text, readingStyle: true)
            }
            footer
        }
        .padding(isUser ? 14 : 0)
        .background(isUser ? SessionStyle.surface : .clear, in: RoundedRectangle(cornerRadius: 16))
        .padding(.leading, isUser && !dynamicTypeSize.isAccessibilitySize ? 16 : 0)
        .padding(.vertical, 4)
        .contextMenu {
            Button("Copy", systemImage: "doc.on.doc") { UIPasteboard.general.string = text }
            if isUser, let editMessage {
                Button("Edit and resend", systemImage: "pencil") { editMessage(text) }
            }
            ShareLink(item: text) { Label("Share text", systemImage: "square.and.arrow.up") }
        }
        .sheet(isPresented: $showingFullMessage) {
            TranscriptDocument(title: "Your message", text: text, markdown: false)
                .chromeTypeSize()
        }
    }

    @ViewBuilder private var footer: some View {
        if isUser {
            HStack {
                Spacer(minLength: 0)
                SessionTimestamp(value: timestamp)
            }
        } else {
            HStack(spacing: 6) {
                Text("✦").font(.caption).foregroundStyle(PhonePalette.accentText).accessibilityHidden(true)
                if let finished {
                    Text(TurnClock.footer(duration: finished, at: timestamp)).font(.caption).foregroundStyle(PhonePalette.muted)
                } else {
                    SessionTimestamp(value: timestamp)
                }
                Spacer(minLength: 0)
            }
        }
    }
}

// The file lives on the host that owns the session, so the phone offers its
// stored path rather than the file.
struct AttachedFileChip: View {
    let file: AttachedFile
    @State private var copied = false

    var body: some View {
        Button {
            UIPasteboard.general.string = file.path
            copied = true
        } label: {
            HStack(spacing: 10) {
                Image(systemName: "doc").foregroundStyle(PhonePalette.muted)
                VStack(alignment: .leading, spacing: 2) {
                    Text(file.name).font(.subheadline.weight(.medium)).lineLimit(1)
                    Text(copied ? "Path copied" : file.sizeLabel ?? "File")
                        .font(.caption).foregroundStyle(PhonePalette.muted)
                }
                Spacer(minLength: 0)
                Image(systemName: copied ? "checkmark" : "doc.on.doc").font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            .padding(.horizontal, 12)
            .frame(minHeight: 44)
            .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 12))
        }
        .buttonStyle(.plain)
        .disabled(file.path.isEmpty)
        .accessibilityLabel("\(file.name), \(file.sizeLabel ?? "file")")
        .accessibilityHint("Copies the path where the agent can read this file")
        .accessibilityIdentifier("attached-file")
        .task(id: copied) {
            guard copied else { return }
            try? await Task.sleep(for: .seconds(2))
            copied = false
        }
    }
}

struct IOSTimelineRow: View {
    let item: TimelineItem
    var compactContext = true
    var finished: String? = nil
	var bodyLoader: ((TimelineItem) async -> RetainedBodyResult?)? = nil
    var attachmentLoader: ((TimelineItem) async -> Data?)? = nil
    var onInspect: () -> Void = {}
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
	@State private var loadedBody: RowChoice<RetainedBodyResult>?

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("IOSTimelineRow")
#endif
        if item.kind == "done" {
            EmptyView()
        } else if let agent = item.agent, agent.isIncoming {
            IOSAgentMessageRow(item: item, agent: agent)
        } else if let agent = item.agent, agent.isSend, compactContext {
            IOSAgentSendRow(call: item, agent: agent)
        } else if compactContext, let title = TranscriptContext.title(for: item) {
            ContextRecordRow(title: title, item: item)
        } else if item.kind == "notice" {
            TranscriptDivider(text: "\(item.title): \(item.noticeText)")
        } else if item.kind == "file" {
            IOSSentFileCard(item: item)
        } else if item.isSaid {
            IOSMessage(label: messageLabel, text: item.text, timestamp: item.timestamp, files: item.files ?? [], finished: finished)
            if item.truncated { ShortenedNote() }
        } else if item.kind == "attachment" {
            if item.role == "assistant", let caption = SentFileState.caption(item) {
                Text(caption).font(.body).textSelection(.enabled)
            }
            AttachmentRow(item: item, loader: attachmentLoader.map { loader in { await loader(item) } })
        } else if item.kind == "toolCall" {
            ToolActivityRow(call: item, results: [], standaloneRecord: true)
        } else if TimelineGroup.isTurnDuration(item) {
            Text(TurnClock.footer(duration: item.text, at: item.timestamp))
                .font(.caption)
                .foregroundStyle(PhonePalette.muted)
                .padding(.vertical, 4)
        } else if item.kind == "event", TranscriptDivider.fits(item.text) {
            TranscriptDivider(text: [eventLabel, TurnClock.time(item.timestamp)].compactMap { $0 }.joined(separator: " · "))
            if item.truncated { ShortenedNote() }
        } else {
            let open = rows.current(opened) ?? rows.isOpen(TranscriptRowID.item(item.id)) ?? (item.kind == "toolResult" && ToolRunSummary.isFailure(item))
            VStack(alignment: .leading, spacing: 8) {
                Button {
                    onInspect()
                    opened = rows.choice(!open)
                    rows.set(TranscriptRowID.item(item.id), open: !open)
                } label: {
                    ToolLineLabel(verb: lineVerb, object: firstLine, isCode: item.kind == "toolResult", failed: item.kind == "toolResult" && ToolRunSummary.isFailure(item), trailing: open ? "chevron.down" : "chevron.right")
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("record-" + item.id)
                .accessibilityValue(open ? "Expanded" : "Collapsed")
                if open {
                    Group {
                        if item.kind == "toolResult" { ToolOutput(result: item) }
                        else {
                            SessionMarkdown(text: item.text, readingStyle: item.kind == "reasoning")
                            if item.truncated { ShortenedNote() }
                        }
                    }
                    .padding(.leading, ToolLineLabel.indent)
                } else if item.truncated { ShortenedNote().padding(.leading, ToolLineLabel.indent) }
				let retainedBody = rows.current(loadedBody) ?? rows.body(TranscriptRowID.item(item.id))
				if item.bodyRef != nil, retainedBody == nil, let bodyLoader {
					Button("Load retained body") {
						Task {
							guard let body = await bodyLoader(item) else { return }
							loadedBody = rows.choice(body)
							rows.set(TranscriptRowID.item(item.id), body: body)
						}
					}.font(.caption)
				} else if let retainedBody {
                    SessionMarkdown(text: retainedBody.text, readingStyle: item.kind == "reasoning")
                    if let error = retainedBody.error {
                        Label(error, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(PhonePalette.muted)
                    }
                }
            }
            .contextMenu { CopyCallID(item: item) }
        }
    }

    private var lineVerb: String {
        switch item.kind {
        case "reasoning": "Thought"
        case "toolResult": "Output"
        default: item.title
        }
    }
    private var firstLine: String { TimelineItem.firstLine(item.text) }

    private var messageLabel: String {
        if item.role == "user" { return "You" }
        if item.isInstructions { return "Instructions" }
        if item.title.contains("commentary") { return "Update" }
        if item.title.contains("analysis") { return "Reasoning" }
        return "Assistant"
    }
    private var eventLabel: String { item.eventLine }
}

/// Call ids are for debugging, so they are copied from a menu rather than shown.
struct CopyCallID: View {
    let item: TimelineItem

    var body: some View {
        if let id = item.callId, !id.isEmpty {
            Button("Copy call ID", systemImage: "number") { UIPasteboard.general.string = id }
        }
    }
}

struct AttachmentRow: View {
    let item: TimelineItem
    let loader: (() async -> Data?)?
    @State private var loaded: Data?
    @State private var attempted = false
    @State private var loading = false
    @State private var retryCount = 0
    @Environment(\.transcriptRows) private var rows
    @Environment(\.cachedAttachment) private var cached

    private var failedKey: String { TranscriptRowID.failedAttachment(item.id) }
    private var data: Data? { loaded ?? cached(item) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            let image = data.flatMap { SessionAttachment.thumbnail($0) }
            let ratio = SessionAttachment.boxRatio(item.attachment?.aspectRatio ?? SessionAttachment.ratio(of: image) ?? SessionAttachment.placeholderRatio)
            Group {
                if let image {
                    Image(uiImage: UIImage(cgImage: image)).resizable().aspectRatio(contentMode: .fit)
                } else if loader != nil, !loading, attempted || rows.isOpen(failedKey) == true {
                    VStack(spacing: 4) {
                        Text("Couldn't load image").foregroundStyle(PhonePalette.muted).multilineTextAlignment(.center)
                        Button("Retry") { retryCount += 1 }.accessibilityIdentifier("attachment-retry")
                    }
                    .font(.footnote)
                    .padding(6)
                } else if loader != nil {
                    ProgressView()
                } else {
                    Label(item.text.isEmpty ? "Image attachment" : item.text, systemImage: "photo").foregroundStyle(PhonePalette.muted).padding(6)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(PhonePalette.muted.opacity(0.12))
            .aspectRatio(ratio, contentMode: .fit)
            .frame(maxWidth: SessionAttachment.maxWidth(ratio, width: .infinity, height: Self.maxHeight), alignment: .leading)
            .clipShape(RoundedRectangle(cornerRadius: 12))
            SessionTimestamp(value: item.timestamp)
        }
        .padding(.vertical, 4)
        .contextMenu { CopyCallID(item: item) }
        .task(id: "\(item.id)-\(retryCount)") {
            guard let loader, data.flatMap({ SessionAttachment.thumbnail($0, maxPixels: 64) }) == nil else { return }
            loading = true
            let result = await loader()
            loading = false
            guard !Task.isCancelled else { return }
            loaded = result
            attempted = true
            rows.set(failedKey, open: data == nil)
        }
    }

    private static let maxHeight: CGFloat = 320
}
