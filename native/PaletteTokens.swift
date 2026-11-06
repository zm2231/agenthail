import Foundation

// Mirrors internal/uiconf/palette.json; a Go test keeps the two equal.
enum PaletteTokens {
    typealias Pair = (light: UInt32, dark: UInt32)

    static let window: Pair = (0xFFFFFF, 0x0F1012)
    static let side: Pair = (0xF3F3F4, 0x141518)
    static let raised: Pair = (0xFFFFFF, 0x1B1C20)
    static let bubble: Pair = (0xF1F1F3, 0x1F2024)
    static let fill: Pair = (0xE9E9EC, 0x24252A)
    static let attention: Pair = (0xFFF5E0, 0x2E2414)
    static let failed: Pair = (0xFDEDEC, 0x2E1A19)

    static let roles: [TextRole: Pair] = [
        .body: (0x1D1D1F, 0xECECEE),
        .muted: (0x55565C, 0xA3A3A8),
        .faint: (0x636369, 0x9A9AA0),
        .meta: (0x636369, 0x9A9AA0),
        .code: (0x1D1D1F, 0xECECEE),
        .working: (0x2459B8, 0x7AA7FF),
        .needsYou: (0x8A5300, 0xFFB224),
        .error: (0xB3241A, 0xFF6B61),
        .done: (0x157146, 0x4CC38A),
        .diffRemoved: (0xB3241A, 0xFF6B61),
        .peer: (0x1D1D1F, 0xECECEE),
    ]

    static let increasedRoles: [TextRole: Pair] = [
        .body: (0x1D1D1F, 0xECECEE),
        .muted: (0x4B4C51, 0xB0B0B4),
        .faint: (0x4C4C51, 0xB0B0B5),
        .meta: (0x4C4C51, 0xB0B0B5),
        .code: (0x1D1D1F, 0xECECEE),
        .working: (0x1E4997, 0x87B0FF),
        .needsYou: (0x6E4200, 0xFFB224),
        .error: (0x951E16, 0xFF918A),
        .done: (0x105837, 0x51C58E),
        .diffRemoved: (0x951E16, 0xFF918A),
        .peer: (0x1D1D1F, 0xECECEE),
    ]
}
