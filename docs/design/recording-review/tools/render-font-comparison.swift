import AppKit
import Foundation

struct Sample {
    let file: String
    let heading: String
    let number: String
}

let canvasWidth: CGFloat = 426
let canvasHeight: CGFloat = 923
let factor: CGFloat = 2
let output = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)

let ink = NSColor(srgbRed: 0.14, green: 0.14, blue: 0.14, alpha: 1)
let secondary = NSColor(srgbRed: 0.43, green: 0.43, blue: 0.43, alpha: 1)
let hairline = NSColor(srgbRed: 0.84, green: 0.84, blue: 0.83, alpha: 1)
let background = NSColor(srgbRed: 0.967, green: 0.966, blue: 0.960, alpha: 1)
let surface = NSColor.white
let selected = NSColor(srgbRed: 0.19, green: 0.19, blue: 0.19, alpha: 1)

func requiredFont(_ name: String, _ size: CGFloat) -> NSFont {
    guard let value = NSFont(name: name, size: size) else { fatalError("Font unavailable: \(name)") }
    return value
}

func rect(_ x: CGFloat, _ top: CGFloat, _ w: CGFloat, _ h: CGFloat) -> NSRect {
    NSRect(x: x, y: canvasHeight - top - h, width: w, height: h)
}

func rounded(_ x: CGFloat, _ top: CGFloat, _ w: CGFloat, _ h: CGFloat,
             radius: CGFloat, fill: NSColor, border: NSColor? = nil, line: CGFloat = 0.75) {
    let p = NSBezierPath(roundedRect: rect(x, top, w, h), xRadius: radius, yRadius: radius)
    fill.setFill(); p.fill()
    if let border { border.setStroke(); p.lineWidth = line; p.stroke() }
}

func divider(_ x: CGFloat, _ top: CGFloat, _ w: CGFloat, _ h: CGFloat = 0.75) {
    hairline.setFill(); rect(x, top, w, h).fill()
}

func text(_ value: String, x: CGFloat, top: CGFloat, width: CGFloat, height: CGFloat,
          font: NSFont, color: NSColor = ink, alignment: NSTextAlignment = .left) {
    let para = NSMutableParagraphStyle()
    para.alignment = alignment
    para.lineBreakMode = .byClipping
    let attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: color, .paragraphStyle: para]
    let measured = (value as NSString).size(withAttributes: attrs)
    guard measured.width <= width + 0.5 else { fatalError("Text exceeds its region: \(value) \(measured.width) > \(width)") }
    (value as NSString).draw(in: rect(x, top, width, height), withAttributes: attrs)
}

func copyIcon(_ x: CGFloat, _ top: CGFloat) {
    let back = NSBezierPath(roundedRect: rect(x + 4, top, 11, 12), xRadius: 2, yRadius: 2)
    let front = NSBezierPath(roundedRect: rect(x, top + 4, 11, 12), xRadius: 2, yRadius: 2)
    secondary.setStroke(); back.lineWidth = 1.25; front.lineWidth = 1.25
    back.stroke(); surface.setFill(); front.fill(); secondary.setStroke(); front.stroke()
}

func row(_ index: Int, _ weight: String, _ reps: String, top: CGFloat, numeric: NSFont) {
    rounded(24, top, 378, 58, radius: 12, fill: surface, border: hairline)
    text("第\(index)组", x: 40, top: top + 18, width: 70, height: 25,
         font: requiredFont("PingFangSC-Medium", 14), color: secondary)
    divider(119, top + 15, 0.7, 28)
    text(weight, x: 142, top: top + 12, width: 52, height: 36, font: numeric)
    text("kg", x: 194, top: top + 22, width: 25, height: 22,
         font: requiredFont("PingFangSC-Regular", 13), color: secondary)
    text("×", x: 224, top: top + 18, width: 18, height: 24,
         font: requiredFont("PingFangSC-Regular", 15), color: secondary)
    text(reps, x: 247, top: top + 12, width: 40, height: 36, font: numeric)
    text("次", x: 288, top: top + 21, width: 24, height: 23,
         font: requiredFont("PingFangSC-Regular", 13), color: secondary)
    copyIcon(362, top + 20)
}

