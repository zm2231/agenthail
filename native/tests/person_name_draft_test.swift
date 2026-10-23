@testable import AgenthailHelpers
import Foundation

@main
struct PersonNameDraftTest {
    static func main() {
        let account = PersonState(name: "zain", set: false)
        let ada = PersonState(name: "Ada", set: true)
        check(PersonNameDraft.field(for: account) == "", "an account name leaves the field empty, shown as its prompt")
        check(PersonNameDraft.field(for: ada) == "Ada", "a set name fills the field")
        check(PersonNameDraft.field(for: PersonState(name: "Grace", set: true)) == "Grace", "a rename made elsewhere replaces the field")
        check(PersonNameDraft.submission("", current: ada) == nil, "an emptied field never resets the name")
        check(PersonNameDraft.submission("   ", current: ada) == nil, "a blank field never resets the name")
        check(PersonNameDraft.submission(" Ada ", current: ada) == nil, "the same name is not sent again")
        check(PersonNameDraft.submission(" Grace ", current: ada) == "Grace", "a new name is sent trimmed")
        check(PersonNameDraft.submission("zain", current: account) == "zain", "setting the account's own name makes it set")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
