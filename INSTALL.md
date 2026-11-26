# Install Kendra

## 1. Requirements

| Need | Check | Get it |
|---|---|---|
| A kubeconfig that works | `kubectl get pods` | the same login you already use |
| Using the `.pkg` | nothing else | macOS 12 or newer |
| Building from source: Go 1.24+ | `go version` | `brew install go` |
| Building on macOS: Xcode command line tools | `xcode-select -p` | `xcode-select --install` |

## 2. Install the macOS package

Open `Kendra-<version>.pkg` and follow the installer. It installs `/Applications/Kendra.app` and links
`/usr/local/bin/knd`. Open **Kendra** from Spotlight. If macOS says the package or app is from an unidentified
developer (not notarized), right-click it and choose **Open** once.

## 3. Install from source

```sh
cd <CURRENT REPO>                                 # the local directory of this repository (contains go.mod)
./install.sh                                      # macOS: Kendra.app + knd; Linux: knd + app-menu entry
open -a Kendra                                    # or: knd
```

`make build && ./knd` builds and runs it without installing. If no window opens, use the printed address
(`http://127.0.0.1:7799/?t=…`) in a browser.

## 4. Build the .pkg (for distribution)

```sh
make pkg                                          # dist/Kendra-<version>.pkg, for this Mac's CPU
```

Unsigned by default. To sign and notarize, set these before `make pkg`:

```sh
export SIGN_APP="Developer ID Application: Your Name (TEAMID)"
export SIGN_PKG="Developer ID Installer: Your Name (TEAMID)"
export NOTARY_PROFILE=my-profile      # created with: xcrun notarytool store-credentials my-profile
```

The package holds a universal binary (Apple silicon and Intel). `make pkg` builds each CPU with cgo and joins them
with `lipo`, so it needs the Xcode command line tools on the Mac that builds it. The Mac app and `knd` start with the
CPU of the Mac they run on.

## 5. Cloud logins

Kendra runs the same credential helpers as `kubectl`. Log in first:

```sh
aws sso login --profile <profile>      # EKS
az login                               # AKS (kubelogin)
```

## 6. Update, remove

```sh
git pull && ./install.sh     # update
./install.sh --uninstall     # remove knd and the macOS app
rm -rf ~/.kendra            # also delete the port-forward log
```

## Troubleshooting

| Symptom | Fix |
|---|---|
| "Open Kendra with the address printed in the terminal" | You opened `127.0.0.1:7799` without the `?t=` token. Use the printed address. |
| `address already in use` | Another Kendra is running. Use `knd --addr 127.0.0.1:7800`. |
| A cluster shows "Login … failed or expired" | Run its login command (`aws sso login`, `az login`) and reload. |
| kapp page lists fewer resources than expected | The banner says how many resource types you cannot list. Ask for read access on them. |
| Istio page says not installed | The cluster has no `networking.istio.io` resources. |
