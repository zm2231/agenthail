import Foundation

/// Turns Appearance settings into ui.conf lines for the daemon to check, and
/// previews a value before it is written. It never validates a value itself.
struct AppearanceDraft: Equatable {
    enum Scope: Equatable {
        case everywhere
        case thisMac
    }

    static let keys = [
        "theme", "theme.accent",
        "background", "background.fit", "background.position", "background.dim", "background.opacity", "background.session-opacity",
        "glass", "glass.blur", "glass.tint", "glass.opacity", "glass.sidebar.opacity", "glass.composer.opacity", "glass.readability",
        "texture", "texture.strength",
    ]

    var scope: Scope = .everywhere
    let device: String

    func name(_ key: String) -> String {
        scope == .thisMac ? "\(key)@device:\(device)" : key
    }

    func set(_ key: String, _ value: String, label: String) -> UIChange {
        UIChange(action: "set", set: ["\(name(key)) = \(value)"], message: "Set \(label)" + suffix)
    }

    func reset() -> UIChange {
        UIChange(action: "unset", unset: Self.keys.map(name), message: "Reset appearance" + suffix)
    }

    func clearOverride(_ key: String, in config: UIConfig) -> UIChange? {
        guard let device = config.overrides[key] else { return nil }
        return UIChange(action: "unset", unset: ["\(key)@\(device)"], message: "Reset \(key) for \(overrideLabel(device))")
    }

    func override(_ key: String, in config: UIConfig) -> String? {
        config.overrides[key].map(overrideLabel)
    }

    func overrideLabel(_ device: String) -> String {
        if device == "device:\(self.device)" { return "This Mac" }
        if device == "mac" { return "All Macs" }
        return device
    }

    private var suffix: String { scope == .thisMac ? " on \(device)" : "" }

    static func preview(_ config: UIConfig, overlay: [String: UIConfig.Value]) -> UIConfig {
        guard !overlay.isEmpty else { return config }
        var previewed = config
        for (key, value) in overlay { previewed.values[key] = value }
        return previewed
    }

    static func format(_ number: Double) -> String {
        let rounded = (number * 100).rounded() / 100
        return rounded == rounded.rounded() ? String(Int(rounded)) : String(format: "%g", rounded)
    }

    static func hex(red: Double, green: Double, blue: Double) -> String {
        func byte(_ value: Double) -> Int { Int((min(max(value, 0), 1) * 255).rounded()) }
        return String(format: "#%02x%02x%02x", byte(red), byte(green), byte(blue))
    }

    static func mesh(_ colors: [String]) -> String {
        "mesh: 3x3, " + colors.joined(separator: ", ")
    }
}
