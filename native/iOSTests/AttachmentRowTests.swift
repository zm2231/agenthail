import ImageIO
import SwiftUI
import UIKit
import UniformTypeIdentifiers
import XCTest
@testable import Agenthail

@MainActor
final class AttachmentRowTests: XCTestCase {
    private var windows: [UIWindow] = []

    override func tearDown() {
        windows.forEach { $0.isHidden = true }
        windows = []
        super.tearDown()
    }

    func testAnImageRowKeepsOneBoxWhileLoadingFailingAndLoaded() async throws {
        let calls = Calls()
        let jpeg = try Self.jpeg(width: 2000, height: 1098)
        let pending = host(width: 2000, height: 1098) { try? await Task.sleep(for: .seconds(60)); return nil }
        let failed = host(width: 2000, height: 1098) { await calls.mark("failed"); return nil }
        let loaded = host(width: 2000, height: 1098) { await calls.mark("loaded"); return jpeg }
        try await wait { await calls.hasAll("failed", "loaded") }
        try await wait { self.center(of: loaded).isTeal }
        let heights = [pending, failed, loaded].map(height)
        for (name, controller) in zip(["pending", "failed", "loaded"], [pending, failed, loaded]) { attach(controller, name) }
        XCTAssertFalse(center(of: pending).isTeal, "the loading row shows no picture yet")
        XCTAssertFalse(center(of: failed).isTeal, "the failed row shows no picture")
        XCTAssertEqual(heights[0], heights[2], accuracy: 1, "a loading image holds the loaded image's box: \(heights)")
        XCTAssertEqual(heights[1], heights[2], accuracy: 1, "a failed image holds the same box: \(heights)")
        XCTAssertGreaterThan(heights[2], 150, "the image row has its picture's height: \(heights)")
    }

    func testARotatedPhotoHoldsItsUprightBoxAndIsDrawnUpright() async throws {
        let calls = Calls()
        let rotated = try Self.jpeg(width: 2000, height: 1098, orientation: 6)
        let flat = try Self.jpeg(width: 2000, height: 1098)
        let pending = host(width: 1098, height: 2000) { try? await Task.sleep(for: .seconds(60)); return nil }
        let upright = host(width: 1098, height: 2000) { await calls.mark("rotated"); return rotated }
        let landscape = host(width: 2000, height: 1098) { await calls.mark("flat"); return flat }
        try await wait { await calls.hasAll("rotated", "flat") }
        try await wait { self.center(of: upright).isTeal && self.center(of: landscape).isTeal }
        attach(upright, "rotated")
        XCTAssertEqual(height(pending), height(upright), accuracy: 1, "a rotated photo's box doesn't change when it loads")
        XCTAssertGreaterThan(height(upright), height(landscape) + 80, "it takes its upright, taller shape")
        XCTAssertTrue(edge(of: upright).isTeal, "the upright picture fills its box rather than sitting letterboxed on its side")
    }

    func testAVeryNarrowFailedImageKeepsRoomForRetry() async throws {
        let calls = Calls()
        let narrow = host(width: 100, height: 1000) { await calls.mark("narrow"); return nil }
        try await wait { await calls.has("narrow") }
        try await Task.sleep(for: .milliseconds(200))
        attach(narrow, "narrow failed")
        XCTAssertEqual(SessionAttachment.boxRatio(0.1), SessionAttachment.boxRatios.lowerBound, "a 1:10 image's box is held at the narrowest ratio")
        XCTAssertGreaterThanOrEqual(SessionAttachment.maxWidth(SessionAttachment.boxRatio(0.1), width: 360, height: 320), 160, "so the box is wide enough for its retry")
    }

    func testARowShownAgainAfterItsImageWasDroppedLoadsAgain() async throws {
        let calls = Calls()
        let jpeg = try Self.jpeg(width: 2000, height: 1098)
        let first = host(width: 2000, height: 1098) { await calls.mark("load"); return jpeg }
        try await wait { self.center(of: first).isTeal }
        windows.forEach { $0.isHidden = true }
        windows = []
        let again = host(width: 2000, height: 1098) { await calls.mark("load"); return jpeg }
        try await wait { self.center(of: again).isTeal }
        let count = await calls.count("load")
        XCTAssertEqual(count, 2, "with nothing cached, showing the row again fetches the image again")
    }