func draw(_ sample: Sample) throws {
    let dest = output.appendingPathComponent(sample.file)
    guard !FileManager.default.fileExists(atPath: dest.path) else { fatalError("Refusing to overwrite \(dest.path)") }
    let widths = Int(canvasWidth * factor), heights = Int(canvasHeight * factor)
    guard let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: widths, pixelsHigh: heights,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0),
        let gc = NSGraphicsContext(bitmapImageRep: bitmap) else { fatalError("Unable to make bitmap") }
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = gc
    gc.cgContext.scaleBy(x: factor, y: factor)
    background.setFill(); NSRect(x: 0, y: 0, width: canvasWidth, height: canvasHeight).fill()

    let title = requiredFont(sample.heading, 29)
    let action = requiredFont(sample.heading, 21)
    let numeric = requiredFont(sample.number, 25)
    let label = requiredFont("PingFangSC-Medium", 14)

    text("训练记录", x: 24, top: 42, width: 300, height: 43, font: title)
    rounded(24, 105, 378, 44, radius: 22, fill: surface, border: hairline)
    rounded(28, 109, 180, 36, radius: 18, fill: selected)
    text("训练后记录", x: 28, top: 117, width: 180, height: 25, font: label, color: .white, alignment: .center)
    text("每组完成后记录", x: 208, top: 117, width: 194, height: 25, font: label, color: secondary, alignment: .center)
    text("深蹲", x: 24, top: 174, width: 60, height: 26, font: label)
    text("卧推", x: 111, top: 174, width: 60, height: 26, font: label, color: secondary)
    text("+", x: 198, top: 171, width: 30, height: 28, font: requiredFont("PingFangSC-Regular", 20), color: secondary)
    divider(24, 208, 378)
    ink.setFill(); rect(24, 207, 40, 2).fill()

    text("深蹲", x: 24, top: 230, width: 250, height: 32, font: action)
    text("3 组", x: 328, top: 236, width: 65, height: 22, font: label, color: secondary, alignment: .right)
    row(1, "40", "8", top: 270, numeric: numeric)
    row(2, "45", "8", top: 338, numeric: numeric)
    row(3, "50", "6", top: 406, numeric: numeric)
    divider(24, 490, 378)
    text("卧推", x: 24, top: 512, width: 250, height: 32, font: action)
    text("2 组", x: 328, top: 518, width: 65, height: 22, font: label, color: secondary, alignment: .right)
    row(1, "35", "10", top: 552, numeric: numeric)
    row(2, "40", "8", top: 620, numeric: numeric)
    rounded(24, 822, 378, 54, radius: 18, fill: selected)
    text("完成", x: 24, top: 836, width: 378, height: 28,
         font: requiredFont("PingFangSC-Semibold", 16), color: .white, alignment: .center)

    gc.flushGraphics()
    NSGraphicsContext.restoreGraphicsState()
    guard let data = bitmap.representation(using: .png, properties: [:]) else { fatalError("PNG encoding failed") }
    try data.write(to: dest, options: .withoutOverwriting)
    print("\(sample.file) | Chinese heading: \(sample.heading) | numbers: \(sample.number) | \(widths)x\(heights)")
}

let samples = [
    Sample(file: "font-comparison-a-2026-11-12.png", heading: "PingFangSC-Semibold", number: "PingFangSC-Semibold"),
    Sample(file: "font-comparison-b-2026-11-12.png", heading: "PingFangSC-Semibold", number: "DINAlternate-Bold"),
    Sample(file: "font-comparison-c-2026-11-12.png", heading: "STSongti-SC-Bold", number: "AvenirNext-DemiBold")
]
for sample in samples { try draw(sample) }
