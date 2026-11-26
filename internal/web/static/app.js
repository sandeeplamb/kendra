// App shell: header pickers, left rail, hash router, command palette, theme.
(async function () {
  const { h, api } = K;
  const S = K.state;
  const $ = (id) => document.getElementById(id);
  const main = $("main"), rail = $("rail"), ctxSel = $("ctx"), nsSel = $("ns");

  // ---------- theme: auto -> light -> dark ----------
  const THEMES = [["auto", "◐ Auto"], ["light", "☀ Light"], ["dark", "☾ Dark"]];
  function applyTheme(t) {
    if (t === "light" || t === "dark") document.documentElement.dataset.theme = t; else delete document.documentElement.dataset.theme;
    $("theme").textContent = THEMES.find((x) => x[0] === t)[1];
  }
  let theme = localStorage.getItem("knd.theme") || "auto";
  applyTheme(theme);
  $("theme").addEventListener("click", () => {
    theme = THEMES[(THEMES.findIndex((x) => x[0] === theme) + 1) % THEMES.length][0];
    localStorage.setItem("knd.theme", theme);
    applyTheme(theme);
  });

  // ---------- bootstrap data ----------
  try {
    [S.info, S.ctxs, S.kinds] = await Promise.all([api("/api/info"), api("/api/contexts"), api("/api/kinds")]);
  } catch (e) {
    main.append(h("div", { class: "banner err" }, "Cannot reach Kendra: " + e.message + ". Open it with the address printed in the terminal."));
    return;
  }
  $("ro").hidden = false; // Kendra is always read-only
  if (!S.ctxs.length) {
    main.append(h("div", { class: "empty" }, "No kubeconfig contexts found. Kendra reads the same files as kubectl (KUBECONFIG or ~/.kube/config)."));
    return;
  }
  const saved = localStorage.getItem("knd.ctx");
  const validScope = (x) => x === "*" || S.ctxs.some((c) => c.name === x) || (x && x.charAt(0) === "@" && K.members(x).length > 0);
  S.ctx = saved && validScope(saved) ? saved : (S.ctxs.find((c) => c.current) || S.ctxs[0]).name;
  S.ns = localStorage.getItem("knd.ns") || "";

  // The real <select> stays (hidden) as the single source of truth; the combobox below drives it.
  ctxSel.append(h("option", { value: "*" }, "All clusters"), ...K.groups().map((g) => h("option", { value: g.value }, g.label)),
    ...S.ctxs.map((c) => h("option", { value: c.name }, (c.prod ? "⚠ " : "") + c.name)));
  if (![...ctxSel.options].some((o) => o.value === S.ctx)) ctxSel.append(h("option", { value: S.ctx }, K.scopeLabel(S.ctx)));
  ctxSel.value = S.ctx;
  const ctxBox = K.clusterPicker(ctxSel);

  async function loadNamespaces() {
    nsSel.replaceChildren(h("option", { value: "" }, "All namespaces"));
    nsSel.disabled = K.multi();
    if (K.multi()) { S.ns = ""; return; }
    try {
      const list = await api("/api/namespaces", { context: S.ctx });
      list.forEach((n) => nsSel.append(h("option", { value: n }, n)));
      if (S.ns && !list.includes(S.ns)) S.ns = "";
    } catch (e) { K.toast("Namespaces: " + e.message, true); }
    nsSel.value = S.ns;
  }
  K.syncNs = () => { nsSel.value = S.ns; };
  const markProd = () => { ctxSel.closest(".pick").classList.toggle("prod", !K.multi() && K.isProd(S.ctx)); ctxBox.sync(); };
  ctxSel.addEventListener("change", async () => {
    S.ctx = ctxSel.value; S.ns = ""; localStorage.setItem("knd.ctx", S.ctx); localStorage.setItem("knd.ns", "");
    markProd(); await loadNamespaces(); loadCrds(); route();
  });
  // jump to a namespace of a cluster (e.g. from the overview) and show a page for it
  K.gotoNs = async (cluster, ns, hash) => {
    if (S.ctx !== cluster) {
      S.ctx = cluster; ctxSel.value = cluster; localStorage.setItem("knd.ctx", cluster);
      markProd(); await loadNamespaces(); loadCrds();
    }
    S.ns = ns; nsSel.value = ns; localStorage.setItem("knd.ns", ns);
    if (location.hash === hash) route(true); else location.hash = hash;
  };
  nsSel.addEventListener("change", () => { S.ns = nsSel.value; localStorage.setItem("knd.ns", S.ns); route(); });
  markProd();
  loadNamespaces();

  // ---------- rail ----------
  const kindItems = (cat) => S.kinds.filter((k) => k.category === cat).map((k) => [k.title, "#/r/" + k.name]);
  // The left rail. Catalogue kinds come from the server by category; pages are added to the group they belong to.
  const NAV = [
    { title: "Fleet", open: true, items: [["Fleet dashboard", "#/fleet"], ["Fleet search", "#/search"], ["Versions", "#/versions"], ["Deprecated APIs", "#/deprecated"]] },
    { title: "Insights", open: true, items: [["Usage vs requests", "#/usage"], ["Image inventory", "#/images"], ["Change timeline", "#/timeline"], ["Compare clusters", "#/compare"]] },
    { title: "Topology", open: true, items: [["Cluster topology", "#/topo/cluster"], ["Zones & pod spread", "#/spread"], ["Namespace topology", "#/topo/namespace"], ["Helm topology", "#/topo/helm"],
      ["Kapp topology", "#/topo/kapp"], ["Istio topology", "#/istio"]] },
    { title: "Health", open: true, items: [["Warnings", "#/warnings"], ["Time machine", "#/timemachine"]] },
    { title: "Workloads", open: true, items: [["Namespace overview", "#/overview"], ["Jobs & CronJobs health", "#/cronjobs"], ...kindItems("Workloads")] },
    { title: "Cluster", items: kindItems("Cluster") },
    { title: "Config", items: kindItems("Config") },
    { title: "Network", items: [...kindItems("Network"), ["Port forwarding", "#/forwards"]] },
    { title: "Storage", items: kindItems("Storage") },
    { title: "Security", items: [["Security overview", "#/security"], ["Cloud identities", "#/identities"], ["Certificates", "#/certs"], ...kindItems("Access Control")] },
    { title: "Platform", items: [["Add-ons", "#/addons"], ["Add-on compatibility", "#/compat"], ["Gateway API", "#/gateway"], ...kindItems("Pod Autoscaling"), ...kindItems("Node Autoscaling"), ["Helm releases", "#/helm-releases"], ["Helm charts", "#/helm-charts"]] },
    { title: "Kyverno", items: kindItems("Kyverno") },
    { title: "Custom Resources", items: [] },
  ];

  const secOpen = (title, dflt) => { const v = localStorage.getItem("knd.sec." + title); return v === null ? dflt : v === "1"; };
  const crdBox = h("div", { class: "crd-box" });
  const platformBox = h("div", { class: "crd-box" }); // Gateway API and Istio resources, filled from the cluster's CRDs
  NAV.forEach((sec) => {
    const d = h("details", { class: "sec", "data-title": sec.title, open: secOpen(sec.title, sec.open) },
      h("summary", {}, sec.title), sec.items.map(([label, href]) => h("a", { href, "data-href": href }, label)), sec.title === "Custom Resources" ? crdBox : sec.title === "Platform" ? platformBox : null);
    d.addEventListener("toggle", () => localStorage.setItem("knd.sec." + sec.title, d.open ? "1" : "0"));
    rail.append(d);
  });
  // ---------- saved views: a page with its cluster scope, namespace and filter ----------
  const savedBox = h("div", { class: "saved-box" });
  rail.prepend(savedBox);
  const renderSaved = () => {
    const list = K.saved.list();
    savedBox.replaceChildren(...(list.length ? [h("div", { class: "saved-head" }, "★ Saved views"), ...list.map((v) =>
      h("div", { class: "sv-item", title: `${v.hash} · ${K.scopeLabel(v.ctx)}${v.ns ? " · " + v.ns : ""}${v.filter ? " · filter: " + v.filter : ""}` },
        h("button", { class: "sv-go", onclick: () => K.restoreView(v) }, v.name), h("button", { class: "sv-x", title: "Remove this saved view", onclick: () => { K.saved.remove(v.name); renderSaved(); } }, "✕")))] : []));
  };
  K.restoreView = async (v) => {
    K.pendingFilter = v.filter || "";
    const valid = v.ctx === "*" || S.ctxs.some((c) => c.name === v.ctx) || (v.ctx.charAt(0) === "@" && K.members(v.ctx).length);
    if (!valid) { K.toast("That cluster scope no longer exists", true); return; }
    if (v.ctx.charAt(0) !== "@" && v.ctx !== "*") { await K.gotoNs(v.ctx, v.ns || "", v.hash); return; }
    if (![...ctxSel.options].some((o) => o.value === v.ctx)) ctxSel.append(h("option", { value: v.ctx }, K.scopeLabel(v.ctx)));
    if (ctxSel.value !== v.ctx) { ctxSel.value = v.ctx; ctxSel.dispatchEvent(new Event("change")); }
    if (location.hash === v.hash) route(true); else location.hash = v.hash;
  };
  $("saveview").addEventListener("click", () => {
    if (document.querySelector(".sv-pop")) { document.querySelector(".sv-pop").remove(); return; }
    const title = (main.querySelector("h1") || {}).textContent || location.hash.replace(/^#\/?/, "") || "View";
    const input = h("input", { class: "filter", value: `${title} · ${K.scopeLabel(S.ctx)}`, spellcheck: "false", "aria-label": "Name of the saved view" });
    const close = () => pop.remove();
    const save = () => {
      const name = input.value.trim(); if (!name) return;
      const f = document.getElementById("filter");
      K.saved.add({ name, hash: location.hash || "#/start", ctx: S.ctx, ns: S.ns, filter: f ? f.value : "" });
      renderSaved(); close(); K.toast("Saved “" + name + "”");
    };
    const pop = h("div", { class: "sv-pop" }, h("div", { class: "faint" }, "Save this page with its cluster scope, namespace and filter"), input,
      h("div", { class: "sv-btns" }, h("button", { class: "btn small", onclick: close }, "Cancel"), h("button", { class: "btn small primary", onclick: save }, "Save")));
    input.addEventListener("keydown", (e) => { if (e.key === "Enter") save(); else if (e.key === "Escape") close(); });
    document.querySelector("header.top").append(pop);
    input.focus(); input.select();
  });
  renderSaved();
  K.onPins = () => { if (location.hash === "" || location.hash.startsWith("#/start")) K.refresh(); };

  const markRail = () => {
    const links = [...rail.querySelectorAll("a")];
    const hash = location.hash;
    // the longest matching link wins, so "#/r/pods" is not shadowed by a shorter prefix
    let best = null;
    links.forEach((a) => { if (hash.startsWith(a.dataset.href) && (!best || a.dataset.href.length > best.dataset.href.length)) best = a; });
    links.forEach((a) => a.classList.toggle("on", a === best));
    for (let n = best && best.parentElement; n && n !== rail; n = n.parentElement) if (n.tagName === "DETAILS") n.open = true;
  };

  // custom resources: every CRD of the cluster, grouped by API group
  async function loadCrds() {
    const cluster = !K.multi() ? S.ctx : (S.ctxs.find((c) => c.current) || S.ctxs[0]).name;
    crdBox.replaceChildren(h("div", { class: "faint rail-note" }, "Loading definitions…"));
    try {
      S.crds = await api("/api/crds", { context: cluster });
    } catch (e) {
      S.crds = [];
      crdBox.replaceChildren(h("div", { class: "faint rail-note", title: e.message }, "Definitions unavailable"));
      return;
    }
    const groups = {};
    S.crds.forEach((c) => (groups[c.group] = groups[c.group] || []).push(c));
    crdBox.replaceChildren(...Object.keys(groups).sort().map((g) => h("details", { class: "grp", "data-title": "crd:" + g, open: secOpen("crd:" + g, false) },
      h("summary", {}, g, h("span", { class: "tag" }, groups[g].length)),
      groups[g].map((c) => h("a", { href: `#/cr/${encodeURIComponent(g)}/${encodeURIComponent(c.plural)}`, "data-href": `#/cr/${encodeURIComponent(g)}/${encodeURIComponent(c.plural)}`, title: c.name }, c.kind)))));
    // Platform: the Gateway API and Istio resources this cluster has, one click away
    const WANT = { "gateway.networking.k8s.io": "Gateway API resources", "networking.istio.io": "Istio networking", "security.istio.io": "Istio security", "telemetry.istio.io": "Istio telemetry", "extensions.istio.io": "Istio extensions" };
    platformBox.replaceChildren(...Object.keys(WANT).filter((g) => groups[g]).map((g) => h("details", { class: "grp", "data-title": "plat:" + g, open: secOpen("plat:" + g, false) },
      h("summary", {}, WANT[g], h("span", { class: "tag" }, groups[g].length)),
      groups[g].map((c) => h("a", { href: `#/cr/${encodeURIComponent(g)}/${encodeURIComponent(c.plural)}`, "data-href": `#/cr/${encodeURIComponent(g)}/${encodeURIComponent(c.plural)}`, title: c.name }, c.kind)))));
    platformBox.querySelectorAll("details.grp").forEach((d) => d.addEventListener("toggle", () => localStorage.setItem("knd.sec." + d.dataset.title, d.open ? "1" : "0")));
    crdBox.querySelectorAll("details.grp").forEach((d) => d.addEventListener("toggle", () => localStorage.setItem("knd.sec." + d.dataset.title, d.open ? "1" : "0")));
    markRail();
  }
  loadCrds();

  // ---------- router ----------
  let gen = 0, cleanup = null;
  async function route(closeDrawer) {
    const my = ++gen;
    if (cleanup) { cleanup(); cleanup = null; }
    if (closeDrawer) K.closeDrawer();
    markRail();
    main.replaceChildren();
    main.scrollTop = 0;
    K.watch((K.multi() ? K.members(S.ctx) : [S.ctx]).slice(0, 10)); // sample these clusters for sparklines and the time machine
    const parts = location.hash.replace(/^#\/?/, "").split("/").map(decodeURIComponent);
    let view, arg;
    switch (parts[0]) {
      case "r": view = K.views.list; arg = parts[1] || "pods"; break;
      case "": case "start": view = K.views.start; break;
      case "overview": view = K.views.overview; break;
      case "cr": view = K.views.cr; arg = [parts[1], parts[2]]; break;
      case "helm-releases": view = K.views.helmReleases; break;
      case "helm-charts": view = K.views.helmCharts; break;
      case "forwards": view = K.views.forwards; break;
      case "kapp": if (parts.length >= 4) { view = K.views.kappApp; arg = parts.slice(1, 4); } else view = K.views.kapp; break;
      case "topo": view = { cluster: K.views.topoCluster, namespace: K.views.topoNamespace, helm: K.views.topoHelm, kapp: K.views.topoKapp }[parts[1]]; if (!view) { location.replace("#/topo/cluster"); return; } break;
      case "map": location.replace("#/topo/kapp"); return;
      case "helm": if (parts.length >= 4) { view = K.views.helmApp; arg = parts.slice(1, 4); } else { location.replace("#/topo/helm"); return; } break;
      case "istio": view = K.views.istio; break;
      case "warnings": view = K.views.warnings; break;
      case "fleet": view = K.views.fleet; break;
      case "search": view = K.views.search; break;
      case "cronjobs": view = K.views.cronjobs; break;
      case "versions": view = K.views.versions; break;
      case "deprecated": view = K.views.deprecated; break;
      case "certs": view = K.views.certs; break;
      case "images": view = K.views.images; break;
      case "usage": view = K.views.usage; break;
      case "addons": view = K.views.addons; break;
      case "compat": view = K.views.compat; break;
      case "spread": view = K.views.spread; break;
      case "gateway": view = K.views.gateway; break;
      case "timemachine": view = K.views.timemachine; break;
      case "timeline": view = K.views.timeline; break;
      case "security": view = K.views.security; break;
      case "identities": view = K.views.identities; break;
      case "compare": view = K.views.compare; arg = [parts[1], parts[2], parts[3]]; break;
      default: location.replace("#/start"); return;
    }
    const live = () => my === gen;
    try {
      const c = await view(main, arg, live);
      if (K.pendingFilter && live()) { const f = document.getElementById("filter"); if (f) { f.value = K.pendingFilter; f.dispatchEvent(new Event("input")); } K.pendingFilter = null; }
      if (typeof c === "function") { if (live()) cleanup = c; else c(); }
    } catch (e) { main.append(h("div", { class: "banner err" }, e.message)); }
  }
  K.refresh = () => route(false);
  window.addEventListener("hashchange", () => route(true));

  // ---------- command palette ----------
  function palette() {
    const items = [];
    items.push({ label: "Start screen", hint: "home", run: () => (location.hash = "#/start") });
    NAV.forEach((sec) => sec.items.forEach(([label, href]) => items.push({ label, hint: sec.title, run: () => (location.hash = href) })));
    (S.crds || []).forEach((c) => items.push({ label: c.kind, hint: c.group, run: () => (location.hash = `#/cr/${encodeURIComponent(c.group)}/${encodeURIComponent(c.plural)}`) }));
    S.ctxs.forEach((c) => items.push({ label: "Cluster: " + c.name, hint: "switch", run: () => { ctxSel.value = c.name; ctxSel.dispatchEvent(new Event("change")); } }));
    items.push({ label: "Cluster: all clusters", hint: "switch", run: () => { ctxSel.value = "*"; ctxSel.dispatchEvent(new Event("change")); } });
    K.saved.list().forEach((v) => items.push({ label: "View: " + v.name, hint: "saved view", run: () => K.restoreView(v) }));
    K.groups().forEach((g) => items.push({ label: "Clusters: " + g.label + " (" + g.count + ")", hint: "group", run: () => { ctxSel.value = g.value; ctxSel.dispatchEvent(new Event("change")); } }));
    [...nsSel.options].filter((o) => o.value).forEach((o) => items.push({ label: "Namespace: " + o.value, hint: "filter", run: () => { nsSel.value = o.value; nsSel.dispatchEvent(new Event("change")); } }));
    const input = h("input", { type: "text", placeholder: "Type a view, resource, cluster or namespace…", spellcheck: "false", "aria-label": "Jump to" });
    const ul = h("ul", {});
    let list = items, at = 0;
    const draw = () => {
      ul.replaceChildren(...list.slice(0, 60).map((it, i) => h("li", { class: i === at ? "on" : "", onclick: () => go(it) }, h("span", {}, it.label), h("span", { class: "faint" }, it.hint))));
      const on = ul.querySelector(".on"); if (on) on.scrollIntoView({ block: "nearest" });
    };
    const go = (it) => { K.closeModal(); it.run(); };
    input.addEventListener("input", () => { const q = input.value.toLowerCase(); list = items.filter((i) => (i.label + " " + i.hint).toLowerCase().includes(q)); at = 0; draw(); });
    input.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown") { at = Math.min(list.length - 1, at + 1); draw(); e.preventDefault(); }
      else if (e.key === "ArrowUp") { at = Math.max(0, at - 1); draw(); e.preventDefault(); }
      else if (e.key === "Enter" && list[at]) go(list[at]);
    });
    K.modal(h("div", { class: "palette" }, input, ul));
    draw(); input.focus();
  }
  $("jump").addEventListener("click", palette);

  document.addEventListener("keydown", (e) => {
    const typing = /^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement.tagName);
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); if ($("modal").hidden) palette(); else K.closeModal(); }
    else if (e.key === "Escape") { if (!$("modal").hidden) K.closeModal(); else if (!$("drawer").hidden) K.closeDrawer(); }
    else if (e.key === "/" && !typing && $("modal").hidden) { const f = document.getElementById("filter"); if (f) { e.preventDefault(); f.focus(); } }
  });

  route(true);
})();
