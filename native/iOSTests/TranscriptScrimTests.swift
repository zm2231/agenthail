import UIKit
import XCTest
@testable import Agenthail

@MainActor
final class TranscriptScrimTests: XCTestCase {
    private let size = CGSize(width: 402, height: 874)
    private var window: UIWindow?

    override func tearDown() {
        window?.isHidden = true
        window = nil
        super.tearDown()
    }

    func testRowsAreCoveredAndTheColumnBeyondThemToo() {
        let mask = TranscriptScrimMask(rows: [CGRect(x: 0, y: 100, width: 300, height: 40), CGRect(x: 10, y: 300, width: 200, height: 20), .zero])
        let reach = TranscriptScrimMask.reach
        XCTAssertEqual(mask.layers.count, 4)
        XCTAssertEqual(mask.layers[0], CGRect(x: -reach, y: 100 - reach, width: 300 + reach * 2, height: 40 + reach * 2))
        XCTAssertEqual(mask.layers[1], CGRect(x: 10 - reach, y: 300 - reach, width: 200 + reach * 2, height: 20 + reach * 2))
        let above = mask.layers[2].insetBy(dx: reach, dy: reach), below = mask.layers[3].insetBy(dx: reach, dy: reach)
        XCTAssertEqual(above.maxY, 100, "the column above the first laid-out row is covered")
        XCTAssertEqual(below.minY, 320, "the column below the last laid-out row is covered")
        XCTAssertEqual(above.minX, 0)
        XCTAssertEqual(above.maxX, 300, "the margins beside the column keep the art")
        let empty = TranscriptScrimMask(rows: [])
        XCTAssertTrue(empty.layers[0].contains(CGRect(x: 0, y: 0, width: 10_000, height: 10_000)), "with no rows yet the whole scrim shows")
    }

    func testTheRowImageIsOpaqueOverTheRowAndClearAtItsEdge() throws {
        let image = try XCTUnwrap(TranscriptScrimMask.image(scale: 3))
        let alpha = try alphas(image)
        let side = image.width
        let reach = TranscriptScrimMask.reach
        let core = Int(((reach - TranscriptScrimMask.core) * 3).rounded(.up))
        XCTAssertEqual(alpha[side / 2 * side + side / 2], 255)
        XCTAssertEqual(alpha[side / 2 * side + core], 255, "the row and its core are fully scrimmed")
        XCTAssertLessThan(alpha[side / 2 * side], 8, "the falloff ends at the layer's edge")
        XCTAssertLessThan(alpha[0], 2)
    }

    func testEveryRowIsReadableMidScrollAndArtShowsBetweenRows() throws {
        let appearance = PhoneAppearance(cache: nil)
        let data = try JSONSerialization.data(withJSONObject: ["rev": "r", "values": ["background": "mesh: 3x3, #e8a33d, #4fa3c7, #d95f76, #7fc8a9, #f2d16b, #6b8fd9, #c46bd9, #59c7c7, #e88d5a"],
                                                               "colors": [String: Any](), "chosen": [String: String](), "overrides": [String: String](), "problems": []])
        appearance.preview(try JSONDecoder().decode(UIConfig.self, from: data))
        let key = PhoneAppearance.Key(surface: .transcript, size: size, scale: 3, dark: true, solid: false, target: 4.5)
        let spec = appearance.spec(key)
        let layers = try XCTUnwrap(BackdropRenderer.layers(spec, panes: [.content: BackdropSpec.PaneStyle(minimum: Float(appearance.config.contentOpacity), strength: spec.strength)]))
        XCTAssertNotNil(layers.panes[.content]?.scrim, "bright art needs a scrim")

        let view = host()
        view.show(layers)
        let rows = [CGRect(x: 0, y: 2400, width: 362, height: 44), CGRect(x: 0, y: 2556, width: 240, height: 120)]
        let scrim = TranscriptScrim()
        scrim.attach(view)
        scrim.report(rows)
        scrim.move(to: CGPoint(x: 20, y: -1000))
        let origin = CGPoint(x: 20, y: -2123.5)
        scrim.move(to: origin)

        let shot = try alphasAndLuminance(view)
        let roles = PhonePalette.textRoles(TextColors(appearance.config), dark: true, increased: false).map { ScrimField.luminance($0.x, $0.y, $0.z) }
        let boundary = ScrimField.boundary(roles: roles, polarity: .dark, target: 4.5)
        for row in rows {
            let onScreen = row.offsetBy(dx: origin.x, dy: origin.y)
            for y in Int(onScreen.minY)..<Int(onScreen.maxY) {
                for x in Int(onScreen.minX)..<Int(onScreen.maxX) {
                    let l = shot[y * Int(size.width) + x]
                    XCTAssertTrue(ScrimField.passes(l, boundary: boundary, polarity: .dark), "row pixel \(x),\(y) reads at \(l), boundary \(boundary)")
                    if !ScrimField.passes(l, boundary: boundary, polarity: .dark) { return }
                }
            }
        }
        let gap = shot[Int(2500 + origin.y) * Int(size.width) + 200]
        XCTAssertFalse(ScrimField.passes(gap, boundary: boundary, polarity: .dark), "the art between rows is not scrimmed (\(gap))")
        let beside = shot[Int(2600 + origin.y) * Int(size.width) + 340]
        XCTAssertFalse(ScrimField.passes(beside, boundary: boundary, polarity: .dark), "the art beside a short row is not scrimmed (\(beside))")
    }

