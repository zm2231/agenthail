@testable import AgenthailHelpers
import Foundation

@main
struct TextScaleTest {
    @MainActor
    static func main() {
        check(TextScale.stepped(1, by: 1) == 1.1 && TextScale.stepped(1, by: -2) == 0.8, "a press steps the size by a tenth")
        check(TextScale.stepped(1.6, by: 1) == 1.6 && TextScale.stepped(0.8, by: -1) == 0.8, "the size stops at 80% and 160%")
        check(TextScale.clamped(1.234) == 1.25, "a stored size rounds to a twentieth: \(TextScale.clamped(1.234))")

        let change = InterfaceStore.textScaleChange(1.2, device: "Studio")
        check(change.action == "set" && change.set == ["text.scale@device:Studio = 1.2"] && change.message == "Text size 120%", "the size is written for this Mac only: \(change)")
        check(InterfaceStore.textScaleChange(1, device: "").set == ["text.scale@mac = 1.0"], "a Mac without a name writes for every Mac")

        var config = UIConfig()
        check(config.textScale == 1, "text is its normal size by default")
        config.values["text.scale"] = .number(3)
        check(config.textScale == 1.6, "a size past the range is held at its edge")

        let body = DesktopType.body
        TextScale.shared.factor = 1.4
        check(abs(ReadingType.body - body * 1.4) < 0.001 && abs(ReadingType.proseLineSpacing - 6 * 1.4) < 0.001, "transcript text and its line spacing follow the whole scale")
        check(abs(DesktopType.body - body * TextScale.chromeCap) < 0.001, "controls and status stop at the cap: \(DesktopType.body)")
        TextScale.shared.factor = 0.8
        check(abs(DesktopType.body - body * 0.8) < 0.001 && abs(ReadingType.body - body * 0.8) < 0.001, "smaller sizes apply to everything")
        TextScale.shared.factor = 1
        print("text scale: ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
