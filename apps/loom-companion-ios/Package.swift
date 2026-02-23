// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "LoomCompanion",
    platforms: [
        .iOS(.v17),
        .macOS(.v14),
    ],
    products: [
        .library(name: "LoomCompanionKit", targets: ["LoomCompanionKit"]),
    ],
    targets: [
        .target(
            name: "LoomCompanionKit",
            path: "Sources/LoomCompanionKit"
        ),
        .executableTarget(
            name: "LoomCompanion",
            dependencies: ["LoomCompanionKit"],
            path: "Sources/LoomCompanion"
        ),
        .testTarget(
            name: "LoomCompanionKitTests",
            dependencies: ["LoomCompanionKit"],
            path: "Tests/LoomCompanionKitTests",
            resources: [.copy("Fixtures")]
        ),
    ]
)
