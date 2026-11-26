# Usage

Open Kendra, pick a cluster (or a group of clusters) in the **Cluster** box and use the left rail, or press
`Ctrl/⌘ K` to jump to any view, resource type, cluster or namespace. `/` focuses the filter.

## Picking clusters

The Cluster box is searchable. Type a name, or pick a group: `PROD`, `TEST`, `DEV`, `ACP`
(from parts of the context name split by `-`, `_`, `.`), `AKS`, `EKS`, `GKE` or `Local`. A group lists its
clusters first: choose **All N in this group**, or one cluster. A group scopes every "all clusters" page.
Star a cluster to pin it first. **Save view** keeps a page with its scope, namespace and filter in the left rail.

## Pages

| Group | Pages |
|---|---|
| Fleet | Fleet dashboard, Versions, Deprecated APIs |
| Insights | Usage vs requests, Image inventory, Change timeline, Compare clusters |
| Topology | Cluster, Zones & pod spread, Namespace, Helm, Kapp, Istio |
| Health | Warnings, Time machine |
| Security | Security overview, Cloud identities, Certificates, RBAC |
| Kyverno | Cluster policies, Policies, Validating and Mutating policies, Policy exceptions, Policy reports, Cluster policy reports, Cleanup policies |
| Platform | Add-ons, Gateway API, autoscalers, Helm, Gateway API and Istio resources |

Every Fleet and Insights table has **Copy Markdown**, **Save CSV** and **Save .md**.

## Resource tabs

Open any resource for **Overview**, **YAML** (secret values are never sent), **Why?** (plain-words
diagnosis for pods, nodes, claims, services and workloads), **Related** (what it uses and what uses it),
**Impact** (what stops if it goes away), **Logs** (pods) and **Events**.

## Trends and the time machine

For the clusters you look at (up to 10 at a time) Kendra takes a small sample once a minute and keeps
six hours of them in memory. Nothing is written to disk and nothing to a cluster. Trends show after a few
samples; the history is lost when you quit.

## Read-only

There is no edit, delete, scale, restart or cordon. The only thing Kendra records, in
`~/.kendra/audit.log`, is a port forward you start.

## Filter syntax

| Type | To see |
|---|---|
| `api` | anything whose name, namespace, cluster or columns contain "api" |
| `!ok` | only warning or failing rows |
| `ns:shop` · `cluster:prod` | namespace / cluster contains it |
| `status:crash\|error` | the Status column contains "crash" or "error" |
| `restarts>3` · `age<1h` | numeric column / created within the last hour |
| `-kube-system` | hide anything containing it |
