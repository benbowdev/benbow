// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import AppKit
import BenbowKit
import SwiftUI

/// "Public Link": one site on the internet for a while through a Cloudflare
/// quick tunnel, for payment providers' webhooks or showing a client.
struct TunnelSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    @State private var minutes = 60
    @State private var password = false
    /// The password, kept only while this sheet is open: Benbow does not
    /// store it.
    @State private var secret: (user: String, password: String)?
    @State private var copied: String?

    private var link: TunnelStatus? {
        model.status?.tunnel.flatMap { $0.site == site.name ? $0 : nil }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Public link for \(site.name)").font(.headline)
            if let l = link {
                open(l)
            } else {
                closed
            }
            Spacer(minLength: 0)
            HStack {
                if link != nil {
                    Button("Close Link", role: .destructive) {
                        secret = nil
                        Task { await model.closeTunnel() }
                    }
                    .disabled(model.busy != nil)
                    Button("1 Hour More") {
                        guard let l = link else { return }
                        Task { _ = await model.openTunnel(site, minutes: min(480, l.minutesLeft + 60), password: l.protected) }
                    }
                    .disabled(model.busy != nil || (link?.minutesLeft ?? 480) > 420)
                }
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
                if link == nil {
                    Button("Create Link") {
                        Task {
                            if let res = await model.openTunnel(site, minutes: minutes, password: password),
                               let user = res.user, let pw = res.password {
                                secret = (user, pw)
                            }
                        }
                    }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!model.running || model.busy != nil)
                }
            }
        }
        .padding(20)
        .frame(width: 560)
        .frame(minHeight: 300)
    }

    @ViewBuilder private var closed: some View {
        Text("Anyone with the link can reach this site from the internet until the link closes: a payment provider sending webhooks, or a client looking at your work.")
            .font(.callout).foregroundStyle(.secondary)
        ExplainBox(title: "What happens", steps: [
            "The first time, Benbow downloads Cloudflare's tunnel program (cloudflared, about 20 MB) into its own folder. Nothing is installed on the Mac.",
            "The site gets a random address at trycloudflare.com. Requests travel through Cloudflare to this Mac; no account is needed.",
            "Pages show the public address. The site's files and database are not changed.",
            "Never served: .git, .env, database dumps, backups and logs. Adminer and Mail stay private.",
        ], caution: "It works while this Mac is awake and Benbow runs. The address is new every time you create a link.",
        collapsible: true)
        Picker("Close after", selection: $minutes) {
            Text("15 minutes").tag(15)
            Text("30 minutes").tag(30)
            Text("1 hour").tag(60)
            Text("2 hours").tag(120)
            Text("8 hours").tag(480)
        }
        .fixedSize()
        Toggle("Ask for a password", isOn: $password)
        Text(password
             ? "Good for a client: you get a password to send along with the link."
             : "Webhooks cannot send a password: leave this off for payment providers.")
            .font(.caption).foregroundStyle(.secondary)
        if !model.running {
            Text("Start Benbow first.").foregroundStyle(.orange).font(.callout)
        }
        if let other = model.status?.tunnel, other.site != site.name {
            Text("\(other.site) has a public link: it will be closed.").font(.callout).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private func open(_ l: TunnelStatus) -> some View {
        if !l.ready {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Waiting for Cloudflare to publish the address…").font(.callout)
            }
            Text("This takes a few seconds.").font(.caption).foregroundStyle(.secondary)
        } else {
            row("Link", l.url) {
                Button("Open") { if let u = URL(string: l.url) { NSWorkspace.shared.open(u) } }
            }
            if let s = secret {
                row("User", s.user)
                row("Password", s.password)
                Label("Copy the password now: Benbow does not keep it, so it cannot show it again.", systemImage: "key")
                    .font(.callout).foregroundStyle(.secondary)
            } else if l.protected {
                Label("Visitors need the password shown when the link was created. To get a new one, close the link and create another.",
                      systemImage: "lock")
                    .font(.callout).foregroundStyle(.secondary)
            } else {
                Label("Anyone with the link can open the site. For webhooks, add the path your provider needs, for example \(l.url)/wp-json/…",
                      systemImage: "globe")
                    .font(.callout).foregroundStyle(.secondary)
            }
        }
        Label("Closes by itself in \(l.minutesLeft) minutes, or when Benbow stops.", systemImage: "timer")
            .font(.callout).foregroundStyle(.secondary)
    }

    private func row(_ title: String, _ value: String, @ViewBuilder extra: () -> some View = { EmptyView() }) -> some View {
        HStack {
            Text(title).frame(width: 70, alignment: .leading).foregroundStyle(.secondary)
            Text(value).textSelection(.enabled).font(.body.monospaced()).lineLimit(1).truncationMode(.middle)
            Spacer()
            Button(copied == value ? "Copied" : "Copy") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(value, forType: .string)
                copied = value
            }
            extra()
        }
    }
}
