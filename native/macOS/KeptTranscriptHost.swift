import AppKit
import SwiftUI

struct KeptTranscriptHost: NSViewRepresentable {
    let model: AgenthailModel
    let pane: SessionPane
    let session: SessionState
    let insets: EdgeInsets
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place
    @Environment(\.windowChromeTop) private var chromeTop
    @Environment(\.textColors) private var textColors
    @Environment(\.isEnabled) private var isEnabled

    func makeNSView(context: Context) -> NSView { NSView() }

    func updateNSView(_ container: NSView, context: Context) {
        let inherited = KeptTranscriptRoot.Inherited(backdrop: backdrop, place: place, chromeTop: chromeTop, textColors: textColors, isEnabled: isEnabled, insets: insets)
        let model = model
        pane.keptTranscripts.show(session, live: pane, in: container, views: KeptTranscripts.Views(
            make: { pane, session in
                let host = NSHostingView(rootView: KeptTranscriptRoot(model: model, pane: pane, session: session, inherited: inherited))
                host.sizingOptions = []
                host.autoresizingMask = [.width, .height]
                return host
            },
            bind: { host, pane, session in
                (host as? NSHostingView<KeptTranscriptRoot>)?.rootView = KeptTranscriptRoot(model: model, pane: pane, session: session, inherited: inherited)
            }
        ))
    }
}

struct KeptTranscriptRoot: View {
    struct Inherited {
        let backdrop: WindowBackdrop?
        let place: BackdropPlace
        let chromeTop: CGFloat
        let textColors: TextColors
        let isEnabled: Bool
        let insets: EdgeInsets
    }

    let model: AgenthailModel
    let pane: SessionPane
    let session: SessionState
    let inherited: Inherited

    var body: some View {
        TranscriptView(pane: pane, session: session, sends: model.localSends[session.id] ?? [])
            .safeAreaInset(edge: .top, spacing: 0) { Color.clear.frame(height: inherited.insets.top) }
            .safeAreaInset(edge: .bottom, spacing: 0) { Color.clear.frame(height: inherited.insets.bottom) }
            .environment(model)
            .environment(pane)
            .environment(\.windowBackdrop, inherited.backdrop)
            .environment(\.backdropPlace, inherited.place)
            .environment(\.windowChromeTop, inherited.chromeTop)
            .environment(\.textColors, inherited.textColors)
            .disabled(!inherited.isEnabled)
    }
}
