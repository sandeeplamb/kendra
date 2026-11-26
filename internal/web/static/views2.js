// More views: namespace overview, custom resources, Helm releases and charts, port forwarding.
(function () {
  const { h } = K;
  const V = K.views;
  const enc = encodeURIComponent;
  const RANK = { bad: 0, warn: 1, ok: 2, idle: 3 };
  const head = (title, sub, ...right) => h("div", { class: "page-head" }, h("h1", {}, title), h("span", { class: "sub" }, sub), h("span", { class: "grow" }), right);
  const scope = () => K.scopeLabel(K.state.ctx);
  const errBanners = (results) => results.filter((r) => r.error).map((r) => h("div", { class: /is not installed/.test(r.error) ? "banner" : "banner err" }, h("b", {}, r.cluster + ": "), r.error));
  const num = (n) => (n ? h("span", { class: "mono" }, n) : h("span", { class: "faint" }, "–"));

  // ---------------- namespace overview ----------------
  V.overview = async function (main, _a, live) {
    const filter = h("input", { class: "filter", id: "filter", placeholder: "Filter namespaces   ( / )", spellcheck: "false" });
    const body = h("div", {});
    const sub = h("span", { class: "sub" });
    main.append(h("div", { class: "page-head" }, h("h1", {}, "Namespace overview"), sub), h("div", { class: "tools" }, filter), body, );
    body.append(h("div", { class: "empty" }, "Counting what runs in every namespace…"));
    let results;
    try { results = await K.api("/api/overview", { context: K.state.ctx }); } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    const rows = results.filter((r) => r.data).flatMap((r) => r.data);
    let hist = {};
    try { hist = await K.history([...new Set(rows.map((r) => r.cluster))].slice(0, 10)); } catch (e) { /* no history yet */ }
    const trend = (o) => { const smp = hist[o.cluster] || []; return smp.length >= 3 ? smp.map((x) => (x.nsBad && x.nsBad[o.name]) || 0) : null; };
    const showTrend = rows.some((o) => trend(o));
    const draw = () => {
      const f = filter.value.toLowerCase();
      const list = rows.filter((r) => !f || (r.name + " " + r.cluster).toLowerCase().includes(f))
        .sort((a, b) => RANK[a.health] - RANK[b.health] || (a.cluster + a.name).localeCompare(b.cluster + b.name));
      sub.textContent = `${scope()} · ${list.length} of ${rows.length} namespaces`;
      const showCl = K.multi();
      body.replaceChildren(...errBanners(results), list.length ? h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, h("th", {}, "Namespace"), showCl ? h("th", {}, "Cluster") : null,
          ["Health", "Pods", showTrend ? "Failing pods, recent" : null, "Workloads", "Services", "Ingresses", "ConfigMaps", "Secrets", "Claims", "Age"].filter(Boolean).map((c) => h("th", {}, c)))),
        h("tbody", {}, list.map((o) => {
          const tr = h("tr", { class: "h-" + o.health, tabindex: 0 },
            h("td", { class: "name" }, o.name), showCl ? h("td", { class: "muted" }, o.cluster) : null,
            h("td", {}, K.pill(o.health, o.phase === "Terminating" ? "terminating" : o.health === "idle" ? "empty" : o.health === "ok" ? "healthy" : o.health === "warn" ? "warning" : "failing")),
            h("td", {}, num(o.pods), o.podsBad ? [" ", K.pill("bad", o.podsBad + " failing")] : null, o.podsWarn ? [" ", K.pill("warn", o.podsWarn + " pending")] : null),
            showTrend ? h("td", {}, trend(o) ? K.spark(trend(o), { cls: Math.max(...trend(o)) > 0 ? "bad" : "ok", w: 84 }) : h("span", { class: "faint" }, "–")) : null,
            h("td", {}, num(o.workloads), o.workloadsBad ? [" ", K.pill("warn", o.workloadsBad + " not ready")] : null),
            h("td", {}, num(o.services)), h("td", {}, num(o.ingresses)), h("td", {}, num(o.configMaps)), h("td", {}, num(o.secrets)), h("td", {}, num(o.claims)),
            h("td", { class: "muted", title: o.created }, K.ago(o.created)));
          const go = () => K.gotoNs(o.cluster, o.name, "#/r/pods");
          tr.addEventListener("click", go);
          tr.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
          return tr;
        }))) : h("div", { class: "empty" }, "No namespace matches."));
    };
    filter.addEventListener("input", draw);
    draw();
  };

  // ---------------- custom resources ----------------
  // #/cr/<group>/<plural>: the instances of one CRD, listed like any other resource
  V.cr = async function (main, [group, plural], live) {
    let crd = (K.state.crds || []).find((c) => c.group === group && c.plural === plural);
    if (!crd) {
      try {
        K.state.crds = await K.api("/api/crds", { context: K.multi() ? K.state.ctxs.find((c) => c.current).name : K.state.ctx });
        crd = K.state.crds.find((c) => c.group === group && c.plural === plural);
      } catch (e) { main.append(h("div", { class: "banner err" }, e.message)); return; }
    }
    if (!live()) return;
    const title = crd ? crd.kind : plural;
    return V.list(main, { group, resource: plural, title, namespaced: crd ? crd.scope === "Namespaced" : true }, live);
  };

  // ---------------- Helm releases and charts ----------------
  const statusHealth = (s) => (s === "deployed" ? "ok" : s === "failed" ? "bad" : s === "superseded" ? "idle" : "warn");

  V.helmReleases = async function (main, _a, live) {
    const filter = h("input", { class: "filter", id: "filter", placeholder: "Filter releases by name, chart, namespace or cluster   ( / )", spellcheck: "false" });
    const body = h("div", {});
    const sub = h("span", { class: "sub" });
    main.append(h("div", { class: "page-head" }, h("h1", {}, "Helm releases"), sub, h("span", { class: "grow" }), h("a", { class: "btn small", href: "#/topo/helm" }, "Helm topology")), h("div", { class: "tools" }, filter), body);
    body.append(h("div", { class: "empty" }, "Finding Helm releases…"));
    let results;
    try { results = await K.api("/api/helm/releases", { context: K.state.ctx }); } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    const rows = results.filter((r) => r.data).flatMap((r) => r.data);
    const draw = () => {
      const f = filter.value.toLowerCase();
      const list = rows.filter((a) => !f || [a.name, a.namespace, a.cluster, a.chart, a.status].join(" ").toLowerCase().includes(f))
        .sort((a, b) => RANK[a.health] - RANK[b.health] || (a.cluster + a.namespace + a.name).localeCompare(b.cluster + b.namespace + b.name));
      sub.textContent = `${scope()} · ${list.length} of ${rows.length} releases`;
      const showCl = K.multi();
      body.replaceChildren(...errBanners(results), list.length ? h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, h("th", {}, "Release"), showCl ? h("th", {}, "Cluster") : null, ["Namespace", "Chart", "App version", "Revision", "Status", "Objects", "Updated"].map((c) => h("th", {}, c)))),
        h("tbody", {}, list.map((a) => {
          const tr = h("tr", { class: "h-" + a.health, tabindex: 0 },
            h("td", { class: "name" }, a.name), showCl ? h("td", { class: "muted" }, a.cluster) : null,
            h("td", {}, h("span", { class: "chip" }, a.namespace)), h("td", { class: "mono" }, a.chart || h("span", { class: "faint" }, "unknown")),
            h("td", { class: "mono" }, a.appVersion || ""), h("td", { class: "mono" }, a.revision),
            h("td", {}, K.pill(a.health === "ok" || a.health === "idle" ? statusHealth(a.status) : a.health, a.status)),
            h("td", {}, a.bad ? K.pill("bad", a.bad + " failing") : null, a.warn ? [" ", K.pill("warn", a.warn + " warning")] : null, a.bad || a.warn ? null : h("span", { class: "mono" }, a.resources)),
            h("td", { class: "muted", title: a.created }, K.ago(a.created) + " ago"));
          const go = () => (location.hash = `#/helm/${enc(a.cluster)}/${enc(a.namespace)}/${enc(a.name)}`);
          tr.addEventListener("click", go);
          tr.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
          return tr;
        }))) : h("div", { class: "empty" }, rows.length ? "No release matches." : "No Helm releases found. Helm stores each release as a Secret labelled owner=helm."));
    };
    filter.addEventListener("input", draw);
    draw();
  };

  V.helmCharts = async function (main, _a, live) {
    const filter = h("input", { class: "filter", id: "filter", placeholder: "Filter charts   ( / )", spellcheck: "false" });
    const inUse = h("div", {}), repo = h("div", {});
    main.append(head("Helm charts", scope()), h("div", { class: "tools" }, filter),
      h("div", { class: "group-head" }, "In use in your clusters"), inUse, h("div", { class: "group-head" }, "In your local Helm repositories"), repo);
    inUse.append(h("div", { class: "muted" }, "Reading releases…"));
    let rel, local;
    try { [rel, local] = await Promise.all([K.api("/api/helm/releases", { context: K.state.ctx }), K.api("/api/helm/charts")]); } catch (e) { inUse.replaceChildren(h("div", { class: "banner err" }, e.message)); return; }
    if (!live()) return;
    const used = {};
    rel.filter((r) => r.data).flatMap((r) => r.data).forEach((a) => {
      const k = a.chart || "(chart unknown)";
      (used[k] = used[k] || []).push(a);
    });
    const draw = () => {
      const f = filter.value.toLowerCase();
      const names = Object.keys(used).filter((k) => !f || k.toLowerCase().includes(f)).sort();
      inUse.replaceChildren(...errBanners(rel), names.length ? h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, ["Chart", "App version", "Releases"].map((c) => h("th", {}, c)))),
        h("tbody", {}, names.map((k) => h("tr", { class: "h-" + used[k].map((a) => a.health).sort((x, y) => RANK[x] - RANK[y])[0] },
          h("td", { class: "name mono" }, k), h("td", { class: "mono" }, [...new Set(used[k].map((a) => a.appVersion).filter(Boolean))].join(", ")),
          h("td", {}, used[k].map((a) => h("a", { class: "chip", href: `#/helm/${enc(a.cluster)}/${enc(a.namespace)}/${enc(a.name)}` }, (K.multi() ? a.cluster + " · " : "") + a.namespace + "/" + a.name)))))))
        : h("div", { class: "muted" }, "No release uses a chart that Kendra can name. The name comes from the helm.sh/chart label that most charts put on their objects."));
      const cs = local.charts.filter((c) => !f || (c.name + " " + c.repo + " " + c.description).toLowerCase().includes(f)).slice(0, 400);
      repo.replaceChildren(local.note ? h("div", { class: "banner warn" }, local.note) : null,
        local.repos.length ? h("div", { class: "stat" }, local.repos.map((r) => h("span", { class: "chip", title: r.url }, r.name))) : null,
        cs.length ? h("table", { class: "cards" }, h("thead", {}, h("tr", {}, ["Chart", "Repository", "Version", "App version", "Description"].map((c) => h("th", {}, c)))),
          h("tbody", {}, cs.map((c) => h("tr", { class: c.deprecated ? "h-warn" : "" }, h("td", { class: "name" }, c.name), h("td", {}, h("span", { class: "chip" }, c.repo)),
            h("td", { class: "mono" }, c.version), h("td", { class: "mono" }, c.appVersion || ""), h("td", { class: "muted", title: c.description }, c.description))))) : null,
        local.charts.length > 400 ? h("div", { class: "faint" }, "Showing the first 400. Use the filter to narrow down.") : null);
    };
    filter.addEventListener("input", draw);
    draw();
  };

  // ---------------- port forwarding ----------------
  V.forwards = async function (main, _a, live) {
    const cluster = !K.multi() ? K.state.ctx : (K.state.ctxs.find((c) => c.current) || K.state.ctxs[0]).name;
    const ctxSel = h("select", {}, K.state.ctxs.map((c) => h("option", { value: c.name }, (c.prod ? "⚠ " : "") + c.name)));
    ctxSel.value = cluster;
    const kind = h("select", {}, h("option", { value: "services" }, "Service"), h("option", { value: "pods" }, "Pod"));
    const ns = h("input", { type: "text", placeholder: "namespace", value: K.state.ns || "", spellcheck: "false" });
    const name = h("input", { type: "text", placeholder: "name", spellcheck: "false" });
    const remote = h("input", { type: "number", min: 1, max: 65535, placeholder: "remote port" });
    const local = h("input", { type: "number", min: 0, max: 65535, placeholder: "any" });
    const msg = h("div", {});
    const table = h("div", {});
    const start = h("button", { class: "btn primary", onclick: async () => {
      start.disabled = true;
      try {
        const f = await K.api("/api/forward", {}, { context: ctxSel.value, kind: kind.value, namespace: ns.value.trim(), name: name.value.trim(),
          remotePort: parseInt(remote.value || "0", 10), localPort: parseInt(local.value || "0", 10) });
        msg.replaceChildren(h("div", { class: "banner" }, "Forwarding ", h("b", { class: "mono" }, f.address), " → ", f.target + ":" + f.remotePort));
        load();
      } catch (e) { msg.replaceChildren(h("div", { class: "banner err" }, e.message)); }
      start.disabled = false;
    } }, "Start forwarding");
    main.append(head("Port forwarding", "opens ports on 127.0.0.1 only, until Kendra exits"),
      h("div", { class: "card", style: "margin-bottom:14px" }, h("div", { class: "form-row" }, h("label", {}, "Cluster", ctxSel), h("label", {}, "Type", kind), h("label", {}, "Namespace", ns), h("label", {}, "Name", name),
        h("label", {}, "Remote", remote), h("label", {}, "Local", local), start), msg), table);
    const load = async () => {
      let l;
      try { l = await K.api("/api/forwards"); } catch (e) { table.replaceChildren(h("div", { class: "banner err" }, e.message)); return; }
      if (!live()) return;
      table.replaceChildren(l.length ? h("table", { class: "cards" }, h("thead", {}, h("tr", {}, ["Local address", "Target", "Pod", "Cluster", "Started", "Status", ""].map((c) => h("th", {}, c)))),
        h("tbody", {}, l.map((f) => h("tr", { class: f.active ? "h-ok" : "h-bad" },
          h("td", {}, f.active ? h("a", { class: "mono", href: "http://" + f.address, target: "_blank", rel: "noopener" }, f.address) : h("span", { class: "mono faint" }, f.address)),
          h("td", { class: "name" }, f.target + ":" + f.remotePort), h("td", { class: "muted" }, f.namespace + "/" + f.pod), h("td", { class: "muted" }, f.cluster),
          h("td", { class: "muted" }, K.ago(f.started) + " ago"), h("td", {}, f.active ? K.pill("ok", "active") : K.pill("bad", f.error || "ended")),
          h("td", {}, h("button", { class: "btn small", onclick: async () => { try { await K.api("/api/unforward", {}, { id: f.id }); load(); } catch (e) { K.toast(e.message, true); } } }, "Stop")))))) : h("div", { class: "empty" }, "No port forwards yet. You can also start one from a pod or service (Forward port…)."));
    };
    await load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  };

  // ---------------- start screen ----------------
  // A ship's wheel with seven spokes (the helm of "Kubernetes", Greek for helmsman) that turns, with a pulsing glow, ripples and orbiting dots.
  function wheel() {
    const NS = "http://www.w3.org/2000/svg";
    const el = (tag, attrs, ...kids) => {
      const e = document.createElementNS(NS, tag);
      for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
      kids.forEach((c) => c && e.append(c));
      return e;
    };
    const spokes = [];
    for (let k = 0; k < 7; k++) {
      const a = ((k * 360) / 7 - 90) * (Math.PI / 180), c = Math.cos(a), s = Math.sin(a);
      spokes.push(el("line", { class: "w-spoke", x1: 100 + 30 * c, y1: 100 + 30 * s, x2: 100 + 90 * c, y2: 100 + 90 * s }));
      spokes.push(el("circle", { class: "w-handle", style: "animation-delay:" + (k * 0.35).toFixed(2) + "s", cx: 100 + 92 * c, cy: 100 + 92 * s, r: 6.5 }));
    }
    return el("svg", { class: "wheel", viewBox: "0 0 200 200", role: "img", "aria-label": "A Kubernetes helm wheel, turning" },
      el("defs", {}, el("radialGradient", { id: "glow" }, el("stop", { offset: "0", "stop-color": "var(--accent)", "stop-opacity": ".28" }), el("stop", { offset: "1", "stop-color": "var(--accent)", "stop-opacity": "0" }))),
      el("circle", { class: "w-glow", cx: 100, cy: 100, r: 100, fill: "url(#glow)" }),
      el("circle", { class: "w-ripple", cx: 100, cy: 100, r: 13 }), el("circle", { class: "w-ripple d2", cx: 100, cy: 100, r: 13 }),
      el("g", { class: "w-orbit" }, el("circle", { class: "w-dot", cx: 100, cy: 4, r: 3.5 }), el("circle", { class: "w-dot b", cx: 196, cy: 100, r: 2.5 }), el("circle", { class: "w-dot c", cx: 40, cy: 183, r: 3 })),
      el("g", { class: "w-turn" }, el("circle", { class: "w-ring", cx: 100, cy: 100, r: 78 }), el("circle", { class: "w-ring thin", cx: 100, cy: 100, r: 30 }), ...spokes),
      el("circle", { class: "w-hub", cx: 100, cy: 100, r: 13 }), el("circle", { class: "w-eye", cx: 100, cy: 100, r: 5 }));
  }

  // Moving background for the start screen: drifting nodes joined by faint links, with
  // small pulses travelling along them (a cluster network). Stops when the page is left.
  function network(box) {
    const cv = document.createElement("canvas");
    cv.className = "bg-net"; cv.setAttribute("aria-hidden", "true");
    const ctx = cv.getContext("2d");
    if (!ctx || matchMedia("(prefers-reduced-motion: reduce)").matches) return cv;
    let W = 0, H = 0, nodes = [], pulses = [], col = "10,140,110", amb = "217,138,31", last = 0;
    const rgb = (v, d) => { const m = /^#?([0-9a-f]{6})$/i.exec(v.trim()); return m ? [0, 2, 4].map((i) => parseInt(m[1].substr(i, 2), 16)).join(",") : d; };
    const colors = () => { const cs = getComputedStyle(document.documentElement); col = rgb(cs.getPropertyValue("--accent"), col); amb = rgb(cs.getPropertyValue("--amber"), amb); };
    const size = () => {
      const r = box.getBoundingClientRect(), dpr = window.devicePixelRatio || 1;
      W = Math.max(1, r.width); H = Math.max(1, r.height);
      cv.width = W * dpr; cv.height = H * dpr; ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      const n = Math.max(10, Math.min(34, Math.round((W * H) / 38000)));
      nodes = Array.from({ length: n }, () => ({ x: Math.random() * W, y: Math.random() * H, vx: (Math.random() - .5) * .5, vy: (Math.random() - .5) * .5, r: 1.5 + Math.random() * 2 }));
    };
    const frame = (t) => {
      if (!cv.isConnected) return;
      requestAnimationFrame(frame);
      if (t - last < 40) return; // ~25 fps is plenty for slow motion
      last = t;
      if (Math.abs(box.clientWidth - W) > 2 || Math.abs(box.clientHeight - H) > 2) size();
      if (((t / 33) | 0) % 90 === 0) colors();
      ctx.clearRect(0, 0, W, H);
      const L = 150;
      for (const a of nodes) {
        a.x += a.vx; a.y += a.vy;
        if (a.x < -10) a.x = W + 10; else if (a.x > W + 10) a.x = -10;
        if (a.y < -10) a.y = H + 10; else if (a.y > H + 10) a.y = -10;
      }
      for (let i = 0; i < nodes.length; i++) for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j], d = Math.hypot(a.x - b.x, a.y - b.y);
        if (d < L) {
          ctx.strokeStyle = "rgba(" + col + "," + (0.14 * (1 - d / L)).toFixed(3) + ")"; ctx.lineWidth = 1;
          ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke();
          if (pulses.length < 4 && Math.random() < 0.0004) pulses.push({ a, b, p: 0, s: 0.008 + Math.random() * 0.008, amber: Math.random() < 0.25 });
        }
      }
      for (const n of nodes) { ctx.fillStyle = "rgba(" + col + ",.3)"; ctx.beginPath(); ctx.arc(n.x, n.y, n.r, 0, 6.2832); ctx.fill(); }
      pulses = pulses.filter((q) => (q.p += q.s) < 1);
      for (const q of pulses) {
        const x = q.a.x + (q.b.x - q.a.x) * q.p, y = q.a.y + (q.b.y - q.a.y) * q.p, c = q.amber ? amb : col;
        const g = ctx.createRadialGradient(x, y, 0, x, y, 9);
        g.addColorStop(0, "rgba(" + c + ",.55)"); g.addColorStop(1, "rgba(" + c + ",0)");
        ctx.fillStyle = g; ctx.beginPath(); ctx.arc(x, y, 9, 0, 6.2832); ctx.fill();
      }
    };
    colors();
    requestAnimationFrame(() => { size(); requestAnimationFrame(frame); });
    return cv;
  }

  // ---------------- fleet: every cluster at a glance, versions, deprecated APIs ----------------
  // One request per cluster, 6 at a time, so cards fill in as clusters answer and a slow or
  // dead cluster never holds the others back. Answers are kept for a minute.
  const fleetCache = {};
  function loadPer(endpoint, names, live, onEach, force) {
    const todo = [];
    names.forEach((n) => {
      const hit = fleetCache[endpoint + "|" + n];
      if (hit && !force && Date.now() - hit.at < 60000) onEach(n, hit.r); else todo.push(n);
    });
    let next = 0;
    const worker = async () => {
      while (next < todo.length && live()) {
        const n = todo[next++];
        let r;
        try { r = (await K.api(endpoint, { context: n }))[0] || { cluster: n, error: "No answer." }; } catch (e) { r = { cluster: n, error: e.message }; }
        if (!live()) return;
        fleetCache[endpoint + "|" + n] = { at: Date.now(), r };
        onEach(n, r);
      }
    };
    return Promise.all(Array.from({ length: Math.min(6, todo.length) }, worker));
  }
  // the clusters a fleet page covers: the picked group, else every cluster
  const fleetNames = () => K.pinsFirst(K.multi() ? K.members(K.state.ctx) : K.state.ctxs.map((c) => c.name));
  const ctxOf = (n) => K.state.ctxs.find((c) => c.name === n) || { name: n };
  const tagsOf = (n) => [h("span", { class: "ctx-badge " + K.provider(ctxOf(n))[1] }, K.provider(ctxOf(n))[0]), ...K.envTags(ctxOf(n)).map(([t, k]) => h("span", { class: "ctx-flag " + k }, t))];
  const shortVer = (v) => (v || "").replace(/^v/, "").replace(/-(eks|gke|aks).*$/i, "");
  const fleetHealth = (r) => (!r ? "load" : r.error ? "err" : r.data.health);

  K.fleet = { loadPer, fleetNames, tagsOf, shortVer, errBanners, scopeNames: () => (K.multi() ? K.members(K.state.ctx) : [K.state.ctx]) };

  V.fleet = async function (main, _a, live) {
    const names = fleetNames();
    const filter = h("input", { class: "filter", id: "filter", placeholder: "Filter clusters   ( / )", spellcheck: "false" });
    const sub = h("span", { class: "sub" });
    const strip = h("div", { class: "fl-strip" });
    const grid = h("div", { class: "fl-grid" });
    const again = h("button", { class: "btn small", onclick: () => K.refresh() }, "Refresh");
    main.append(h("div", { class: "page-head" }, h("h1", {}, "Fleet dashboard"), sub, h("span", { class: "grow" }), again),
      h("div", { class: "tools" }, filter), strip, grid);
    const res = {};
    let hist = {};
    const watched = names.slice(0, 10); // the pinned and first clusters are sampled for trends
    K.watch(watched);
    const loadHist = async () => { try { hist = await K.history(watched); if (live()) draw(); } catch (e) { /* none yet */ } };
    let status = "", sorted = false, doneN = 0;
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      const cnt = { ok: 0, warn: 0, bad: 0, err: 0, load: 0 };
      names.forEach((n) => cnt[fleetHealth(res[n])]++);
      let nodes = 0, ready = 0, podsBad = 0;
      names.forEach((n) => { const d = res[n] && res[n].data; if (d) { nodes += d.nodes; ready += d.nodesReady; podsBad += d.podsBad; } });
      sub.textContent = `${K.scopeLabel(K.multi() ? K.state.ctx : "*")} · ${names.length} cluster${names.length === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      const chip = (k, label, cls) => h("button", { class: "fl-chip " + cls + (status === k ? " on" : ""), onclick: () => { status = status === k ? "" : k; draw(); } }, h("b", {}, cnt[k]), " " + label);
      strip.replaceChildren(chip("bad", "failing", "bad"), chip("warn", "need a look", "warn"), chip("ok", "healthy", "ok"), chip("err", "unreachable", "err"),
        h("span", { class: "fl-tot faint" }, `${ready}/${nodes} nodes ready · ${podsBad} failing pods`));
      const order = { err: 0, bad: 1, warn: 2, ok: 3, load: 4 };
      let list = names.filter((n) => (!f || (n + " " + tagsOf(n).map((x) => x.textContent).join(" ")).toLowerCase().includes(f)) && (!status || fleetHealth(res[n]) === status));
      if (sorted) { const pins = K.pins(); list = list.sort((a, b) => (pins.includes(b) ? 1 : 0) - (pins.includes(a) ? 1 : 0) || order[fleetHealth(res[a])] - order[fleetHealth(res[b])] || a.localeCompare(b)); }
      grid.replaceChildren(...list.map((n) => card(n, res[n])));
      if (!list.length) grid.append(h("div", { class: "empty" }, "No cluster matches."));
    };
    const card = (n, r) => {
      const hl = fleetHealth(r), d = r && r.data;
      const stat = (label, val, bad) => h("div", { class: "fl-stat" + (bad ? " bad" : "") }, h("b", {}, val), h("span", {}, label));
      const body = !r ? [h("div", { class: "fl-load" }, "Reading…")]
        : r.error ? [h("div", { class: "fl-err" }, r.error)]
        : [h("div", { class: "fl-ver" }, h("b", {}, shortVer(d.version)), d.nodeSkew ? h("span", { class: "pill warn", title: "Some nodes run another minor version than the control plane" }, "node skew") : null),
          h("div", { class: "fl-stats" }, stat("nodes", d.nodesReady + "/" + d.nodes, d.nodesReady < d.nodes), stat("pods", d.pods), stat("failing", d.podsBad, d.podsBad > 0),
            stat("pending", d.podsWarn, false), stat("warnings 1h", d.warnings, false), stat("namespaces", d.namespaces)),
          d.nodesPressure ? h("div", { class: "fl-note" }, `${d.nodesPressure} node${d.nodesPressure === 1 ? "" : "s"} under memory, disk or PID pressure`) : null,
          d.cordoned ? h("div", { class: "fl-note" }, `${d.cordoned} node${d.cordoned === 1 ? "" : "s"} cordoned`) : null,
          ...(d.notes || []).map((x) => h("div", { class: "fl-note faint" }, "Could not read " + x)),
          (hist[n] || []).length >= 3 ? h("div", { class: "fl-trend", title: "Failing pods and warning events, as sampled while Kendra was watching this cluster" },
            K.spark(hist[n].map((x) => x.podsBad), { cls: hist[n].some((x) => x.podsBad > 0) ? "bad" : "ok", w: 100 }), h("span", { class: "faint" }, "failing pods · last " + Math.max(1, Math.round((Date.parse(hist[n][hist[n].length - 1].t) - Date.parse(hist[n][0].t)) / 60000)) + "m"),
            K.spark(hist[n].map((x) => x.warnings), { cls: "warn", w: 100 }), h("span", { class: "faint" }, "warnings")) : null];
      return h("button", { class: "fl-card h-" + hl, title: "Open the topology of " + n, onclick: () => K.gotoNs(n, "", "#/topo/cluster") },
        h("div", { class: "fl-head" }, ...tagsOf(n), h("span", { class: "fl-dot " + hl })), h("div", { class: "fl-name" }, n), ...body);
    };
    filter.addEventListener("input", draw);
    draw();
    loadHist();
    await loadPer("/api/fleet", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
    if (!live()) return;
    sorted = true; draw();
    loadHist();
  };

  V.versions = async function (main, _a, live) {
    const names = fleetNames();
    const filter = h("input", { class: "filter", id: "filter", placeholder: "Filter clusters or versions   ( / )", spellcheck: "false" });
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    let shown = [], newestNow = 0;
    const exportTable = () => ({
      head: ["Cluster", "Tags", "Platform", "Control plane", "Minors behind newest", "Node versions", "Node skew", "Error"],
      rows: shown.map((n) => { const r = res[n], d = r && r.data; return [n, tagsOf(n).map((x) => x.textContent).join(" "), d ? d.platform : "", d ? d.version : "", d ? newestNow - d.minor : "",
        d ? d.nodeVersions.map((v) => v.version + " x" + v.count).join("; ") : "", d ? (d.nodeSkew ? "yes" : "no") : "", r && r.error ? r.error : ""]; }),
    });
    main.append(h("div", { class: "page-head" }, h("h1", {}, "Versions"), sub, h("span", { class: "grow" }), K.exportBar(exportTable, "kendra-versions"), h("button", { class: "btn small", onclick: () => K.refresh() }, "Refresh")),
      h("div", { class: "tools" }, filter), strip, body);
    const res = {}; let doneN = 0, minor = "";
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      const got = names.filter((n) => res[n] && res[n].data);
      const newest = Math.max(0, ...got.map((n) => res[n].data.minor));
      const byMinor = {};
      got.forEach((n) => { const m = res[n].data.minor; byMinor[m] = (byMinor[m] || 0) + 1; });
      const behind = got.filter((n) => newest - res[n].data.minor >= 1).length, skew = got.filter((n) => res[n].data.nodeSkew).length;
      sub.textContent = `${names.length} cluster${names.length === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}${newest ? ` · newest here: 1.${newest}` : ""}`;
      strip.replaceChildren(...Object.keys(byMinor).sort((a, b) => b - a).map((m) => h("button", { class: "fl-chip " + (newest - m >= 2 ? "bad" : newest - m === 1 ? "warn" : "ok") + (minor === m ? " on" : ""),
        onclick: () => { minor = minor === m ? "" : m; draw(); } }, h("b", {}, byMinor[m]), " on 1." + m)),
        h("span", { class: "fl-tot faint" }, `${behind} behind the newest · ${skew} with node skew`));
      const rows = names.filter((n) => {
        const d = res[n] && res[n].data;
        if (minor && !(d && String(d.minor) === minor)) return false;
        return !f || (n + " " + (d ? d.version + " " + d.nodeVersions.map((v) => v.version).join(" ") : "")).toLowerCase().includes(f);
      }).sort((a, b) => {
        const A = res[a] && res[a].data, B = res[b] && res[b].data;
        if (!A || !B) return (A ? 1 : 0) - (B ? 1 : 0) || a.localeCompare(b);
        return A.minor - B.minor || a.localeCompare(b);
      });
      shown = rows; newestNow = newest;
      body.replaceChildren(rows.length ? h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, ["Cluster", "Platform", "Control plane", "Behind", "Nodes (kubelet versions)", ""].map((c) => h("th", {}, c)))),
        h("tbody", {}, rows.map((n) => {
          const r = res[n], d = r && r.data, gap = d ? newest - d.minor : 0;
          const tr = h("tr", { tabindex: 0, class: !r ? "" : r.error ? "h-bad" : gap >= 2 ? "h-bad" : gap === 1 || d.nodeSkew ? "h-warn" : "h-ok" },
            h("td", { class: "name" }, h("span", { class: "vtags" }, ...tagsOf(n)), n),
            h("td", {}, d ? d.platform || "–" : ""),
            h("td", { class: "mono" }, !r ? h("span", { class: "faint" }, "reading…") : r.error ? h("span", { class: "faint", title: r.error }, "unreachable") : shortVer(d.version)),
            h("td", {}, d ? (gap > 0 ? K.pill(gap >= 2 ? "bad" : "warn", gap + " minor" + (gap > 1 ? "s" : "")) : K.pill("ok", "newest")) : ""),
            h("td", {}, d ? d.nodeVersions.map((v) => h("span", { class: "chip" + (minorOfVer(v.version) !== d.minor ? " state" : ""), title: v.version }, shortVer(v.version) + " × " + v.count)) : ""),
            h("td", {}, d && d.nodeSkew ? K.pill("warn", "node skew") : ""));
          tr.addEventListener("click", () => K.gotoNs(n, "", "#/topo/cluster"));
          return tr;
        }))) : h("div", { class: "empty" }, "No cluster matches."));
    };
    const minorOfVer = (v) => { const m = /^v?\d+\.(\d+)/.exec(v); return m ? +m[1] : 0; };
    filter.addEventListener("input", draw);
    draw();
    await loadPer("/api/fleet", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  V.deprecated = async function (main, _a, live) {
    const names = K.multi() ? K.members(K.state.ctx) : [K.state.ctx];
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    const exportTable = () => ({
      head: ["Cluster", "Cluster version", "API version", "Kind", "Resource", "Status", "Deprecated in", "Removed in", "Objects written through it", "Written by", "Requested", "Use instead", "Examples"],
      rows: names.flatMap((n) => (res[n] && res[n].data ? res[n].data.items.map((i) => [n, res[n].data.version, i.apiVersion, i.kind || "", i.resource,
        i.urgency === "bad" ? "removed in next upgrade" : i.urgency === "warn" ? "removal planned" : "no removal planned", i.deprecatedIn || "", i.removedIn || "",
        (i.objects || 0) + (i.more ? "+" : ""), (i.managers || []).join("; "), i.requested ? "yes" : "no", i.replacement || "", (i.examples || []).join("; ")]) : [])),
    });
    main.append(h("div", { class: "page-head" }, h("h1", {}, "Deprecated APIs"), sub, h("span", { class: "grow" }), K.exportBar(exportTable, "kendra-deprecated-apis"), h("button", { class: "btn small", onclick: () => K.refresh() }, "Refresh")),
      h("div", { class: "banner" }, "Deprecated API versions each cluster still serves, with the objects whose last write went through that version (from ", h("code", {}, "managedFields"),
        "), so you can tell what kubectl, Helm, kapp or a controller still sends. “Requested” means a client called that version since the API server started. “Next upgrade” means it is removed in the next minor version."),
      strip, body);
    const res = {}; let doneN = 0;
    const draw = () => {
      let bad = 0, warn = 0, hit = 0;
      const sections = [], clean = [], failed = [];
      names.forEach((n) => {
        const r = res[n]; if (!r) return;
        if (r.error) { failed.push(n); return; }
        const items = r.data.items;
        if (!items.length) { clean.push(n); return; }
        hit++; items.forEach((i) => { if (i.urgency === "bad") bad++; else if (i.urgency === "warn") warn++; });
        sections.push([n, r.data]);
      });
      sub.textContent = `${K.scopeLabel(K.multi() ? K.state.ctx : K.state.ctx)} · ${names.length} cluster${names.length === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      strip.replaceChildren(h("span", { class: "fl-chip bad" }, h("b", {}, bad), " removed in the next upgrade"), h("span", { class: "fl-chip warn" }, h("b", {}, warn), " removal planned"),
        h("span", { class: "fl-chip ok" }, h("b", {}, clean.length), " clusters clean"), h("span", { class: "fl-tot faint" }, `${hit} cluster${hit === 1 ? "" : "s"} with findings`));
      sections.sort((a, b) => b[1].items.filter((i) => i.urgency === "bad").length - a[1].items.filter((i) => i.urgency === "bad").length || a[0].localeCompare(b[0]));
      body.replaceChildren(...errBanners(failed.map((n) => res[n])),
        ...sections.map(([n, d]) => h("div", { class: "dep-sec" },
          h("div", { class: "group-head" }, n, " ", ...tagsOf(n), h("span", { class: "faint" }, "  " + shortVer(d.version))),
          d.note ? h("div", { class: "faint dep-note" }, d.note) : null,
          h("div", { class: "tbl-scroll" }, h("table", { class: "cards" }, h("thead", {}, h("tr", {}, ["API version", "Resource", "Status", "Written through it", "Requested", "Use instead", "Examples"].map((c) => h("th", {}, c)))),
            h("tbody", {}, d.items.map((i) => h("tr", { class: "h-" + (i.urgency === "bad" ? "bad" : i.urgency === "warn" ? "warn" : "ok") },
              h("td", { class: "mono" }, i.apiVersion), h("td", { title: i.resource }, i.kind || i.resource),
              h("td", {}, K.pill(i.urgency, i.removedIn ? (i.urgency === "bad" ? "next upgrade (" + i.removedIn + ")" : "removed in " + i.removedIn) : "no removal planned")),
              h("td", {}, i.objects ? [h("b", {}, i.objects + (i.more ? "+" : "")), h("span", { class: "faint" }, " via " + i.managers.join(", "))] : h("span", { class: "faint" }, "none found")),
              h("td", {}, i.requested ? K.pill("warn", "yes") : h("span", { class: "faint" }, "–")),
              h("td", { class: "mono" }, i.replacement || h("span", { class: "faint" }, "–")),
              h("td", {}, (i.examples || []).map((e) => h("span", { class: "chip" }, e)))))))))),
        clean.length ? h("div", { class: "faint dep-clean" }, "Nothing deprecated found in: " + clean.join(", ")) : null);
      if (!names.length) body.replaceChildren(h("div", { class: "empty" }, "No clusters in this scope."));
    };
    draw();
    await loadPer("/api/deprecated", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // The clusters of the kubeconfig as a grid of cards: provider badge, name, prod warning.
  function clusterGrid(ctxs) {
    const kind = K.provider, env = K.envTags;
    const grid = h("div", { class: "ctx-grid" });
    const find = h("input", { class: "ctx-find", type: "search", placeholder: "Filter clusters…", oninput: (e) => draw(e.target.value.trim().toLowerCase()) });
    const draw = (q) => {
      const list = ctxs.filter((c) => !q || c.name.toLowerCase().includes(q));
      const pinned = K.pins();
      list.sort((a, b) => (pinned.includes(b.name) ? 1 : 0) - (pinned.includes(a.name) ? 1 : 0));
      grid.replaceChildren(...list.map((c) => {
        const [label, cls] = kind(c);
        return h("button", { class: "ctx-card" + (c.prod ? " prod" : "") + (c.name === K.state.ctx ? " cur" : ""), title: c.cluster,
          onclick: () => K.gotoNs(c.name, "", "#/topo/cluster") },
          h("span", { class: "ctx-badge " + cls }, label),
          h("span", { class: "ctx-name" }, c.name),
          ...env(c).map(([t, k]) => h("span", { class: "ctx-flag " + k }, t)),
          h("span", { class: "ctx-pin" + (K.isPinned(c.name) ? " on" : ""), title: K.isPinned(c.name) ? "Unpin" : "Pin to the top", onclick: (e) => { e.stopPropagation(); K.togglePin(c.name); draw(find.value.trim().toLowerCase()); } }, K.isPinned(c.name) ? "★" : "☆"));
      }), list.length ? null : h("div", { class: "faint" }, "No cluster matches."));
    };
    draw("");
    const head = h("div", { class: "ctx-head" },
      h("span", { class: "ctx-title" }, `${ctxs.length} cluster${ctxs.length === 1 ? "" : "s"} in your kubeconfig`),
      ctxs.length > 8 ? find : null);
    return h("div", { class: "ctx-wrap" }, head, grid);
  }

  V.start = async function (main) {
    const ctxs = K.state.ctxs;
    const quick = [["Fleet dashboard", "#/fleet", "every cluster at a glance"], ["Cluster topology", "#/topo/cluster", "node pools, nodes, namespaces"], ["Kapp apps", "#/kapp", "every app, in any namespace"],
      ["Namespace overview", "#/overview", "all namespaces at a glance"], ["Warnings", "#/warnings", "what is wrong, grouped"]];
    main.append(h("div", { class: "start" }, h("div", { class: "aurora" }), wheel(),
      h("h1", {}, "Nothing open yet"),
      h("p", { class: "muted" }, "Pick a view on the left, or press ", h("kbd", {}, "Ctrl K"), " to jump to anything."),
      h("div", { class: "quick" }, quick.map(([t, href, d]) => h("a", { class: "card", href }, h("b", {}, t), h("span", { class: "muted" }, d)))),
      clusterGrid(ctxs)));
    const box = main.querySelector(".start");
    box.prepend(network(box));
  };
})();
