package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Helm keeps one Secret per release revision (type helm.sh/release.v1, label owner=helm)
// and stamps what it creates with the label app.kubernetes.io/managed-by=Helm and the
// annotations meta.helm.sh/release-name and meta.helm.sh/release-namespace. Kendra
// reads only the labels of the release Secrets (never their data, which holds the
// whole manifest and values) and finds the objects by those marks. Objects from charts
// that do not set the managed-by label are not found.
const (
	helmManagedBy   = "app.kubernetes.io/managed-by=Helm"
	helmAnnRelease  = "meta.helm.sh/release-name"
	helmAnnReleaseN = "meta.helm.sh/release-namespace"
	partialMeta     = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1,application/json"
)

type helmRev struct {
	Name, Namespace, Status string
	Revision                int
	Created                 string
}

// helmRevisions lists the release revisions from the labels of the owner=helm Secrets.
func (c *Cluster) helmRevisions(ctx context.Context, ns, name string) ([]helmRev, error) {
	sel := "owner=helm"
	if name != "" {
		sel += ",name=" + name
	}
	path := "/api/v1/secrets"
	if ns != "" {
		path = "/api/v1/namespaces/" + ns + "/secrets"
	}
	raw, err := c.Cs.CoreV1().RESTClient().Get().AbsPath(path).Param("labelSelector", sel).
		SetHeader("Accept", partialMeta).DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var l struct {
		Items []struct {
			Metadata struct {
				Namespace         string            `json:"namespace"`
				CreationTimestamp string            `json:"creationTimestamp"`
				Labels            map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, err
	}
	var out []helmRev
	for _, it := range l.Items {
		rev, _ := strconv.Atoi(it.Metadata.Labels["version"])
		out = append(out, helmRev{Name: it.Metadata.Labels["name"], Namespace: it.Metadata.Namespace,
			Status: it.Metadata.Labels["status"], Revision: rev, Created: it.Metadata.CreationTimestamp})
	}
	return out, nil
}

func helmHealth(status string, a *App) {
	switch {
	case status == "failed":
		a.Health = "bad"
	case status != "deployed" && status != "superseded" && status != "":
		if a.Health == "ok" || a.Health == "idle" {
			a.Health = "warn" // pending-install, pending-upgrade, uninstalling...
		}
	}
}

// chartOf reads the chart and app version from the labels charts put on their objects.
func chartOf(res []Res, a *App) {
	for _, r := range res {
		if a.Chart == "" {
			a.Chart = r.Labels["helm.sh/chart"]
		}
		if a.AppVersion == "" {
			a.AppVersion = r.Labels["app.kubernetes.io/version"]
		}
	}
}

func releaseOf(r Res) (ns, name string) {
	an := r.Obj.GetAnnotations()
	ns, name = an[helmAnnReleaseN], an[helmAnnRelease]
	if name == "" {
		name = r.Labels["app.kubernetes.io/instance"]
	}
	if ns == "" {
		ns = r.Namespace
	}
	return ns, name
}

// ListHelmReleases finds every Helm release in the cluster with a health summary of
// the objects it created.
func (c *Cluster) ListHelmReleases(ctx context.Context) ([]App, int, error) {
	revs, err := c.helmRevisions(ctx, "", "")
	if err != nil {
		return nil, 0, err
	}
	latest := map[string]helmRev{}
	for _, r := range revs {
		k := r.Namespace + "/" + r.Name
		if cur, ok := latest[k]; !ok || r.Revision > cur.Revision {
			latest[k] = r
		}
	}
	res, skipped := c.collect(ctx, helmManagedBy)
	by := map[string][]Res{}
	for _, r := range res {
		ns, name := releaseOf(r)
		by[ns+"/"+name] = append(by[ns+"/"+name], r)
	}
	apps := []App{}
	for k, r := range latest {
		a := App{Cluster: c.Name, Name: r.Name, Namespace: r.Namespace, LabelValue: k, Created: r.Created, Status: r.Status, Revision: r.Revision}
		fillApp(&a, by[k])
		chartOf(by[k], &a)
		helmHealth(r.Status, &a)
		a.LastChange = &Change{Name: fmt.Sprintf("revision %d", r.Revision), StartedAt: r.Created,
			Successful: r.Status == "deployed", FinishedAt: r.Created, Description: fmt.Sprintf("revision %d: %s", r.Revision, r.Status)}
		apps = append(apps, a)
	}
	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Namespace+"/"+apps[i].Name < apps[j].Namespace+"/"+apps[j].Name
	})
	return apps, skipped, nil
}

// GetHelmRelease returns one release with its objects and revision history.
func (c *Cluster) GetHelmRelease(ctx context.Context, ns, name string) (*AppInfo, error) {
	revs, err := c.helmRevisions(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, fmt.Errorf("helm release %q not found in namespace %q", name, ns)
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].Revision > revs[j].Revision })
	all, skipped := c.collect(ctx, helmManagedBy)
	var res []Res
	for _, r := range all {
		if rns, rname := releaseOf(r); rns == ns && rname == name {
			res = append(res, r)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Kind != res[j].Kind {
			return res[i].Kind < res[j].Kind
		}
		return res[i].Namespace+res[i].Name < res[j].Namespace+res[j].Name
	})
	a := App{Cluster: c.Name, Name: name, Namespace: ns, LabelValue: ns + "/" + name, Created: revs[0].Created, Status: revs[0].Status, Revision: revs[0].Revision}
	fillApp(&a, res)
	chartOf(res, &a)
	helmHealth(revs[0].Status, &a)
	var changes []Change
	for _, r := range revs {
		changes = append(changes, Change{Name: fmt.Sprintf("revision %d", r.Revision), StartedAt: r.Created, FinishedAt: r.Created,
			Successful: r.Status == "deployed" || r.Status == "superseded", Description: fmt.Sprintf("revision %d: %s", r.Revision, r.Status)})
	}
	if len(changes) > 0 {
		a.LastChange = &changes[0]
	}
	state, _ := json.MarshalIndent(map[string]any{"release": name, "namespace": ns, "status": revs[0].Status, "revision": revs[0].Revision,
		"note": "Release values and manifest are stored in the release Secret. Kendra never reads them."}, "", "  ")
	return &AppInfo{App: a, Resources: res, Skipped: skipped, Changes: changes, State: string(state), Commands: []string{
		fmt.Sprintf("helm status %s -n %s --kube-context %s", name, ns, c.Name),
		fmt.Sprintf("helm history %s -n %s --kube-context %s", name, ns, c.Name),
		fmt.Sprintf("helm get values %s -n %s --kube-context %s", name, ns, c.Name),
		fmt.Sprintf("helm get manifest %s -n %s --kube-context %s", name, ns, c.Name),
	}}, nil
}
