@testable import AgenthailHelpers
import Foundation

@main
struct InspectorSettingTextTest {
    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }

    static func main() {
        let options = [TurnSetting.Option(id: "default", label: "Root default"), TurnSetting.Option(id: "120000", label: "120K")]
        let session = TurnSetting(key: TurnSetting.sessionCompactWindow, value: "default", options: options, applies: "nextMessage")
        let newSessions = TurnSetting(key: TurnSetting.compactWindow, value: "120000", options: options, applies: "newSessions")
        check(InspectorSettingText.title(newSessions) == "Compact new sessions at" && InspectorSettingText.title(session) == "Compact this session at", "the compact settings are named for what they set")
        check(InspectorSettingText.value(session) == "Same as new sessions", "the session's default option reads as following new sessions: \(InspectorSettingText.value(session))")
        check(InspectorSettingText.label(options[0], in: newSessions) == "Root default" && InspectorSettingText.value(newSessions) == "120K", "other settings keep their option labels")
        check(session.current?.id == "default", "the option id is unchanged, so picking still sends it")
        check(InspectorSettingText.note(session) == "Applies when the session next goes idle" && InspectorSettingText.note(newSessions) == nil, "a session setting says when it applies; a new-sessions one says nothing extra")
        let model = TurnSetting(key: TurnSetting.model, value: "x", options: [], applies: "nextTurn")
        check(InspectorSettingText.title(model) == model.chipName && InspectorSettingText.note(model) == "From the next turn", "other settings keep their names and notes")
        print("inspector setting text ok")
    }
}
