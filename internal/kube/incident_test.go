package kube

import (
	"strings"
	"testing"
)

func TestIncidentBundleForDeployment(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	b, err := c.IncidentBundle(ctx(t), "fake-prod", "deployments", "shop", "web")
	if err != nil {
		t.Fatal(err)
	}
	md := b.Markdown
	for _, want := range []string{
		"# Incident bundle: Deployment web in shop",
		"- Cluster: `fake-prod` (EKS v1.31.3-eks-abc)",
		"## What is wrong",
		"## Pods",
		"web-abc-2", // the crash-looping pod behind the deployment
		"CrashLoopBackOff",
		"## What it uses and what uses it",
		"db-creds", // missing Secret the deployment refers to
		"kubectl --context fake-prod -n shop rollout status deployment/web",
		"Logs, YAML and Secret values are not included",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("bundle lacks %q:\n%s", want, md)
		}
	}
	if b.Title != "Deployment web in shop" {
		t.Errorf("title = %q", b.Title)
	}
}

func TestIncidentBundleForPodAndNode(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	p, err := c.IncidentBundle(ctx(t), "fake-prod", "pods", "shop", "web-abc-2")
	if err != nil || !strings.Contains(p.Markdown, "logs web-abc-2 --all-containers --previous") || !strings.Contains(p.Markdown, "CrashLoopBackOff") {
		t.Errorf("pod bundle = %v, %v", p, err)
	}
	n, err := c.IncidentBundle(ctx(t), "fake-prod", "nodes", "", "node-b")
	if err != nil || !strings.Contains(n.Markdown, "Node `node-b`") || strings.Contains(n.Markdown, "-n ") {
		t.Errorf("node bundle = %v, %v", n, err)
	}
}

func TestIncidentBundleRejectsOtherKinds(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	if _, err := c.IncidentBundle(ctx(t), "fake-prod", "secrets", "shop", "db-creds"); err == nil {
		t.Error("a Secret must not get a bundle")
	}
}

func TestMarkdownTableEscapesPipes(t *testing.T) {
	var b strings.Builder
	mdTable(&b, []string{"A"}, [][]string{{"x|y\nz"}})
	if !strings.Contains(b.String(), "| x\\|y z |") {
		t.Errorf("table = %q", b.String())
	}
}
