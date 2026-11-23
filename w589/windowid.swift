import CoreGraphics
import Foundation

let pid = Int32(CommandLine.arguments[1])!
let windows = CGWindowListCopyWindowInfo([.optionAll], kCGNullWindowID) as? [[String: Any]] ?? []
let mine = windows.filter { ($0[kCGWindowOwnerPID as String] as? Int32) == pid && ($0[kCGWindowLayer as String] as? Int) == 0 }
let sized = mine.compactMap { window -> (Int, Double)? in
    guard let number = window[kCGWindowNumber as String] as? Int, let bounds = window[kCGWindowBounds as String] as? [String: Any],
          let height = bounds["Height"] as? Double, let width = bounds["Width"] as? Double, height > 300 else { return nil }
    return (number, width * height)
}
if let largest = sized.max(by: { $0.1 < $1.1 }) { print(largest.0) } else { exit(1) }
