package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sandeeplamb/kendra/internal/kube"
)

func TestHistoryKeepsSixHours(t *testing.T) {
	h := newHistory()
	for i := 0; i < maxSamples+25; i++ {
		h.add("a", kube.Sample{Pods: i})
	}
	l := h.get("a")
	if len(l) != maxSamples || l[0].Pods != 25 || l[len(l)-1].Pods != maxSamples+24 {
		t.Errorf("len %d first %d last %d", len(l), l[0].Pods, l[len(l)-1].Pods)
	}
}

func TestWatchIsCappedAndExpires(t *testing.T) {
	h := newHistory()
	now := time.Now()
	var names []string
	for i := 0; i < 14; i++ {
		names = append(names, fmt.Sprintf("c%02d", i))
	}
	if got := h.setWatch(names, now); len(got) != maxWatched {
		t.Errorf("watching %d clusters, want at most %d", len(got), maxWatched)
	}
	// 20 minutes later every watch has expired; only the new one stays
	if got := h.setWatch([]string{"fresh"}, now.Add(20*time.Minute)); len(got) != 1 || got[0] != "fresh" {
		t.Errorf("after expiry = %v", got)
	}
}

func TestDueSkipsRecentAndInFlight(t *testing.T) {
	h := newHistory()
	now := time.Now()
	h.setWatch([]string{"a", "b"}, now)
	h.add("a", kube.Sample{T: now.Add(-10 * time.Second)})
	due := h.due(50*time.Second, now)
	if len(due) != 1 || due[0] != "b" {
		t.Fatalf("due = %v (a was sampled 10s ago)", due)
	}
	if again := h.due(50*time.Second, now); len(again) != 0 {
		t.Errorf("b is in flight, due = %v", again)
	}
	h.done("b")
	if again := h.due(50*time.Second, now); len(again) != 1 {
		t.Errorf("after done, due = %v", again)
	}
}

func TestWatchHistoryTimemachineEndpoints(t *testing.T) {
	ts, s := newTestServer(t)
	h := map[string]string{"X-Knd": "1"}
	if r, _ := do(t, ts, "POST", "/api/watch", "", true, nil, `{"contexts":["c1"]}`); r.StatusCode != 403 {
		t.Errorf("watch without X-Knd: %d, want 403", r.StatusCode)
	}
	if r, b := do(t, ts, "POST", "/api/watch", "", true, h, `{"contexts":["c1","nope"]}`); r.StatusCode != 200 || !strings.Contains(b, `"c1"`) || strings.Contains(b, "nope") {
		t.Errorf("watch = %d %s", r.StatusCode, b)
	}
	s.hist.add("c1", kube.Sample{T: time.Unix(1000, 0), Pods: 3, Problems: []kube.Problem{{Kind: "Pod", Name: "x", Level: "bad", Reason: "CrashLoopBackOff"}}})
	s.hist.add("c1", kube.Sample{T: time.Unix(1060, 0), Pods: 4})
	if _, b := do(t, ts, "GET", "/api/history?context=c1", "", true, nil, ""); !strings.Contains(b, `"pods":3`) || strings.Contains(b, "CrashLoopBackOff") {
		t.Errorf("history must carry numbers only: %s", b)
	}
	if _, b := do(t, ts, "GET", "/api/timemachine?context=c1&at=1010", "", true, nil, ""); !strings.Contains(b, "CrashLoopBackOff") || !strings.Contains(b, "1060") {
		t.Errorf("timemachine = %s", b)
	}
}
