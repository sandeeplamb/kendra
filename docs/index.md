# kendra

Kendra is a read-only console for every Kubernetes cluster in your kubeconfig, with first-class
Carvel kapp and Istio topology. It starts a small web server on `127.0.0.1` and shows it in a native
macOS window (Chrome or Edge in app mode on Linux and Windows). It reads the kubeconfig the way
`kubectl` does: if `kubectl get pods` works, Kendra works. Nothing is installed in the clusters, and it
never changes one.

## Quick Links

- [Install](install.md)
- [Usage](usage.md)
- [Runbooks](runbooks/index.md)

## What it is for

| You want to | Open |
|---|---|
| See every cluster at a glance | Fleet dashboard |
| Plan an EKS or AKS upgrade | Versions, Deprecated APIs |
| Find what expires or drifts | Certificates, Image inventory, Compare clusters |
| Check how pods sit on nodes and zones | Zones & pod spread |
| Find out why something is unhealthy | **Why?** tab on a pod, node, service or workload |
| See what was failing 20 minutes ago | Time machine |

## Support

Open an issue at <https://github.com/sandeeplamb/kendra/issues>.
