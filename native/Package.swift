// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "Agenthail",
    platforms: [.macOS(.v15)],
    products: [
        .executable(name: "AgenthailMac", targets: ["AgenthailMac"])
    ],
    dependencies: [
        .package(url: "https://github.com/gonzalezreal/textual", exact: "0.5.0"),
        .package(url: "https://github.com/sindresorhus/KeyboardShortcuts", exact: "3.1.0")
    ],
    targets: [
        .executableTarget(
            name: "AgenthailMac",
            dependencies: [
                .product(name: "Textual", package: "textual"),
                .product(name: "KeyboardShortcuts", package: "KeyboardShortcuts")
            ],
            path: ".",
            exclude: ["Agenthail.xcodeproj", "iOS", "iOSTests", "iOSUITests", "tests", "DESIGN.md", "PRODUCT.md", "project.yml", "GenerateIcon.swift"],
            sources: [
                "AgenthailApp.swift",
                "DuplicateApplicationPolicy.swift",
                "StatusRefreshPolicy.swift",
                "AgenthailModels.swift",
                "TurnSettings.swift",
                "AgenthailAPI.swift",
                "UIConfig.swift",
                "SurfaceMode.swift",
                "PaletteTokens.swift",
                "TextColors.swift",
                "RoleStyle.swift",
                "RoleInlineStyle.swift",
                "Backdrop.swift",
                "BackdropLayers.swift",
                "ScrimField.swift",
                "GlassSolve.swift",
                "HolderPins.swift",
                "ComposerAttachment.swift",
                "ComposerTextSync.swift",
                "ComposerCompletion.swift",
                "SessionSelection.swift",
                "ProjectActions.swift",
                "HostNames.swift",
                "UsageMeterText.swift",
                "UsageAccounts.swift",
                "SessionTree.swift",
                "MetadataOverlay.swift",
                "ClaudeSubagentList.swift",
                "SessionStore.swift",
                "EventRetryBackoff.swift",
                "CatalogBatch.swift",
                "CatalogInbox.swift",
                "CatalogFeed.swift",
                "ToolPresentation.swift",
                "TextDiff.swift",
                "ToolDiffCache.swift",
                "DiffPresentation.swift",
                "ToolRunSummary.swift",
                "TranscriptRows.swift",
                "AgentMessage.swift",
                "ClampedText.swift",
                "TurnClock.swift",
                "TimelineMetadata.swift",
                "TimelineEventLabel.swift",
                "OperationsRefreshPolicy.swift",
                "RequestNotifications.swift",
                "RequestAnswer.swift",
                "QuestionPager.swift",
                "QuestionPagerDrafts.swift",
                "QuestionPagerView.swift",
                "RequestCard.swift",
                "AgenthailModel.swift",
                "VoiceAPI.swift",
                "VoiceAudioBridge.swift",
                "VoiceCallDiagnostics.swift",
                "VoiceOperatorModel.swift",
                "macOS"
            ],
        )
    ]
)
