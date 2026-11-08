import CoreGraphics

enum TileGrid {
    static let minimumWidth: CGFloat = 50
    static let spacing: CGFloat = 2
    static let rows = 2
    static let minimumColumns = 3

    static func columns(width: CGFloat) -> Int {
        max(minimumColumns, Int((max(width, 0) + spacing) / (minimumWidth + spacing)))
    }

    static func fill(columns: Int, order: (Int) -> ProjectTiles) -> ProjectTiles {
        let slots = rows * columns - 2
        let all = order(slots)
        return all.more.isEmpty ? all : order(slots - 1)
    }
}
