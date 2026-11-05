# Releasing a tester build

Betas are a signed, notarized DMG. Versions are `0.1.0-beta.N`. From
0.1.0-beta.3 the app updates itself through Sparkle: the DMG goes on a
GitHub release and `dist/appcast.xml` on getcovo.dev.

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

   The Team ID is in developer.apple.com → Account → Membership details,
   and in the "Developer ID Application: Name (TEAMID)" certificate (the
   code in an "Apple Development: Name (…)" certificate is a personal ID,
   not the Team ID).
4. The Sparkle signing key (EdDSA), in the login keychain under the account
   `covo`. Its public half is `SUPublicEDKey` in `app/Info.plist`: every
   update must be signed with this exact key, or installed apps refuse it.
   Made once with Sparkle's tool (after `swift package resolve` in `app/`):

   ```sh
   app/.build/artifacts/sparkle/Sparkle/bin/generate_keys --account covo
   ```

   Keep an offline backup (`generate_keys --account covo -x <file>`, then
   store the file somewhere safe and delete it). On another Mac,
   `generate_keys --account covo -f <file>` imports it. If the key is lost,
   installed copies can only move to a new key through a manual download.

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
SHA-256 next to it. Sparkle's framework is inside the app (its XPC services
are dropped: Covo is not sandboxed) and signed the same way. Last, it signs
the DMG with the Sparkle key and writes `dist/appcast.xml`, which points at
`https://github.com/getcovo/covo/releases/download/v<version>/Covo-<version>.dmg`.

Then publish, in this order (an appcast must never point at a missing DMG):

1. Tag the commit (`git tag -a v<version>`) and push the tag.
2. GitHub release `v<version>` with the DMG and its `.sha256`, notes in the
   description (Sparkle shows the release page).
3. Copy `dist/appcast.xml` to the website (`public/appcast.xml`) and deploy.

Before the repository is public, GitHub release downloads need a login,
so Sparkle cannot fetch them: until then, send the DMG by hand.

Components (PHP, Caddy, MySQL…) are not in the DMG: Covo downloads them on
first use and checks them against the SHA-256 pinned in its manifest.
