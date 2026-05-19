# Code Signing Policy

This document describes the code signing policy for the RustleBoost project.

## Signed Artifacts

The following artifacts are signed for each release:

- `RustleBoost_x.x.x_x64-setup.exe` — Windows installer (NSIS)

## Certificate

RustleBoost releases are signed using a certificate provided by
**[SignPath Foundation](https://signpath.org)** — a non-profit organization that provides
free code signing for open source projects.

The code signing certificate is issued to **SignPath Foundation**.

## Team

| Name | Role | Responsibilities |
|------|------|-----------------|
| Artem Kolesnichenko ([@gministr](https://github.com/gministr)) | Author / Approver | Source code, build scripts, release approval |

**Role definitions:**
- **Author** — trusted contributor with write access to source code and build scripts
- **Approver** — responsible for approving each release for signing

## Signing Process

1. A release commit is tagged with a version number (e.g., `v1.0.1`)
2. The Approver manually reviews the changes since the last release
3. The Approver submits a signing request in SignPath
4. SignPath builds the artifact from source and signs it
5. The signed installer is uploaded to GitHub Releases

## Verification

To verify the signature of a downloaded installer:

```powershell
Get-AuthenticodeSignature .\RustleBoost_x.x.x_x64-setup.exe
```

The signature should show:
- **Status:** Valid
- **SignerCertificate:** SignPath Foundation

## Security

- Multi-factor authentication (MFA) is enabled on all GitHub accounts with write access
- MFA is enabled on the SignPath account
- The signing key is stored securely in SignPath's HSM infrastructure

## Attribution

Code signing infrastructure provided by [SignPath.io](https://signpath.io) and
[SignPath Foundation](https://signpath.org).
