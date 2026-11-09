// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import Foundation

/// Starts the core's daemon with the benbow binary shipped in the app.
public enum Daemon {
    /// The benbow binary: $BENBOW_BIN (development), else the app bundle's
    /// Contents/Resources/bin/benbow, next to benbow-hostshelper.
    public static func binary(bundle: Bundle = .main, environment: [String: String] = ProcessInfo.processInfo.environment) -> URL? {
        if let path = environment["BENBOW_BIN"], !path.isEmpty {
            return URL(fileURLWithPath: path)
        }
        return bundle.resourceURL?.appendingPathComponent("bin/benbow")
    }

    /// Runs `benbow daemon start --json`: idempotent, and it restarts the
    /// daemon when the app ships a different benbow binary.
    public static func start(binary: URL, environment: [String: String]? = nil) async throws -> DaemonInfo {
        let output = try await run(binary, ["daemon", "start", "--json"], environment: environment)
        return try EnvelopeDecoder.decode(DaemonInfo.self, from: output)
    }

    static func run(_ binary: URL, _ args: [String], environment: [String: String]? = nil) async throws -> Data {
        try await Task.detached {
            let p = Process()
            p.executableURL = binary
            p.arguments = args
            if let environment { p.environment = environment }
            let out = Pipe()
            p.standardOutput = out
            p.standardError = FileHandle.nullDevice
            try p.run()
            let data = out.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            return data
        }.value
    }
}
