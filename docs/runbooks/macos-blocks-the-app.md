# macOS blocks the app or package

## Symptoms

- "Kendra cannot be opened because the developer cannot be verified" when opening the `.pkg` or the app.

## Cause

The package and app are signed ad hoc, not with an Apple Developer ID, and are not notarized. macOS Gatekeeper asks
for approval the first time on every Mac.

## Resolution

1. Right-click the `.pkg` (or `Kendra.app`) and choose **Open**, then **Open** again.
2. Or allow it in **System Settings → Privacy & Security** ("Open Anyway").
3. To remove the prompt for everyone, build the package with a Developer ID: set `SIGN_APP`, `SIGN_PKG` and
   `NOTARY_PROFILE`, then run `make pkg` on a Mac.

## Validation

Kendra opens in its own window with its own Dock icon and Cmd+Q.

## References

- [Install](../install.md)
