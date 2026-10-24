import SwiftUI
import XCTest
@testable import Agenthail

final class RoleInlineStyleTests: XCTestCase {
    private func color(_ role: TextRole, _ colors: TextColors, dark: Bool = true, increased: Bool = false) -> Color {
        RoleInlineStyle.color(role, colors: colors, dark: dark, increased: increased, accentText: PhoneInlineStyle.accentText)
    }

    func testServedLinkAndCodeColoursAreUsed() {
        let colors = TextColors(served: [
            "link": ServedColor(dark: "#123456", light: "#654321", increased: .init(dark: "#abcdef", light: "#fedcba")),
            "code": ServedColor(dark: "#00ff00", light: "#007700"),
        ])
        XCTAssertEqual(color(.link, colors), Color(hex: 0x123456))
        XCTAssertEqual(color(.link, colors, dark: false), Color(hex: 0x654321))
        XCTAssertEqual(color(.link, colors, increased: true), Color(hex: 0xABCDEF))
        XCTAssertEqual(color(.code, colors), Color(hex: 0x00FF00))
    }

    func testLinksFollowTheAccentWhenNotSetApart() {
        let system = TextColors(served: ["link": ServedColor(systemAccent: true)])
        XCTAssertEqual(color(.link, system), Color(hex: SessionStyle.accentHex.dark), "a system-accent link draws the phone's accent text")
        let chosen = TextColors(served: ["accent": ServedColor(dark: "#5577ff", light: "#2244cc")], accentChoice: 0x3366FF)
        XCTAssertEqual(color(.link, chosen), Color(hex: 0x5577FF), "with no link colour, links use the readable accent")
        XCTAssertEqual(color(.code, chosen), Color(hex: PaletteTokens.roles[.code]!.dark), "code falls back to the built-in role")
    }
}
