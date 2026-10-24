import SwiftUI

extension View {
    func contentTitleBand(_ holds: Bool, toggle: SessionPane?) -> some View {
        modifier(ContentTitleBand(holds: holds, toggle: toggle))
    }
}

private struct ContentTitleBand: ViewModifier {
    let holds: Bool
    let toggle: SessionPane?

    func body(content: Content) -> some View {
        content.overlay(alignment: .topLeading) {
            if holds {
                ZStack(alignment: .leading) {
                    Color.clear
                        .allowsHitTesting(false)
                        .windowButtonBand(leading: WindowButtons.paneInset)
                    if let toggle {
                        SidebarToggle(pane: toggle)
                            .frame(width: WindowButtons.toggleWidth)
                            .padding(.leading, WindowButtons.trailing)
                    }
                }
                .frame(height: WindowButtons.bandHeight)
            }
        }
    }
}
