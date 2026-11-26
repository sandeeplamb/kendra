const assert = require("assert");
const F = require("../static/filter.js");
const cols = ["Name", "Ready", "Status", "Restarts", "Age"];
const now = Date.parse("2026-11-25T12:00:00Z");
const mk = (name, ns, status, restarts, created, health) => ({ name, namespace: ns, cluster: "c1", created,
  cells: [name, "1/1", status, String(restarts), "1h"], health });
const rows = [
  mk("api-1", "shop", "Running", 0, "2026-11-25T11:50:00Z", "ok"),
  mk("worker-1", "shop", "CrashLoopBackOff", 14, "2026-11-20T11:50:00Z", "bad"),
  mk("db-0", "kube-system", "Running", 5, "2026-11-25T08:00:00Z", "ok"),
];
const names = (t) => F.apply(rows, cols, t, now).map((r) => r.name).join(",");
assert.strictEqual(names(""), "api-1,worker-1,db-0");
assert.strictEqual(names("api"), "api-1");
assert.strictEqual(names("!ok"), "worker-1");
assert.strictEqual(names("ns:shop"), "api-1,worker-1");
assert.strictEqual(names("status:crash|error"), "worker-1");
assert.strictEqual(names("restarts>3"), "worker-1,db-0");
assert.strictEqual(names("age<1h"), "api-1");
assert.strictEqual(names("-kube-system"), "api-1,worker-1");
assert.strictEqual(names("ns:shop restarts>3"), "worker-1");
assert.strictEqual(names("health:bad"), "worker-1");
assert.strictEqual(F.parseAge("2d"), 172800);
console.log("filter tests passed");
