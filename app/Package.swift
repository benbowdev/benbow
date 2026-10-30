// swift-tools-version: 6.0
// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import PackageDescription

let package = Package(
    name: "Covo",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Covo", targets: ["Covo"]),
    ],
    targets: [
        // CovoKit talks to the core's local API; no UI, fully testable.
        .target(name: "CovoKit"),
        .executableTarget(name: "Covo", dependencies: ["CovoKit"]),
        .testTarget(
            name: "CovoKitTests",
            dependencies: ["CovoKit"],
            resources: [.copy("Fixtures")]
        ),
    ]
)
