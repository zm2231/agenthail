import Foundation

@MainActor
protocol PasteTokenMarking: AnyObject {
    var marker: String { get }
}

/// A marker is a noncharacter, which Unicode keeps out of interchanged text, then a private-use id.
@MainActor
final class PastedTextStore {
    static let shared = PastedTextStore()
    static let lineLimit = 8
    static let characterLimit = 600
    static let markerLength = 2
    private static let leads: ClosedRange<UInt32> = 0xFDD0...0xFDEF
    private static let ids: ClosedRange<UInt32> = 0xE000...0xF6FF

    private var texts: [String: String] = [:]
    private var issued = 0

    static func isLong(_ text: String) -> Bool {
        text.count > characterLimit || lines(text) > lineLimit
    }

    static func lines(_ text: String) -> Int {
        var trimmed = Substring(text)
        while trimmed.hasSuffix("\n") { trimmed = trimmed.dropLast() }
        return trimmed.isEmpty ? 0 : trimmed.split(separator: "\n", omittingEmptySubsequences: false).count
    }

    static func label(_ text: String) -> String {
        let count = lines(text)
        return count > 1 ? "Pasted text · \(count) lines" : "Pasted text · \(text.count) characters"
    }

    func add(_ text: String) -> String? {
        let perLead = Self.ids.count
        guard issued < Self.leads.count * perLead,
              let lead = Unicode.Scalar(Self.leads.lowerBound + UInt32(issued / perLead)),
              let id = Unicode.Scalar(Self.ids.lowerBound + UInt32(issued % perLead)) else { return nil }
        issued += 1
        let marker = String(lead) + String(id)
        texts[marker] = text
        return marker
    }

    func text(for marker: String) -> String? { texts[marker] }

    enum Piece: Equatable {
        case plain(String)
        case token(String)
    }

    func pieces(_ text: String) -> [Piece] {
        guard text.unicodeScalars.contains(where: { Self.leads.contains($0.value) }) else { return [.plain(text)] }
        var result: [Piece] = []
        var plain = String.UnicodeScalarView()
        var scalars = text.unicodeScalars.makeIterator()
        var pending = scalars.next()
        while let scalar = pending {
            pending = scalars.next()
            if Self.leads.contains(scalar.value), let id = pending, texts[String(scalar) + String(id)] != nil {
                if !plain.isEmpty { result.append(.plain(String(plain))) }
                plain = String.UnicodeScalarView()
                result.append(.token(String(scalar) + String(id)))
                pending = scalars.next()
            } else {
                plain.append(scalar)
            }
        }
        if !plain.isEmpty { result.append(.plain(String(plain))) }
        return result
    }

    func containsMarker(_ text: String) -> Bool {
        pieces(text).contains { if case .token = $0 { true } else { false } }
    }

    func expand(_ text: String) -> String {
        pieces(text).reduce(into: "") { out, piece in
            switch piece {
            case let .plain(plain): out += plain
            case let .token(marker): out += texts[marker] ?? ""
            }
        }
    }

    static func draft(of storage: NSAttributedString) -> String {
        var drafted = ""
        let string = storage.string as NSString
        storage.enumerateAttribute(.init("NSAttachment"), in: NSRange(location: 0, length: storage.length)) { value, range, _ in
            if let token = value as? PasteTokenMarking {
                drafted += String(repeating: token.marker, count: range.length)
            } else {
                drafted += string.substring(with: range)
            }
        }
        return drafted
    }

    static func tokenLocations(in storage: NSAttributedString) -> [Int] {
        var locations: [Int] = []
        storage.enumerateAttribute(.init("NSAttachment"), in: NSRange(location: 0, length: storage.length)) { value, range, _ in
            if value is PasteTokenMarking { locations += range.location ..< NSMaxRange(range) }
        }
        return locations
    }

    static func draftOffset(_ offset: Int, tokens: [Int]) -> Int {
        offset + tokens.filter { $0 < offset }.count * (markerLength - 1)
    }

    static func viewOffset(_ offset: Int, tokens: [Int]) -> Int {
        var shift = 0
        for location in tokens {
            let start = location + shift
            if offset <= start { break }
            if offset < start + markerLength { return location + 1 }
            shift += markerLength - 1
        }
        return offset - shift
    }

    static func viewRange(_ range: NSRange, tokens: [Int]) -> NSRange {
        let start = viewOffset(range.location, tokens: tokens)
        return NSRange(location: start, length: viewOffset(NSMaxRange(range), tokens: tokens) - start)
    }
}
