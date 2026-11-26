package kube

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed compat/compat.json
var compatJSON []byte

// now is replaced in tests.
var now = time.Now

type compatRelease struct {
	Series  string `json:"series"` // "1.21", or "*" for any release
	K8sMin  int    `json:"k8sMin"`
	K8sMax  int    `json:"k8sMax"` // 0 = no published upper bound
	Eol     string `json:"eol,omitempty"`
	EolDate string `json:"eolDate,omitempty"` // YYYY-MM-DD; once passed, the release is out of support
	Ended   bool   `json:"ended,omitempty"`   // out of support, no date published
	Note    string `json:"note,omitempty"`
}

type compatEntry struct {
	ID          string          `json:"id"`
	Basis       string          `json:"basis"`       // supported | tested
	OlderStatus string          `json:"olderStatus"` // status of a series older than all listed
	ListedFrom  int             `json:"listedFrom"`  // the source table starts at this Kubernetes minor (0 = no limit)
	ListedTo    int             `json:"listedTo"`
	Retired     string          `json:"retired,omitempty"`
	Source      string          `json:"source"`
	Latest      string          `json:"latest"`
	Releases    []compatRelease `json:"releases"`
}

type platformVersion struct {
	K8s         int    `json:"k8s"`
	StandardEnd string `json:"standardEnd"`
	ExtendedEnd string `json:"extendedEnd,omitempty"`
}

type platformData struct {
	Source        string            `json:"source"`
	ExtendedLabel string            `json:"extendedLabel"`
	Versions      []platformVersion `json:"versions"`
}

type compatData struct {
	VerifiedOn string                  `json:"verifiedOn"`
	Addons     []compatEntry           `json:"addons"`
	Platforms  map[string]platformData `json:"platforms"`
}

var (
	compatOnce sync.Once
	compatDB   compatData
	compatErr  error
)

func loadCompat() (*compatData, error) {
	compatOnce.Do(func() { compatErr = json.Unmarshal(compatJSON, &compatDB) })
	return &compatDB, compatErr
}

// CompatRow is one add-on judged against the cluster's current and a target Kubernetes minor.
// Status values: ok (an official source covers it), bad (outside the supported window or out of
// support), unclear (no authoritative statement). Missing evidence is never "ok".
type CompatRow struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Latest    string `json:"latest,omitempty"` // latest release series known to the data file
	Current   string `json:"current"`          // status against the cluster's minor
	Target    string `json:"target"`           // status against the target minor
	Eol       string `json:"eol,omitempty"`
	Note      string `json:"note"`
	Source    string `json:"source,omitempty"`
}

// PlatformSupport is where a cluster's own Kubernetes version stands in the EKS or AKS calendar.
type PlatformSupport struct {
	Version string `json:"version"` // "1.31"
	Phase   string `json:"phase"`   // standard | extended | ended | unknown
	Ends    string `json:"ends,omitempty"`
	Note    string `json:"note"`
	Level   string `json:"level"` // ok | warn | bad
}

// CompatReport is the matrix of one cluster.
type CompatReport struct {
	Version        string           `json:"version"`
	Minor          int              `json:"minor"`
	Target         int              `json:"target"`
	Platform       string           `json:"platform"`
	PlatformNow    *PlatformSupport `json:"platformNow,omitempty"`
	PlatformTarget *PlatformSupport `json:"platformTarget,omitempty"`
	PlatformSource string           `json:"platformSource,omitempty"`
	VerifiedOn     string           `json:"verifiedOn"`
	Stale          bool             `json:"stale"` // the data file is older than 90 days
	Rows           []CompatRow      `json:"rows"`
}

var seriesRe = regexp.MustCompile(`^v?(\d+)\.(\d+)`)

func parseSeries(v string) (major, minor int, ok bool) {
	m := seriesRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

func cmpSeries(a, b string) int {
	am, an, _ := parseSeries(a)
	bm, bn, _ := parseSeries(b)
	if am != bm {
		return am - bm
	}
	return an - bn
}

func addonID(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "-"))
}

