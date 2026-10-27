@testable import AgenthailHelpers
import AppKit
import ImageIO

@main
struct SelectionWashContrastTest {
    static let accents: [UInt32] = [0x0A84FF, 0x4F8FF7, 0x3FB68B, 0xE9A23B, 0xE5534B, 0xA371F7, 0xE86BA5, 0x1B2A6B]

    static func art(_ name: String) -> WallpaperImage {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("../../internal/uiconf/art/\(name).jpg")
        let source = CGImageSourceCreateWithURL(url as CFURL, nil)!
        return WallpaperImage(id: name, image: CGImageSourceCreateImageAtIndex(source, 0, nil)!)
    }

    static func pixels(_ image: CGImage, width: Int, height: Int) -> [Float] {
        var raw = [UInt8](repeating: 0, count: width * height * 4)
        let ctx = CGContext(data: &raw, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
        return raw.map { Float($0) / 255 }
    }

    static func rgb(_ hex: UInt32) -> BackdropRGB {
        BackdropRGB(Float((hex >> 16) & 0xFF) / 255, Float((hex >> 8) & 0xFF) / 255, Float(hex & 0xFF) / 255)
    }

    static func contrast(_ a: Float, _ b: Float) -> Float { (max(a, b) + 0.05) / (min(a, b) + 0.05) }

    static func worst(_ layers: BackdropLayers, wash: BackdropRGB, roles: [Float], width w: Int, height h: Int) -> (plain: Float, selected: Float) {
        let pane = layers.panes[.sidebar]!
        let under = pixels(pane.base!, width: w, height: h)
        let scrim = pane.scrim.map { pixels($0, width: w, height: h) } ?? []
        let plate = pane.plate.map { pixels($0, width: w, height: h) } ?? []
        var plain = Float.infinity, selected = Float.infinity
        for i in 0..<(w * h) {
            var enc = (under[i * 4], under[i * 4 + 1], under[i * 4 + 2])
            var lin = (ScrimField.linear(enc.0), ScrimField.linear(enc.1), ScrimField.linear(enc.2))
            func over(_ c: BackdropRGB, _ a: Float) {
                enc = (enc.0 * (1 - a) + c.x * a, enc.1 * (1 - a) + c.y * a, enc.2 * (1 - a) + c.z * a)
                lin = (lin.0 * (1 - a) + ScrimField.linear(c.x) * a, lin.1 * (1 - a) + ScrimField.linear(c.y) * a, lin.2 * (1 - a) + ScrimField.linear(c.z) * a)
            }
            func over(_ layer: [Float]) {
                guard !layer.isEmpty, layer[i * 4 + 3] > 0 else { return }
                let a = layer[i * 4 + 3]
                over(BackdropRGB(layer[i * 4] / a, layer[i * 4 + 1] / a, layer[i * 4 + 2] / a), a)
            }
            func shown() -> [Float] { [ScrimField.luminance(enc.0, enc.1, enc.2), 0.2126 * lin.0 + 0.7152 * lin.1 + 0.0722 * lin.2] }
            over(layers.paneTint, pane.minimum)
            over(scrim)
            for l in shown() { for r in roles { plain = min(plain, contrast(l, r)) } }
            over(plate)
            let s = BackdropSpec.selectionStrength
            let fromLinear = (ScrimField.gamma(lin.0), ScrimField.gamma(lin.1), ScrimField.gamma(lin.2))
            for c in [enc, fromLinear] {
                let l = ScrimField.luminance(c.0 + (wash.x - c.0) * s, c.1 + (wash.y - c.1) * s, c.2 + (wash.z - c.2) * s)
                for r in roles { selected = min(selected, contrast(l, r)) }
            }
        }
        return (plain, selected)
    }

    static func main() {
        var failures = 0
        let size = CGSize(width: 288, height: 180)
        let ground = rgb(PaletteTokens.window.dark)
        let field = art("field-dark")
        for increased in [false, true] {
            let target: Float = increased ? 7 : 4.5
            let roles = TextColors.builtIn.roles(dark: true, increased: increased).map(rgb)
            let rows = DesktopGlass.selectedRowRoles.compactMap { TextColors.builtIn.hex($0, dark: true, increased: increased) }.map(rgb)
            let rowL = rows.map { ScrimField.luminance($0.x, $0.y, $0.z) }
            for accent in accents {
                let wash = rgb(accent)
                let selection = increased ? nil : BackdropSpec.Selection(color: wash, roles: rows)
                let spec = BackdropSpec(wallpaper: .image(field), fit: "cover", texture: "none", blur: 30, paneTint: ground, target: target, polarity: .dark, ground: ground, roles: roles, size: size, scale: 1, selection: selection)
                guard let layers = BackdropRenderer.layers(spec, panes: [.sidebar: .init(minimum: 0.45, strength: 1)]) else {
                    print("FAIL: no sidebar layers (increased \(increased))")
                    failures += 1
                    continue
                }
                let w = Int(size.width), h = Int(size.height)
                if !increased {
                    var bare = spec
                    bare.selection = nil
                    let unselected = BackdropRenderer.layers(bare, panes: [.sidebar: .init(minimum: 0.45, strength: 1)])!
                    if pixels(unselected.panes[.sidebar]!.scrim!, width: w, height: h) != pixels(layers.panes[.sidebar]!.scrim!, width: w, height: h) {
                        print(String(format: "FAIL: the selection changes the unselected sidebar scrim (accent #%06X)", accent))
                        failures += 1
                    }
                }
                let outlined = increased || !layers.selectionReadable
                if !increased && outlined { print(String(format: "accent #%06X cannot wash a row at %.1f:1, so its selection is outlined", accent, target)) }
                let result = worst(layers, wash: wash, roles: rowL, width: w, height: h)
                let checks = outlined ? [("unselected or outlined", result.plain)] : [("unselected", result.plain), ("selected", result.selected)]
                for (name, value) in checks where value < target {
                    print(String(format: "FAIL: a %@ row over dark Field reads %.2f:1 < %.1f (increase contrast %@, accent #%06X)", name, value, target, String(increased), accent))
                    failures += 1
                }
            }
        }
        if failures > 0 { exit(1) }
        print("selection wash: row text over dark Field reaches the target on selected and unselected rows, with and without Increase Contrast")
    }
}
