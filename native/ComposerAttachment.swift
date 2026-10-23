import Foundation
import ImageIO
import UniformTypeIdentifiers

// The bytes travel to the daemon that owns the session; a local path would
// mean nothing to a session on a paired host.
struct ComposerAttachment: Identifiable, Hashable, Sendable {
    let id = UUID()
    let name: String
    let data: Data
    let mediaType: String

    var isImage: Bool { ComposerAttachments.imageMediaTypes.contains(mediaType) }
}

enum ComposerAttachmentProblem: Error, Equatable {
    case folder
    case tooLarge(String)
    case unreadable(String)
    case tooMany

    var message: String {
        switch self {
        case .folder: return "Folders can't be attached."
        case let .tooLarge(name): return "\(name) is larger than 25 MB."
        case let .unreadable(name): return "\(name) couldn't be read."
        case .tooMany: return "A message can carry up to \(ComposerAttachments.maxCount) attachments."
        }
    }
}

enum ComposerAttachments {
    static let maxCount = 8
    static let maxFileBytes = 25 << 20
    static let maxImageBytes = 5 << 20
    static let maxImageEdge = 2048
    static let imageMediaTypes: Set<String> = ["image/png", "image/jpeg", "image/gif"]

    static func isEmpty(text: String, attachments: [ComposerAttachment]) -> Bool {
        attachments.isEmpty && text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    static func file(at url: URL) throws(ComposerAttachmentProblem) -> ComposerAttachment {
        let values = try? url.resourceValues(forKeys: [.isDirectoryKey, .fileSizeKey, .contentTypeKey])
        if values?.isDirectory == true || url.hasDirectoryPath { throw .folder }
        let name = url.lastPathComponent
        if let size = values?.fileSize, size > maxFileBytes { throw .tooLarge(name) }
        guard let data = try? Data(contentsOf: url, options: .mappedIfSafe) else { throw .unreadable(name) }
        let type = values?.contentType ?? UTType(filenameExtension: url.pathExtension)
        if type?.conforms(to: .image) == true, let image = image(data, name: name) { return image }
        guard data.count <= maxFileBytes else { throw .tooLarge(name) }
        return ComposerAttachment(name: name, data: data, mediaType: type?.preferredMIMEType ?? "application/octet-stream")
    }

    // The daemon accepts only PNG, JPEG and GIF images up to 5 MB, and has
    // no HEIC decoder, so anything else becomes an upright JPEG here.
    static func image(_ data: Data, name: String) -> ComposerAttachment? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil),
              let type = CGImageSourceGetType(source) as String?,
              let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any] else { return nil }
        let width = properties[kCGImagePropertyPixelWidth] as? Int ?? 0
        let height = properties[kCGImagePropertyPixelHeight] as? Int ?? 0
        let orientation = properties[kCGImagePropertyOrientation] as? Int ?? 1
        let mediaType = UTType(type)?.preferredMIMEType ?? ""
        if imageMediaTypes.contains(mediaType), data.count <= maxImageBytes, max(width, height) <= maxImageEdge, orientation == 1 {
            return ComposerAttachment(name: name, data: data, mediaType: mediaType)
        }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: min(max(width, height), maxImageEdge),
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        let output = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(output, UTType.jpeg.identifier as CFString, 1, nil) else { return nil }
        CGImageDestinationAddImage(destination, image, [kCGImageDestinationLossyCompressionQuality: 0.85] as CFDictionary)
        guard CGImageDestinationFinalize(destination), output.length <= maxImageBytes else { return nil }
        let base = (name as NSString).deletingPathExtension
        return ComposerAttachment(name: (base.isEmpty ? "image" : base) + ".jpg", data: output as Data, mediaType: "image/jpeg")
    }

    // A copied or dropped item can offer several representations. A file
    // wins, then an image. Text beside an image (cells copied from a
    // spreadsheet) keeps the paste as text, unless the text is only the web
    // address browsers add when copying an image.
    static func pastedType(_ types: [String], text: String?) -> String? {
        if types.contains(UTType.fileURL.identifier) { return UTType.fileURL.identifier }
        let images = types.filter { UTType($0)?.conforms(to: .image) == true }
        guard !images.isEmpty else { return nil }
        if let text, !isWebAddress(text) { return nil }
        return images.first { $0 == UTType.png.identifier } ?? images.first
    }

    private static func isWebAddress(_ text: String) -> Bool {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.contains(where: \.isWhitespace), let scheme = URL(string: trimmed)?.scheme?.lowercased() else { return false }
        return ["http", "https", "data"].contains(scheme)
    }

    static func adding(_ new: [ComposerAttachment], to existing: [ComposerAttachment]) -> (attachments: [ComposerAttachment], problem: ComposerAttachmentProblem?) {
        var result = existing
        var problem: ComposerAttachmentProblem?
        for attachment in new where !result.contains(where: { $0.name == attachment.name && $0.data == attachment.data }) {
            guard result.count < maxCount else {
                problem = .tooMany
                break
            }
            result.append(attachment)
        }
        return (result, problem)
    }
}

struct PastedAttachments {
    private(set) var attachments: [ComposerAttachment] = []
    private(set) var problem: ComposerAttachmentProblem?
    private var images = 0
    private var offered = 0

    var isEmpty: Bool { offered == 0 }

    mutating func add(_ data: Data?, type: String) {
        offered += 1
        do throws(ComposerAttachmentProblem) {
            attachments.append(try attachment(data, type: type))
        } catch {
            problem = problem ?? error
        }
    }

    private mutating func attachment(_ data: Data?, type: String) throws(ComposerAttachmentProblem) -> ComposerAttachment {
        if type == UTType.fileURL.identifier {
            guard let data, let url = URL(dataRepresentation: data, relativeTo: nil), url.isFileURL else { throw .unreadable("The pasted file") }
            return try ComposerAttachments.file(at: url)
        }
        images += 1
        let name = "Pasted image\(images > 1 ? " \(images)" : "").\(UTType(type)?.preferredFilenameExtension ?? "png")"
        guard let data, let image = ComposerAttachments.image(data, name: name) else { throw .unreadable(name) }
        return image
    }
}
