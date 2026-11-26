package kube

import (
	"testing"
	"time"
)

func entryFor(t *testing.T, id string) *compatEntry {
	t.Helper()
	db, err := loadCompat()
	if err != nil {
		t.Fatal(err)
	}
	for i := range db.Addons {
		if db.Addons[i].ID == id {
			return &db.Addons[i]
		}
	}
	t.Fatalf("no data for %s", id)
	return nil
}

func TestCompatJudge(t *testing.T) {
	cm, ms, keda := entryFor(t, "cert-manager"), entryFor(t, "metrics-server"), entryFor(t, "keda")
	cases := []struct {
		name   string
		e      *compatEntry
		series string
		k8s    int
		want   string
	}{
		{"supported and inside", cm, "1.21", 34, "ok"},
		{"supported, cluster too old", cm, "1.21", 31, "bad"},
		{"supported, cluster too new", cm, "1.20", 36, "bad"},
		{"series older than any listed, policy says unsupported", cm, "1.15", 31, "bad"},
		{"series newer than data file", cm, "1.30", 34, "unclear"},
		{"open upper bound", ms, "0.8", 40, "ok"},
		{"open bound, below minimum", ms, "0.8", 30, "bad"},
		{"older series without data is not bad", ms, "0.6", 31, "unclear"},
		{"tested window, inside", keda, "2.19", 33, "ok"},
		{"tested window, outside is not a no", keda, "2.19", 31, "unclear"},
		{"no row between listed series", keda, "2.18", 33, "unclear"},
		{"unknown cluster version", cm, "1.21", 0, "unclear"},
	}
	for _, c := range cases {
		if got, why := c.e.judge(c.series, c.k8s); got != c.want {
			t.Errorf("%s: %s %d = %s (%s), want %s", c.name, c.series, c.k8s, got, why, c.want)
		}
	}
}

func TestCompatDataHasSources(t *testing.T) {
	db, err := loadCompat()
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Addons) == 0 || db.VerifiedOn == "" {
		t.Fatalf("empty data file: %+v", db)
	}
	for _, e := range db.Addons {
		if e.Source == "" || len(e.Releases) == 0 || (e.Basis != "supported" && e.Basis != "tested") {
			t.Errorf("entry %s is incomplete: %+v", e.ID, e)
		}
	}
}

func TestCompatAgainstFakeCluster(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	rep, err := c.Compat(ctx(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	// the fake API reports v1.31.3-eks-abc
	if rep.Minor != 31 || rep.Target != 32 || rep.Platform != "EKS" || rep.PlatformNow == nil || rep.PlatformTarget == nil || rep.PlatformSource == "" {
		t.Errorf("report = %+v", rep)
	}
	var cm *CompatRow
	for i := range rep.Rows {
		if rep.Rows[i].Name == "cert-manager" {
			cm = &rep.Rows[i]
		}
	}
	// cert-manager v1.15.1 is older than every supported release
	if cm == nil || cm.Version != "v1.15.1" || cm.Current != "bad" || cm.Target != "bad" || cm.Source == "" {
		t.Errorf("cert-manager = %+v", cm)
	}
	rep2, err := c.Compat(ctx(t), 35)
	if err != nil || rep2.Target != 35 {
		t.Errorf("explicit target: %+v, %v", rep2, err)
	}
}

func freezeNow(t *testing.T, day string) {
	t.Helper()
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	old := now
	now = func() time.Time { return d.Add(12 * time.Hour) }
	t.Cleanup(func() { now = old })
}

func TestCompatEndedRetiredAndListedBounds(t *testing.T) {
	freezeNow(t, "2026-11-26")
	istio, eso, karp, ing, dd := entryFor(t, "istio"), entryFor(t, "external-secrets"), entryFor(t, "karpenter"), entryFor(t, "ingress-nginx"), entryFor(t, "datadog-operator")
	cases := []struct {
		name   string
		e      *compatEntry
		series string
		k8s    int
		want   string
	}{
		{"istio supported release inside range", istio, "1.31", 34, "ok"},
		{"istio release past its end date", istio, "1.29", 33, "bad"},
		{"istio release older than any listed", istio, "1.20", 33, "bad"},
		{"eso release past its end date", eso, "2.14", 37, "bad"},
		{"eso current release, tested version", eso, "2.15", 37, "ok"},
		{"eso current release, other version is not a no", eso, "2.15", 34, "unclear"},
		{"karpenter regular release that was superseded", karp, "1.12", 34, "bad"},
		{"karpenter LTS inside range", karp, "1.14", 36, "ok"},
		{"karpenter LTS, cluster newer than its row", karp, "1.14", 37, "bad"},
		{"karpenter cluster below the source table", karp, "1.14", 29, "unclear"},
		{"karpenter cluster above the source table", karp, "1.15", 38, "unclear"},
		{"retired project is never ok", ing, "1.15", 33, "bad"},
		{"datadog any release, inside tested range", dd, "1.20", 33, "ok"},
		{"datadog below tested range", dd, "1.20", 18, "unclear"},
	}
	for _, c := range cases {
		if got, why := c.e.judge(c.series, c.k8s); got != c.want {
			t.Errorf("%s: %s on %d = %s (%s), want %s", c.name, c.series, c.k8s, got, why, c.want)
		}
	}
}

func TestPlatformSupport(t *testing.T) {
	freezeNow(t, "2026-11-26")
	db, _ := loadCompat()
	eks, aks := db.Platforms["EKS"], db.Platforms["AKS"]
	cases := []struct {
		name  string
		p     platformData
		k8s   int
		phase string
	}{
		{"eks 1.31 extended support ends today, still covered", eks, 31, "extended"},
		{"eks 1.30 not in the table", eks, 30, "unknown"},
		{"eks 1.33 standard support over, in extended", eks, 33, "extended"},
		{"eks 1.34 in standard", eks, 34, "standard"},
		{"eks 1.35 in standard", eks, 35, "standard"},
		{"aks 1.31 only LTS is left", aks, 31, "extended"},
		{"aks 1.30 ended", aks, 30, "ended"},
		{"aks 1.33 ended with no LTS date", aks, 33, "ended"},
		{"aks 1.35 in standard", aks, 35, "standard"},
	}
	for _, c := range cases {
		got := c.p.support(c.k8s)
		if got == nil || got.Phase != c.phase {
			t.Errorf("%s: %+v, want phase %s", c.name, got, c.phase)
		}
	}
	freezeNow(t, "2026-11-27")
	if got := eks.support(31); got.Phase != "ended" || got.Level != "bad" {
		t.Errorf("eks 1.31 the day after = %+v", got)
	}
}
