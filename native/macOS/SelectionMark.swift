import SwiftUI

struct SelectionMark<Mark: InsettableShape>: ViewModifier {
    let selected: Bool
    let shape: Mark
    let bar: Bool
    let radius: CGFloat?
    @Environment(\.colorSchemeContrast) private var contrast
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        content.modifier(SelectionMarkStyle(selected: selected, shape: shape, bar: bar, increased: contrast == .increased,
                                            backdrop: backdrop, plate: radius.flatMap { radius in place.role.map { (role: $0, radius: radius) } }))
    }
}

struct SelectionMarkStyle<Mark: InsettableShape>: ViewModifier {
    let selected: Bool
    let shape: Mark
    let bar: Bool
    let increased: Bool
    var backdrop: WindowBackdrop?
    var plate: (role: BackdropRole, radius: CGFloat)?

    func body(content: Content) -> some View {
        content
            .background { part(.fill) }
            .overlay { part(.outline) }
    }

    @ViewBuilder
    private func part(_ part: SelectionPart) -> some View {
        if selected {
            if let backdrop {
                BackdropReader(backdrop: backdrop) { layers in
                    SelectionLook(part: part, shape: shape, bar: bar, increased: increased, layers: layers, plate: plate)
                }
            } else {
                SelectionLook(part: part, shape: shape, bar: bar, increased: increased, layers: nil, plate: nil)
            }
        }
    }
}

private enum SelectionPart {
    case fill
    case outline
}

private struct BackdropReader<Inner: View>: View {
    @ObservedObject var backdrop: WindowBackdrop
    let inner: (BackdropLayerSet?) -> Inner

    var body: some View { inner(backdrop.active ? backdrop.layers : nil) }
}

private struct SelectionLook<Mark: InsettableShape>: View {
    let part: SelectionPart
    let shape: Mark
    let bar: Bool
    let increased: Bool
    let layers: BackdropLayerSet?
    let plate: (role: BackdropRole, radius: CGFloat)?

    var body: some View {
        let outline = increased || layers?.layers.selectionReadable == false
        switch part {
        case .fill where !outline:
            ZStack {
                if let layers, let plate {
                    BackdropLayer(style: BackdropLayerStyle(kind: .plate, role: plate.role, radius: plate.radius), set: layers, anchor: nil)
                        .allowsHitTesting(false)
                        .accessibilityHidden(true)
                }
                shape.fill(DesktopPalette.selection)
            }
        case .outline where outline:
            shape.strokeBorder(DesktopPalette.accent, lineWidth: 1.5)
                .overlay(alignment: .leading) {
                    if bar {
                        Capsule().fill(DesktopPalette.accent).frame(width: 3).padding(.vertical, DesktopSpace.xs)
                    }
                }
        default:
            EmptyView()
        }
    }
}

extension View {
    func selectionMark(_ selected: Bool, bar: Bool = false) -> some View {
        modifier(SelectionMark(selected: selected, shape: RoundedRectangle(cornerRadius: DesktopRadius.control), bar: bar, radius: DesktopRadius.control))
    }

    func selectionMark<Mark: InsettableShape>(_ selected: Bool, in shape: Mark) -> some View {
        modifier(SelectionMark(selected: selected, shape: shape, bar: false, radius: nil))
    }
}
