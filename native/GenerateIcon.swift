import AppKit

let output = CommandLine.arguments[1]
let size = NSSize(width: 1024, height: 1024)
let image = NSImage(size: size)
image.lockFocus()
NSGraphicsContext.current?.imageInterpolation = .high

let shape = NSBezierPath(roundedRect: NSRect(x: 64, y: 64, width: 896, height: 896), xRadius: 220, yRadius: 220)
let gradient = NSGradient(colors: [
    NSColor(red: 1.0, green: 0.55, blue: 0.30, alpha: 1),
    NSColor(red: 0.94, green: 0.34, blue: 0.16, alpha: 1)
])!
gradient.draw(in: shape, angle: -55)

let paragraph = NSMutableParagraphStyle()
paragraph.alignment = .center
let attributes: [NSAttributedString.Key: Any] = [
    .font: NSFont.systemFont(ofSize: 520, weight: .black),
    .foregroundColor: NSColor(calibratedWhite: 0.06, alpha: 1),
    .paragraphStyle: paragraph
]
NSString(string: "A").draw(in: NSRect(x: 130, y: 184, width: 764, height: 620), withAttributes: attributes)

image.unlockFocus()
guard let data = image.tiffRepresentation,
      let bitmap = NSBitmapImageRep(data: data),
      let png = bitmap.representation(using: .png, properties: [:]) else {
    exit(1)
}
try png.write(to: URL(fileURLWithPath: output))
