import UIKit

enum ScreenshotInk {
    static func shows(_ image: UIImage, in rect: CGRect) -> Bool {
        guard let source = image.cgImage else { return false }
        let scale = CGFloat(source.width) / image.size.width
        let bounds = CGRect(x: 0, y: 0, width: source.width, height: source.height)
        let crop = CGRect(x: rect.minX * scale, y: rect.minY * scale, width: rect.width * scale, height: rect.height * scale).integral.intersection(bounds)
        guard !crop.isEmpty, let region = source.cropping(to: crop) else { return false }
        let width = 64, height = 128
        var pixels = [UInt8](repeating: 0, count: width * height)
        let drawn = pixels.withUnsafeMutableBytes { buffer -> Bool in
            guard let context = CGContext(data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width,
                                          space: CGColorSpaceCreateDeviceGray(), bitmapInfo: CGImageAlphaInfo.none.rawValue) else { return false }
            context.interpolationQuality = .medium
            context.draw(region, in: CGRect(x: 0, y: 0, width: width, height: height))
            return true
        }
        return drawn && pixels.count { $0 > 40 } >= 100
    }
}
