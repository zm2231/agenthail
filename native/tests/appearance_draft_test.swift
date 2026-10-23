@testable import AgenthailHelpers
import Foundation

@main
struct AppearanceDraftTest {
    static func main() throws {
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }
        var draft = AppearanceDraft(device: "Studio")
        let everywhere = draft.set("glass.blur", "24", label: "glass blur to 24")
        check(everywhere == UIChange(action: "set", set: ["glass.blur = 24"], message: "Set glass blur to 24"), "a change for all devices writes the plain key, got \(everywhere)")
        draft.scope = .thisMac
        let here = draft.set("glass.blur", "24", label: "glass blur to 24")
        check(here == UIChange(action: "set", set: ["glass.blur@device:Studio = 24"], message: "Set glass blur to 24 on Studio"), "a change for this Mac writes a device line, got \(here)")

        let reset = draft.reset()
        check(reset.action == "unset" && reset.unset?.count == AppearanceDraft.keys.count && reset.unset?.allSatisfy { $0.hasSuffix("@device:Studio") } == true, "reset for this Mac unsets only this Mac's lines, got \(String(describing: reset.unset))")
        draft.scope = .everywhere
        check(draft.reset().unset == AppearanceDraft.keys, "reset for all devices unsets the plain keys")

        let config = try JSONDecoder().decode(UIConfig.self, from: Data("""
        {"rev":"r1","values":{"glass.blur":40,"texture":"grain"},"chosen":{"glass.blur":"40","texture":"grain"},"overrides":{"glass.blur":"device:Studio","texture":"mac"},"problems":[]}
        """.utf8))
        check(draft.override("glass.blur", in: config) == "This Mac" && draft.override("texture", in: config) == "All Macs" && draft.override("theme", in: config) == nil, "overrides name who owns the value")
        check(draft.clearOverride("glass.blur", in: config)?.unset == ["glass.blur@device:Studio"] && draft.clearOverride("texture", in: config)?.unset == ["texture@mac"] && draft.clearOverride("theme", in: config) == nil, "clearing an override unsets exactly that device line")

        let previewed = AppearanceDraft.preview(config, overlay: ["glass.blur": .number(12)])
        check(previewed.glassBlur == 12 && previewed.texture == "grain" && config.glassBlur == 40, "a preview changes only the dragged key and leaves the daemon's config alone")

        check(AppearanceDraft.format(0.2) == "0.2" && AppearanceDraft.format(24) == "24" && AppearanceDraft.format(0.456) == "0.46" && AppearanceDraft.format(0.999) == "1", "numbers are written short")
        check(AppearanceDraft.hex(red: 1, green: 0.5, blue: 0) == "#ff8000", "colours are written as #rrggbb")
        check(AppearanceDraft.mesh(Array(repeating: "#000000", count: 9)) == "mesh: 3x3, " + Array(repeating: "#000000", count: 9).joined(separator: ", "), "a custom mesh is written as a 3x3 mesh")

        let preset = UIPreset(value: "art: tide", name: "Tide", dark: "image: art/tide-dark.jpg", light: "image: art/tide-light.jpg")
        check(preset.background(dark: true) == .image(path: "art/tide-dark.jpg") && preset.background(dark: false) == .image(path: "art/tide-light.jpg"), "a preset tile shows the picture for the current appearance")

        if failures > 0 { exit(1) }
        print("appearance draft: settings become checked ui.conf lines for the right devices")
    }
}
