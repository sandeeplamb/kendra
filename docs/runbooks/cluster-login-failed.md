# Cluster login failed or expired

## Symptoms

- A Fleet card is red and says "Login for this cluster failed or expired: getting credentials: exec: …".
- A list shows the same line for one cluster while the others load.

## Cause

The kubeconfig uses an exec plugin (`aws eks get-token`, `kubelogin`, `gke-gcloud-auth-plugin`) and its login
has expired, or the plugin is not on the `PATH` of the app. Kendra runs the same helpers as `kubectl`.

## Resolution

1. Log in again, exactly as for `kubectl`:
   ```sh
   aws sso login --profile <profile>     # EKS
   az login                              # AKS
   ```
2. If the error names an executable (`kubelogin`, `aws`), install it, for example `brew install Azure/kubelogin/kubelogin`.
3. In Kendra press **Refresh**. The mac app reads the `PATH` of your login shell, so a tool installed in a
   new place needs the app to be restarted (Cmd+Q, then open it again).

## Validation

`kubectl --context <context> get ns` works in a terminal, and the cluster card turns healthy after Refresh.

## References

- [Install](../install.md)
