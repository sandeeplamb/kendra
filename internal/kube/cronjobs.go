package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobRun is one Job: what state it ended in or is in, and for a failed one the pod that explains it.
type JobRun struct {
	Name      string `json:"name"`
	State     string `json:"state"` // succeeded | failed | running | pending
	Reason    string `json:"reason,omitempty"`
	Started   string `json:"started,omitempty"`  // RFC 3339
	Finished  string `json:"finished,omitempty"` // RFC 3339
	Pod       string `json:"pod,omitempty"`      // a failed pod of this job
	PodReason string `json:"podReason,omitempty"`
}

// CronJobInfo is the health of one CronJob.
type CronJobInfo struct {
	Namespace    string   `json:"namespace"`
	Name         string   `json:"name"`
	Schedule     string   `json:"schedule"`
	TimeZone     string   `json:"timeZone,omitempty"`
	Suspended    bool     `json:"suspended"`
	Active       int      `json:"active"`
	LastSchedule string   `json:"lastSchedule,omitempty"`
	LastSuccess  string   `json:"lastSuccess,omitempty"`
	NextRun      string   `json:"nextRun,omitempty"`
	Last         *JobRun  `json:"last,omitempty"`
	FailedStreak int      `json:"failedStreak"`
	Level        string   `json:"level"` // ok | warn | bad
	Notes        []string `json:"notes,omitempty"`
}

// LooseJob is a Job that no CronJob owns and that failed or has been running for over a day.
type LooseJob struct {
	Namespace string `json:"namespace"`
	JobRun
}

// CronReport is what one cluster reports for the Jobs and CronJobs page.
type CronReport struct {
	CronJobs  []CronJobInfo `json:"cronJobs"`
	LooseJobs []LooseJob    `json:"looseJobs"`
}

func jobState(j *batchv1.Job) (state, reason string) {
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobFailed:
			return "failed", c.Reason
		case batchv1.JobComplete, batchv1.JobSuccessCriteriaMet:
			return "succeeded", ""
		}
	}
	if j.Status.Active > 0 {
		return "running", ""
	}
	return "pending", ""
}

func jobRun(j *batchv1.Job) JobRun {
	st, why := jobState(j)
	r := JobRun{Name: j.Name, State: st, Reason: why}
	if j.Status.StartTime != nil {
		r.Started = at(*j.Status.StartTime)
	} else {
		r.Started = at(j.CreationTimestamp)
	}
	if j.Status.CompletionTime != nil {
		r.Finished = at(*j.Status.CompletionTime)
	} else {
		for _, c := range j.Status.Conditions {
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue && !c.LastTransitionTime.IsZero() {
				r.Finished = at(c.LastTransitionTime)
			}
		}
	}
	return r
}

// pickFailedPod chooses the pod that best explains a failed job and says why it failed.
func pickFailedPod(pods []corev1.Pod) (name, reason string) {
	for _, p := range pods {
		for _, cs := range append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
			switch {
			case cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0:
				t := cs.State.Terminated
				return p.Name, fmt.Sprintf("%s (exit %d)", firstNonEmpty(t.Reason, "Error"), t.ExitCode)
			case cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing":
				return p.Name, cs.State.Waiting.Reason
			}
		}
	}
	for _, p := range pods {
		if p.Status.Phase == corev1.PodFailed {
			return p.Name, firstNonEmpty(p.Status.Reason, "Failed")
		}
	}
	if len(pods) > 0 {
		return pods[0].Name, ""
	}
	return "", ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func humanDur(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	return shortDur(d)
}

func (c *Cluster) failedPod(ctx context.Context, ns, job string) (string, string) {
	pl, err := c.Cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + job})
	if err != nil {
		return "", ""
	}
	return pickFailedPod(pl.Items)
}

