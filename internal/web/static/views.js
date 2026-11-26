// Views: resource lists, warnings, kapp apps (list, detail, map) and the Istio topology.
(function () {
  const { h } = K;
  const V = K.views;
  const enc = encodeURIComponent;
  const RANK = { bad: 0, warn: 1, ok: 2, idle: 3 };
  const MAX_ROWS = 600;

  const head = (title, sub, ...right) => h("div", { class: "page-head" }, h("h1", {}, title), h("span", { class: "sub" }, sub), h("span", { class: "grow" }), right);
  // "not installed" is not a failure (the add-on is simply absent), so it is shown calmly
  const errBanners = (results) => results.filter((r) => r.error).map((r) => h("div", { class: /is not installed/.test(r.error) ? "banner" : "banner err" }, h("b", {}, r.cluster + ": "), r.error));
  const scope = () => K.scopeLabel(K.state.ctx);
  const nsScope = () => (K.state.ns ? K.state.ns : "all namespaces");
  const kindOf = (resource, group) => K.state.kinds.find((k) => k.name === resource && k.group === (group || ""));
  const kindBySingular = (s) => K.state.kinds.find((k) => k.singular === s);

  // open any object, catalogue kind or not
  function openRes(r, cluster) {
    const k = r.resource ? kindOf(r.resource, r.group) : kindBySingular(r.Kind || r.kind);
    K.openResource({ cluster, kind: k ? k.name : "", ns: r.namespace, name: r.name, group: r.group, version: r.version,
      resource: r.resource || (k && k.name), title: r.kind, health: r.health, detail: r.detail });
  }

  // ---------------- resource list ----------------
  // spec is a catalogue kind name ("pods"), or {group, resource, title, namespaced} for a custom resource
  V.list = async function (main, spec, live) {
    const cr = typeof spec === "object";
    const kind = cr ? { name: spec.resource, group: spec.group, title: spec.title, singular: spec.title, namespaced: spec.namespaced, cr: true }
      : K.state.kinds.find((k) => k.name === spec);
    if (!kind) { main.append(h("div", { class: "empty" }, "Unknown resource type.")); return; }
    const kindName = cr ? spec.group + "/" + spec.resource : kind.name;
    const filter = h("input", { class: "filter", placeholder: "Filter: name, ns:shop, status:crash|error, restarts>3, age<1h, !ok, -kube-system   ( / )", id: "filter", spellcheck: "false" });
    filter.value = sessionStorage.getItem("knd.filter." + kindName) || "";
    let mode = "all", wide = false, liveOn = true, rows = [], cols = [], results = [], shown = MAX_ROWS;
    const segEl = h("div", { class: "seg" });
    const body = h("div", {});
    const titleSub = h("span", { class: "sub" });
    const wideBtn = h("button", { class: "btn small", onclick: () => { wide = !wide; wideBtn.textContent = wide ? "Fewer columns" : "More columns"; draw(); } }, "More columns");
    const liveBtn = h("button", { class: "btn small primary", onclick: () => { liveOn = !liveOn; liveBtn.classList.toggle("primary", liveOn); liveBtn.textContent = liveOn ? "Live" : "Paused"; } }, "Live");
    main.append(h("div", { class: "page-head" }, h("h1", {}, kind.title), titleSub, h("span", { class: "grow" }), wideBtn, liveBtn),
      h("div", { class: "tools" }, filter, segEl), body);
    filter.addEventListener("input", () => { sessionStorage.setItem("knd.filter." + kindName, filter.value); shown = MAX_ROWS; draw(); });

    function draw() {
      const base = KFilter.apply(rows, cols.map((c) => c.name), filter.value);
      const count = (f) => base.filter(f).length;
      const segs = [["all", "All", base.length], ["bad", "Failing", count((r) => r.health === "bad")], ["warn", "Warning", count((r) => r.health === "warn")], ["ok", "Healthy", count((r) => r.health === "ok" || r.health === "idle")]];
      segEl.replaceChildren(...segs.map(([id, label, n]) => h("button", { class: mode === id ? "on" : "", onclick: () => { mode = id; draw(); } }, label, h("span", { class: "n" }, n))));
      const view = base.filter((r) => mode === "all" || (mode === "ok" ? r.health === "ok" || r.health === "idle" : r.health === mode))
        .sort((a, b) => RANK[a.health] - RANK[b.health] || (a.cluster + a.namespace + a.name).localeCompare(b.cluster + b.namespace + b.name));
      titleSub.textContent = `${scope()} · ${kind.namespaced ? nsScope() : "cluster-wide"} · ${view.length} of ${rows.length}`;
      const vis = cols.map((c, i) => ({ c, i })).filter(({ c }) => c.name !== "Name" && (wide || c.priority === 0));
      const showNs = kind.namespaced && !K.state.ns;
      const showCl = K.multi();
      const usage = kind.name === "pods";
      const maxCpu = Math.max(1, ...view.map((r) => r.cpuM || 0)), maxMem = Math.max(1, ...view.map((r) => r.memB || 0));
      const trs = view.slice(0, shown).map((r) => {
        const tr = h("tr", { class: "h-" + r.health, tabindex: 0 },
          h("td", { class: "name", title: r.note || r.name }, r.name),
          showCl ? h("td", { class: "muted" }, r.cluster) : null,
          showNs ? h("td", { class: "muted" }, r.namespace) : null,
          vis.map(({ c, i }) => {
            const v = r.cells[i] || "";
            if (c.name === "Status") return h("td", {}, K.pill(r.health, v));
            if (c.name === "Age") return h("td", { class: "muted", title: r.created }, v);
            return h("td", { class: c.type === "integer" ? "mono" : "", title: v }, v);
          }),
          usage ? [h("td", {}, r.hasUsage ? h("span", { class: "meter" }, h("span", { class: "mono" }, r.cpuM + "m"), h("i", {}, h("b", { style: `width:${Math.round(100 * r.cpuM / maxCpu)}%` }))) : h("span", { class: "faint" }, "–")),
            h("td", {}, r.hasUsage ? h("span", { class: "meter" }, h("span", { class: "mono" }, K.bytes(r.memB)), h("i", {}, h("b", { style: `width:${Math.round(100 * r.memB / maxMem)}%` }))) : h("span", { class: "faint" }, "–"))] : null);
        const open = () => {
          if (kind.name === "customresourcedefinitions") { // a definition opens the list of its instances
            const i = r.name.indexOf(".");
            location.hash = `#/cr/${enc(r.name.slice(i + 1))}/${enc(r.name.slice(0, i))}`;
            return;
          }
          K.openResource({ cluster: r.cluster, kind: kind.cr ? "" : kind.name, ns: r.namespace, name: r.name, row: r, cols,
            resource: kind.name, group: kind.cr ? kind.group : undefined, title: kind.singular });
        };
        tr.addEventListener("click", open);
        tr.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
        return tr;
      });
      const table = h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, h("th", {}, "Name"), showCl ? h("th", {}, "Cluster") : null, showNs ? h("th", {}, "Namespace") : null,
          vis.map(({ c }) => h("th", {}, c.name)), usage ? [h("th", {}, "CPU"), h("th", {}, "Memory")] : null)),
        h("tbody", {}, trs));
      body.replaceChildren(...errBanners(results), view.length ? table : h("div", { class: "empty" }, rows.length ? "Nothing matches the filter." : "No " + kind.title.toLowerCase() + " here."),
        view.length > shown ? h("button", { class: "btn", onclick: () => { shown += MAX_ROWS; draw(); } }, `Show ${Math.min(MAX_ROWS, view.length - shown)} more of ${view.length - shown}`) : null);
    }

    async function load(first) {
      try {
        results = await K.api("/api/list", Object.assign({ context: K.state.ctx, ns: kind.namespaced ? K.state.ns : "" },
          kind.cr ? { group: kind.group, resource: kind.name } : { kind: kind.name }));
      } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); return; }
      if (!live()) return;
      rows = results.filter((r) => r.data).flatMap((r) => r.data.rows);
      const ok = results.find((r) => r.data);
      cols = ok ? ok.data.columns : [];
      draw();
    }
    body.append(h("div", { class: "empty" }, "Loading…"));
    await load(true);
    const t = setInterval(() => { if (liveOn && !document.hidden && document.getElementById("drawer").hidden) load(); }, 10000);
    return () => clearInterval(t);
  };

  // ---------------- warnings ----------------
  V.warnings = async function (main, _a, live) {
    main.append(head("Warnings", `${scope()} · ${nsScope()}`), h("div", { class: "empty" }, "Reading events…"));
    let results;
    try { results = await K.api("/api/warnings", { context: K.state.ctx, ns: K.state.ns }); } catch (e) { main.append(h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    main.replaceChildren(head("Warnings", `${scope()} · ${nsScope()}`), ...errBanners(results));
    results.filter((r) => r.data).forEach((r) => {
      if (K.multi()) main.append(h("div", { class: "group-head" }, r.cluster));
      if (!r.data.length) { main.append(h("div", { class: "empty" }, "No warning events. 🎉")); return; }
      main.append(h("div", { class: "cards-grid" }, r.data.map((g) => h("div", { class: "card" },
        h("h3", {}, g.reason, " ", K.pill("warn", "×" + g.count)),
        h("div", { class: "muted" }, g.message),
        g.hint ? h("div", { class: "finding warn", style: "margin-top:8px" }, g.hint) : null,
        h("div", { class: "faint", style: "margin-top:8px" }, "last " + K.ago(g.last) + " ago · " + g.namespaces.join(", ")),
        h("div", { style: "margin-top:6px" }, g.objects.map((o) => h("span", { class: "chip" }, o)))))));
    });
  };

  // ---------------- kapp ----------------
  const stateChip = (ns) => h("span", { class: "chip state", title: "kapp keeps this app's state in this namespace" }, ns);
  const appHref = (a) => `#/kapp/${enc(a.cluster)}/${enc(a.namespace)}/${enc(a.name)}`;
  const GRAPH_TITLES = { 0: "Ingress", 1: "Services", 2: "Workloads", 3: "ReplicaSets / Jobs", 4: "Pods", 5: "Config & storage" };

  V.kapp = async function (main, _a, live) {
    const filter = h("input", { class: "filter", id: "filter", placeholder: "Filter apps by name, namespace or cluster   ( / )", spellcheck: "false" });
    const body = h("div", {});
    const sub = h("span", { class: "sub" });
    const unm = h("div", {});
    main.append(h("div", { class: "page-head" }, h("h1", {}, "kapp apps"), sub, h("span", { class: "grow" }),
      h("a", { class: "btn small", href: "#/topo/kapp" }, "Kapp topology"), h("button", { class: "btn small", onclick: () => showUnmanaged() }, "Not in kapp")),
      h("div", { class: "tools" }, filter), body, unm);
    body.append(h("div", { class: "empty" }, "Finding kapp apps in every namespace…"));
    let results;
    try { results = await K.api("/api/kapp/apps", { context: K.state.ctx }); } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    const apps = results.filter((r) => r.data).flatMap((r) => r.data);
    const skipped = results.reduce((n, r) => n + (r.skipped || 0), 0);
    function draw() {
      const f = filter.value.toLowerCase();
      const list = apps.filter((a) => !f || [a.name, a.namespace, a.cluster, ...(a.namespaces || [])].join(" ").toLowerCase().includes(f))
        .sort((a, b) => RANK[a.health] - RANK[b.health] || (a.cluster + a.namespace + a.name).localeCompare(b.cluster + b.namespace + b.name));
      sub.textContent = `${scope()} · ${list.length} of ${apps.length} apps`;
      const showCl = K.multi();
      body.replaceChildren(...errBanners(results),
        skipped ? h("div", { class: "banner warn" }, `${skipped} resource types could not be read (missing permission or a broken API). Counts may be low.`) : null,
        list.length ? h("table", { class: "cards" },
          h("thead", {}, h("tr", {}, h("th", {}, "App"), showCl ? h("th", {}, "Cluster") : null, h("th", {}, "State namespace"), h("th", {}, "Resources"), h("th", {}, "Runs in"), h("th", {}, "Health"), h("th", {}, "Last deploy"))),
          h("tbody", {}, list.map((a) => {
            const tr = h("tr", { class: "h-" + a.health, tabindex: 0 },
              h("td", { class: "name" }, a.name), showCl ? h("td", { class: "muted" }, a.cluster) : null,
              h("td", {}, stateChip(a.namespace)), h("td", { class: "mono" }, a.resources),
              h("td", {}, (a.namespaces || []).slice(0, 4).map((n) => h("span", { class: "chip" }, n)), (a.namespaces || []).length > 4 ? h("span", { class: "faint" }, "+" + (a.namespaces.length - 4)) : null),
              h("td", {}, a.bad ? K.pill("bad", a.bad + " failing") : null, " ", a.warn ? K.pill("warn", a.warn + " warning") : null, a.bad || a.warn ? null : K.pill(a.health, a.resources ? "healthy" : "empty")),
              h("td", { class: "muted", title: a.lastChange ? a.lastChange.description : "" },
                a.lastChange ? [a.lastChange.successful ? "✓ " : a.lastChange.finishedAt ? "✗ " : "… ", K.ago(a.lastChange.startedAt) + " ago"] : "–"));
            const go = () => (location.hash = appHref(a));
            tr.addEventListener("click", go);
            tr.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
            return tr;
          }))) : h("div", { class: "empty" }, apps.length ? "No app matches." : "No kapp apps found. kapp stores each app as a ConfigMap labelled kapp.k14s.io/is-app."));
    }
    filter.addEventListener("input", draw);
    draw();

    async function showUnmanaged() {
      unm.replaceChildren(h("div", { class: "group-head" }, "Workloads that no kapp app owns"), h("div", { class: "muted" }, "Looking…"));
      try {
        const r = await K.api("/api/kapp/unmanaged", { context: K.state.ctx });
        unm.replaceChildren(h("div", { class: "group-head" }, "Workloads that no kapp app owns (created by hand, Helm or another tool)"), ...errBanners(r));
        r.filter((x) => x.data).forEach((x) => {
          if (!x.data.length) { unm.append(h("div", { class: "muted" }, x.cluster + ": everything is in a kapp app.")); return; }
          unm.append(h("table", { class: "cards" }, h("tbody", {}, x.data.slice(0, 200).map((w) => h("tr", { class: "h-" + w.health, onclick: () => openRes({ kind: w.kind, resource: w.resource, namespace: w.namespace, name: w.name, health: w.health, detail: w.detail, group: "" }, x.cluster) },
            h("td", { class: "name" }, w.name), h("td", { class: "muted" }, w.kind), h("td", {}, h("span", { class: "chip" }, w.namespace)), h("td", { class: "muted" }, x.cluster))))));
        });
      } catch (e) { unm.replaceChildren(h("div", { class: "banner err" }, e.message)); }
    }
  };

  V.kappApp = async function (main, [cluster, ns, name], live, flavor) {
    flavor = flavor || "kapp";
    const helm = flavor === "helm", back = helm ? "#/topo/helm" : "#/kapp";
    main.append(head(name, "loading…"));
    let r;
    try { r = await K.api(helm ? "/api/helm/release" : "/api/kapp/app", { context: cluster, ns, name }); } catch (e) { main.replaceChildren(h("a", { href: back }, helm ? "← Helm topology" : "← All apps"), h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    const { info, graph } = r, app = info.app;
    const body = h("div", {});
    const tabs = h("div", { class: "tabs" });
    const panes = {
      topology() {
        const wrap = h("div", { class: "graph-wrap" });
        body.append(wrap);
        KGraph.render(wrap, graph, { titles: GRAPH_TITLES, empty: "This app owns no workloads, services or config to draw.", onClick: (n) => {
          if (n.count) return K.toast(n.count + " pods: " + (n.detail || "all ready"));
          if (n.missing) return K.toast(`${n.kind} ${n.name} is referenced but does not exist`, true);
          const k = kindBySingular(n.kind);
          if (k) K.openResource({ cluster, kind: k.name, ns: n.namespace, name: n.name, resource: k.name, health: n.health, detail: n.detail });
        } });
        body.append(h("div", { class: "legend" }, h("span", {}, h("i", { style: "border-color:var(--accent)" }), "routes traffic"), h("span", {}, h("i", { style: "border-color:var(--amber)" }), "selects pods"),
          h("span", {}, h("i", { style: "border-color:var(--muted)" }), "owns"), h("span", {}, h("i", { style: "border-top-style:dashed;border-color:var(--muted)" }), "mounts / reads"), h("span", {}, "red dashed box = referenced but missing")));
        if (graph.other.length) {
          const by = {};
          graph.other.forEach((o) => (by[o.kind] = (by[o.kind] || []).concat(o)));
          body.append(h("div", { class: "group-head" }, "Also in this app"));
          Object.keys(by).sort().forEach((k) => body.append(h("div", { style: "margin:4px 0" }, h("b", {}, k + " "), by[k].map((o) => h("span", { class: "chip", style: "cursor:pointer", title: o.detail || "", onclick: () => openRes({ kind: o.kind, name: o.name, namespace: o.namespace, group: o.group, health: o.health, detail: o.detail, resource: "" }, cluster) }, (o.namespace ? o.namespace + "/" : "") + o.name)))));
        }
      },
      resources() {
        body.append(h("table", { class: "cards" }, h("thead", {}, h("tr", {}, ["Kind", "Name", "Namespace", "Health", "Age"].map((c) => h("th", {}, c)))),
          h("tbody", {}, [...info.resources].sort((a, b) => RANK[a.health] - RANK[b.health] || a.kind.localeCompare(b.kind)).map((x) => h("tr", { class: "h-" + x.health, onclick: () => openRes(x, cluster) },
            h("td", { class: "muted" }, x.kind), h("td", { class: "name" }, x.name), h("td", {}, x.namespace ? h("span", { class: "chip" }, x.namespace) : h("span", { class: "faint" }, "cluster")),
            h("td", {}, K.pill(x.health, x.detail || x.health)), h("td", { class: "muted" }, K.ago(x.created)))))));
      },
      changes() {
        if (!info.changes.length) return body.append(h("div", { class: "empty" }, helm ? "No revisions found." : "No recorded deploys (kapp keeps the last few, or none if app changes are disabled)."));
        body.append(h("table", { class: "cards" }, h("thead", {}, h("tr", {}, ["When", "Result", "Took", "Description", "Namespaces"].map((c) => h("th", {}, c)))),
          h("tbody", {}, info.changes.map((c) => {
            const took = c.finishedAt ? Math.round((Date.parse(c.finishedAt) - Date.parse(c.startedAt)) / 1000) + "s" : "";
            return h("tr", { class: c.successful ? "h-ok" : c.finishedAt ? "h-bad" : "h-warn" },
              h("td", { title: c.startedAt }, K.ago(c.startedAt) + " ago"), h("td", {}, K.pill(c.successful ? "ok" : c.finishedAt ? "bad" : "warn", c.successful ? "succeeded" : c.finishedAt ? "failed" : "unfinished")),
              h("td", { class: "mono" }, took), h("td", { title: c.description }, c.description), h("td", {}, (c.namespaces || []).map((n) => h("span", { class: "chip" }, n))));
          }))));
      },
      state() {
        body.append(h("div", { class: "group-head" }, "Handy commands"), ...info.commands.map((c) => h("div", { class: "actions" }, h("code", { class: "mono" }, c), h("button", { class: "btn small", onclick: () => K.copy(c) }, "Copy"))),
          h("div", { class: "group-head" }, "State ConfigMap (what kapp stores)"), h("pre", { class: "code" }, info.state));
      },
    };
    const show = (id) => { [...tabs.children].forEach((b) => b.classList.toggle("on", b.dataset.id === id)); body.replaceChildren(); panes[id](); };
    [["topology", "Topology"], ["resources", `Resources (${app.resources})`], ["changes", `${helm ? "Revisions" : "Deploys"} (${info.changes.length})`], ["state", "State & commands"]]
      .forEach(([id, label]) => tabs.append(h("button", { "data-id": id, onclick: () => show(id) }, label)));
    main.replaceChildren(h("a", { href: back }, helm ? "← Helm topology" : "← All apps"),
      h("div", { class: "page-head", style: "margin-top:8px" }, h("h1", {}, app.name), K.pill(app.health, app.health === "idle" ? "no resources" : app.health), h("span", { class: "grow" }),
        h("span", { class: "sub" }, cluster)),
      h("div", { class: "stat" }, helm ? K.pill(app.status === "failed" ? "bad" : app.status === "deployed" ? "ok" : "warn", `helm · ${app.status} · revision ${app.revision}`) : stateChip(app.namespace), h("span", { class: "muted" }, `${app.resources} resources in`), (app.namespaces || []).map((n) => h("span", { class: "chip" }, n)),
        app.lastChange ? h("span", { class: "muted" }, `· last ${helm ? "revision" : "deploy"} ${app.lastChange.successful ? "succeeded" : app.lastChange.finishedAt ? "failed" : "unfinished"} ${K.ago(app.lastChange.startedAt)} ago`) : null),
      info.skipped ? h("div", { class: "banner warn" }, `${info.skipped} resource types could not be read. Some objects may be missing from this view.`) : null,
      tabs, body);
    show("topology");
  };

  // Apps (kapp) or releases (Helm) on the left, the namespaces they touch on the right.
  const mapView = (flavor) => async function (main, _a, live) {
    const helm = flavor === "helm", label = helm ? "Helm" : "kapp", noun = helm ? "release" : "app";
    main.append(head(label + " topology", scope()), h("div", { class: "empty" }, helm ? "Finding Helm releases…" : "Finding every kapp app…"));
    let results;
    try { results = await K.api(helm ? "/api/helm/releases" : "/api/kapp/apps", { context: K.state.ctx }); } catch (e) { main.append(h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    main.replaceChildren(head(label + " topology", `${scope()} · ${noun}s and the namespaces they run in`), ...errBanners(results));
    results.filter((r) => r.data).forEach((r) => {
      const nodes = [], edges = [], nsNodes = {}, nsHealth = {}, nsApps = {};
      r.data.forEach((a) => {
        const id = "app:" + a.namespace + "/" + a.name;
        nodes.push({ id, kind: helm ? "Helm release" : "kapp app", name: a.name, namespace: a.namespace, layer: 0, health: a.health,
          detail: helm ? `rev ${a.revision} · ${a.status} · ${a.resources} objects` : `${a.resources} resources`, _app: a });
        // kapp keeps its state in a namespace that may differ from where the app runs; Helm's secrets live in the release namespace
        const targets = new Set([...(a.namespaces || []), a.namespace]);
        targets.forEach((n) => {
          nsApps[n] = (nsApps[n] || 0) + 1;
          nsNodes[n] = true;
          if ((RANK[a.health] ?? 3) < (RANK[nsHealth[n]] ?? 3)) nsHealth[n] = a.health;
          const c = (a.nsCounts || {})[n] || 0, isState = n === a.namespace;
          edges.push({ from: id, to: "ns:" + n, type: c ? "routes" : "state", label: [c ? c + " objects" : "", isState ? (helm ? "release" : "state") : ""].filter(Boolean).join(" · ") });
        });
      });
      Object.keys(nsNodes).forEach((n) => nodes.push({ id: "ns:" + n, kind: "Namespace", name: n, layer: 1, health: nsHealth[n] || "ok", detail: `${nsApps[n]} ${noun}${nsApps[n] > 1 ? "s" : ""}`, _ns: n }));
      if (K.multi()) main.append(h("div", { class: "group-head" }, r.cluster));
      const wrap = h("div", { class: "graph-wrap" });
      main.append(wrap);
      KGraph.render(wrap, { nodes, edges }, { titles: { 0: helm ? "Helm releases" : "kapp apps", 1: "Namespaces" }, empty: helm ? "No Helm releases in this cluster." : "No kapp apps in this cluster.", onClick: (n) => {
        if (n._app) location.hash = `#/${flavor}/${enc(n._app.cluster)}/${enc(n._app.namespace)}/${enc(n._app.name)}`;
        else if (n._ns) K.gotoNs(r.cluster, n._ns, "#/topo/namespace");
      } });
    });
    main.append(h("div", { class: "legend" }, h("span", {}, h("i", { style: "border-color:var(--accent)" }), `${noun} owns objects in namespace`),
      h("span", {}, h("i", { style: "border-top-style:dashed;border-color:var(--muted)" }), helm ? "namespace holds only the release record" : "namespace holds only the app's state")));
  };
  V.topoKapp = mapView("kapp");
  V.topoHelm = mapView("helm");
  V.helmApp = (main, a, live) => V.kappApp(main, a, live, "helm");

  // single-cluster pages use the picked cluster, or the current one when "All clusters" is picked
  const oneCluster = () => (!K.multi() ? K.state.ctx : (K.state.ctxs.find((c) => c.current) || K.state.ctxs[0] || {}).name);

  // node pools -> nodes -> namespaces
  V.topoCluster = async function (main, _a, live) {
    const cluster = oneCluster();
    const sub = `${cluster}${K.multi() ? " (pick a cluster above to change)" : ""}`;
    main.append(head("Cluster topology", sub), h("div", { class: "empty" }, "Reading nodes and pods…"));
    let g;
    try { g = await K.api("/api/topology/cluster", { context: cluster }); } catch (e) { main.replaceChildren(head("Cluster topology", cluster), h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    main.replaceChildren(head("Cluster topology", sub));
    const wrap = h("div", { class: "graph-wrap" });
    main.append(wrap, h("div", { class: "legend" }, h("span", {}, h("i", { style: "border-color:var(--accent)" }), "namespace has pods on the node (DaemonSet pods are not drawn)"),
      h("span", {}, "node colour = readiness · namespace colour = worst pod in it")));
    KGraph.render(wrap, g, { titles: { 0: "Node pools", 1: "Nodes", 2: "Namespaces" }, empty: "No nodes found.", onClick: (n) => {
      if (n.kind === "Node" && !n.missing) K.openResource({ cluster, kind: "nodes", ns: "", name: n.name, resource: "nodes", health: n.health, detail: n.detail });
      else if (n.kind === "Namespace") K.gotoNs(cluster, n.name, "#/topo/namespace");
    } });
  };

  // one namespace: Ingress -> Service -> workload -> pods -> config, whatever created it
  V.topoNamespace = async function (main, _a, live) {
    const cluster = oneCluster(), ns = K.state.ns;
    const sub = `${cluster} · ${ns || "no namespace"}`;
    main.append(head("Namespace topology", sub));
    if (!ns) {
      let list = [];
      try { list = await K.api("/api/namespaces", { context: cluster }); } catch (e) { main.append(h("div", { class: "banner err" }, e.message)); return; }
      if (!live()) return;
      main.append(h("div", { class: "empty" }, "Pick a namespace in the top bar, or here:"),
        h("div", { style: "text-align:center" }, list.map((n) => h("button", { class: "btn small", style: "margin:3px", onclick: () => {
          K.state.ns = n; localStorage.setItem("knd.ns", n);
          const sel = document.getElementById("ns"); if (sel.disabled) { sel.disabled = false; } sel.value = n; K.refresh(); } }, n))));
      return;
    }
    main.append(h("div", { class: "empty" }, "Reading the namespace…"));
    let g;
    try { g = await K.api("/api/topology/namespace", { context: cluster, ns }); } catch (e) { main.replaceChildren(head("Namespace topology", sub), h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    main.replaceChildren(head("Namespace topology", sub));
    const wrap = h("div", { class: "graph-wrap" });
    main.append(wrap, h("div", { class: "legend" }, h("span", {}, h("i", { style: "border-color:var(--accent)" }), "routes traffic"), h("span", {}, h("i", { style: "border-color:var(--amber)" }), "selects pods"),
      h("span", {}, h("i", { style: "border-color:var(--muted)" }), "owns"), h("span", {}, h("i", { style: "border-top-style:dashed;border-color:var(--muted)" }), "mounts / reads"), h("span", {}, "red dashed box = referenced but missing")));
    KGraph.render(wrap, g, { titles: GRAPH_TITLES, externalLabel: "", empty: "Nothing runs in this namespace.", onClick: (n) => {
      if (n.count) return K.toast(n.count + " pods: " + (n.detail || "all ready"));
      if (n.missing) return K.toast(`${n.kind} ${n.name} is referenced but does not exist`, true);
      const k = kindBySingular(n.kind);
      if (k) K.openResource({ cluster, kind: k.name, ns: n.namespace, name: n.name, resource: k.name, health: n.health, detail: n.detail });
    } });
  };

  // ---------------- Istio ----------------
  V.istio = async function (main, _a, live) {
    let cluster = K.state.ctx;
    if (K.multi()) cluster = (K.state.ctxs.find((c) => c.current) || K.state.ctxs[0] || {}).name;
    main.append(head("Istio topology", `${cluster} · ${nsScope()}`), h("div", { class: "empty" }, "Reading gateways, virtual services and destination rules…"));
    let r;
    try { r = await K.api("/api/istio", { context: cluster, ns: K.state.ns }); } catch (e) { main.replaceChildren(head("Istio topology", cluster), h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    main.replaceChildren(head("Istio topology", `${cluster} · ${nsScope()}${K.multi() ? " (pick a cluster above to change)" : ""}`));
    if (!r.installed) { main.append(h("div", { class: "empty" }, "Istio resources (networking.istio.io) are not installed in this cluster.")); return; }
    main.append(h("div", { class: "stat" }, Object.entries(r.counts).map(([k, v]) => h("span", { class: "chip" }, v + " " + k))));
    const wrap = h("div", { class: "graph-wrap" });
    main.append(wrap);
    const plural = { Gateway: "gateways", VirtualService: "virtualservices", DestinationRule: "destinationrules", ServiceEntry: "serviceentries" };
    KGraph.render(wrap, r.graph, { titles: { 0: "Gateways", 1: "Virtual services", 2: "Destination rules", 3: "Services & hosts", 4: "Pods" }, empty: "No Istio gateways or virtual services here.", onClick: (n) => {
      if (n.missing) return K.toast(`${n.kind} ${n.name} is referenced but not found`, true);
      if (n.count) return K.toast(`${n.count} pods · ${n.detail}`);
      if (n.kind === "Service") return K.openResource({ cluster, kind: "services", ns: n.namespace, name: n.name, resource: "services", health: n.health, detail: n.detail });
      if (plural[n.kind]) K.openResource({ cluster, kind: "", ns: n.namespace, name: n.name, group: "networking.istio.io", version: "", resource: plural[n.kind], title: n.kind, health: n.health, detail: n.detail });
    } });
    main.append(h("div", { class: "legend" }, h("span", {}, h("i", { style: "border-color:var(--accent)" }), "routes (label: path · port · subset · weight)"), h("span", {}, h("i", { style: "border-color:var(--amber)" }), "applies to / selects pods"),
      h("span", {}, "red dashed = referenced but missing")));
  };
})();
