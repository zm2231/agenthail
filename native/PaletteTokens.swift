import Foundation

/// The colours every Agenthail app draws text, surfaces and status in, as
/// sRGB hex for light and dark appearance. Status colours each have one
/// job: attention is only what needs the person, working is live work,
/// done is finished, failed is an error or broken state.
enum PaletteTokens {
    typealias Pair = (light: UInt32, dark: UInt32)

    static let window: Pair = (0xFFFFFF, 0x17181A)
    static let side: Pair = (0xF3F3F4, 0x1D1E21)
    static let raised: Pair = (0xFFFFFF, 0x232427)
    static let text: Pair = (0x1D1D1F, 0xECECEE)
    static let text2: Pair = (0x5F6066, 0xA3A3A8)
    static let muted: Pair = (0x6E6E76, 0x8E8E93)
    static let attention: Pair = (0x965A00, 0xFFB224)
    static let working: Pair = (0x2A64CC, 0x7AA7FF)
    static let done: Pair = (0x1A7F4E, 0x4CC38A)
    static let failed: Pair = (0xC4271B, 0xFF6B61)

    /// Every colour text is drawn in, which a backdrop must keep readable.
    static let textRoles: [Pair] = [text, text2, muted, attention, working, done, failed]
}
