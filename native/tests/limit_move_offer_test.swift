@testable import AgenthailHelpers
import Foundation

@main
struct LimitMoveOfferTest {
    static func main() {
        let offered = try! JSONDecoder().decode(LimitResumeState.self, from: Data(#"{"state":"waiting","moveTo":{"root":"work","account":"work@example.com","reason":"most headroom, session 10%"}}"#.utf8))
        expect(offered.moveTo?.root == "work", "the offer's root decodes")
        expect(offered.moveTo?.name == "work@example.com", "an offer is named by its account")
        expect(offered.moveTo?.reason == "most headroom, session 10%", "the offer's reason decodes")
        let unnamed = try! JSONDecoder().decode(LimitResumeState.self, from: Data(#"{"state":"waiting","moveTo":{"root":"spare"}}"#.utf8))
        expect(unnamed.moveTo?.name == "spare", "an offer without an account is named by its root")
        let plain = try! JSONDecoder().decode(LimitResumeState.self, from: Data(#"{"state":"waiting"}"#.utf8))
        expect(plain.moveTo == nil, "a stop without an offer has none")
        expect(plain.note == nil && plain.label.hasPrefix("Usage limit"), "a stop from a daemon that sends no note keeps the generic label: \(plain.label)")
        let noted = try! JSONDecoder().decode(LimitResumeState.self, from: Data(#"{"state":"waiting","resetsAt":"2026-09-12T18:20:00Z","note":"Its account has room again; restart it to continue"}"#.utf8))
        expect(noted.label == "Its account has room again; restart it to continue", "a stop's note is its label: \(noted.label)")
        let lapsed = try! JSONDecoder().decode(LimitResumeState.self, from: Data(#"{"state":"lapsed","note":"Resuming was refused after its reset; resume it when the limit lifts"}"#.utf8))
        expect(lapsed.label == "Resuming was refused after its reset; resume it when the limit lifts", "a kept-stopped session's note is its label: \(lapsed.label)")
        let blank = try! JSONDecoder().decode(LimitResumeState.self, from: Data(#"{"state":"lapsed","note":" "}"#.utf8))
        expect(blank.label == "Stopped by usage limit", "a blank note falls back to the generic label: \(blank.label)")
        print("limit move offer tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL limit_move_offer_test: \(message)\n".utf8))
            exit(1)
        }
    }
}
