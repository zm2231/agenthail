import AppKit
import Vision

// Flags frames where two recognized text lines sit on top of each other, which normal transcript layout never does.
for path in CommandLine.arguments.dropFirst() {
    guard let image = NSImage(contentsOfFile: path), let cg = image.cgImage(forProposedRect: nil, context: nil, hints: nil) else { continue }
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = .fast
    try? VNImageRequestHandler(cgImage: cg).perform([request])
    let boxes = (request.results ?? []).map(\.boundingBox).filter { $0.height < 0.05 && $0.width > 0.08 }
    var hits = 0
    for (i, a) in boxes.enumerated() {
        for b in boxes[(i + 1)...] {
            let x = min(a.maxX, b.maxX) - max(a.minX, b.minX)
            let y = min(a.maxY, b.maxY) - max(a.minY, b.minY)
            if x > 0.05, y > 0.5 * min(a.height, b.height) { hits += 1 }
        }
    }
    if hits > 0 { print("\(path) \(hits)") }
}