    private func host(width: Int, height: Int, loader: @escaping () async -> Data?) -> UIHostingController<AnyView> {
        let item = TimelineItem(id: "image", kind: "attachment", role: "user", title: "Image", text: "Image attachment", timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil, attachment: SessionAttachment(id: "a1", mediaType: "image/jpeg", width: width, height: height, bytes: 1))
        let controller = UIHostingController(rootView: AnyView(AttachmentRow(item: item, loader: loader).frame(width: 360).padding(16)))
        controller.sizingOptions = .intrinsicContentSize
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first!
        let window = UIWindow(windowScene: scene)
        window.frame = CGRect(x: 0, y: 0, width: 392, height: 500)
        window.rootViewController = controller
        window.makeKeyAndVisible()
        windows.append(window)
        return controller
    }

    private func height(_ controller: UIHostingController<AnyView>) -> CGFloat {
        controller.sizeThatFits(in: CGSize(width: 392, height: 2000)).height
    }

    private func render(_ controller: UIHostingController<AnyView>) -> UIImage {
        controller.view.layoutIfNeeded()
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        return UIGraphicsImageRenderer(bounds: controller.view.bounds, format: format).image { _ in controller.view.drawHierarchy(in: controller.view.bounds, afterScreenUpdates: true) }
    }

    private func center(of controller: UIHostingController<AnyView>) -> Pixel {
        guard let cg = render(controller).cgImage else { return Pixel(r: 0, g: 0, b: 0) }
        var bytes = [UInt8](repeating: 0, count: 4)
        let drawn = bytes.withUnsafeMutableBytes { buffer -> Bool in
            guard let context = CGContext(data: buffer.baseAddress, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            context.draw(cg, in: CGRect(x: -CGFloat(cg.width / 2), y: -CGFloat(cg.height / 2), width: CGFloat(cg.width), height: CGFloat(cg.height)))
            return true
        }
        return drawn ? Pixel(r: bytes[0], g: bytes[1], b: bytes[2]) : Pixel(r: 0, g: 0, b: 0)
    }

    private func edge(of controller: UIHostingController<AnyView>) -> Pixel {
        guard let cg = render(controller).cgImage else { return Pixel(r: 0, g: 0, b: 0) }
        let box = height(controller) - 32
        var bytes = [UInt8](repeating: 0, count: 4)
        let drawn = bytes.withUnsafeMutableBytes { buffer -> Bool in
            guard let context = CGContext(data: buffer.baseAddress, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            let y = CGFloat(cg.height) / 2 + box / 2 - 30
            context.draw(cg, in: CGRect(x: -CGFloat(cg.width / 2), y: -(CGFloat(cg.height) - y), width: CGFloat(cg.width), height: CGFloat(cg.height)))
            return true
        }
        return drawn ? Pixel(r: bytes[0], g: bytes[1], b: bytes[2]) : Pixel(r: 0, g: 0, b: 0)
    }

    private func wait(_ condition: @escaping () async -> Bool) async throws {
        for _ in 0 ..< 100 {
            if await condition() { return }
            try await Task.sleep(for: .milliseconds(50))
        }
        XCTFail("the row didn't reach the state within 5 s")
    }

    private func attach(_ controller: UIHostingController<AnyView>, _ name: String) {
        let shot = XCTAttachment(image: render(controller))
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    private static func jpeg(width: Int, height: Int, orientation: Int = 1) throws -> Data {
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let image = try XCTUnwrap(UIGraphicsImageRenderer(size: CGSize(width: width, height: height), format: format).image { context in
            UIColor(red: 0.2, green: 0.8, blue: 0.85, alpha: 1).setFill()
            context.fill(CGRect(x: 0, y: 0, width: width, height: height))
        }.cgImage)
        let data = NSMutableData()
        let destination = try XCTUnwrap(CGImageDestinationCreateWithData(data, UTType.jpeg.identifier as CFString, 1, nil))
        CGImageDestinationAddImage(destination, image, [kCGImagePropertyOrientation: orientation, kCGImageDestinationLossyCompressionQuality: 0.6] as CFDictionary)
        XCTAssertTrue(CGImageDestinationFinalize(destination))
        return data as Data
    }
}

private struct Pixel {
    let r: UInt8, g: UInt8, b: UInt8
    var isTeal: Bool { r < 120 && g > 150 && b > 150 }
}

private actor Calls {
    private var counts: [String: Int] = [:]
    func mark(_ name: String) { counts[name, default: 0] += 1 }
    func has(_ name: String) -> Bool { counts[name] != nil }
    func hasAll(_ names: String...) -> Bool { names.allSatisfy { counts[$0] != nil } }
    func count(_ name: String) -> Int { counts[name] ?? 0 }
}
