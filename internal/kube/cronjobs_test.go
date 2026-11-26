package kube

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func cronReport(t *testing.T) map[string]CronJobInfo {
	t.Helper()
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	r, err := c.CronJobs(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]CronJobInfo{}
	for _, x := range r.CronJobs {
		by[x.Namespace+"/"+x.Name] = x
	}
	return by
}

func hasNote(i CronJobInfo, sub string) bool {
	for _, n := range i.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func TestCronJobsFailedSuspendedHealthy(t *testing.T) {
	by := cronReport(t)
	b := by["batch/backup"]
	if b.Level != "bad" || b.Last == nil || b.Last.Name != "backup-2" || b.Last.State != "failed" || b.FailedStreak != 1 || !hasNote(b, "last run failed: BackoffLimitExceeded") || b.NextRun == "" {
		t.Errorf("backup = %+v", b)
	}
	if b.LastSuccess == "" {
		t.Errorf("backup lost its last success: %+v", b)
	}
	r := by["batch/report"]
	if r.Level != "warn" || !r.Suspended || !hasNote(r, "suspended") || r.NextRun != "" {
		t.Errorf("report = %+v", r)
	}
	h := by["batch/healthy"]
	if h.Level != "ok" || len(h.Notes) != 0 || h.Last == nil || h.Last.State != "succeeded" || h.NextRun == "" {
		t.Errorf("healthy = %+v", h)
	}
	if next, _ := time.Parse(time.RFC3339, h.NextRun); !next.After(time.Now()) || next.Sub(time.Now()) > 31*time.Minute {
		t.Errorf("healthy next run %s is not within 30 minutes", h.NextRun)
	}
	// the failed ones sort first
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	rep, _ := c.CronJobs(ctx(t))
	if rep.CronJobs[0].Name != "backup" {
		t.Errorf("order = %+v", rep.CronJobs)
	}
}

func TestCronJobsLooseJobs(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	rep, err := c.CronJobs(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, j := range rep.LooseJobs {
		got[j.Namespace+"/"+j.Name] = j.State
	}
	if got["tools/migrate"] != "failed" || got["tools/stuck-import"] != "running" || len(got) != 2 {
		t.Errorf("loose jobs = %+v (CronJob-owned jobs must not appear)", got)
	}
}

func TestJobStateAndFailedPod(t *testing.T) {
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "j-a"}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}}}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "j-b"}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}}}}}},
	}
	if n, why := pickFailedPod(pods); n != "j-b" || why != "OOMKilled (exit 137)" {
		t.Errorf("pick = %s %s", n, why)
	}
	waiting := []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "j-c"}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}}}}
	if n, why := pickFailedPod(waiting); n != "j-c" || why != "ImagePullBackOff" {
		t.Errorf("pick = %s %s", n, why)
	}
	if n, _ := pickFailedPod(nil); n != "" {
		t.Errorf("no pods must give no pod, got %q", n)
	}
}

func TestCronJobNotScheduledForAWhile(t *testing.T) {
	old := now
	now = func() time.Time { return time.Now().Add(72 * time.Hour) } // three days pass with no new run
	t.Cleanup(func() { now = old })
	by := cronReport(t)
	h := by["batch/healthy"]
	if h.Level != "warn" || !hasNote(h, "not scheduled for") || !hasNote(h, "runs about every 30m") {
		t.Errorf("healthy = %+v", h)
	}
	if b := by["batch/backup"]; !hasNote(b, "not scheduled for") {
		t.Errorf("backup = %+v", b)
	}
}
