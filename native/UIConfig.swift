import Foundation

/// ui.conf as the daemon resolved it for this device. Values arrive checked
/// and filled with defaults; this only reads them.
struct UIConfig: Codable, Equatable {
    struct Problem: Codable, Equatable, Hashable {
        let line: Int
        let message: String
    }

    enum Value: Codable, Equatable {
        case number(Double)
        case text(String)
        case list([String])

        init(from decoder: Decoder) throws {
            let container = try decoder.singleValueContainer()
            if let number = try? container.decode(Double.self) {
                self = .number(number)
            } else if let list = try? container.decode([String].self) {
                self = .list(list)
            } else {
                self = .text(try container.decode(String.self))
            }
        }

        func encode(to encoder: Encoder) throws {
            var container = encoder.singleValueContainer()
            switch self {
            case .number(let value): try container.encode(value)
            case .list(let value): try container.encode(value)
            case .text(let value): try container.encode(value)
            }
        }
    }

    enum Background: Equatable {
        case none
        case desktop
        case image(path: String)
        case linear(angle: Double, stops: [(color: RGBColor, location: Double)])
        case mesh(columns: Int, rows: Int, colors: [RGBColor])

        static func == (lhs: Background, rhs: Background) -> Bool {
            switch (lhs, rhs) {
            case (.none, .none), (.desktop, .desktop): return true
            case let (.image(a), .image(b)): return a == b
            case let (.linear(a, s), .linear(b, t)):
                return a == b && s.map(\.color) == t.map(\.color) && s.map(\.location) == t.map(\.location)
            case let (.mesh(c, r, x), .mesh(d, s, y)): return c == d && r == s && x == y
            default: return false
            }
        }
    }

    struct RGBColor: Equatable, Hashable {
        let red: Double
        let green: Double
        let blue: Double

        init?(hex: String) {
            let digits = hex.trimmingCharacters(in: .whitespaces).dropFirst(hex.hasPrefix("#") ? 1 : 0)
            guard digits.count == 6, let value = UInt32(digits, radix: 16) else { return nil }
            red = Double((value >> 16) & 0xFF) / 255
            green = Double((value >> 8) & 0xFF) / 255
            blue = Double(value & 0xFF) / 255
        }
    }

    var rev: String = ""
    var values: [String: Value] = [:]
    /// Each key as ui.conf writes it for this device, for settings screens.
    var chosen: [String: String] = [:]
    /// The device line, mac or device:<name>, that each overridden key comes from.
    var overrides: [String: String] = [:]
    var problems: [Problem] = []

    static let defaults = UIConfig()

    func text(_ key: String, _ fallback: String) -> String {
        if case .text(let value) = values[key] { return value }
        return fallback
    }

    func number(_ key: String, _ fallback: Double) -> Double {
        if case .number(let value) = values[key] { return value }
        return fallback
    }

    func list(_ key: String, _ fallback: [String]) -> [String] {
        if case .list(let value) = values[key] { return value }
        return fallback
    }

    var theme: String { text("theme", "system") }
    var accent: RGBColor? { RGBColor(hex: text("theme.accent", "system")) }

    var background: Background { Self.parseBackground(text("background", "none")) ?? .none }
    var backgroundLight: Background { Self.parseBackground(text("background.light", text("background", "none"))) ?? .none }

    func background(dark: Bool) -> Background { dark ? background : backgroundLight }

    var imagePaths: [String] {
        var paths: [String] = []
        for case .image(let path) in [background, backgroundLight] where !paths.contains(path) { paths.append(path) }
        return paths
    }
    var backgroundFit: String { text("background.fit", "cover") }
    var backgroundPosition: String { text("background.position", "center") }
    var backgroundDim: Double { number("background.dim", 0) }
    var backgroundOpacity: Double { number("background.opacity", 1) }
    var backgroundSessionOpacity: Double { number("background.session-opacity", 1) }

