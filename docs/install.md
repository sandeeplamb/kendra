# Install

## Requirements

| Need | Check | Get it |
|---|---|---|
| A kubeconfig that works | `kubectl get pods` | the login you already use |
| Using the `.pkg` | macOS 12 or newer | nothing else |
| Building from source | `go version` (1.24+) | `brew install go` |
| Building on macOS | `xcode-select -p` | `xcode-select --install` |

## macOS package

Open `Kendra-<version>.pkg`. It installs `/Applications/Kendra.app` and links `/usr/local/bin/knd`.
It holds a universal binary, so it runs on Apple silicon and Intel. If macOS blocks it, see
[macOS blocks the app or package](runbooks/macos-blocks-the-app.md).

## From source

Run these in the repository directory (the one with `go.mod`):

```sh
./install.sh                 # macOS: Kendra.app + knd on PATH. Linux: knd + app-menu entry
open -a Kendra               # or Spotlight: "Kendra"; from a terminal: knd
./install.sh --uninstall     # removes the app and knd (keeps ~/.kendra)
```

## Cloud logins

Kendra runs the same credential helpers as `kubectl`. Log in first:

```sh
aws sso login --profile <profile>      # EKS
az login                               # AKS (kubelogin)
```

## Build the package

```sh
make pkg                     # dist/Kendra-<version>.pkg, universal
```

Unsigned by default. To sign and notarize, set `SIGN_APP`, `SIGN_PKG` and `NOTARY_PROFILE` first (see
`INSTALL.md` in the repository).
