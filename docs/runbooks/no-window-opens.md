# No window opens

## Symptoms

- `knd` prints `Kendra is running at http://127.0.0.1:…/?t=…` but no window appears.
- The page says "Open Kendra with the address printed in the terminal".

## Cause

- The build has no native window (Linux, Windows, or `knd --no-open`), and no Chrome, Edge or Brave was found.
- The address was opened without the `?t=` token, which is new on every run.
- Port 7799 is already used by another Kendra.

## Resolution

1. Copy the full address printed in the terminal, token included, into a browser.
2. If `address already in use`, start on another port: `knd --addr 127.0.0.1:7800`. Only `127.0.0.1` is accepted.
3. On macOS use the app: `open -a Kendra`. To force a Chrome app window use `KND_BROWSER=1 knd`.

## Validation

The Kendra start screen with the list of clusters appears.

## References

- [Install](../install.md)
