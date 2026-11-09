// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import BenbowKit
import SwiftUI

/// Runs wp-cli, Composer or Artisan for a site and shows the output. The
/// same as `benbow wp|composer|artisan <site> …` in a terminal, without input.
struct ToolSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site

    @State private var tool = "composer"
    @State private var command = ""
    @State private var running = false
    @State private var result: ToolResult?
    @State private var error: String?

    private var tools: [String] {
        switch site.framework {
        case "laravel": ["artisan", "composer"]
        case "wordpress": ["wp", "composer"]
        default: ["composer"]
        }
    }

    private var examples: String {
        switch tool {
        case "artisan": "migrate, route:list, make:model Post -m"
        case "wp": "plugin list, user list, cache flush"
        default: "install, require guzzlehttp/guzzle, show"
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Run a command for \(site.name)").font(.headline)
            HStack {
                Picker("", selection: $tool) {
                    ForEach(tools, id: \.self) { Text($0).tag($0) }
                }
                .labelsHidden()
                .fixedSize()
                TextField("Arguments", text: $command, prompt: Text(examples))
                    .font(.system(.body, design: .monospaced))
                    .onSubmit { Task { await run() } }
                Button(running ? "Running…" : "Run") { Task { await run() } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(running || command.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 6) {
                    if let result {
                        Text(result.exitCode == 0 ? "Done" : "Exit code \(result.exitCode)")
                            .font(.caption.weight(.medium))
                            .foregroundStyle(result.exitCode == 0 ? .green : .red)
                        Text(result.output + (result.errors.map { "\n" + $0 } ?? ""))
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    } else if let error {
                        Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled)
                    } else {
                        Text("Runs `\(tool) …` with PHP \(site.php) in \(site.projectRoot). Commands that ask questions get no answers here: use `benbow \(tool) \(site.name) …` in a terminal for those.")
                            .font(.callout).foregroundStyle(.secondary)
                    }
                }
                .padding(8)
            }
            .background(.background.secondary, in: RoundedRectangle(cornerRadius: 6))
            HStack {
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
            }
        }
        .padding(20)
        .frame(width: 680, height: 460)
        .onAppear { tool = tools[0] }
    }

    private func run() async {
        let args = Self.split(command)
        guard !args.isEmpty else { return }
        running = true
        defer { running = false }
        error = nil
        result = nil
        do {
            result = try await model.runTool(site, tool: tool, args: args + (tool == "artisan" ? ["--no-interaction"] : tool == "composer" ? ["--no-interaction"] : []))
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Splits a command line like a shell: spaces separate, quotes group.
    static func split(_ s: String) -> [String] {
        var out: [String] = []
        var cur = ""
        var quote: Character?
        var inWord = false
        for ch in s {
            if let q = quote {
                if ch == q { quote = nil } else { cur.append(ch) }
            } else if ch == "\"" || ch == "'" {
                quote = ch
                inWord = true
            } else if ch == " " || ch == "\t" {
                if inWord { out.append(cur); cur = ""; inWord = false }
            } else {
                cur.append(ch)
                inWord = true
            }
        }
        if inWord { out.append(cur) }
        return out
    }
}
