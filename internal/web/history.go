package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sandeeplamb/kendra/internal/kube"
)

// History keeps a short series of cluster samples in memory, for sparklines and the
// time machine. Only clusters the UI says it is watching are sampled, at most
// maxWatched at a time, so a 50-cluster kubeconfig never means 50 polls a minute.
// Nothing is written to disk and nothing to a cluster.
const (
	maxSamples  = 360 // six hours at one sample a minute
	maxWatched  = 10
	watchTTL    = 15 * time.Minute
	sampleEvery = time.Minute
)

type history struct {
	mu       sync.Mutex
	series   map[string][]kube.Sample
	watch    map[string]time.Time // cluster -> watched until
	inflight map[string]bool
}

func newHistory() *history {
	return &history{series: map[string][]kube.Sample{}, watch: map[string]time.Time{}, inflight: map[string]bool{}}
}

// setWatch marks clusters as watched for the next watchTTL. When more than maxWatched
// are watched, the ones whose watch expires first are dropped.
func (h *history) setWatch(names []string, now time.Time) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for n, until := range h.watch {
		if until.Before(now) {
			delete(h.watch, n)
		}
	}
	for i, n := range names {
		if i >= maxWatched {
			break
		}
		h.watch[n] = now.Add(watchTTL)
	}
	for len(h.watch) > maxWatched {
		oldest, at := "", now.Add(time.Hour)
		for n, until := range h.watch {
			if until.Before(at) {
				oldest, at = n, until
			}
		}
		delete(h.watch, oldest)
	}
	out := make([]string, 0, len(h.watch))
	for n := range h.watch {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (h *history) add(name string, s kube.Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := append(h.series[name], s)
	if len(l) > maxSamples {
		l = append([]kube.Sample(nil), l[len(l)-maxSamples:]...)
	}
	h.series[name] = l
}

func (h *history) get(name string) []kube.Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]kube.Sample(nil), h.series[name]...)
}

// due returns the watched clusters that have no sample in the last `age` and are not
// being sampled right now, and marks them in flight.
func (h *history) due(age time.Duration, now time.Time) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for n, until := range h.watch {
		if until.Before(now) || h.inflight[n] {
			continue
		}
		if l := h.series[n]; len(l) > 0 && now.Sub(l[len(l)-1].T) < age {
			continue
		}
		h.inflight[n] = true
		out = append(out, n)
	}
	return out
}

func (h *history) done(name string) {
	h.mu.Lock()
	delete(h.inflight, name)
	h.mu.Unlock()
}

// sampleDue samples every due cluster, three at a time.
func (s *server) sampleDue(ctx context.Context, age time.Duration) {
	names := s.hist.due(age, time.Now())
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer s.hist.done(n)
			c, err := s.mgr.Get(n)
			if err != nil {
				return
			}
			cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
			defer cancel()
			if smp, err := c.TakeSample(cctx); err == nil {
				s.hist.add(n, *smp)
			}
		}()
	}
	wg.Wait()
}

// runSampler samples the watched clusters once a minute until ctx ends.
func (s *server) runSampler(ctx context.Context) {
	t := time.NewTicker(sampleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sampleDue(ctx, sampleEvery-5*time.Second)
		}
	}
}

type watchReq struct {
	Contexts []string `json:"contexts"`
}

// watch says which clusters the page is looking at. It changes nothing in a cluster.
func (s *server) watch(r *http.Request) (any, error) {
	var w watchReq
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(&w); err != nil {
		return nil, bad("invalid request")
	}
	known := map[string]bool{}
	cs, err := s.mgr.Contexts()
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		known[c.Name] = true
	}
	var names []string
	for _, n := range w.Contexts {
		if known[n] {
			names = append(names, n)
		}
	}
	all := s.hist.setWatch(names, time.Now())
	go s.sampleDue(context.Background(), 50*time.Second) // first picture right away
	return map[string]any{"watching": all, "max": maxWatched}, nil
}

// lite drops the list of problems: sparklines only need the numbers.
func lite(l []kube.Sample) []kube.Sample {
	out := make([]kube.Sample, len(l))
	for i, x := range l {
		x.Problems = nil
		out[i] = x
	}
	return out
}

// historyAPI returns the numbers of the series of each named cluster.
func (s *server) historyAPI(r *http.Request) (any, error) {
	out := map[string][]kube.Sample{}
	for _, n := range strings.Split(q(r, "context"), ",") {
		if n != "" {
			out[n] = lite(s.hist.get(n))
		}
	}
	return out, nil
}

// timemachine returns the sample nearest to `at` (unix seconds) with its problems,
// the latest sample for comparison, and the times that exist.
func (s *server) timemachine(r *http.Request) (any, error) {
	l := s.hist.get(q(r, "context"))
	times := make([]int64, len(l))
	for i, x := range l {
		times[i] = x.T.Unix()
	}
	res := map[string]any{"times": times}
	if len(l) == 0 {
		return res, nil
	}
	at, err := strconv.ParseInt(q(r, "at"), 10, 64)
	if err != nil {
		at = l[len(l)-1].T.Unix()
	}
	best := 0
	for i, x := range l {
		if abs64(x.T.Unix()-at) < abs64(l[best].T.Unix()-at) {
			best = i
		}
	}
	res["sample"], res["latest"] = l[best], l[len(l)-1]
	return res, nil
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