func (e *compatEntry) find(series string) *compatRelease {
	for i := range e.Releases {
		if e.Releases[i].Series == series {
			return &e.Releases[i]
		}
	}
	for i := range e.Releases {
		if e.Releases[i].Series == "*" {
			return &e.Releases[i]
		}
	}
	return nil
}

func (r *compatRelease) ended() (bool, string) {
	if r.Ended {
		return true, "release " + r.Series + " is out of support (" + r.Eol + ")"
	}
	if r.EolDate != "" {
		if t, err := time.Parse("2006-01-02", r.EolDate); err == nil && !now().Before(t.Add(24*time.Hour)) {
			return true, "release " + r.Series + " went out of support on " + r.EolDate
		}
	}
	return false, ""
}

func minorRange(r *compatRelease) string {
	if r.K8sMax == 0 {
		return fmt.Sprintf("1.%d and newer", r.K8sMin)
	}
	if r.K8sMin == r.K8sMax {
		return fmt.Sprintf("1.%d", r.K8sMin)
	}
	return fmt.Sprintf("1.%d to 1.%d", r.K8sMin, r.K8sMax)
}

// judge returns the status of one release series against a Kubernetes minor, and a reason.
func (e *compatEntry) judge(series string, k8s int) (string, string) {
	if k8s <= 0 {
		return "unclear", "cluster version unknown"
	}
	r := e.find(series)
	if r == nil {
		oldest, newest := e.Releases[0].Series, e.Releases[0].Series
		for _, x := range e.Releases {
			if cmpSeries(x.Series, oldest) < 0 {
				oldest = x.Series
			}
			if cmpSeries(x.Series, newest) > 0 {
				newest = x.Series
			}
		}
		switch {
		case cmpSeries(series, oldest) < 0:
			if e.OlderStatus == "bad" {
				return "bad", "release " + series + " is older than the supported releases (" + oldest + " and newer)"
			}
			return "unclear", "release " + series + " is older than the rows in the data file (" + oldest + " and newer)"
		case cmpSeries(series, newest) > 0:
			return "unclear", "release " + series + " is newer than the data file"
		}
		return "unclear", "no row for release " + series + " in the data file"
	}
	if end, why := r.ended(); end {
		return "bad", why
	}
	if e.Retired != "" {
		return "bad", e.Retired
	}
	if (e.ListedFrom > 0 && k8s < e.ListedFrom) || (e.ListedTo > 0 && k8s > e.ListedTo) {
		return "unclear", fmt.Sprintf("the source table covers Kubernetes 1.%d to 1.%d only", e.ListedFrom, e.ListedTo)
	}
	rng := minorRange(r)
	in := k8s >= r.K8sMin && (r.K8sMax == 0 || k8s <= r.K8sMax)
	var res, why string
	switch {
	case in && e.Basis == "supported":
		res, why = "ok", "supports Kubernetes "+rng
	case in:
		res, why = "ok", "tested on Kubernetes "+rng
	case e.Basis == "supported":
		res, why = "bad", "supports Kubernetes "+rng+" only"
	default:
		res, why = "unclear", "tested on Kubernetes "+rng+", not on 1."+strconv.Itoa(k8s)
	}
	if r.Note != "" {
		why += ". " + r.Note
	}
	return res, why
}