    func testSolidTranscriptHasNoScrimAndCoversTheArt() throws {
        let appearance = PhoneAppearance(cache: nil)
        let data = try JSONSerialization.data(withJSONObject: ["rev": "r", "values": ["background": "mesh: 2x2, #ffffff, #f0e0d0, #e0f0ff, #ffffff"],
                                                               "colors": [String: Any](), "chosen": [String: String](), "overrides": [String: String](), "problems": []])
        appearance.preview(try JSONDecoder().decode(UIConfig.self, from: data))
        let key = PhoneAppearance.Key(surface: .transcript, size: size, scale: 3, dark: false, solid: true, target: 4.5)
        let spec = appearance.spec(key)
        let layers = try XCTUnwrap(BackdropRenderer.layers(spec, panes: [.content: BackdropSpec.PaneStyle(minimum: 1, strength: spec.strength)]))
        XCTAssertNil(layers.panes[.content]?.scrim)
        let view = host()
        view.show(layers)
        let shot = try alphasAndLuminance(view)
        let tint = ScrimField.luminance(layers.paneTint.x, layers.paneTint.y, layers.paneTint.z)
        for i in stride(from: 0, to: shot.count, by: 997) {
            XCTAssertEqual(shot[i], tint, accuracy: 0.01)
        }
    }

    private func host() -> TranscriptScrimView {
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first!
        let window = UIWindow(windowScene: scene)
        window.frame = CGRect(origin: .zero, size: size)
        let root = UIViewController()
        window.rootViewController = root
        window.isHidden = false
        self.window = window
        let view = TranscriptScrimView(frame: root.view.bounds)
        root.view.addSubview(view)
        view.layoutIfNeeded()
        return view
    }

    private func alphasAndLuminance(_ view: UIView) throws -> [Float] {
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let image = UIGraphicsImageRenderer(size: view.bounds.size, format: format).image { _ in
            view.drawHierarchy(in: view.bounds, afterScreenUpdates: true)
        }
        let cg = try XCTUnwrap(image.cgImage)
        let w = cg.width, h = cg.height
        var bytes = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = try XCTUnwrap(CGContext(data: &bytes, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                          space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        return (0..<(w * h)).map { i in
            ScrimField.luminance(Float(bytes[i * 4]) / 255, Float(bytes[i * 4 + 1]) / 255, Float(bytes[i * 4 + 2]) / 255)
        }
    }

    private func alphas(_ image: CGImage) throws -> [UInt8] {
        let w = image.width, h = image.height
        var bytes = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = try XCTUnwrap(CGContext(data: &bytes, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                          space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
        return (0..<(w * h)).map { bytes[$0 * 4 + 3] }
    }
}
