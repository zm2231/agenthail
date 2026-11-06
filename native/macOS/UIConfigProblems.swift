import SwiftUI

struct UIConfigProblems: View {
    let problems: [UIConfig.Problem]

    var body: some View {
        if !problems.isEmpty {
            Section {
                ForEach(problems, id: \.self) { problem in
                    Label {
                        Text("ui.conf line \(problem.line): \(problem.message)")
                            .textSelection(.enabled)
                    } icon: {
                        Image(systemName: "exclamationmark.triangle")
                            .foregroundStyle(DesktopPalette.text2)
                    }
                }
            } header: {
                Text("Lines in ui.conf that were skipped")
            }
        }
    }
}
