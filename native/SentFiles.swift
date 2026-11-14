import Foundation
import UniformTypeIdentifiers

enum SentFileName {
    static let maxLength = 120

    static func safe(_ name: String, fallback: String = "file") -> String {
        let kept = name.unicodeScalars.filter { scalar in
            !CharacterSet.controlCharacters.contains(scalar) && !CharacterSet.illegalCharacters.contains(scalar) && scalar != "/" && scalar != "\\" && scalar != ":"
        }
        var text = String(String.UnicodeScalarView(kept)).trimmingCharacters(in: .whitespacesAndNewlines)
        while text.hasPrefix(".") { text.removeFirst() }
        text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if text.isEmpty { return fallback }
        guard text.count > maxLength else { return text }
        let ext = (text as NSString).pathExtension
        if !ext.isEmpty && ext.count <= 16 {
            let stem = (text as NSString).deletingPathExtension
            return String(stem.prefix(maxLength - ext.count - 1)) + "." + ext
        }
        return String(text.prefix(maxLength))
    }

    static func folder(_ id: String) -> String {
        let kept = id.unicodeScalars.filter { CharacterSet.alphanumerics.contains($0) || $0 == "-" || $0 == "_" }
        let text = String(String.UnicodeScalarView(kept)).prefix(maxLength)
        return text.isEmpty ? "attachment" : String(text)
    }
}

struct SentFileCache {
    static let fileLimit: Int64 = 50 << 20
    static let maxAge: TimeInterval = 7 * 24 * 3600
    static let maxBytes: Int64 = 500 << 20

    let root: URL

    static var standard: SentFileCache {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        return SentFileCache(root: caches.appendingPathComponent("SentFiles", isDirectory: true))
    }

    func location(id: String, name: String) -> URL {
        root.appendingPathComponent(SentFileName.folder(id), isDirectory: true).appendingPathComponent(SentFileName.safe(name))
    }

    func cached(id: String, name: String) -> URL? {
        let url = location(id: id, name: name)
        guard FileManager.default.fileExists(atPath: url.path) else { return nil }
        touch(url.deletingLastPathComponent())
        return url
    }

    func store(_ temporary: URL, id: String, name: String) throws -> URL {
        let target = location(id: id, name: name)
        let folder = target.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        if FileManager.default.fileExists(atPath: target.path) { try FileManager.default.removeItem(at: target) }
        try FileManager.default.moveItem(at: temporary, to: target)
        Self.quarantine(target)
        touch(folder)
        return target
    }

    func touch(_ folder: URL) {
        try? FileManager.default.setAttributes([.modificationDate: Date()], ofItemAtPath: folder.path)
    }

    @discardableResult
    func prune(now: Date = Date(), maxAge: TimeInterval = Self.maxAge, maxBytes: Int64 = Self.maxBytes) -> [URL] {
        guard let folders = try? FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: [.contentModificationDateKey, .isDirectoryKey]) else { return [] }
        var entries = folders.compactMap { folder -> (url: URL, used: Date, bytes: Int64)? in
            guard (try? folder.resourceValues(forKeys: [.isDirectoryKey]))?.isDirectory == true else { return nil }
            let used = (try? folder.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? .distantPast
            return (folder, used, Self.size(of: folder))
        }
        var removed: [URL] = []
        entries.sort { $0.used < $1.used }
        var total = entries.reduce(Int64(0)) { $0 + $1.bytes }
        for entry in entries where now.timeIntervalSince(entry.used) > maxAge || total > maxBytes {
            guard (try? FileManager.default.removeItem(at: entry.url)) != nil else { continue }
            total -= entry.bytes
            removed.append(entry.url)
        }
        return removed
    }

    private static func size(of folder: URL) -> Int64 {
        let files = FileManager.default.enumerator(at: folder, includingPropertiesForKeys: [.fileSizeKey])
        var total: Int64 = 0
        while let file = files?.nextObject() as? URL {
            total += Int64((try? file.resourceValues(forKeys: [.fileSizeKey]))?.fileSize ?? 0)
        }
        return total
    }

    // Gatekeeper asks before a quarantined script or app runs.
    static func quarantine(_ url: URL) {
        #if os(macOS)
        let value = String(format: "0081;%08x;Agenthail;", Int(Date().timeIntervalSince1970))
        _ = value.withCString { pointer in
            setxattr(url.path, "com.apple.quarantine", pointer, strlen(pointer), 0, 0)
        }
        #endif
    }

    static func isQuarantined(_ url: URL) -> Bool {
        getxattr(url.path, "com.apple.quarantine", nil, 0, 0, 0) > 0
    }
}

enum SentFileState {
    static func reason(_ item: TimelineItem, hostName: String?) -> String? {
        guard item.attachment == nil else { return nil }
        let host = hostName.map { $0.isEmpty ? "its computer" : $0 } ?? "its computer"
        guard let bytes = item.files?.first?.bytes else { return "No longer on \(host)." }
        if bytes > SentFileCache.fileLimit {
            return "Too large to open here, \(ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file))."
        }
        return "This file can't be opened here."
    }

    static func name(_ item: TimelineItem) -> String {
        item.attachment?.name ?? item.files?.first?.name ?? item.title
    }

    static func type(_ item: TimelineItem) -> UTType? {
        item.attachment.flatMap { UTType(mimeType: $0.mediaType) } ?? UTType(filenameExtension: (name(item) as NSString).pathExtension)
    }

    static func isPDF(_ item: TimelineItem) -> Bool {
        item.attachment?.mediaType == "application/pdf"
    }

    static func icon(_ item: TimelineItem) -> String {
        guard let type = type(item) else { return "doc" }
        if type.conforms(to: .pdf) { return "doc.richtext" }
        if type.conforms(to: .image) { return "photo" }
        if type.conforms(to: .movie) || type.conforms(to: .video) { return "film" }
        if type.conforms(to: .audio) { return "waveform" }
        if type.conforms(to: .archive) { return "doc.zipper" }
        if type.conforms(to: .sourceCode) || type.conforms(to: .text) { return "doc.plaintext" }
        return "doc"
    }

    static func detail(_ item: TimelineItem) -> String {
        let size = (item.attachment?.bytes.map(Int64.init) ?? item.files?.first?.bytes).map { ByteCountFormatter.string(fromByteCount: $0, countStyle: .file) }
        return [type(item)?.localizedDescription, size].compactMap { $0 }.joined(separator: " · ")
    }

    static func caption(_ item: TimelineItem) -> String? {
        let text = item.text.trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty || text == "Image attachment" ? nil : text
    }
}