// CronJobs reports the health of every CronJob (schedule, next run, last success, last Job, a failed
// streak, suspended, missed or stuck runs) and lists Jobs that no CronJob owns and that failed or have
// run for over a day. The next run is worked out from the schedule; the time zone is the CronJob's
// timeZone, or UTC when it has none. Read-only: list calls only.
func (c *Cluster) CronJobs(ctx context.Context) (*CronReport, error) {
	cl, err := c.Cs.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	jl, err := c.Cs.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	byCron := map[string][]*batchv1.Job{} // ns/name of the CronJob
	rep := &CronReport{CronJobs: []CronJobInfo{}, LooseJobs: []LooseJob{}}
	for i := range jl.Items {
		j := &jl.Items[i]
		owned := false
		for _, o := range j.OwnerReferences {
			if o.Kind == "CronJob" {
				byCron[j.Namespace+"/"+o.Name] = append(byCron[j.Namespace+"/"+o.Name], j)
				owned = true
			}
		}
		if owned {
			continue
		}
		r := jobRun(j)
		started, _ := time.Parse(time.RFC3339, r.Started)
		if r.State == "failed" || (r.State == "running" && now().Sub(started) > 24*time.Hour) {
			if len(rep.LooseJobs) < 200 {
				rep.LooseJobs = append(rep.LooseJobs, LooseJob{j.Namespace, r})
			}
		}
	}
	lookups := 0
	for _, cj := range cl.Items {
		info := CronJobInfo{Namespace: cj.Namespace, Name: cj.Name, Schedule: cj.Spec.Schedule, Level: "ok", Active: len(cj.Status.Active)}
		if cj.Spec.TimeZone != nil {
			info.TimeZone = *cj.Spec.TimeZone
		}
		info.Suspended = cj.Spec.Suspend != nil && *cj.Spec.Suspend
		if cj.Status.LastScheduleTime != nil {
			info.LastSchedule = at(*cj.Status.LastScheduleTime)
		}
		if cj.Status.LastSuccessfulTime != nil {
			info.LastSuccess = at(*cj.Status.LastSuccessfulTime)
		}
		note := func(level, s string) {
			info.Level = worse(info.Level, level)
			info.Notes = append(info.Notes, s)
		}

		jobs := byCron[cj.Namespace+"/"+cj.Name]
		sort.Slice(jobs, func(a, b int) bool { return jobs[a].CreationTimestamp.After(jobs[b].CreationTimestamp.Time) })
		for i, j := range jobs {
			r := jobRun(j)
			if i == 0 {
				info.Last = &r
			}
			if r.State == "failed" && info.FailedStreak == i {
				info.FailedStreak++
			}
		}

		// next run and the gap between runs
		var interval time.Duration
		if sp, err := parseCron(cj.Spec.Schedule); err != nil {
			note("warn", "schedule not understood: "+err.Error())
		} else {
			loc := time.UTC
			if info.TimeZone != "" {
				if l, err := time.LoadLocation(info.TimeZone); err != nil {
					note("warn", "unknown time zone "+info.TimeZone)
				} else {
					loc = l
				}
			}
			if n1 := sp.next(now().In(loc)); !n1.IsZero() {
				if n2 := sp.next(n1); !n2.IsZero() {
					interval = n2.Sub(n1)
				}
				if !info.Suspended {
					info.NextRun = n1.UTC().Format(time.RFC3339)
				}
			} else if !info.Suspended {
				note("warn", "the schedule never runs")
			}
		}

		if info.Suspended {
			note("warn", "suspended")
		}
		if info.Last != nil && info.Last.State == "failed" {
			msg := "last run failed"
			if info.Last.Reason != "" {
				msg += ": " + info.Last.Reason
			}
			if info.FailedStreak > 1 {
				msg += fmt.Sprintf(" (%d in a row)", info.FailedStreak)
			}
			note("bad", msg)
			if lookups < 40 {
				lookups++
				info.Last.Pod, info.Last.PodReason = c.failedPod(ctx, cj.Namespace, info.Last.Name)
			}
		} else if info.LastSuccess == "" && len(jobs) > 0 && info.Last.State != "running" {
			note("warn", "has never succeeded")
		}
		if !info.Suspended && interval > 0 {
			switch {
			case info.LastSchedule != "":
				if t, err := time.Parse(time.RFC3339, info.LastSchedule); err == nil && now().Sub(t) > 2*interval+5*time.Minute {
					note("warn", "not scheduled for "+humanDur(now().Sub(t))+" (runs about every "+humanDur(interval)+")")
				}
			case now().Sub(cj.CreationTimestamp.Time) > 2*interval+5*time.Minute:
				note("warn", "never scheduled")
			}
		}
		for _, j := range jobs {
			if st, _ := jobState(j); st != "running" || j.Status.StartTime == nil {
				continue
			}
			limit := max(time.Hour, 2*interval)
			if run := now().Sub(j.Status.StartTime.Time); run > limit {
				note("warn", "job "+j.Name+" has been running for "+humanDur(run))
			}
		}
		rep.CronJobs = append(rep.CronJobs, info)
	}
	rank := map[string]int{"bad": 0, "warn": 1, "ok": 2}
	sort.SliceStable(rep.CronJobs, func(i, j int) bool {
		a, b := rep.CronJobs[i], rep.CronJobs[j]
		if rank[a.Level] != rank[b.Level] {
			return rank[a.Level] < rank[b.Level]
		}
		return strings.ToLower(a.Namespace+"/"+a.Name) < strings.ToLower(b.Namespace+"/"+b.Name)
	})
	return rep, nil
}
