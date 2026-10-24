import Foundation

// Mirrors internal/uiconf/palette.json; a Go test keeps the two equal.
enum PaletteTokens {
    typealias Pair = (light: UInt32, dark: UInt32)

    static let window: Pair = (0xFFFFFF, 0x17181A)
    static let side: Pair = (0xF3F3F4, 0x1D1E21)
    static let raised: Pair = (0xFFFFFF, 0x232427)
    static let bubble: Pair = (0xF1F1F3, 0x26272A)
    static let fill: Pair = (0xE9E9EC, 0x2A2B2F)

    static let roles: [TextRole: Pair] = [
        .body: (0x1D1D1F, 0xECECEE),
        .muted: (0x5F6066, 0xA3A3A8),
        .faint: (0x6E6E76, 0x8E8E93),
        .meta: (0x6E6E76, 0x8E8E93),
        .code: (0x1D1D1F, 0xECECEE),
        .working: (0x2A64CC, 0x7AA7FF),
        .needsYou: (0x965A00, 0xFFB224),
        .error: (0xC4271B, 0xFF6B61),
        .done: (0x1A7F4E, 0x4CC38A),
        .diffRemoved: (0xC4271B, 0xFF6B61),
        .peer: (0x2A64CC, 0x7AA7FF),
    ]
}
