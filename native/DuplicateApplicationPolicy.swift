func shouldTerminateDuplicateApplication(
    candidateBundleIdentifier: String?,
    candidateProcessIdentifier: Int32,
    ownBundleIdentifier: String?,
    ownProcessIdentifier: Int32
) -> Bool {
    guard let ownBundleIdentifier, !ownBundleIdentifier.isEmpty else { return false }
    return candidateBundleIdentifier == ownBundleIdentifier && candidateProcessIdentifier != ownProcessIdentifier
}
