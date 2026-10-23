@testable import AgenthailHelpers
import AppKit

@main
struct WindowImageTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 40, height: 30), styleMask: [.titled], backing: .buffered, defer: false)

        let unavailable = WindowImage.png(of: window, using: nil)
        check(unavailable == .failure(.unavailable), "without CGWindowListCreateImage the capture fails, got \(unavailable)")
        check(WindowImage.Failure.unavailable.message.contains("CGWindowListCreateImage"), "the failure names the missing function")

        let nothing: WindowImage.CreateImage = { _, _, _, _ in nil }
        check(WindowImage.png(of: window, using: nothing) == .failure(.empty), "a window server that returns no image is reported")

        let red: WindowImage.CreateImage = { _, _, _, _ in
            let context = CGContext(data: nil, width: 8, height: 6, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            context.setFillColor(red: 1, green: 0, blue: 0, alpha: 1)
            context.fill(CGRect(x: 0, y: 0, width: 8, height: 6))
            return Unmanaged.passRetained(context.makeImage()!)
        }
        if case .success(let png) = WindowImage.png(of: window, using: red), let image = NSBitmapImageRep(data: png) {
            check(png.starts(with: [0x89, 0x50, 0x4E, 0x47]) && image.pixelsWide == 8 && image.pixelsHigh == 6, "the window server's image comes back as a PNG of its size")
        } else {
            check(false, "a window server image was not turned into a PNG")
        }

        if failures > 0 { exit(1) }
        print("window image: captures come from the window server, and say so when it cannot provide one")
    }
}
