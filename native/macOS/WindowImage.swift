import AppKit

/// The window server's copy keeps glass that cacheDisplay drops and reads occluded windows whole; an app may read its own windows without screen recording permission.
enum WindowImage {
    typealias CreateImage = @convention(c) (CGRect, UInt32, UInt32, UInt32) -> Unmanaged<CGImage>?

    enum Failure: Error, Equatable {
        case unavailable
        case empty

        var message: String {
            switch self {
            case .unavailable:
                "This version of macOS no longer provides CGWindowListCreateImage, which Agenthail uses to capture its own window, so it cannot take a snapshot."
            case .empty:
                "The window server returned no image of the Agenthail window."
            }
        }
    }

    // Unavailable to Swift from the macOS 15 SDK on, but still in CoreGraphics.
    static let system: CreateImage? = {
        guard let handle = dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", RTLD_NOW),
              let symbol = dlsym(handle, "CGWindowListCreateImage") else { return nil }
        return unsafeBitCast(symbol, to: CreateImage.self)
    }()

    @MainActor
    static func png(of window: NSWindow, using create: CreateImage? = system) -> Result<Data, Failure> {
        guard let create else { return .failure(.unavailable) }
        let includingWindow: UInt32 = 1 << 3
        let boundsIgnoreFraming: UInt32 = 1 << 0
        let bestResolution: UInt32 = 1 << 3
        guard let image = create(.null, includingWindow, UInt32(window.windowNumber), boundsIgnoreFraming | bestResolution)?.takeRetainedValue(),
              image.width > 1, image.height > 1,
              let png = NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:]) else { return .failure(.empty) }
        return .success(png)
    }
}