    var glass: Bool { text("glass", "on") == "on" }
    var glassBlur: Double { number("glass.blur", 30) }
    var glassTint: String { text("glass.tint", "ground") }
    var contentOpacity: Double { number("glass.opacity", 0.18) }
    var sidebarOpacity: Double { number("glass.sidebar.opacity", 0.45) }
    var chromeOpacity: Double { number("glass.chrome.opacity", 0) }
    var composerOpacity: Double { number("glass.composer.opacity", 0.5) }
    var readability: Bool { text("glass.readability", "auto") == "auto" }
    var border: Double { number("glass.border", 0.08) }
    var gutter: Double { number("glass.gutter", 8) }

    var texture: String { text("texture", "none") }
    var textureStrength: Double { number("texture.strength", 0.2) }
    var textureSpacing: Double { number("texture.spacing", 3) }

    var scrimTint: String { text("scrim.tint", "auto") }
    var scrimFeather: Double { number("scrim.feather", 240) }
    var contrastTarget: Double { number("contrast.target", 4.5) }

    var tabs: Bool { text("titlebar.tabs", "sessions") == "sessions" }
    var tabLabel: String { text("titlebar.tabs.label", "alias-title") }
    var tabPreview: Bool { text("titlebar.tabs.preview", "on") == "on" }
    var statusItems: [String] { list("statusbar", ["usage", "working", "needs-you", "hosts", "terminal"]) }

    var hasBackdrop: Bool { glass && (background != .none || backgroundLight != .none) }

    static func parseBackground(_ value: String) -> Background? {
        if value == "none" { return Background.none }
        if value == "desktop" { return .desktop }
        guard let colon = value.firstIndex(of: ":") else { return nil }
        let kind = value[..<colon].trimmingCharacters(in: .whitespaces)
        let spec = value[value.index(after: colon)...].trimmingCharacters(in: .whitespaces)
        let parts = spec.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        switch kind {
        case "image":
            return spec.isEmpty ? nil : .image(path: spec)
        case "gradient":
            guard parts.count >= 3, parts[0].hasSuffix("deg"), let angle = Double(parts[0].dropLast(3)) else { return nil }
            var stops: [(color: RGBColor, location: Double)] = []
            for part in parts.dropFirst() {
                let fields = part.split(separator: " ")
                guard fields.count == 2, let color = RGBColor(hex: String(fields[0])), fields[1].hasSuffix("%"), let location = Double(fields[1].dropLast()) else { return nil }
                stops.append((color, location / 100))
            }
            return .linear(angle: angle, stops: stops)
        case "mesh":
            guard let size = parts.first else { return nil }
            let dims = size.split(separator: "x").compactMap { Int($0) }
            guard dims.count == 2, dims[0] >= 2, dims[1] >= 2 else { return nil }
            let colors = parts.dropFirst().compactMap(RGBColor.init(hex:))
            guard colors.count == dims[0] * dims[1], colors.count == parts.count - 1 else { return nil }
            return .mesh(columns: dims[0], rows: dims[1], colors: colors)
        default:
            return nil
        }
    }
}

/// One change to ui.conf, checked by the daemon the same way agenthail ui is.
struct UIChange: Encodable, Equatable {
    var action: String
    var set: [String]? = nil
    var unset: [String]? = nil
    var rev: String? = nil
    var message: String? = nil
}

struct UIChangeEntry: Decodable, Equatable, Identifiable {
    let rev: String
    let author: String
    let time: String
    let message: String

    var id: String { rev }
    var date: Date? { ISO8601DateFormatter().date(from: time) }
}

struct UIChangeLog: Decodable {
    let entries: [UIChangeEntry]
}

/// A built-in background: the value to write and what it draws in each appearance.
struct UIPreset: Decodable, Equatable, Identifiable {
    let value: String
    let name: String
    let dark: String
    let light: String

    var id: String { value }

    func background(dark isDark: Bool) -> UIConfig.Background {
        UIConfig.parseBackground(isDark ? dark : light) ?? .none
    }
}

struct UIPresetList: Decodable {
    let presets: [UIPreset]
}

struct UploadedBackground: Decodable {
    let path: String
}
