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
        print("limit move offer tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL limit_move_offer_test: \(message)\n".utf8))
            exit(1)
        }
    }
}
