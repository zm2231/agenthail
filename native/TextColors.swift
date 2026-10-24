import SwiftUI

enum TextRole: String, CaseIterable, Sendable {
    case body, muted, faint, meta, link, code, working
    case needsYou = "needs-you"
    case error, done
    case diffRemoved = "diff.removed"
    case peer
}

struct ServedColor: Codable, Equatable, Sendable {
    struct Pair: Codable, Equatable, Sendable {
        var dark: String
        var light: String
    }

    var dark: String?
    var light: String?
    var increased: Pair?
    var lifted: [String]?
    var systemAccent: Bool?
}

struct TextColors: Equatable, Sendable {
    var served: [String: ServedColor] = [:]
    var accentChoice: UInt32?
    var target: Double = 4.5

    static let builtIn = TextColors()

    init(served: [String: ServedColor] = [:], accentChoice: UInt32? = nil, target: Double = 4.5) {
        self.served = served
        self.accentChoice = accentChoice
        self.target = target
    }

    init(_ config: UIConfig) {
        self.init(served: config.colors ?? [:], accentChoice: config.accent.map(Self.hex), target: config.contrastTarget)
    }

    func hex(_ role: TextRole, dark: Bool, increased: Bool) -> UInt32? {
        if let color = served[role.rawValue] {
            return Self.pick(color, dark: dark, increased: increased)
        }
        return PaletteTokens.roles[role].map { dark ? $0.dark : $0.light }
    }

    func accentText(dark: Bool, increased: Bool) -> UInt32? {
        served["accent"].flatMap { Self.pick($0, dark: dark, increased: increased) }
    }

    func roles(dark: Bool, increased: Bool) -> [UInt32] {
        TextRole.allCases.compactMap { hex($0, dark: dark, increased: increased) }
    }

    private static func pick(_ color: ServedColor, dark: Bool, increased: Bool) -> UInt32? {
        if color.systemAccent == true { return nil }
        let value = increased ? (dark ? color.increased?.dark : color.increased?.light) : nil
        return parse(value ?? (dark ? color.dark : color.light))
    }

    static func parse(_ hex: String?) -> UInt32? {
        guard let hex, hex.hasPrefix("#"), hex.count == 7 else { return nil }
        return UInt32(hex.dropFirst(), radix: 16)
    }

    private static func hex(_ color: UIConfig.RGBColor) -> UInt32 {
        UInt32((color.red * 255).rounded()) << 16 | UInt32((color.green * 255).rounded()) << 8 | UInt32((color.blue * 255).rounded())
    }
}

extension EnvironmentValues {
    @Entry var textColors = TextColors.builtIn
}

extension Color {
    init(hex: UInt32) {
        self.init(.sRGB, red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }
}
