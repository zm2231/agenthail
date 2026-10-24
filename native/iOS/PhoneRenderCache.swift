import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

enum PhoneRenderCache {
    private struct LayerFacts: Codable {
        var minimum: Float
        var scrimPeak: Float
        var tint: [Float]
        var width: Double
        var height: Double
    }

    static func write(_ rendered: BackdropImage, to url: URL) {
        write(rendered.image, to: url)
    }

    static func read(_ url: URL, size: CGSize) -> BackdropImage? {
        image(url).map { BackdropImage(image: $0, size: size) }
    }

    static func write(_ layers: BackdropLayers, to url: URL) {
        guard let pane = layers.panes[.content], let base = pane.base else { return }
        write(base, to: sibling(url, "base"))
        if let scrim = pane.scrim { write(scrim, to: sibling(url, "scrim")) }
        let facts = LayerFacts(minimum: pane.minimum, scrimPeak: pane.scrimPeak, tint: [layers.paneTint.x, layers.paneTint.y, layers.paneTint.z],
                               width: layers.size.width, height: layers.size.height)
        try? JSONEncoder().encode(facts).write(to: sibling(url, "facts", ext: "json"), options: .atomic)
    }

    static func readLayers(_ url: URL, polarity: ScrimField.Polarity) -> BackdropLayers? {
        guard let data = try? Data(contentsOf: sibling(url, "facts", ext: "json")),
              let facts = try? JSONDecoder().decode(LayerFacts.self, from: data), facts.tint.count == 3,
              let base = image(sibling(url, "base")) else { return nil }
        let scrim = facts.scrimPeak > 0 ? image(sibling(url, "scrim")) : nil
        guard facts.scrimPeak <= 0 || scrim != nil else { return nil }
        let tint = BackdropRGB(facts.tint[0], facts.tint[1], facts.tint[2])
        return BackdropLayers(size: CGSize(width: facts.width, height: facts.height), polarity: polarity, base: nil, scrim: nil, scrimPeak: 0, scrimColor: tint, paneTint: tint,
                              panes: [.content: BackdropLayers.Pane(base: base, minimum: facts.minimum, scrim: scrim, scrimPeak: facts.scrimPeak)])
    }

    private static func sibling(_ url: URL, _ part: String, ext: String = "png") -> URL {
        url.deletingPathExtension().appendingPathExtension(part).appendingPathExtension(ext)
    }

    private static func write(_ image: CGImage, to url: URL) {
        guard let destination = CGImageDestinationCreateWithURL(url as CFURL, UTType.png.identifier as CFString, 1, nil) else { return }
        CGImageDestinationAddImage(destination, image, nil)
        CGImageDestinationFinalize(destination)
    }

    private static func image(_ url: URL) -> CGImage? {
        guard let source = CGImageSourceCreateWithURL(url as CFURL, nil) else { return nil }
        return CGImageSourceCreateImageAtIndex(source, 0, nil)
    }
}
