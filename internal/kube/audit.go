package kube

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"
)

var auditMu sync.Mutex

// AuditPath is where changes are recorded (KND_AUDIT_LOG overrides it).
func AuditPath() string {
	if p := os.Getenv("KND_AUDIT_LOG"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".kendra", "audit.log")
}

// Audit appends one JSON line describing a change made through Kendra.
func Audit(ctxName, action, kind, ns, name, detail string) {
	auditMu.Lock()
	defer auditMu.Unlock()
	p := AuditPath()
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	_ = json.NewEncoder(f).Encode(map[string]string{"time": time.Now().UTC().Format(time.RFC3339), "user": who,
		"context": ctxName, "action": action, "kind": kind, "namespace": ns, "name": name, "detail": detail})
}
