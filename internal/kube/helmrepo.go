package kube

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// HelmChart is one chart version listed in a local Helm repository index.
type HelmChart struct {
	Repo        string `json:"repo"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion,omitempty"`
	Description string `json:"description,omitempty"`
	Created     string `json:"created,omitempty"`
	Deprecated  bool   `json:"deprecated,omitempty"`
}

// HelmRepo is a repository from `helm repo add`.
type HelmRepo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func helmDirs() (config, cache string) {
	home, _ := os.UserHomeDir()
	config, cache = os.Getenv("HELM_CONFIG_HOME"), os.Getenv("HELM_CACHE_HOME")
	switch runtime.GOOS {
	case "darwin":
		if config == "" {
			config = filepath.Join(home, "Library", "Preferences", "helm")
		}
		if cache == "" {
			cache = filepath.Join(home, "Library", "Caches", "helm")
		}
	case "windows":
		if config == "" {
			config = filepath.Join(os.Getenv("APPDATA"), "helm")
		}
		if cache == "" {
			cache = filepath.Join(os.Getenv("TEMP"), "helm")
		}
	default:
		if config == "" {
			config = filepath.Join(envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "helm")
		}
		if cache == "" {
			cache = filepath.Join(envOr("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "helm")
		}
	}
	return config, cache
}

// LocalHelmCharts reads the repositories you added with `helm repo add` and the index
// files `helm repo update` cached. It lists the newest few versions of every chart.
// Nothing is fetched from the network.
func LocalHelmCharts(perChart int) ([]HelmRepo, []HelmChart, error) {
	config, cache := helmDirs()
	raw, err := os.ReadFile(filepath.Join(config, "repositories.yaml"))
	if err != nil {
		return nil, nil, err
	}
	var rf struct {
		Repositories []HelmRepo `json:"repositories"`
	}
	if err := yaml.Unmarshal(raw, &rf); err != nil {
		return nil, nil, err
	}
	var charts []HelmChart
	for _, r := range rf.Repositories {
		b, err := os.ReadFile(filepath.Join(cache, "repository", r.Name+"-index.yaml"))
		if err != nil {
			continue // `helm repo update` has not run for this repository
		}
		var idx struct {
			Entries map[string][]struct {
				Version     string `json:"version"`
				AppVersion  string `json:"appVersion"`
				Description string `json:"description"`
				Created     string `json:"created"`
				Deprecated  bool   `json:"deprecated"`
			} `json:"entries"`
		}
		if yaml.Unmarshal(b, &idx) != nil {
			continue
		}
		for name, vs := range idx.Entries {
			sort.SliceStable(vs, func(i, j int) bool { return vs[i].Created > vs[j].Created })
			for i, v := range vs {
				if i >= perChart {
					break
				}
				charts = append(charts, HelmChart{Repo: r.Name, Name: name, Version: v.Version, AppVersion: v.AppVersion,
					Description: strings.TrimSpace(v.Description), Created: v.Created, Deprecated: v.Deprecated})
			}
		}
	}
	sort.Slice(charts, func(i, j int) bool {
		if charts[i].Name != charts[j].Name {
			return charts[i].Name < charts[j].Name
		}
		return charts[i].Repo+charts[i].Created > charts[j].Repo+charts[j].Created
	})
	return rf.Repositories, charts, nil
}
