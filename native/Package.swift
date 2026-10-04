// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "Agenthail",
    platforms: [.macOS(.v15)],
    products: [
        .executable(name: "AgenthailMac", targets: ["AgenthailMac"])
    ],
    dependencies: [
        .package(url: "https://github.com/gonzalezreal/textual", exact: "0.5.0")
    ],
    targets: [
        .executableTarget(
            name: "AgenthailMac",
            dependencies: [.product(name: "Textual", package: "textual")],
            path: ".",
            exclude: ["Agenthail.xcodeproj", "iOS", "iOSTests", "iOSUITests", "tests", "DESIGN.md", "PRODUCT.md", "project.yml", "GenerateIcon.swift"],
            sources: [
                "AgenthailApp.swift",
                "DuplicateApplicationPolicy.swift",
                "ResponsivePairLayout.swift",
                "StatusRefreshPolicy.swift",
                "AgenthailModels.swift",
                "TurnSettings.swift",
                "AgenthailAPI.swift",
                "SessionSelection.swift",
                "EventRetryBackoff.swift",
                "ToolPresentation.swift",
                "OperationsRefreshPolicy.swift",
                "AgenthailModel.swift",
                "AgenthailViews.swift",
                "macOS"
            ],
        )
    ]
)
