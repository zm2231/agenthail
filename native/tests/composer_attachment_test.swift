@testable import AgenthailHelpers
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

@main
struct ComposerAttachmentTest {
    static func main() {
        let small = encoded(width: 64, height: 32, as: .png)
        let kept = ComposerAttachments.image(small, name: "shot.png")
        check(kept?.data == small && kept?.mediaType == "image/png" && kept?.isImage == true, "a small PNG is sent as it is")

        let large = ComposerAttachments.image(encoded(width: 4000, height: 1000, as: .png), name: "wide.png")
        check(large?.mediaType == "image/jpeg" && large?.name == "wide.jpg", "an oversized image becomes a JPEG")
        check(large.map { dimensions($0.data) } == [2048, 512], "the long edge is capped and the aspect kept")

        let heic = encoded(width: 40, height: 30, as: .heic)
        let photo = ComposerAttachments.image(heic, name: "IMG_0001.HEIC")
        check(photo?.mediaType == "image/jpeg" && photo?.name == "IMG_0001.jpg" && photo.map { dimensions($0.data) } == [40, 30], "a HEIC photo becomes a JPEG the daemon accepts")
        check(ComposerAttachments.image(Data("not an image".utf8), name: "x.png") == nil, "bytes that are not an image are refused")

        let folder = FileManager.default.temporaryDirectory.appendingPathComponent("composer-attachment-\(UUID().uuidString)", isDirectory: true)
        try! FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        check(problem { () throws(ComposerAttachmentProblem) -> ComposerAttachment in try ComposerAttachments.file(at: folder) } == .folder, "a folder can't be attached")
        let notes = folder.appendingPathComponent("notes.txt")
        try! Data("synthetic notes\n".utf8).write(to: notes)
        let file = try? ComposerAttachments.file(at: notes)
        check(file?.name == "notes.txt" && file?.mediaType == "text/plain" && file?.isImage == false, "a document keeps its name and type")
        let picture = folder.appendingPathComponent("photo.heic")
        try! heic.write(to: picture)
        check((try? ComposerAttachments.file(at: picture))?.mediaType == "image/jpeg", "a picked image file is prepared like any other image")
        check(problem { () throws(ComposerAttachmentProblem) -> ComposerAttachment in try ComposerAttachments.file(at: folder.appendingPathComponent("missing.txt")) } == .unreadable("missing.txt"), "an unreadable file names itself")

        let one = ComposerAttachment(name: "a.txt", data: Data("a".utf8), mediaType: "text/plain")
        let repeated = ComposerAttachment(name: "a.txt", data: Data("a".utf8), mediaType: "text/plain")
        check(ComposerAttachments.adding([repeated], to: [one]).attachments.count == 1, "the same file is attached once")
        let many = (0..<10).map { ComposerAttachment(name: "\($0).txt", data: Data("\($0)".utf8), mediaType: "text/plain") }
        let capped = ComposerAttachments.adding(many, to: [])
        check(capped.attachments.count == ComposerAttachments.maxCount && capped.problem == .tooMany, "attachments stop at the limit and say so")
        check(!ComposerAttachments.isEmpty(text: " ", attachments: [one]) && ComposerAttachments.isEmpty(text: " \n", attachments: []), "attachments count as content")
        print("composer attachment tests passed")
    }

    static func encoded(width: Int, height: Int, as type: UTType) -> Data {
        let context = CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        context.setFillColor(CGColor(red: 0.8, green: 0.1, blue: 0.1, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: width / 2, height: height))
        let output = NSMutableData()
        let destination = CGImageDestinationCreateWithData(output, type.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(destination, context.makeImage()!, nil)
        precondition(CGImageDestinationFinalize(destination))
        return output as Data
    }

    static func dimensions(_ data: Data) -> [Int] {
        let source = CGImageSourceCreateWithData(data as CFData, nil)!
        let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as! [CFString: Any]
        return [properties[kCGImagePropertyPixelWidth] as! Int, properties[kCGImagePropertyPixelHeight] as! Int]
    }

    static func problem(_ body: () throws(ComposerAttachmentProblem) -> ComposerAttachment) -> ComposerAttachmentProblem? {
        do throws(ComposerAttachmentProblem) {
            _ = try body()
            return nil
        } catch {
            return error
        }
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
