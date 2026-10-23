import AppKit
@testable import AgenthailHelpers
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

@main
struct ComposerPasteTest {
    static func main() async {
        let png = encoded(as: .png)
        let tiff = encoded(as: .tiff)
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent("composer-paste-\(UUID().uuidString)", isDirectory: true)
        try! FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        let notes = folder.appendingPathComponent("notes.txt")
        try! Data("synthetic notes\n".utf8).write(to: notes)

        let pasteboard = NSPasteboard(name: NSPasteboard.Name("composer-paste-\(UUID().uuidString)"))
        defer { pasteboard.releaseGlobally() }

        pasteboard.clearContents()
        pasteboard.setString("plain words", forType: .string)
        check(ComposerAttachments.pasted(from: pasteboard) == nil, "text on the pasteboard stays a text paste")

        pasteboard.clearContents()
        pasteboard.setData(png, forType: .png)
        let shot = ComposerAttachments.pasted(from: pasteboard)
        check(shot?.attachments.map(\.name) == ["Pasted image.png"] && shot?.attachments.first?.data == png && shot?.problem == nil, "a copied screenshot becomes one PNG attachment")

        pasteboard.clearContents()
        pasteboard.setData(tiff, forType: .tiff)
        let preview = ComposerAttachments.pasted(from: pasteboard)?.attachments
        check(preview?.count == 1 && preview?.first?.mediaType == "image/jpeg" && preview?.first?.name == "Pasted image.jpg", "a TIFF from Preview becomes a JPEG the daemon accepts")

        pasteboard.clearContents()
        let browser = NSPasteboardItem()
        browser.setData(tiff, forType: .tiff)
        browser.setString("https://example.com/cat.png", forType: .string)
        pasteboard.writeObjects([browser])
        check(ComposerAttachments.pasted(from: pasteboard)?.attachments.first?.isImage == true, "a browser image with its address as text pastes the image")

        pasteboard.clearContents()
        let cells = NSPasteboardItem()
        cells.setData(png, forType: .png)
        cells.setString("a\tb\n1\t2", forType: .string)
        pasteboard.writeObjects([cells])
        check(ComposerAttachments.pasted(from: pasteboard) == nil, "spreadsheet cells that also offer a picture paste as text")

        pasteboard.clearContents()
        pasteboard.writeObjects([notes as NSURL, folder.appendingPathComponent("missing.png") as NSURL])
        let finder = ComposerAttachments.pasted(from: pasteboard)
        check(finder?.attachments.map(\.name) == ["notes.txt"] && finder?.problem == .unreadable("missing.png"), "files copied in Finder attach by name and a missing one says so")

        pasteboard.clearContents()
        pasteboard.writeObjects([item(png), item(encoded(as: .png, width: 20))])
        let two = ComposerAttachments.pasted(from: pasteboard)?.attachments
        check(two?.map(\.name) == ["Pasted image.png", "Pasted image 2.png"], "several copied images each become an attachment")

        pasteboard.clearContents()
        pasteboard.setData(Data("not an image".utf8), forType: .png)
        check(ComposerAttachments.pasted(from: pasteboard)?.problem == .unreadable("Pasted image.png"), "unreadable image bytes report a problem instead of pasting text")

        let dragged = NSItemProvider()
        dragged.registerDataRepresentation(forTypeIdentifier: UTType.png.identifier, visibility: .all) { $0(png, nil); return nil }
        let file = NSItemProvider(object: notes as NSURL)
        let words = NSItemProvider(object: "only words" as NSString)
        let dropped = await ComposerAttachments.pasted(from: [dragged, file, words])
        check(dropped?.attachments.map(\.name) == ["Pasted image.png", "notes.txt"], "dropped images and files attach in order")
        check(await ComposerAttachments.pasted(from: [words]) == nil, "dropped text is not an attachment")
        print("composer paste tests passed")
    }

    static func item(_ png: Data) -> NSPasteboardItem {
        let item = NSPasteboardItem()
        item.setData(png, forType: .png)
        return item
    }

    static func encoded(as type: UTType, width: Int = 64) -> Data {
        let context = CGContext(data: nil, width: width, height: 32, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        context.setFillColor(CGColor(red: 0.1, green: 0.4, blue: 0.8, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: width / 2, height: 32))
        let output = NSMutableData()
        let destination = CGImageDestinationCreateWithData(output, type.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(destination, context.makeImage()!, nil)
        precondition(CGImageDestinationFinalize(destination))
        return output as Data
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
