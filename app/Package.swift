// swift-tools-version: 6.0
// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import PackageDescription

let package = Package(
    name: "Benbow",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Benbow", targets: ["Benbow"]),
    ],
    dependencies: [
        // App updates (docs/updates.md). Exact version: SwiftPM checks the
        // framework's checksum against the one in Sparkle's Package.swift.
        .package(url: "https://github.com/sparkle-project/Sparkle", exact: "2.10.1"),
    ],
    targets: [
        // BenbowKit talks to the core's local API; no UI, fully testable.
        .target(name: "BenbowKit"),
        .executableTarget(
            name: "Benbow",
            dependencies: ["BenbowKit", .product(name: "Sparkle", package: "Sparkle")],
            // Sparkle.framework sits in Contents/Frameworks of the bundle.
            linkerSettings: [.unsafeFlags(["-Xlinker", "-rpath", "-Xlinker", "@executable_path/../Frameworks"])]
        ),
        .testTarget(
            name: "BenbowKitTests",
            dependencies: ["BenbowKit"],
            resources: [.copy("Fixtures")]
        ),
    ]
)
