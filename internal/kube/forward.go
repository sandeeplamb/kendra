package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// Forward is a running port forward from 127.0.0.1 to a pod port.
type Forward struct {
	ID         string `json:"id"`
	Cluster    string `json:"cluster"`
	Namespace  string `json:"namespace"`
	Target     string `json:"target"` // what the user asked for, e.g. service/web
	Pod        string `json:"pod"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	Address    string `json:"address"`
	Started    string `json:"started"`
	Active     bool   `json:"active"`
	Error      string `json:"error,omitempty"`

	stop chan struct{}
}

// Forwards tracks the port forwards started from the UI. They end when Kendra exits.
type Forwards struct {
	mu   sync.Mutex
	list map[string]*Forward
	seq  int
}

// NewForwards returns an empty set.
func NewForwards() *Forwards { return &Forwards{list: map[string]*Forward{}} }

// List returns all forwards, newest first, including ones that have ended.
func (f *Forwards) List() []Forward {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Forward, 0, len(f.list))
	for _, x := range f.list {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	return out
}

// Stop ends a forward and forgets it.
func (f *Forwards) Stop(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.list[id]
	if !ok {
		return fmt.Errorf("no such port forward")
	}
	if x.Active {
		close(x.stop)
		x.Active = false
	}
	delete(f.list, id)
	Audit(x.Cluster, "port-forward-stop", "pods", x.Namespace, x.Pod, fmt.Sprintf("127.0.0.1:%d", x.LocalPort))
	return nil
}

// podPort picks the container port a service port leads to: the targetPort, by number,
// or by name looked up in the pod's containers.
func podPort(svc *corev1.Service, pod *corev1.Pod, remote int) (int, error) {
	var sp *corev1.ServicePort
	for i := range svc.Spec.Ports {
		if remote == 0 && len(svc.Spec.Ports) == 1 || int(svc.Spec.Ports[i].Port) == remote {
			sp = &svc.Spec.Ports[i]
			break
		}
	}
	if sp == nil {
		return 0, fmt.Errorf("service %s has no port %d", svc.Name, remote)
	}
	t := sp.TargetPort
	if t.Type == 0 { // intstr.Int
		if t.IntVal == 0 {
			return int(sp.Port), nil
		}
		return int(t.IntVal), nil
	}
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.Name == t.StrVal {
				return int(p.ContainerPort), nil
			}
		}
	}
	return 0, fmt.Errorf("pod %s has no container port named %q", pod.Name, t.StrVal)
}

func readyPod(pods []corev1.Pod) *corev1.Pod {
	var running *corev1.Pod
	for i := range pods {
		p := &pods[i]
		if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
			continue
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return p
			}
		}
		if running == nil {
			running = p
		}
	}
	return running
}

// Start opens 127.0.0.1:local (0 = any free port) to a port of a pod, or of a pod behind
// a service. kind is "pods" or "services".
func (f *Forwards) Start(ctx context.Context, c *Cluster, kind, ns, name string, remote, local int) (*Forward, error) {
	if c.Cfg == nil {
		return nil, fmt.Errorf("no connection settings for this cluster")
	}
	if local < 0 || local > 65535 || remote < 0 || remote > 65535 {
		return nil, fmt.Errorf("ports must be between 0 and 65535")
	}
	var pod *corev1.Pod
	podP := remote
	switch kind {
	case "pods":
		p, err := c.Cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		pod = p
		if podP == 0 {
			for _, ct := range p.Spec.Containers {
				if len(ct.Ports) > 0 {
					podP = int(ct.Ports[0].ContainerPort)
					break
				}
			}
		}
	case "services":
		svc, err := c.Cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if len(svc.Spec.Selector) == 0 {
			return nil, fmt.Errorf("service %s has no selector, so there is no pod to forward to", name)
		}
		pl, err := c.Cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labels.Set(svc.Spec.Selector).String()})
		if err != nil {
			return nil, err
		}
		if pod = readyPod(pl.Items); pod == nil {
			return nil, fmt.Errorf("service %s has no running pod", name)
		}
		if podP, err = podPort(svc, pod, remote); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("only pods and services can be forwarded")
	}
	if podP == 0 {
		return nil, fmt.Errorf("say which port to forward")
	}
	if pod.Status.Phase != corev1.PodRunning {
		return nil, fmt.Errorf("pod %s is %s, not Running", pod.Name, pod.Status.Phase)
	}

	u := c.Cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod.Name).SubResource("portforward").URL()
	rt, up, err := spdy.RoundTripperFor(c.Cfg)
	if err != nil {
		return nil, err
	}
	dialer := spdy.NewDialer(up, &http.Client{Transport: rt}, http.MethodPost, u)
	stop, ready := make(chan struct{}), make(chan struct{})
	var errBuf bytes.Buffer
	pf, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("%d:%d", local, podP)}, stop, ready, io.Discard, &errBuf)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.seq++
	fw := &Forward{ID: "pf" + strconv.Itoa(f.seq), Cluster: c.Name, Namespace: ns, Target: kind[:len(kind)-1] + "/" + name, Pod: pod.Name,
		RemotePort: podP, Started: time.Now().UTC().Format(time.RFC3339), Active: true, stop: stop}
	f.list[fw.ID] = fw
	f.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		err := pf.ForwardPorts()
		f.mu.Lock()
		if x, ok := f.list[fw.ID]; ok && x.Active {
			x.Active = false
			if err != nil {
				x.Error = err.Error()
			} else if errBuf.Len() > 0 {
				x.Error = errBuf.String()
			} else {
				x.Error = "connection closed"
			}
		}
		f.mu.Unlock()
		done <- err
	}()
	select {
	case <-ready:
	case err := <-done:
		f.mu.Lock()
		delete(f.list, fw.ID)
		f.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("%s", errBuf.String())
		}
		return nil, err
	case <-time.After(15 * time.Second):
		close(stop)
		f.mu.Lock()
		delete(f.list, fw.ID)
		f.mu.Unlock()
		return nil, fmt.Errorf("timed out opening the port forward")
	}
	ports, err := pf.GetPorts()
	if err != nil || len(ports) == 0 {
		close(stop)
		return nil, fmt.Errorf("could not read the local port")
	}
	f.mu.Lock()
	fw.LocalPort = int(ports[0].Local)
	fw.Address = fmt.Sprintf("127.0.0.1:%d", fw.LocalPort)
	out := *fw
	f.mu.Unlock()
	Audit(c.Name, "port-forward", kind, ns, name, fmt.Sprintf("%s -> pod %s:%d", fw.Address, pod.Name, podP))
	return &out, nil
}
