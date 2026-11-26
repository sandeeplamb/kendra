// Insights, continued: add-ons across clusters and the change timeline.
(function () {
  const { h } = K;
  const V = K.views;
  const F = K.fleet;
  const head = (title, sub, ...right) => h("div", { class: "page-head" }, h("h1", {}, title), sub, h("span", { class: "grow" }), right);
  const refresh = () => h("button", { class: "btn small", onclick: () => K.refresh() }, "Refresh");
  const filterBox = (ph) => h("input", { class: "filter", id: "filter", placeholder: ph, spellcheck: "false" });

  // ---------------- add-ons and operators ----------------
  V.addons = async function (main, _a, live) {
    const names = K.pinsFirst(F.scopeNames());
    const filter = filterBox("Filter clusters or add-ons   ( / )");
    const sub = h("span", { class: "sub" }), body = h("div", {});
    const res = {}; let doneN = 0, shown = [];
    const exportTable = () => ({ head: ["Cluster", "Add-on", "Version", "Namespace", "Ready", "Desired", "Status", "Notes"],
      rows: shown.flatMap((n) => (res[n] && res[n].data ? res[n].data.map((a) => [n, a.name, a.version || "", a.namespace || "", a.ready, a.desired, a.level, (a.notes || []).join("; ")]) : [])) });
    main.append(head("Add-ons and operators", sub, K.exportBar(exportTable, "kendra-addons"), refresh()),
      h("div", { class: "faint dep-note" }, "cert-manager, External Secrets, ExternalDNS, Velero, Prometheus Operator, ingress-nginx, AWS Load Balancer Controller, metrics-server, KEDA, Karpenter, Istio, Kyverno and Gatekeeper, found by their workloads and API groups. The version is the image tag of the controller."),
      h("div", { class: "tools" }, filter), body);
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      const cols = [...new Set(names.flatMap((n) => (res[n] && res[n].data ? res[n].data.map((a) => a.name) : [])))].sort();
      const vers = {};
      names.forEach((n) => (res[n] && res[n].data ? res[n].data : []).forEach((a) => { (vers[a.name] = vers[a.name] || new Set()).add(a.version || "?"); }));
      shown = names.filter((n) => !f || n.toLowerCase().includes(f) || (res[n] && res[n].data && res[n].data.some((a) => a.name.toLowerCase().includes(f))));
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · ${cols.length} add-on${cols.length === 1 ? "" : "s"} found${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      const attention = shown.flatMap((n) => (res[n] && res[n].data ? res[n].data.filter((a) => a.level !== "ok").map((a) => ({ n, a })) : []));
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])),
        attention.length ? h("div", { class: "card att" }, h("div", { class: "group-head" }, "Needs a look"), attention.map(({ n, a }) =>
          h("div", { class: "att-row" }, K.pill(a.level, a.level === "bad" ? "down" : "check"), h("b", {}, a.name), h("span", { class: "muted" }, " on " + n + ": "), h("span", {}, (a.notes && a.notes.length ? a.notes.join(" · ") : `${a.ready}/${a.desired} ready`))))) : null,
        cols.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards matrix" },
          h("thead", {}, h("tr", {}, h("th", {}, "Cluster"), cols.map((c) => h("th", { title: vers[c].size > 1 ? vers[c].size + " versions in use" : "" }, c, vers[c].size > 1 ? h("span", { class: "pill warn" }, vers[c].size + " versions") : null)))),
          h("tbody", {}, shown.map((n) => {
            const d = res[n] && res[n].data, by = {};
            (d || []).forEach((a) => (by[a.name] = a));
            return h("tr", {}, h("td", { class: "name" }, h("span", { class: "vtags" }, ...F.tagsOf(n)), n),
              cols.map((c) => { const a = by[c];
                return h("td", {}, !res[n] ? h("span", { class: "faint" }, "…") : !a ? h("span", { class: "faint" }, "–")
                  : h("span", { class: "chip ad-" + a.level, title: [`${a.ready}/${a.desired} ready`, a.namespace, ...(a.notes || [])].filter(Boolean).join(" · ") }, a.version || (a.via === "CRDs only" ? "CRDs only" : "yes"))); }));
          })))) : doneN >= names.length ? h("div", { class: "empty" }, "No known add-on found.") : null);
    };
    filter.addEventListener("input", draw);
    draw();
    await F.loadPer("/api/addons", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- add-on compatibility with the cluster version ----------------
  V.compat = async function (main, _a, live) {
    const names = K.pinsFirst(F.scopeNames());
    const filter = filterBox("Filter clusters or add-ons   ( / )");
    const target = h("select", {}, h("option", { value: "" }, "next minor"), ...Array.from({ length: 12 }, (_, i) => h("option", { value: 29 + i }, "1." + (29 + i))));
    target.value = localStorage.getItem("knd.compat.target") || "";
    const sub = h("span", { class: "sub" }), body = h("div", {});
    const res = {}; let doneN = 0, shown = [];
    const lvl = { ok: ["ok", "✅ yes"], bad: ["bad", "❌ no"], unclear: ["warn", "⚠️ unclear"] };
    const st = (s) => { const [c, t] = lvl[s] || lvl.unclear; return K.pill(c, t); };
    const exportTable = () => ({ head: ["Cluster", "Platform", "Cluster version", "Target", "Add-on", "Installed", "Latest known", "Now", "Target status", "Support end", "Note", "Source"],
      rows: shown.flatMap((n) => (res[n] && res[n].data ? res[n].data.rows.map((r) => [n, res[n].data.platform, res[n].data.version, "1." + res[n].data.target, r.name, r.version || "", r.latest || "", r.current, r.target, r.eol || "", r.note, r.source || ""]) : [])) });
    main.append(head("Add-on compatibility", sub, K.exportBar(exportTable, "kendra-compat"), refresh()),
      h("div", { class: "faint dep-note" }, "Each add-on found in the cluster, judged against the cluster's Kubernetes version and a target version. ✅ means an official source lists that release for that Kubernetes version. ❌ means it is outside the supported window. ⚠️ means no official statement (or the project only says what it tests). The ranges come from a file shipped with Kendra, with the date it was last checked and a link to the source for each add-on. Add-ons without data show ⚠️."),
      h("div", { class: "tools" }, filter, h("label", {}, "Target Kubernetes ", target)), body);
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      shown = names.filter((n) => !f || n.toLowerCase().includes(f) || (res[n] && res[n].data && res[n].data.rows.some((r) => r.name.toLowerCase().includes(f))));
      const any = names.map((n) => res[n] && res[n].data).find(Boolean);
      sub.textContent = `${K.scopeLabel(K.state.ctx)}${any ? " · data checked " + any.verifiedOn : ""}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])),
        any && any.stale ? h("div", { class: "card att" }, h("div", { class: "group-head" }, "The compatibility data is older than 90 days. Check the sources before relying on it.")) : null,
        ...shown.filter((n) => res[n] && res[n].data).map((n) => {
          const d = res[n].data;
          const rows = d.rows.filter((r) => !f || n.toLowerCase().includes(f) || r.name.toLowerCase().includes(f));
          return h("div", { class: "card" },
            h("div", { class: "group-head" }, h("span", { class: "vtags" }, ...F.tagsOf(n)), n, h("span", { class: "faint" }, `  ${d.platform || "Kubernetes"} ${d.version} → target 1.${d.target}`)),
            d.platformNow ? h("div", { class: "att-row" }, K.pill(d.platformNow.level, d.platform + " " + d.platformNow.version), h("span", {}, " " + d.platformNow.note),
              d.platformTarget ? h("span", { class: "muted" }, "   Target " + d.platformTarget.version + ": " + d.platformTarget.note) : null,
              d.platformSource ? h("a", { href: d.platformSource, target: "_blank", rel: "noreferrer noopener" }, "   source") : null) : null,
            rows.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
              h("thead", {}, h("tr", {}, ["Add-on", "Installed", "Latest known", "Now", "Target 1." + d.target, "Support end", "Note"].map((t) => h("th", {}, t)))),
              h("tbody", {}, rows.map((r) => h("tr", {},
                h("td", { class: "name" }, r.name, r.namespace ? h("span", { class: "faint" }, "  " + r.namespace) : null),
                h("td", {}, r.version || "?"), h("td", {}, r.latest || "–"), h("td", {}, st(r.current)), h("td", {}, st(r.target)),
                h("td", {}, r.eol || "–"),
                h("td", { class: "faint" }, r.note, r.source ? h("a", { href: r.source, target: "_blank", rel: "noreferrer noopener" }, "  source") : null)))))) : h("div", { class: "empty" }, "No known add-on found."));
        }));
    };
    filter.addEventListener("input", draw);
    target.addEventListener("change", () => { localStorage.setItem("knd.compat.target", target.value); K.refresh(); });
    draw();
    await F.loadPer(`/api/compat?target=${target.value}`, names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- Jobs and CronJobs health ----------------
  V.cronjobs = async function (main, _a, live) {
    const names = K.pinsFirst(F.scopeNames());
    const filter = filterBox("Filter by cluster, namespace or name   ( / )");
    const onlyProblems = h("input", { type: "checkbox" });
    onlyProblems.checked = localStorage.getItem("knd.cron.problems") === "1";
    const sub = h("span", { class: "sub" }), body = h("div", {});
    const res = {}; let doneN = 0, shown = [];
    const until = (iso) => { const s = Math.round((new Date(iso) - Date.now()) / 1000); if (s <= 0) return "now"; if (s < 3600) return "in " + Math.round(s / 60) + "m"; if (s < 172800) return "in " + Math.round(s / 3600) + "h"; return "in " + Math.round(s / 86400) + "d"; };
    const when = (iso) => (iso ? K.ago(iso) + " ago" : "never");
    const stateLvl = { succeeded: "ok", failed: "bad", running: "warn", pending: "warn" };
    const exportTable = () => ({ head: ["Cluster", "Namespace", "CronJob", "Schedule", "Time zone", "Next run", "Last scheduled", "Last success", "Last job", "Last job state", "Failed pod", "Failed in a row", "Status", "Notes"],
      rows: shown.map(({ n, c }) => [n, c.namespace, c.name, c.schedule, c.timeZone || "", c.nextRun || "", c.lastSchedule || "", c.lastSuccess || "", c.last ? c.last.name : "", c.last ? c.last.state : "", c.last && c.last.pod ? c.last.pod : "", c.failedStreak, c.level, (c.notes || []).join("; ")]) });
    main.append(head("Jobs and CronJobs health", sub, K.exportBar(exportTable, "kendra-cronjobs"), refresh()),
      h("div", { class: "faint dep-note" }, "Every CronJob with its next run (worked out from the schedule, in its time zone, or UTC when it has none), last run, last success and last Job. Flags failed runs, suspended CronJobs, CronJobs that stopped being scheduled and Jobs that run for too long. Below each cluster: Jobs that no CronJob owns and that failed or have run for over a day."),
      h("div", { class: "tools" }, filter, h("label", { class: "chk" }, onlyProblems, " only warnings and failures")), body);
    const openRes = (n, kind, ns, name) => { const k = K.state.kinds.find((x) => x.name === kind); return h("a", { href: "javascript:void 0", onclick: (e) => { e.preventDefault(); K.openResource({ cluster: n, kind, ns, name, resource: kind, title: k && k.singular }); } }, name); };
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      const keep = (n, ns, nm) => !f || (n + " " + ns + " " + nm).toLowerCase().includes(f);
      shown = [];
      const cards = names.filter((n) => res[n] && res[n].data).map((n) => {
        const d = res[n].data;
        const cjs = d.cronJobs.filter((c) => keep(n, c.namespace, c.name) && (!onlyProblems.checked || c.level !== "ok"));
        const loose = d.looseJobs.filter((j) => keep(n, j.namespace, j.name));
        cjs.forEach((c) => shown.push({ n, c }));
        if (!cjs.length && !loose.length) return null;
        const bad = d.cronJobs.filter((c) => c.level === "bad").length, warn = d.cronJobs.filter((c) => c.level === "warn").length;
        return h("div", { class: "card" },
          h("div", { class: "group-head" }, h("span", { class: "vtags" }, ...F.tagsOf(n)), n, h("span", { class: "faint" }, `  ${d.cronJobs.length} CronJob${d.cronJobs.length === 1 ? "" : "s"} · ${bad} failing · ${warn} to look at`)),
          cjs.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
            h("thead", {}, h("tr", {}, ["", "Namespace", "CronJob", "Schedule", "Next run", "Last scheduled", "Last success", "Last job", "Notes"].map((t) => h("th", {}, t)))),
            h("tbody", {}, cjs.map((c) => h("tr", {},
              h("td", {}, K.pill(c.level, c.level === "ok" ? "ok" : c.level === "bad" ? "fail" : "check")),
              h("td", {}, c.namespace), h("td", { class: "name" }, openRes(n, "cronjobs", c.namespace, c.name)),
              h("td", { class: "mono" }, c.schedule, c.timeZone ? h("span", { class: "faint" }, "  " + c.timeZone) : null),
              h("td", {}, c.suspended ? h("span", { class: "faint" }, "suspended") : c.nextRun ? until(c.nextRun) : "–"),
              h("td", {}, when(c.lastSchedule)), h("td", {}, when(c.lastSuccess)),
              h("td", {}, c.last ? [K.pill(stateLvl[c.last.state] || "warn", c.last.state), " ", openRes(n, "jobs", c.namespace, c.last.name),
                c.last.pod ? h("span", { class: "faint" }, "  pod ", openRes(n, "pods", c.namespace, c.last.pod), c.last.podReason ? " (" + c.last.podReason + ")" : "") : null] : h("span", { class: "faint" }, "no job kept")),
              h("td", { class: "faint" }, (c.notes || []).join(" · ")))))) ) : null,
          loose.length ? h("div", {}, h("div", { class: "group-head" }, "Jobs without a CronJob that failed or run for over a day"),
            h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
              h("thead", {}, h("tr", {}, ["Namespace", "Job", "State", "Reason", "Started"].map((t) => h("th", {}, t)))),
              h("tbody", {}, loose.map((j) => h("tr", {}, h("td", {}, j.namespace), h("td", { class: "name" }, openRes(n, "jobs", j.namespace, j.name)),
                h("td", {}, K.pill(stateLvl[j.state] || "warn", j.state)), h("td", {}, j.reason || "–"), h("td", {}, when(j.started)))))))) : null);
      }).filter(Boolean);
      const total = names.map((n) => (res[n] && res[n].data ? res[n].data.cronJobs.length : 0)).reduce((a, b) => a + b, 0);
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · ${total} CronJob${total === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])), ...cards,
        doneN >= names.length && !cards.length ? h("div", { class: "empty" }, onlyProblems.checked ? "No CronJob needs a look." : "No CronJobs or problem Jobs found.") : null);
    };
    filter.addEventListener("input", draw);
    onlyProblems.addEventListener("change", () => { localStorage.setItem("knd.cron.problems", onlyProblems.checked ? "1" : "0"); draw(); });
    draw();
    await F.loadPer("/api/cronjobs", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- fleet search: images, labels, names, env var names, annotations ----------------
  V.search = async function (main, _a, live) {
    const names = K.pinsFirst(F.scopeNames());
    const input = h("input", { class: "filter", id: "filter", placeholder: "Search an image, label, name or env var name   ( / )", spellcheck: "false", value: localStorage.getItem("knd.search.q") || "" });
    const scopeDefs = [["image", "images"], ["label", "labels"], ["name", "names"], ["env", "env var names"], ["annotation", "annotations"]];
    let saved; try { saved = JSON.parse(localStorage.getItem("knd.search.scopes")); } catch (e) { saved = null; }
    const on = new Set(Array.isArray(saved) && saved.length ? saved : ["image", "label", "name"]);
    const boxes = scopeDefs.map(([k, label]) => { const cb = h("input", { type: "checkbox", checked: on.has(k) }); cb.addEventListener("change", () => { cb.checked ? on.add(k) : on.delete(k); localStorage.setItem("knd.search.scopes", JSON.stringify([...on])); }); return h("label", { class: "chk" }, cb, " " + label); });
    const sub = h("span", { class: "sub" }), body = h("div", {});
    const ns = K.state.ns;
    let res = {}, doneN = 0, asked = "", token = 0, shown = [];
    const exportTable = () => ({ head: ["Cluster", "Namespace", "Kind", "Name", "Found in", "Match"], rows: shown });
    main.append(head("Fleet search", sub, K.exportBar(exportTable, "kendra-search"), refresh()),
      h("div", { class: "faint dep-note" }, "Finds text in the images, labels, names, env var names (never values) and annotations of Deployments, StatefulSets, DaemonSets, CronJobs, Services, Ingresses and Namespaces, in every cluster of the current scope. Part of a word is enough, case does not matter. Secret and ConfigMap contents are never read."),
      h("div", { class: "tools" }, input, h("button", { class: "btn small", onclick: () => run() }, "Search"), ...boxes), body);
    const draw = () => {
      const rows = [];
      body.replaceChildren();
      if (!asked) { sub.textContent = K.scopeLabel(K.state.ctx) + (ns ? " · namespace " + ns : " · all namespaces"); body.append(h("div", { class: "empty" }, "Type at least 2 characters and press Enter.")); shown = []; return; }
      const cards = names.filter((n) => res[n] && res[n].data && res[n].data.hits.length).map((n) => {
        const d = res[n].data;
        d.hits.forEach((x) => rows.push([n, x.namespace || "", x.kind, x.name, x.scope, x.match]));
        return h("div", { class: "card" },
          h("div", { class: "group-head" }, h("span", { class: "vtags" }, ...F.tagsOf(n)), n, h("span", { class: "faint" }, `  ${d.hits.length}${d.truncated ? "+" : ""} found`)),
          d.truncated ? h("div", { class: "faint" }, "More than 500 matches. Narrow the search.") : null,
          d.unreadable && d.unreadable.length ? h("div", { class: "faint" }, "Could not read: " + d.unreadable.join(", ")) : null,
          h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
            h("thead", {}, h("tr", {}, ["Namespace", "Kind", "Name", "Found in", "Match"].map((t) => h("th", {}, t)))),
            h("tbody", {}, d.hits.map((x) => {
              const k = K.state.kinds.find((y) => y.singular === x.kind.toLowerCase());
              return h("tr", {}, h("td", {}, x.namespace || "–"), h("td", {}, x.kind),
                h("td", { class: "name" }, k ? h("a", { href: "javascript:void 0", onclick: (e) => { e.preventDefault(); K.openResource({ cluster: n, kind: k.name, ns: x.namespace || "", name: x.name, resource: k.name }); } }, x.name) : x.name),
                h("td", {}, x.scope), h("td", { class: "faint" }, x.match));
            }))))); });
      shown = rows;
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · "${asked}" · ${rows.length} match${rows.length === 1 ? "" : "es"} in ${cards.length} cluster${cards.length === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      body.append(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])), ...cards);
      if (doneN >= names.length && !rows.length) body.append(h("div", { class: "empty" }, "Nothing found."));
    };
    const run = async () => {
      const t = input.value.trim();
      if (t.length < 2 || !on.size) { draw(); return; }
      localStorage.setItem("knd.search.q", t);
      asked = t; res = {}; doneN = 0; const mine = ++token; draw();
      await F.loadPer(`/api/search?q=${encodeURIComponent(t)}&scopes=${[...on].join(",")}&ns=${encodeURIComponent(ns)}`, names, () => live() && mine === token, (n, r) => { res[n] = r; doneN++; if (live() && mine === token) draw(); });
    };
    input.addEventListener("keydown", (e) => { if (e.key === "Enter") run(); });
    draw();
    if (input.value.trim().length >= 2) await run();
  };

  // ---------------- change timeline ----------------
  V.timeline = async function (main, _a, live) {
    const names = K.pinsFirst(F.scopeNames());
    const ns = K.state.ns;
    const hours = h("select", {}, [[1, "last hour"], [6, "last 6 hours"], [24, "last 24 hours"], [72, "last 3 days"], [168, "last 7 days"]].map(([v, l]) => h("option", { value: v }, l)));
    hours.value = localStorage.getItem("knd.tl.hours") || "6";
    const filter = filterBox("Filter by object, reason or message   ( / )");
    const types = { rollout: true, event: true, restart: true, node: true }, onlyProblems = h("input", { type: "checkbox" });
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    const res = {}; let doneN = 0, shown = [];
    const exportTable = () => ({ head: ["Time", "Cluster", "Type", "Level", "Namespace", "Object", "What", "Detail", "Count"],
      rows: shown.map((i) => [i.time, i.cluster, i.type, i.level, i.namespace || "", i.object, i.title, i.detail || "", i.count || ""]) });
    main.append(head("Change timeline", sub, K.exportBar(exportTable, "kendra-timeline"), refresh()),
      h("div", { class: "tools" }, filter, hours, h("label", { class: "chk" }, onlyProblems, " only warnings and failures")), strip, body);
    const label = { rollout: "rollouts", event: "events", restart: "restarts", node: "node changes" };
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      const all = names.flatMap((n) => (res[n] && res[n].data ? res[n].data.map((i) => ({ ...i, cluster: n })) : []));
      const cnt = { rollout: 0, event: 0, restart: 0, node: 0 };
      all.forEach((i) => cnt[i.type]++);
      shown = all.filter((i) => types[i.type] && (!onlyProblems.checked || i.level !== "info") && (!f || (i.object + " " + i.title + " " + (i.detail || "") + " " + (i.namespace || "")).toLowerCase().includes(f)))
        .sort((a, b) => (a.time < b.time ? 1 : -1));
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · ${ns ? "namespace " + ns : "all namespaces"} · ${shown.length} of ${all.length}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      strip.replaceChildren(...Object.keys(label).map((t) => h("button", { class: "fl-chip " + (t === "restart" ? "bad" : t === "node" ? "warn" : "ok") + (types[t] ? " on" : ""),
        onclick: () => { types[t] = !types[t]; draw(); } }, h("b", {}, cnt[t]), " " + label[t])));
      const multi = names.length > 1;
      let lastDay = "";
      const rows = [];
      shown.slice(0, 500).forEach((i) => {
        const d = new Date(i.time), day = d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "short" });
        if (day !== lastDay) { lastDay = day; rows.push(h("div", { class: "tl-day" }, day)); }
        const [kind, name] = i.object.split("/");
        const k = K.state.kinds.find((x) => x.singular === kind);
        rows.push(h("div", { class: "tl-row " + i.level },
          h("span", { class: "tl-time", title: d.toLocaleString() }, d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }), h("span", { class: "faint" }, " " + K.ago(i.time))),
          h("span", { class: "tl-dot " + i.type }), h("div", { class: "tl-main" },
            h("div", {}, h("b", {}, i.title), i.count > 1 ? h("span", { class: "faint" }, " ×" + i.count) : null, "  ",
              k && name ? h("a", { class: "tl-obj", href: "javascript:void 0", onclick: (e) => { e.preventDefault(); K.openResource({ cluster: i.cluster, kind: k.name, ns: i.namespace || "", name, resource: k.name }); } }, (i.namespace ? i.namespace + "/" : "") + i.object) : h("span", { class: "tl-obj" }, i.object),
              multi ? h("span", { class: "faint" }, "  " + i.cluster) : null),
            i.detail ? h("div", { class: "faint tl-detail" }, i.detail) : null)));
      });
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])), rows.length ? h("div", { class: "tl" }, rows) : doneN >= names.length ? h("div", { class: "empty" }, "Nothing changed in this window.") : null,
        shown.length > 500 ? h("div", { class: "faint" }, "Showing the latest 500. Narrow the window or filter.") : null);
    };
    filter.addEventListener("input", draw);
    onlyProblems.addEventListener("change", draw);
    hours.addEventListener("change", () => { localStorage.setItem("knd.tl.hours", hours.value); K.refresh(); });
    draw();
    await F.loadPer(`/api/timeline?hours=${hours.value}&ns=${encodeURIComponent(ns)}`, names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };
  // ---------------- zones and pod spread ----------------
  V.spread = async function (main, _a, live) {
    const names = K.pinsFirst(F.scopeNames());
    const filter = filterBox("Filter workloads or nodes   ( / )");
    const only = h("input", { type: "checkbox" }), sys = h("input", { type: "checkbox", checked: true });
    const sub = h("span", { class: "sub" }), body = h("div", {});
    const res = {}, opened = new Set(names.length <= 2 ? names : []); let doneN = 0, shown = [];
    const zl = (w) => Object.keys(w.byZone).sort().map((z) => `${z}=${w.byZone[z]}`).join("; ");
    const exportTable = () => ({ head: ["Cluster", "Namespace", "Workload", "Pods", "Per zone", "Nodes used", "Verdict"],
      rows: shown.map((w) => [w.cluster, w.namespace, w.workload, w.pods, zl(w), Object.keys(w.byNode).length, w.verdict]) });
    main.append(head("Zones and pod spread", sub, K.exportBar(exportTable, "kendra-spread"), refresh()),
      h("div", { class: "faint dep-note" }, "Where the nodes sit in availability zones, how many pods each node runs, and whether the pods of each workload are spread over nodes and zones. Zones come from the node label topology.kubernetes.io/zone."),
      h("div", { class: "tools" }, filter, h("label", { class: "chk" }, only, " only workloads that are not spread well"), h("label", { class: "chk" }, sys, " hide kube-* namespaces")), body);
    const pct = (a, b) => (b ? Math.round((100 * a) / b) : 0);
    const section = (n, r) => {
      const d = r.data, f = filter.value.trim().toLowerCase();
      const maxPods = Math.max(1, ...d.nodes.map((x) => x.pods)), avg = d.nodes.length ? d.nodes.reduce((a, x) => a + x.pods, 0) / d.nodes.length : 0;
      const zonesMaxPods = Math.max(1, ...d.zones.map((z) => z.pods)), totalPods = d.zones.reduce((a, z) => a + z.pods, 0);
      const nodeCounts = d.zones.map((z) => z.nodes), uneven = d.zones.length > 1 && Math.max(...nodeCounts) - Math.min(...nodeCounts) > 1;
      const wls = d.workloads.filter((w) => !(sys.checked && w.namespace.startsWith("kube-")) && (!only.checked || w.level !== "ok")
        && (!f || (w.namespace + " " + w.workload).toLowerCase().includes(f)));
      wls.forEach((w) => shown.push({ ...w, cluster: n }));
      const bad = d.workloads.filter((w) => w.level === "bad" && !(sys.checked && w.namespace.startsWith("kube-"))).length;
      const warn = d.workloads.filter((w) => w.level === "warn" && !(sys.checked && w.namespace.startsWith("kube-"))).length;
      const det = h("details", { class: "sp-cluster", open: opened.has(n) },
        h("summary", {}, h("span", { class: "vtags" }, ...F.tagsOf(n)), h("b", {}, n), h("span", { class: "faint" }, `  ${d.zones.length} zone${d.zones.length === 1 ? "" : "s"} · ${d.nodes.length} nodes · ${totalPods} pods`),
          bad ? K.pill("bad", bad + " on one node") : null, warn ? K.pill("warn", warn + " in one zone or lopsided") : null, uneven ? K.pill("warn", "uneven nodes per zone") : null),
        h("div", { class: "group-head" }, "Availability zones"),
        h("div", { class: "sp-zones" }, d.zones.map((z) => h("div", { class: "sp-zrow" }, h("span", { class: "sp-zname" }, z.name),
          h("span", { class: "sp-blocks", title: `${z.nodesReady}/${z.nodes} nodes ready` }, Array.from({ length: z.nodes }, (_, i) => h("i", { class: i < z.nodesReady ? "" : "down" }))),
          h("span", { class: "sp-znum" }, z.nodes + " node" + (z.nodes === 1 ? "" : "s")),
          h("span", { class: "sp-pbar" }, h("b", { style: `width:${pct(z.pods, zonesMaxPods)}%` })), h("span", { class: "sp-znum" }, `${z.pods} pods · ${pct(z.pods, totalPods)}%`)))),
        h("div", { class: "group-head" }, "Nodes and the pods on them"),
        h("div", { class: "sp-nodes" }, d.zones.map((z) => h("div", { class: "sp-zone" }, h("div", { class: "sp-zhead" }, z.name),
          h("div", { class: "sp-tiles" }, d.nodes.filter((x) => x.zone === z.name && (!f || x.name.toLowerCase().includes(f) || (x.pool || "").toLowerCase().includes(f))).map((x) => {
            const cls = !x.ready ? "down" : x.cordoned ? "cordoned" : x.pods === 0 ? "empty" : x.pods > Math.max(avg * 1.6, avg + 5) ? "busy" : "";
            return h("button", { class: "sp-node " + cls, title: `${x.name}\npool ${x.pool || "–"}\n${x.pods}${x.maxPods ? " of " + x.maxPods : ""} pods · CPU ${pct(x.cpuReq, x.cpuAlloc)}% requested · memory ${pct(x.memReq, x.memAlloc)}% requested${x.cordoned ? "\ncordoned" : ""}${x.ready ? "" : "\nnot ready"}`,
              onclick: () => K.openResource({ cluster: n, kind: "nodes", ns: "", name: x.name, resource: "nodes" }) },
              h("div", { class: "sp-nname" }, x.name.replace(/\.[a-z0-9.-]*(internal|compute)$/i, "")), h("div", { class: "sp-npods" }, h("b", {}, x.pods), " pods"),
              h("div", { class: "sp-pbar" }, h("b", { style: `width:${pct(x.pods, maxPods)}%` })), h("div", { class: "sp-nmeta faint" }, (x.pool && x.pool !== "(no pool label)" ? x.pool + " · " : "") + "CPU " + pct(x.cpuReq, x.cpuAlloc) + "%"));
          }))))),
        h("div", { class: "group-head" }, `Workload spread (${wls.length})`),
        wls.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
          h("thead", {}, h("tr", {}, ["Workload", "Pods", "Per zone", "Nodes used", "Spread"].map((c) => h("th", {}, c)))),
          h("tbody", {}, wls.slice(0, 300).map((w) => h("tr", { class: w.level === "bad" ? "h-bad" : w.level === "warn" ? "h-warn" : "" },
            h("td", { class: "name" }, h("span", { class: "muted" }, w.namespace + "/"), w.workload), h("td", {}, w.pods),
            h("td", {}, Object.keys(w.byZone).sort().map((z) => h("span", { class: "chip" }, z + " × " + w.byZone[z]))), h("td", {}, Object.keys(w.byNode).length + " of " + w.pods),
            h("td", {}, w.level === "ok" ? K.pill("ok", w.verdict) : K.pill(w.level, w.verdict))))))) : h("div", { class: "faint rel-empty" }, only.checked ? "Every workload is spread well." : "No workload matches."));
      det.addEventListener("toggle", () => { det.open ? opened.add(n) : opened.delete(n); });
      return det;
    };
    const draw = () => {
      shown = [];
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · ${names.length} cluster${names.length === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])), ...names.filter((n) => res[n] && res[n].data).map((n) => section(n, res[n])),
        doneN < names.length && !names.some((n) => res[n]) ? h("div", { class: "empty" }, "Reading nodes and pods…") : null);
    };
    filter.addEventListener("input", draw);
    [only, sys].forEach((x) => x.addEventListener("change", draw));
    draw();
    await F.loadPer("/api/spread", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };
  // ---------------- Gateway API ----------------
  V.gateway = async function (main, _a, live) {
    const names = K.pinsFirst(F.scopeNames());
    const filter = filterBox("Filter gateways, routes, hosts or services   ( / )");
    const only = h("input", { type: "checkbox" });
    const sub = h("span", { class: "sub" }), body = h("div", {});
    const res = {}, opened = new Set(names.length <= 2 ? names : []); let doneN = 0, shown = [];
    const okRoute = (r) => r.accepted === "True" && r.resolvedRefs !== "False" && !r.backends.some((b) => b.missing);
    const exportTable = () => ({ head: ["Cluster", "Kind", "Namespace", "Route", "Hosts", "Gateways", "Backends", "Accepted", "Refs resolved", "Message"],
      rows: shown.map((r) => [r.cluster, r.kind, r.namespace, r.name, r.hostnames.join("; "), r.parents.join("; "), r.backends.map((b) => `${b.namespace}/${b.name}:${b.port || ""} w${b.weight}${b.missing ? " MISSING" : ""}`).join("; "), r.accepted, r.resolvedRefs, r.message || ""]) });
    main.append(head("Gateway API", sub, K.exportBar(exportTable, "kendra-gateway-api"), refresh()),
      h("div", { class: "faint dep-note" }, "Gateways with their listeners and addresses, and HTTPRoute, GRPCRoute and TLSRoute objects with the services they send traffic to. Clusters without the Gateway API CRDs are listed as not installed."),
      h("div", { class: "tools" }, filter, h("label", { class: "chk" }, only, " only gateways and routes with a problem")), body);
    const pill = (v, good) => (v === good ? K.pill("ok", v === "True" ? "yes" : v) : K.pill(v === "False" ? "bad" : "warn", v === "False" ? "no" : v));
    const section = (n, d) => {
      const f = filter.value.trim().toLowerCase();
      const gws = d.gateways.filter((g) => (!only.checked || g.programmed !== "True") && (!f || (g.namespace + " " + g.name + " " + g.class + " " + g.listeners.map((l) => l.hostname).join(" ")).toLowerCase().includes(f)));
      const routes = d.routes.filter((r) => (!only.checked || !okRoute(r)) && (!f || (r.namespace + " " + r.name + " " + r.hostnames.join(" ") + " " + r.backends.map((b) => b.name).join(" ")).toLowerCase().includes(f)));
      routes.forEach((r) => shown.push({ ...r, cluster: n }));
      const badG = d.gateways.filter((g) => g.programmed !== "True").length, badR = d.routes.filter((r) => !okRoute(r)).length;
      const det = h("details", { class: "sp-cluster", open: opened.has(n) },
        h("summary", {}, h("span", { class: "vtags" }, ...F.tagsOf(n)), h("b", {}, n), h("span", { class: "faint" }, `  ${d.gateways.length} gateway${d.gateways.length === 1 ? "" : "s"} · ${d.routes.length} route${d.routes.length === 1 ? "" : "s"}`),
          badG ? K.pill("bad", badG + " gateway not programmed") : null, badR ? K.pill("warn", badR + " route with a problem") : null),
        d.classes.length ? h("div", { class: "faint dep-note" }, "Classes: " + d.classes.map((c) => `${c.name} (${c.controller}${c.accepted === "True" ? "" : ", not accepted"})`).join(", ")) : null,
        h("div", { class: "group-head" }, `Gateways (${gws.length})`),
        gws.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" }, h("thead", {}, h("tr", {}, ["Gateway", "Class", "Address", "Listeners", "Programmed"].map((c) => h("th", {}, c)))),
          h("tbody", {}, gws.map((g) => h("tr", { class: g.programmed === "True" ? "" : "h-bad", title: g.message || "" },
            h("td", { class: "name" }, h("span", { class: "muted" }, g.namespace + "/"), g.name), h("td", {}, g.class), h("td", { class: "mono" }, g.addresses.join(", ") || "–"),
            h("td", {}, g.listeners.map((l) => h("span", { class: "chip" + (l.ready === "False" ? " state" : "") }, `${l.protocol}:${l.port}${l.hostname ? " " + l.hostname : ""} · ${l.routes} route${l.routes === 1 ? "" : "s"}`))),
            h("td", {}, pill(g.programmed, "True"))))))) : h("div", { class: "faint rel-empty" }, "No gateway."),
        h("div", { class: "group-head" }, `Routes (${routes.length})`),
        routes.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" }, h("thead", {}, h("tr", {}, ["Route", "Hosts", "Gateway", "Sends traffic to", "Accepted", "Refs"].map((c) => h("th", {}, c)))),
          h("tbody", {}, routes.slice(0, 300).map((r) => h("tr", { class: okRoute(r) ? "" : "h-warn", title: r.message || "" },
            h("td", { class: "name" }, h("span", { class: "faint" }, r.kind + " "), h("span", { class: "muted" }, r.namespace + "/"), r.name), h("td", {}, r.hostnames.map((x) => h("span", { class: "chip" }, x))),
            h("td", { class: "mono" }, r.parents.join(", ")),
            h("td", {}, r.backends.map((b) => h("span", { class: "chip" + (b.missing ? " state" : ""), title: b.missing ? "No such Service" : "" }, `${b.name}${b.port ? ":" + b.port : ""}${b.weight !== 1 ? " ×" + b.weight : ""}${b.missing ? " (missing)" : ""}`))),
            h("td", {}, pill(r.accepted, "True")), h("td", {}, pill(r.resolvedRefs, "True"), r.message ? h("div", { class: "faint tl-detail" }, r.message) : null)))))) : h("div", { class: "faint rel-empty" }, "No route."));
      det.addEventListener("toggle", () => { det.open ? opened.add(n) : opened.delete(n); });
      return det;
    };
    const draw = () => {
      shown = [];
      sub.textContent = `${K.scopeLabel(K.state.ctx)}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      const absent = names.filter((n) => res[n] && res[n].error && /is not installed/.test(res[n].error));
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error && !/is not installed/.test(res[n].error)).map((n) => res[n])), ...names.filter((n) => res[n] && res[n].data).map((n) => section(n, res[n].data)),
        absent.length ? h("div", { class: "faint dep-clean" }, "Gateway API is not installed in: " + absent.join(", ")) : null);
    };
    filter.addEventListener("input", draw); only.addEventListener("change", draw);
    draw();
    await F.loadPer("/api/gateway", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- time machine ----------------
  V.timemachine = async function (main, _a, live) {
    const all = K.state.ctxs.map((c) => c.name);
    const first = K.multi() ? K.pinsFirst(K.members(K.state.ctx))[0] || all[0] : K.state.ctx;
    const pick = h("select", {}, all.map((n) => h("option", { value: n }, n))); pick.value = first;
    const sub = h("span", { class: "sub" }), body = h("div", {});
    main.append(head("Time machine", sub, h("label", { class: "chk" }, "Cluster ", pick), refresh()),
      h("div", { class: "faint dep-note" }, "Kendra takes a small picture of the clusters you look at once a minute (up to 10 at a time, kept in memory for 6 hours, lost when you quit). Drag the slider to see what was failing then, and what has been fixed or broken since."), body);
    let timer = null, playing = false;
    const stop = () => { playing = false; if (timer) clearInterval(timer); timer = null; };
    const show = async () => {
      stop();
      const cluster = pick.value;
      K.watch([cluster]);
      let info, hist;
      try { [info, hist] = await Promise.all([K.api("/api/timemachine", { context: cluster }), K.history([cluster])]); } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); return; }
      if (!live()) return;
      const times = info.times || [], series = (hist[cluster] || []);
      sub.textContent = cluster + " · " + times.length + " sample" + (times.length === 1 ? "" : "s");
      if (times.length < 2) { body.replaceChildren(h("div", { class: "empty" }, times.length ? "Only one picture so far. Come back in a minute or two." : "Kendra has just started watching this cluster. The first picture arrives within a few seconds, then one a minute.", h("div", {}, h("button", { class: "btn small", onclick: show }, "Check again")))); return; }
      const slider = h("input", { type: "range", class: "tm-slider", min: 0, max: times.length - 1, value: times.length - 1, "aria-label": "Time" });
      const when = h("span", { class: "tm-when" }), vs = h("span", { class: "tm-vs faint" });
      const mark = h("div", { class: "tm-mark" });
      const bar = h("div", { class: "tm-bar" }, K.spark(series.map((x) => x.podsBad), { w: 600, h: 48, cls: "bad", stretch: true }), mark);
      const panel = h("div", {});
      const key = (p) => p.kind + "|" + (p.namespace || "") + "|" + p.name;
      const render = async () => {
        const i = +slider.value, t = times[i];
        mark.style.left = (times.length > 1 ? (100 * i) / (times.length - 1) : 0) + "%";
        const d = new Date(t * 1000), mins = Math.round((Date.now() - d.getTime()) / 60000);
        when.textContent = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) + (mins > 0 ? `  (${mins} min ago)` : "  (latest)");
        let r; try { r = await K.api("/api/timemachine", { context: cluster, at: t }); } catch (e) { return; }
        if (!live() || +slider.value !== i) return;
        const smp = r.sample, now = r.latest, then = smp.problems || [], nowKeys = new Set((now.problems || []).map(key)), thenKeys = new Set(then.map(key));
        vs.textContent = i === times.length - 1 ? "this is the latest picture" : "compared with now";
        const stat = (label, v, bad) => h("div", { class: "tm-stat" + (bad ? " bad" : "") }, h("b", {}, v), h("span", {}, label));
        const row = (p, tag) => h("div", { class: "rel-item " + (p.level === "bad" ? "bad" : "warn") }, h("span", { class: "rel-kind" }, p.kind), h("span", { class: "rel-name" }, (p.namespace ? p.namespace + "/" : "") + p.name),
          h("span", { class: "rel-note" }, p.reason + (tag ? "  ·  " + tag : "")));
        const fresh = (now.problems || []).filter((p) => !thenKeys.has(key(p)));
        panel.replaceChildren(h("div", { class: "tm-grid" }, stat("pods", smp.pods), stat("failing", smp.podsBad, smp.podsBad > 0), stat("pending", smp.podsWarn), stat("nodes ready", `${smp.nodesReady}/${smp.nodes}`, smp.nodesReady < smp.nodes), stat("warnings (10 min)", smp.warnings),
          smp.cpuUsed ? stat("CPU used", smp.cpuUsed + "m") : null),
          h("div", { class: "group-head" }, `Wrong at this time (${then.length}${then.length >= 300 ? "+" : ""})`),
          then.length ? then.map((p) => row(p, i === times.length - 1 ? "" : nowKeys.has(key(p)) ? "still wrong now" : "fine now")) : h("div", { class: "faint rel-empty" }, "Nothing was wrong."),
          i === times.length - 1 ? null : [h("div", { class: "group-head" }, `Wrong now but not then (${fresh.length})`), fresh.length ? fresh.map((p) => row(p, "new since")) : h("div", { class: "faint rel-empty" }, "Nothing new.")]);
      };
      let deb = null;
      slider.addEventListener("input", () => { stop(); render(); });
      const step = (d) => { slider.value = Math.max(0, Math.min(times.length - 1, +slider.value + d)); render(); };
      const play = h("button", { class: "btn small", onclick: () => {
        if (playing) { stop(); play.textContent = "▶ Play"; return; }
        if (+slider.value >= times.length - 1) slider.value = 0;
        playing = true; play.textContent = "❚❚ Pause"; render();
        timer = setInterval(() => { if (+slider.value >= times.length - 1) { stop(); play.textContent = "▶ Play"; return; } step(1); }, 700);
      } }, "▶ Play");
      body.replaceChildren(bar, slider, h("div", { class: "tm-ctl" }, h("button", { class: "btn small", onclick: () => step(-1) }, "◀"), play, h("button", { class: "btn small", onclick: () => step(1) }, "▶"), when, vs), panel);
      render();
    };
    pick.addEventListener("change", show);
    await show();
    return stop; // stop playing when the page is left
  };
})();
