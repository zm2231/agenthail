import SwiftUI

struct IOSTurnSettingChips: View {
    var model: AgenthailIOSModel
    let sessionID: String
    let settings: [TurnSetting]
    let showModels: () -> Void

    private var order: [String] { [TurnSetting.model, TurnSetting.effort, TurnSetting.mode].filter { settings.setting($0) != nil } }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            ForEach(Array((0...order.count).reversed()), id: \.self) { shown in
                HStack(spacing: 6) {
                    ForEach(order.prefix(shown), id: \.self) { chip($0) }
                    if shown < order.count { more(Array(order.dropFirst(shown))) }
                }
            }
        }
        .disabled(model.pendingControls.contains(sessionID))
    }

    @ViewBuilder private func chip(_ key: String) -> some View {
        if let setting = settings.setting(key) {
            switch key {
            case TurnSetting.model:
                Button(action: showModels) { Self.chip(setting.valueLabel) }
                    .buttonStyle(.plain)
                    .accessibilityLabel(setting.chipAccessibilityLabel)
                    .accessibilityIdentifier("composer-model-picker")
            case TurnSetting.effort:
                menu(setting, options: setting.options)
                    .accessibilityIdentifier("composer-effort")
            default:
                menu(setting, options: setting.quietOptions)
                    .accessibilityIdentifier("composer-mode")
            }
        }
    }

    private func more(_ keys: [String]) -> some View {
        Menu {
            ForEach(keys, id: \.self) { key in
                if let setting = settings.setting(key) {
                    if key == TurnSetting.model {
                        Button("\(setting.chipName): \(setting.valueLabel)", systemImage: "cpu", action: showModels)
                    } else {
                        TurnSettingPicker(model: model, sessionID: sessionID, setting: setting, options: key == TurnSetting.effort ? setting.options : setting.quietOptions)
                    }
                }
            }
        } label: {
            Self.chip(Image(systemName: "ellipsis"))
        }
        .accessibilityLabel(keys.compactMap { settings.setting($0)?.chipAccessibilityLabel }.joined(separator: ", "))
        .accessibilityIdentifier("composer-turn-more")
    }

    private func menu(_ setting: TurnSetting, options: [TurnSetting.Option]) -> some View {
        Menu {
            TurnSettingPicker(model: model, sessionID: sessionID, setting: setting, options: options)
        } label: {
            Self.chip(setting.valueLabel)
        }
        .accessibilityLabel(setting.chipAccessibilityLabel)
    }

    static func chip(_ text: String) -> some View {
        chip(Text(text))
    }

    static func chip(_ label: some View) -> some View {
        label.font(.caption.weight(.medium))
            .foregroundStyle(.primary)
            .lineLimit(1)
            .fixedSize()
            .padding(.horizontal, 12).frame(minHeight: 44)
            .background(Color.primary.opacity(0.05), in: Capsule())
    }
}