func (p *platformData) support(k8s int) *PlatformSupport {
	if p == nil || k8s <= 0 {
		return nil
	}
	ps := &PlatformSupport{Version: "1." + strconv.Itoa(k8s), Phase: "unknown", Level: "warn", Note: "no date in the data file for this version"}
	day := func(s string) (time.Time, bool) {
		t, err := time.Parse("2006-01-02", s)
		return t.Add(24 * time.Hour), err == nil // supported through the whole end day
	}
	for _, v := range p.Versions {
		if v.K8s != k8s {
			continue
		}
		std, ok := day(v.StandardEnd)
		if !ok {
			return ps
		}
		switch {
		case now().Before(std):
			ps.Phase, ps.Ends, ps.Level, ps.Note = "standard", v.StandardEnd, "ok", "standard support until "+v.StandardEnd
			if std.Sub(now()) < 90*24*time.Hour {
				ps.Level = "warn"
				ps.Note += " (under 90 days left)"
			}
		case v.ExtendedEnd != "":
			if ext, ok := day(v.ExtendedEnd); ok && now().Before(ext) {
				ps.Phase, ps.Ends, ps.Level, ps.Note = "extended", v.ExtendedEnd, "warn", "standard support ended "+v.StandardEnd+"; "+p.ExtendedLabel+" until "+v.ExtendedEnd
				return ps
			}
			fallthrough
		default:
			ps.Phase, ps.Ends, ps.Level, ps.Note = "ended", v.StandardEnd, "bad", "support ended "+v.StandardEnd
			if v.ExtendedEnd != "" {
				ps.Ends = v.ExtendedEnd
				ps.Note = "support ended " + v.ExtendedEnd
			}
		}
		return ps
	}
	return ps
}

// Compat judges every add-on found by Addons against the cluster's Kubernetes minor and a target
// minor (0 = the next one), and places the cluster's own version in the EKS or AKS calendar.
// Reads the version endpoint, a few nodes and the add-on workloads. Read-only.
func (c *Cluster) Compat(ctx context.Context, target int) (*CompatReport, error) {
	db, err := loadCompat()
	if err != nil {
		return nil, err
	}
	sv, err := c.Cs.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}
	rep := &CompatReport{Version: sv.GitVersion, Minor: minorOf(sv.GitVersion), VerifiedOn: db.VerifiedOn, Rows: []CompatRow{}}
	rep.Target = target
	if rep.Target == 0 && rep.Minor > 0 {
		rep.Target = rep.Minor + 1
	}
	if t, err := time.Parse("2006-01-02", db.VerifiedOn); err != nil || now().Sub(t) > 90*24*time.Hour {
		rep.Stale = true
	}
	if nl, err := c.Cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 5}); err == nil {
		rep.Platform = platformOf(sv.GitVersion, nl.Items)
	} else {
		rep.Platform = platformOf(sv.GitVersion, nil)
	}
	if p, ok := db.Platforms[rep.Platform]; ok {
		rep.PlatformNow, rep.PlatformTarget, rep.PlatformSource = p.support(rep.Minor), p.support(rep.Target), p.Source
	}
	adds, err := c.Addons(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*compatEntry{}
	for i := range db.Addons {
		byID[db.Addons[i].ID] = &db.Addons[i]
	}
	for _, a := range adds {
		if a.Via != "workload" {
			continue
		}
		row := CompatRow{Name: a.Name, Version: a.Version, Namespace: a.Namespace}
		e := byID[addonID(a.Name)]
		maj, min, ok := parseSeries(a.Version)
		series := fmt.Sprintf("%d.%d", maj, min)
		switch {
		case e == nil:
			row.Current, row.Target, row.Note = "unclear", "unclear", "no compatibility data for this add-on yet"
		case !ok:
			row.Current, row.Target, row.Note = "unclear", "unclear", "version unknown (tag \""+a.Version+"\" is not a release number)"
			row.Latest, row.Source = e.Latest, e.Source
		default:
			cur, why := e.judge(series, rep.Minor)
			tgt, tw := e.judge(series, rep.Target)
			row.Current, row.Target, row.Note = cur, tgt, why
			if tgt != cur {
				row.Note = "now: " + why + "; target 1." + strconv.Itoa(rep.Target) + ": " + tw
			}
			if r := e.find(series); r != nil {
				row.Eol = r.Eol
				if row.Eol == "" {
					row.Eol = r.EolDate
				}
			}
			row.Latest, row.Source = e.Latest, e.Source
			if e.Latest != "" && cmpSeries(series, e.Latest) < 0 {
				row.Note += "; newer release " + e.Latest + " exists"
			}
		}
		rep.Rows = append(rep.Rows, row)
	}
	return rep, nil
}
