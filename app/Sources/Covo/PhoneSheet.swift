// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

import CoreImage
import CoreImage.CIFilterBuiltins
import CovoKit
import SwiftUI

/// "Open on Phone": one site on the local network for a while, with QR
/// codes for the site and for installing Covo's certificate on the phone.
struct PhoneSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let site: Site
    @State private var minutes = 30
    @State private var https = false

    private var share: PhoneStatus? {
        model.status?.phone.flatMap { $0.site == site.name ? $0 : nil }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Open \(site.name) on your phone").font(.headline)
            if let s = share {
                if let plain = s.plainUrl, !https {
                    HStack(alignment: .top, spacing: 24) {
                        qr(plain, title: "Scan with the phone's camera", note: plain)
                        VStack(alignment: .leading, spacing: 8) {
                            Text("The site opens right away: nothing to install.")
                                .font(.callout)
                            Text("It is plain HTTP on your Wi-Fi, like any address of a router or printer. Something that needs HTTPS (camera, location, payments, some redirects to https) only works with the certificate.")
                                .font(.caption).foregroundStyle(.secondary)
                            Button("Use HTTPS Instead…") { https = true }
                        }
                    }
                } else {
                    HStack(alignment: .top, spacing: 24) {
                        qr(s.caPageUrl, title: "1. Install Covo's certificate", note: "Once per phone. Scan, then on iPhone: Settings → Profile Downloaded → Install, then Settings → General → About → Certificate Trust Settings → turn on Covo.")
                        qr(s.url, title: "2. Open the site", note: s.url)
                    }
                    if s.plainUrl != nil {
                        Button("Back to the Quick Address") { https = false }
                    }
                }
                Label("Served on \(s.addresses.joined(separator: ", ")) only, for \(s.minutesLeft) more minutes. The site's files and database are not changed.",
                      systemImage: "wifi")
                    .font(.callout).foregroundStyle(.secondary)
                if s.firewall == "block-all" {
                    Label("The macOS firewall blocks all incoming connections: allow them in System Settings → Network → Firewall, or the phone cannot connect.",
                          systemImage: "exclamationmark.triangle.fill")
                        .font(.callout).foregroundStyle(.orange)
                } else if s.firewall == "on" {
                    Label("The macOS firewall is on: if macOS asks whether “caddy” may accept incoming connections, choose Allow.",
                          systemImage: "info.circle")
                        .font(.callout).foregroundStyle(.secondary)
                }
                Text("The phone must be on the same Wi-Fi. iPhone finds .local names by itself; Android may not.")
                    .font(.caption).foregroundStyle(.secondary)
            } else {
                Text("Covo serves this site on your local network: scan a QR code and it opens on the phone, nothing to install (HTTPS with Covo's certificate is optional). Other devices on the same Wi-Fi can open it until it turns itself off. Its files and database are not changed.")
                    .font(.callout).foregroundStyle(.secondary)
                Picker("Turn off after", selection: $minutes) {
                    Text("15 minutes").tag(15)
                    Text("30 minutes").tag(30)
                    Text("1 hour").tag(60)
                    Text("2 hours").tag(120)
                    Text("8 hours").tag(480)
                }
                .fixedSize()
                if !model.running {
                    Text("Start Covo first.").foregroundStyle(.orange).font(.callout)
                }
                if model.status?.phone != nil {
                    Text("Another site is open on the phone: it will be closed.").font(.callout).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 0)
            HStack {
                if share != nil {
                    Button("Stop Sharing", role: .destructive) { Task { await model.stopPhone() } }
                        .disabled(model.busy != nil)
                }
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
                if share == nil {
                    Button("Open on Network") { Task { await model.sharePhone(site, minutes: minutes) } }
                        .keyboardShortcut(.defaultAction)
                        .disabled(!model.running || model.busy != nil)
                }
            }
        }
        .padding(20)
        .frame(width: 560)
        .frame(minHeight: 300)
    }

    private func qr(_ text: String, title: String, note: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).font(.callout.weight(.semibold))
            if let image = Self.qrImage(text) {
                Image(nsImage: image)
                    .interpolation(.none)
                    .resizable()
                    .frame(width: 170, height: 170)
                    .background(.white)
            }
            Text(note).font(.caption).foregroundStyle(.secondary).textSelection(.enabled).frame(width: 220, alignment: .leading)
        }
    }

    static func qrImage(_ text: String) -> NSImage? {
        let f = CIFilter.qrCodeGenerator()
        f.message = Data(text.utf8)
        f.correctionLevel = "M"
        guard let out = f.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)) else { return nil }
        let rep = NSCIImageRep(ciImage: out)
        let image = NSImage(size: rep.size)
        image.addRepresentation(rep)
        return image
    }
}
