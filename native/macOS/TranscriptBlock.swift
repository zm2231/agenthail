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
    var truncated = false
    var timestamp: String? = nil
    /// How long the turn this reply ended took, from the host's turn duration record.
    var finished: String? = nil

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
                    blocks.append(TranscriptBlock(id: item.id, kind: .instructions(item.text), truncated: item.truncated))
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
                    if !text.isEmpty || !files.isEmpty { blocks.append(TranscriptBlock(id: item.id, kind: .user(text, files: files), truncated: item.truncated, timestamp: item.timestamp)) }
                    if images > 0 { blocks.append(TranscriptBlock(id: item.id + "#images", kind: .annotation(images == 1 ? "Image attached" : "\(images) images attached"))) }
                } else if item.isPeerMessage {
                    blocks.append(TranscriptBlock(id: item.id, kind: .peer(sender: item.peerSender, text: item.text), truncated: item.truncated, timestamp: item.timestamp))
                } else {
                    blocks.append(TranscriptBlock(id: item.id, kind: .assistant(item.text), truncated: item.truncated, timestamp: item.timestamp))
                }
            case "attachment":
                flushTools()
                if let attachment = item.attachment, attachment.isImage {
                    blocks.append(TranscriptBlock(id: item.id, kind: .image(attachment), truncated: item.truncated))
                } else {
                    blocks.append(TranscriptBlock(id: item.id, kind: .annotation("Image attached"), truncated: item.truncated))
                }
            case "notice":
                flushTools()
                let text = item.noticeText
                if !text.isEmpty { blocks.append(TranscriptBlock(id: item.id, kind: .notice(text), truncated: item.truncated)) }
            case "event" where item.title == "Turn duration":
                flushTools()
                if let last = blocks.indices.last, case .assistant = blocks[last].kind, blocks[last].finished == nil {
                    blocks[last].finished = item.text
                    blocks[last].timestamp = item.timestamp ?? blocks[last].timestamp
                } else {
                    blocks.append(TranscriptBlock(id: item.id, kind: .annotation(TurnClock.footer(duration: item.text, at: item.timestamp)), truncated: item.truncated))
                }
            case "event":
                flushTools()
                let title = [item.title, item.status].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
                let text = item.text.trimmingCharacters(in: .whitespacesAndNewlines)
                blocks.append(TranscriptBlock(id: item.id, kind: text.isEmpty ? .annotation(title) : .notice("\(title): \(text)"), truncated: item.truncated))
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

enum TurnClock {
    /// "Finished 3:13 · 6:10 PM" from a host duration such as "3m13s" and the record's time.
    static func footer(duration: String?, at timestamp: String?) -> String {
        [duration.map { "Finished " + clock($0) }, time(timestamp)].compactMap { $0 }.joined(separator: " · ")
    }

    /// Minutes and seconds, or hours, minutes and seconds, from a duration such as "1h2m3s" or "12s".
    static func clock(_ duration: String) -> String {
        var parts: [Character: Int] = [:]
        var number = ""
        for character in duration {
            if character.isNumber { number.append(character) } else if let value = Int(number) {
                parts[character] = value
                number = ""
            } else {
                return duration
            }
        }
        guard number.isEmpty, !parts.isEmpty, Set(parts.keys).isSubset(of: ["h", "m", "s"]) else { return duration }
        let hours = parts["h"] ?? 0, minutes = parts["m"] ?? 0, seconds = parts["s"] ?? 0
        return hours > 0 ? String(format: "%d:%02d:%02d", hours, minutes, seconds) : String(format: "%d:%02d", minutes, seconds)
    }

    static func time(_ timestamp: String?) -> String? {
        guard let timestamp, let date = date(timestamp) else { return nil }
        return date.formatted(date: Calendar.current.isDateInToday(date) ? .omitted : .abbreviated, time: .shortened)
    }

    static func date(_ timestamp: String) -> Date? {
        (try? Date(timestamp, strategy: Date.ISO8601FormatStyle(includingFractionalSeconds: true))) ?? (try? Date(timestamp, strategy: Date.ISO8601FormatStyle()))
    }
}

enum TimelineMetadata {
    /// Titles, call ids, statuses and raw timestamps are for debugging; a reader only needs to know the host shortened a record.
    static let shortened = "Shortened by the host"

    static func note(_ item: TimelineItem) -> String? {
        item.truncated ? shortened : nil
    }
}
