import Foundation

@main
struct DuplicateApplicationPolicyTest {
    static func main() {
        let ownBundleIdentifier = "com.agenthail.app"
        let ownProcessIdentifier: Int32 = 100

        guard shouldTerminateDuplicateApplication(
            candidateBundleIdentifier: ownBundleIdentifier,
            candidateProcessIdentifier: 101,
            ownBundleIdentifier: ownBundleIdentifier,
            ownProcessIdentifier: ownProcessIdentifier
        ) else { exit(1) }

        guard !shouldTerminateDuplicateApplication(
            candidateBundleIdentifier: ownBundleIdentifier,
            candidateProcessIdentifier: ownProcessIdentifier,
            ownBundleIdentifier: ownBundleIdentifier,
            ownProcessIdentifier: ownProcessIdentifier
        ) else { exit(1) }

        guard !shouldTerminateDuplicateApplication(
            candidateBundleIdentifier: "com.example.unrelated",
            candidateProcessIdentifier: 101,
            ownBundleIdentifier: ownBundleIdentifier,
            ownProcessIdentifier: ownProcessIdentifier
        ) else { exit(1) }

        guard !shouldTerminateDuplicateApplication(
            candidateBundleIdentifier: nil,
            candidateProcessIdentifier: 101,
            ownBundleIdentifier: ownBundleIdentifier,
            ownProcessIdentifier: ownProcessIdentifier
        ) else { exit(1) }

        guard !shouldTerminateDuplicateApplication(
            candidateBundleIdentifier: ownBundleIdentifier,
            candidateProcessIdentifier: 101,
            ownBundleIdentifier: nil,
            ownProcessIdentifier: ownProcessIdentifier
        ) else { exit(1) }

        guard !shouldTerminateDuplicateApplication(
            candidateBundleIdentifier: ownBundleIdentifier,
            candidateProcessIdentifier: 101,
            ownBundleIdentifier: "",
            ownProcessIdentifier: ownProcessIdentifier
        ) else { exit(1) }
    }
}
