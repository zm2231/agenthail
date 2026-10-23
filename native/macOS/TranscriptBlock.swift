import Foundation

struct TranscriptBlock: Identifiable, Equatable {
    enum Kind: Equatable {
        case user(String, files: [AttachedFile])
        case assistant(String)
        case peer(sender: String, text: String)
        case tools([TimelineItem])
        case annotation(String)
        case notice(String)
        case instructions(String)
        case image(SessionAttachment)
    }

    let id: String
    let kind: Kind

    static func build(_ items: [TimelineItem]) -> [TranscriptBlock] {
        var blocks: [TranscriptBlock] = []
        var tools: [TimelineItem] = []
        func flushTools() {
            guard let first = tools.first else { return }
            blocks.append(TranscriptBlock(id: "tools-\(first.id)", kind: .tools(tools)))
            tools = []
        }
        for item in items {
            if item.isInstructions {
                flushTools()
                if !item.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    blocks.append(TranscriptBlock(id: item.id, kind: .instructions(item.text)))
                }
                continue
            }
            switch item.kind {
            case "message", "text":
                flushTools()
                let files = item.files ?? []
                guard !item.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !files.isEmpty else { continue }
                if item.role == "user" {
                    let (text, images) = stripImageMarkers(item.text)
                    if !text.isEmpty || !files.isEmpty { blocks.append(TranscriptBlock(id: item.id, kind: .user(text, files: files))) }
                    if images > 0 { blocks.append(TranscriptBlock(id: item.id + "#images", kind: .annotation(images == 1 ? "Image attached" : "\(images) images attached"))) }
                } else if item.isPeerMessage {
                    blocks.append(TranscriptBlock(id: item.id, kind: .peer(sender: item.peerSender, text: item.text)))
                } else {
                    blocks.append(TranscriptBlock(id: item.id, kind: .assistant(item.text)))
                }
            case "attachment":
                flushTools()
                if let attachment = item.attachment, attachment.isImage {
                    blocks.append(TranscriptBlock(id: item.id, kind: .image(attachment)))
                } else {
                    blocks.append(TranscriptBlock(id: item.id, kind: .annotation("Image attached")))
                }
            case "notice":
                flushTools()
                let text = item.noticeText
                if !text.isEmpty { blocks.append(TranscriptBlock(id: item.id, kind: .notice(text))) }
            case "event" where item.title == "Turn duration":
                flushTools()
                blocks.append(TranscriptBlock(id: item.id, kind: .annotation("Worked for \(item.text)")))
            case "event":
                flushTools()
                let title = [item.title, item.status].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
                let text = item.text.trimmingCharacters(in: .whitespacesAndNewlines)
                blocks.append(TranscriptBlock(id: item.id, kind: text.isEmpty ? .annotation(title) : .notice("\(title): \(text)")))
            case "context", "goal", "done", "phase":
                continue
            default:
                tools.append(item)
            }
        }
        flushTools()
        return blocks
    }
}

func stripImageMarkers(_ text: String) -> (text: String, images: Int) {
    var images = 0
    let kept = text.components(separatedBy: "\n").filter { line in
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        let isMarker = (trimmed.hasPrefix("[Image: ") || trimmed.hasPrefix("[Image attachment: ")) && trimmed.hasSuffix("]") && !trimmed.dropFirst().contains("[")
        if isMarker { images += 1 }
        return !isMarker
    }
    return (kept.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines), images)
}

enum TimelineMetadata {
    /// Titles, call ids, statuses and raw timestamps are for debugging; a reader only needs to know the host shortened a record.
    static func note(_ item: TimelineItem) -> String? {
        item.truncated ? "Shortened by the host" : nil
    }
}
