# Releasing a tester build

Preliminary betas go to a few testers as a signed, notarized DMG sent by
hand (no Sparkle, no public download yet). Versions are `0.1.0-beta.N`.

## Once per Mac

1. Apple Developer Program membership (paid). Only the team's **Account
   Holder** can create Developer ID certificates (Admin is not enough): if
   someone else is the Account Holder, they create it and send it as a
   `.p12` to import into the login keychain.
2. A **Developer ID Application** certificate in the login keychain: Xcode →
   Settings → Accounts → the team → Manage Certificates… → + → Developer ID
   Application. Check with `security find-identity -v -p codesigning`.
3. An app-specific password for the Apple ID (account.apple.com → Sign-In
   and Security → App-Specific Passwords), then store the notarization
   credentials in the keychain (prompts for the password, never on the
   command line):

   ```sh
   xcrun notarytool store-credentials covo-notary --apple-id <apple-id> --team-id <team-id>
   ```

   The Team ID is in developer.apple.com → Account → Membership details
   (the code in an "Apple Development: Name (…)" certificate is a personal
   ID, not the Team ID).

## Each release

```sh
go test ./... && go tool golangci-lint run
go test -tags integration -timeout 45m ./test/e2e/
app/build.sh test
app/release.sh 0.1.0-beta.1
```

`app/release.sh` refuses a dirty working tree, builds with the version
stamped in `covo version` and the app's Info.plist (build number = commit
count), signs `covo`, `covo-hostshelper` and the app with the hardened
runtime and a secure timestamp, notarizes and staples the app, then makes,
signs, notarizes and staples `dist/Covo-<version>.dmg` and writes its
SHA-256 next to it. Tag the commit (`git tag v0.1.0-beta.1`) and send the DMG
with `docs/beta.md`.

Components (PHP, Caddy, MySQL…) are not in the DMG: Covo downloads them on
first use and checks them against the SHA-256 pinned in its manifest.
