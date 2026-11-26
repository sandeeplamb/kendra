// Core helpers: DOM builder, API client, dialogs, resource drawer. No innerHTML anywhere:
// every value from a cluster goes in as text, so it can never run as markup.
(function (root) {
  // DOM append/prepend/replaceChildren turn null into the text "null": skip empty values instead.
  for (const m of ["append", "prepend", "replaceChildren"]) {
    const orig = Element.prototype[m];
    Element.prototype[m] = function (...a) { return orig.apply(this, a.flat(2).filter((x) => x != null && x !== false)); };
  }

  const K = (root.K = { state: { ctxs: [], ctx: "", ns: "", kinds: [], info: {} }, views: {} });

  K.h = function h(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k.startsWith("on") && typeof v === "function") e.addEventListener(k.slice(2), v);
      else if (k === "class") e.className = v;
      else if (k === "value") e.value = v;
      else e.setAttribute(k, v === true ? "" : v);
    }
    const add = (c) => {
      if (c == null || c === false) return;
      if (Array.isArray(c)) c.forEach(add);
      else e.append(c instanceof Node ? c : document.createTextNode(String(c)));
    };
    kids.forEach(add);
    return e;
  };
  const h = K.h;

  K.api = async function (path, params, body) {
    const u = new URL(path, location.origin);
    for (const [k, v] of Object.entries(params || {})) if (v !== undefined && v !== "") u.searchParams.set(k, v);
    const cx = u.searchParams.get("context");
    if (cx && cx.charAt(0) === "@") u.searchParams.set("context", K.members(cx).join(",")); // a group goes to the server as a list
    const opt = { credentials: "same-origin" };
    if (body) {
      opt.method = "POST";
      opt.headers = { "Content-Type": "application/json", "X-Knd": "1" };
      opt.body = JSON.stringify(body);
    }
    const r = await fetch(u, opt);
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(j.error || r.statusText);
    return j;
  };

  K.ago = function (iso) {
    if (!iso) return "";
    const s = (Date.now() - Date.parse(iso)) / 1000;
    if (Number.isNaN(s)) return "";
    if (s < 60) return Math.max(0, Math.floor(s)) + "s";
    if (s < 3600) return Math.floor(s / 60) + "m";
    if (s < 86400) return Math.floor(s / 3600) + "h";
    return Math.floor(s / 86400) + "d";
  };
  K.bytes = (b) => (b >= 1073741824 ? (b / 1073741824).toFixed(1) + "Gi" : Math.round(b / 1048576) + "Mi");
  K.pill = (health, text) => h("span", { class: "pill " + (health || "") }, text);
  K.isProd = (name) => !!(K.state.ctxs.find((c) => c.name === name) || {}).prod;

  // a titled list of related objects; each opens in the drawer
  K.relBlock = (title, list, cluster, empty) => h("div", { class: "rel-block" }, h("div", { class: "group-head" }, title + (list.length ? " (" + list.length + ")" : "")),
    list.length ? list.map((r) => {
      const open = r.resource && !/does not exist/.test(r.note || "") && !r.name.startsWith("(");
      return h(open ? "button" : "div", { class: "rel-item " + (r.level || ""), title: open ? "Open " + r.kind + " " + r.name : "",
        onclick: open ? () => K.openResource({ cluster, kind: r.resource, ns: r.namespace || "", name: r.name, resource: r.resource }) : null },
        h("span", { class: "rel-kind" }, r.kind), h("span", { class: "rel-name" }, (r.namespace ? r.namespace + "/" : "") + r.name), r.note ? h("span", { class: "rel-note" }, r.note) : null);
    }) : h("div", { class: "faint rel-empty" }, empty));

  // ---- sparklines and history: Kendra samples the clusters you look at, in memory only ----
  K.spark = function (values, opt) {
    const o = Object.assign({ w: 96, h: 24, cls: "" }, opt || {}), NS = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(NS, "svg");
    svg.setAttribute("class", "spark " + o.cls + (o.stretch ? " stretch" : "")); svg.setAttribute("viewBox", `0 0 ${o.w} ${o.h}`); svg.setAttribute("width", o.w); svg.setAttribute("height", o.h);
    if (o.stretch) svg.setAttribute("preserveAspectRatio", "none");
    const v = values.filter((x) => x != null);
    if (v.length < 2) return svg;
    const lo = Math.min(0, ...v), hi = Math.max(...v, lo + 1), px = (i) => (i * (o.w - 2)) / (values.length - 1) + 1, py = (x) => o.h - 2 - ((x - lo) / (hi - lo)) * (o.h - 4);
    const pts = values.map((x, i) => (x == null ? null : `${px(i).toFixed(1)},${py(x).toFixed(1)}`)).filter(Boolean);
    const area = document.createElementNS(NS, "polygon"); area.setAttribute("points", `${pts[0].split(",")[0]},${o.h - 1} ${pts.join(" ")} ${pts[pts.length - 1].split(",")[0]},${o.h - 1}`); area.setAttribute("class", "spark-area");
    const line = document.createElementNS(NS, "polyline"); line.setAttribute("points", pts.join(" ")); line.setAttribute("class", "spark-line");
    const last = pts[pts.length - 1].split(","), dot = document.createElementNS(NS, "circle"); dot.setAttribute("cx", last[0]); dot.setAttribute("cy", last[1]); dot.setAttribute("r", "1.8"); dot.setAttribute("class", "spark-dot");
    svg.append(area, line, dot);
    return svg;
  };
  let lastWatch = { key: "", at: 0 };
  K.watch = function (names) {
    const key = names.join(","), now = Date.now();
    if (!names.length || (key === lastWatch.key && now - lastWatch.at < 4 * 60000)) return;
    lastWatch = { key, at: now };
    K.api("/api/watch", {}, { contexts: names }).catch(() => {});
  };
  K.history = async (names) => (names.length ? K.api("/api/history", { context: names.join(",") }) : {});

  // ---- pinned clusters and saved views (kept in this browser/app only) ----
  const store = (k) => { try { return JSON.parse(localStorage.getItem(k) || "[]"); } catch { return []; } };
  K.pins = () => store("knd.pins").filter((n) => K.state.ctxs.some((c) => c.name === n));
  K.isPinned = (n) => K.pins().includes(n);
  K.togglePin = (n) => { const p = K.pins(), i = p.indexOf(n); if (i >= 0) p.splice(i, 1); else p.push(n); localStorage.setItem("knd.pins", JSON.stringify(p)); };
  K.pinsFirst = (names) => { const p = K.pins(); return [...names.filter((n) => p.includes(n)), ...names.filter((n) => !p.includes(n))]; };
  K.saved = {
    list: () => store("knd.saved"),
    add: (v) => localStorage.setItem("knd.saved", JSON.stringify([...store("knd.saved").filter((x) => x.name !== v.name), v])),
    remove: (name) => localStorage.setItem("knd.saved", JSON.stringify(store("knd.saved").filter((x) => x.name !== name))),
  };

  // ---- export: copy or save a table as CSV or Markdown ----
  K.relatedKinds = ["pods", "deployments", "statefulsets", "daemonsets", "services", "ingresses", "configmaps", "secrets", "persistentvolumeclaims", "serviceaccounts", "nodes"];
  K.impactKinds = ["pods", "deployments", "statefulsets", "daemonsets", "services", "configmaps", "secrets", "persistentvolumeclaims", "serviceaccounts", "nodes", "namespaces"];
  K.whyKinds = ["pods", "nodes", "persistentvolumeclaims", "services", "deployments", "statefulsets", "daemonsets"];
  K.incidentKinds = K.whyKinds;
  const cell = (v) => (v == null ? "" : String(v));
  K.toCSV = ({ head, rows }) => {
    const q = (v) => { let t = cell(v); if (/^[=+@]/.test(t)) t = "'" + t; return /[",\n\r]/.test(t) ? '"' + t.replace(/"/g, '""') + '"' : t; };
    return [head, ...rows].map((r) => r.map(q).join(",")).join("\r\n") + "\r\n";
  };
  K.toMarkdown = ({ head, rows }) => {
    const q = (v) => cell(v).replace(/\|/g, "\\|").replace(/\r?\n/g, " ");
    return ["| " + head.map(q).join(" | ") + " |", "|" + head.map(() => "---").join("|") + "|", ...rows.map((r) => "| " + r.map(q).join(" | ") + " |")].join("\n") + "\n";
  };
  // In the macOS app a Save panel opens (the page cannot download files there); in a browser the file downloads.
  K.saveText = async function (name, text, mime) {
    if (K.state.info.nativeSave) {
      const r = await K.api("/api/save", {}, { name, text });
      K.toast(r.cancelled ? "Save cancelled" : "Saved " + r.path);
      return;
    }
    const a = h("a", { href: URL.createObjectURL(new Blob([text], { type: mime + ";charset=utf-8" })), download: name });
    document.body.append(a); a.click(); a.remove();
    K.toast("Downloaded " + name);
  };
  // getTable() -> {head: [...], rows: [[...]]}, read when a button is pressed
  K.exportBar = function (getTable, base) {
    const day = new Date().toISOString().slice(0, 10);
    const run = (fn) => async () => { try { const t = getTable(); if (!t.rows.length) { K.toast("Nothing to export", true); return; } await fn(t); } catch (e) { K.toast(e.message, true); } };
    return h("span", { class: "export" },
      h("button", { class: "btn small", title: "Copy as a Markdown table", onclick: run(async (t) => { await navigator.clipboard.writeText(K.toMarkdown(t)); K.toast("Markdown table copied"); }) }, "Copy Markdown"),
      h("button", { class: "btn small", title: "Save as CSV", onclick: run((t) => K.saveText(`${base}-${day}.csv`, K.toCSV(t), "text/csv")) }, "Save CSV"),
      h("button", { class: "btn small", title: "Save as Markdown", onclick: run((t) => K.saveText(`${base}-${day}.md`, K.toMarkdown(t), "text/markdown")) }, "Save .md"));
  };

  // ---- clusters: provider, environment tags and groups (from the context names) ----
  K.provider = (c) => {
    const n = (c.name + " " + (c.cluster || "")).toLowerCase();
    if (c.local) return ["Local", "local"];
    if (/aks|azmk8s|azure/.test(n)) return ["AKS", "aks"];
    if (/eks|amazonaws/.test(n)) return ["EKS", "eks"];
    if (/gke|gcp|google/.test(n)) return ["GKE", "gke"];
    return ["K8s", "k8s"];
  };
  // PROD comes from the server's own pattern (KND_PROD); the others from name parts split by - _ . /
  K.envTags = (c) => {
    const n = c.name.toLowerCase(), has = (w) => new RegExp("(^|[-_/.])(" + w + ")([-_/.0-9]|$)").test(n), out = [];
    if (c.prod) out.push(["PROD", "prod"]);
    if (has("test|tst")) out.push(["TEST", "test"]);
    if (has("dev")) out.push(["DEV", "dev"]);
    if (has("acp")) out.push(["ACP", "acp"]);
    return out;
  };
  // A scope is "*" (all), a single context name, or a group: "@tag:DEV", "@cloud:AKS", "@~text" (name contains text).
  K.multi = () => { const x = K.state.ctx; return x === "*" || x.charAt(0) === "@"; };
  K.members = (scope) => {
    const all = K.state.ctxs;
    if (scope === "*") return all.map((c) => c.name);
    if (scope.startsWith("@tag:")) return all.filter((c) => K.envTags(c).some((t) => t[0] === scope.slice(5))).map((c) => c.name);
    if (scope.startsWith("@cloud:")) return all.filter((c) => K.provider(c)[0] === scope.slice(7)).map((c) => c.name);
    if (scope.startsWith("@~")) { const t = scope.slice(2).toLowerCase(); return all.filter((c) => c.name.toLowerCase().includes(t)).map((c) => c.name); }
    return [scope];
  };
  // the groups that have at least one cluster: [{value, label, count}]
  K.groups = () => {
    const out = [], add = (value, label) => { const n = K.members(value).length; if (n) out.push({ value, label, count: n }); };
    ["PROD", "TEST", "DEV", "ACP"].forEach((t) => add("@tag:" + t, t + " clusters"));
    ["AKS", "EKS", "GKE", "Local"].forEach((t) => add("@cloud:" + t, t + " clusters"));
    return out;
  };
  K.scopeLabel = (scope) => {
    if (scope === "*") return "all clusters";
    if (scope.startsWith("@~")) return `clusters matching “${scope.slice(2)}” (${K.members(scope).length})`;
    if (scope.charAt(0) === "@") { const g = K.groups().find((x) => x.value === scope); return g ? `${g.label} (${g.count})` : scope; }
    return scope;
  };

  // ---- searchable cluster picker: a button + popup that drives the (hidden) <select> ----
  K.clusterPicker = function (sel) {
    const btn = h("button", { type: "button", class: "cb", "aria-haspopup": "listbox", title: "Choose a cluster, a group, or all clusters" });
    const input = h("input", { class: "cb-q", type: "search", placeholder: "Search clusters: name, dev, prod, aks, acp…", spellcheck: "false", "aria-label": "Search clusters" });
    const list = h("div", { class: "cb-list", role: "listbox" });
    const pop = h("div", { class: "cb-pop", hidden: true }, input, list);
    sel.parentNode.append(btn, pop);
    const badges = (c) => [h("span", { class: "ctx-badge " + K.provider(c)[1] }, K.provider(c)[0]), ...K.envTags(c).map(([t, k]) => h("span", { class: "ctx-flag " + k }, t))];
    const sync = () => {
      const v = sel.value, c = K.state.ctxs.find((x) => x.name === v);
      btn.replaceChildren(...(c ? [...badges(c), h("span", { class: "cb-name" }, c.name)] : [h("span", { class: "cb-name" }, "◎ " + K.scopeLabel(v))]), h("span", { class: "cb-car" }, "▾"));
    };
    let rows = [], at = 0, drill = ""; // drill: a group whose clusters are being listed
    const close = () => { pop.hidden = true; document.removeEventListener("mousedown", outside, true); };
    const outside = (e) => { if (!pop.contains(e.target) && !btn.contains(e.target)) close(); };
    const choose = (value) => {
      if (![...sel.options].some((o) => o.value === value)) sel.append(h("option", { value }, K.scopeLabel(value)));
      close();
      if (sel.value !== value) { sel.value = value; sel.dispatchEvent(new Event("change")); }
    };
    const draw = () => {
      const raw = input.value.trim(), q = raw.toLowerCase();
      rows = [];
      if (drill) {
        const g = K.groups().find((x) => x.value === drill);
        const members = K.members(drill);
        rows.push({ sect: g ? g.label : "Group", back: true, label: "← All groups" });
        rows.push({ sect: g ? g.label : "Group", value: drill, label: `All ${members.length} in this group`, count: members.length });
        K.pinsFirst(members).map((n) => K.state.ctxs.find((c) => c.name === n)).filter((c) => c && (!q || c.name.toLowerCase().includes(q))).forEach((c) => rows.push({ sect: "Pick one cluster", value: c.name, c }));
        at = Math.max(1, rows.findIndex((r) => r.value === sel.value));
        paint();
        return;
      }
      if (!q) {
        rows.push({ sect: "Scope", value: "*", label: "All clusters", count: K.state.ctxs.length });
        K.groups().forEach((g) => rows.push({ sect: "Scope", value: g.value, label: g.label, count: g.count, group: true }));
      } else {
        const n = K.members("@~" + raw).length;
        if (n > 1) rows.push({ sect: "Scope", value: "@~" + raw, label: `All ${n} clusters matching “${raw}”`, count: n });
        K.groups().filter((g) => g.label.toLowerCase().includes(q)).forEach((g) => rows.push({ sect: "Scope", value: g.value, label: g.label, count: g.count, group: true }));
      }
      const pinned = K.pins();
      if (!q) pinned.forEach((n) => rows.push({ sect: "Pinned", value: n, c: K.state.ctxs.find((c) => c.name === n) }));
      K.state.ctxs.filter((c) => !q || (c.name + " " + K.provider(c)[0] + " " + K.envTags(c).map((t) => t[0]).join(" ")).toLowerCase().includes(q))
        .sort((a, b) => (pinned.includes(b.name) ? 1 : 0) - (pinned.includes(a.name) ? 1 : 0))
        .forEach((c) => rows.push({ sect: "Clusters", value: c.name, c }));
      at = Math.max(0, rows.findIndex((r) => r.value === sel.value));
      paint();
    };
    // a group opens its list of clusters; everything else is chosen right away
    const pick = (r) => {
      if (r.back) { drill = ""; input.value = ""; draw(); input.focus(); }
      else if (r.group) { drill = r.value; input.value = ""; draw(); input.focus(); }
      else choose(r.value);
    };
    const paint = () => {
      const out = []; let last = "";
      rows.forEach((r, i) => {
        if (r.sect !== last) { last = r.sect; out.push(h("div", { class: "cb-sect" }, r.sect)); }
        out.push(h("div", { class: "cb-row" + (i === at ? " on" : "") + (r.value === sel.value ? " cur" : ""), role: "option", "data-i": i, onmousedown: (e) => { e.preventDefault(); pick(r); } },
          r.c ? [...badges(r.c), h("span", { class: "cb-name" }, r.c.name), h("span", { class: "cb-pin" + (K.isPinned(r.c.name) ? " on" : ""), title: K.isPinned(r.c.name) ? "Unpin" : "Pin to the top",
            onmousedown: (e) => { e.preventDefault(); e.stopPropagation(); K.togglePin(r.c.name); draw(); K.onPins && K.onPins(); } }, K.isPinned(r.c.name) ? "★" : "☆")]
            : [h("span", { class: "cb-name" }, (r.back ? "" : "◎ ") + r.label), r.group ? h("span", { class: "faint" }, r.count + " ›") : r.back ? null : h("span", { class: "faint" }, r.count + "")]));
      });
      if (!rows.length) out.push(h("div", { class: "cb-empty faint" }, "No cluster matches."));
      list.replaceChildren(...out);
      const on = list.querySelector(".cb-row.on"); if (on) on.scrollIntoView({ block: "nearest" });
    };
    const open = () => { pop.hidden = false; drill = ""; input.value = ""; draw(); input.focus(); document.addEventListener("mousedown", outside, true); };
    btn.addEventListener("click", () => (pop.hidden ? open() : close()));
    input.addEventListener("input", draw);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Escape") { if (drill) { drill = ""; draw(); } else { close(); btn.focus(); } }
      else if (e.key === "ArrowDown") { e.preventDefault(); at = Math.min(rows.length - 1, at + 1); paint(); }
      else if (e.key === "ArrowUp") { e.preventDefault(); at = Math.max(0, at - 1); paint(); }
      else if (e.key === "Enter" && rows[at]) { e.preventDefault(); pick(rows[at]); }
    });
    sync();
    return { sync };
  };

  let toastTimer;
  K.toast = function (msg, err) {
    const t = document.getElementById("toast");
    t.textContent = msg;
    t.className = "toast" + (err ? " err" : "");
    t.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (t.hidden = true), err ? 6000 : 2800);
  };

  K.closeModal = function () {
    const m = document.getElementById("modal");
    m.hidden = true;
    m.replaceChildren();
    if (K._modalDone) { const f = K._modalDone; K._modalDone = null; f(); }
  };

  K.modal = function (node, onClose) {
    const m = document.getElementById("modal");
    m.replaceChildren(node);
    m.hidden = false;
    K._modalDone = onClose || null;
    m.onmousedown = (e) => { if (e.target === m) K.closeModal(); };
  };

  K.copy = async function (text) {
    try { await navigator.clipboard.writeText(text); K.toast("Copied"); } catch { K.toast("Copy failed", true); }
  };

  // ---------- resource drawer ----------
  let drawerCleanup = null;
  K.closeDrawer = function () {
    const d = document.getElementById("drawer");
    if (drawerCleanup) { drawerCleanup(); drawerCleanup = null; }
    d.hidden = true;
    d.replaceChildren();
  };

  // res: {cluster, kind (catalogue plural or ""), ns, name, row?, group, version, resource, title}
  K.openResource = function (res) {
    K.closeDrawer();
    const d = document.getElementById("drawer");
    const kind = K.state.kinds.find((k) => k.name === (res.kind || res.resource) && (res.group === undefined || k.group === res.group));
    const tabs = [["overview", "Overview"], ["yaml", "YAML"]];
    if (kind && K.whyKinds.includes(kind.name)) tabs.push(["why", "Why?"]);
    if (kind && kind.name === "pods") tabs.push(["logs", "Logs"]);
    if (kind && K.relatedKinds.includes(kind.name)) tabs.push(["related", "Related"]);
    if (kind && K.impactKinds.includes(kind.name)) tabs.push(["impact", "Impact"]);
    if (kind && !res.group && K.incidentKinds.includes(kind.name)) tabs.push(["incident", "Incident"]);
    tabs.push(["events", "Events"]);
    const body = h("div", { class: "body" });
    const bar = h("div", { class: "tabs" });
    const show = (id) => {
      if (drawerCleanup) { drawerCleanup(); drawerCleanup = null; }
      [...bar.children].forEach((b) => b.classList.toggle("on", b.dataset.id === id));
      body.replaceChildren();
      const r = K.drawerTabs[id](body, res, kind);
      if (typeof r === "function") drawerCleanup = r;
    };
    tabs.forEach(([id, label]) => bar.append(h("button", { "data-id": id, onclick: () => show(id) }, label)));
    d.append(h("header", {},
      h("button", { class: "btn small close", onclick: K.closeDrawer, "aria-label": "Close" }, "Close ✕"),
      h("div", { class: "faint" }, (res.title || (kind && kind.singular) || res.resource || "") + (res.ns ? " · " + res.ns : "") + " · " + res.cluster),
      h("h2", {}, res.name), bar), body);
    d.hidden = false;
    show("overview");
  };

  // Forward a local port to a pod or a service. Binds 127.0.0.1 only.
  K.forwardDialog = function (res, kind) {
    const remote = h("input", { type: "number", min: 1, max: 65535, placeholder: kind === "services" ? "service port" : "container port" });
    const local = h("input", { type: "number", min: 0, max: 65535, placeholder: "any free port" });
    const out = h("div", {});
    const go = h("button", { class: "btn primary", onclick: async () => {
      go.disabled = true;
      try {
        const f = await K.api("/api/forward", {}, { context: res.cluster, kind, namespace: res.ns, name: res.name,
          remotePort: parseInt(remote.value || "0", 10), localPort: parseInt(local.value || "0", 10) });
        out.replaceChildren(h("div", { class: "banner" }, "Forwarding ", h("b", { class: "mono" }, f.address), " → pod ", f.pod, ":" + f.remotePort, " "),
          h("a", { href: "http://" + f.address, target: "_blank", rel: "noopener" }, "Open in browser"), " · ", h("a", { href: "#/forwards", onclick: K.closeModal }, "All port forwards"));
      } catch (e) { out.replaceChildren(h("div", { class: "banner err" }, e.message)); go.disabled = false; }
    } }, "Start");
    K.modal(h("div", { class: "dialog", role: "dialog", "aria-modal": "true" },
      h("h3", {}, "Forward a port to " + res.name),
      K.isProd(res.cluster) ? h("div", { class: "banner err" }, "This looks like a production cluster.") : null,
      h("p", { class: "muted" }, "Opens a port on 127.0.0.1 only, for as long as Kendra runs. Leave the service port empty if it has just one."),
      h("label", {}, "Remote port", remote), h("label", {}, "Local port", local), out,
      h("div", { class: "row" }, h("button", { class: "btn", onclick: K.closeModal }, "Close"), go)));
    remote.focus();
  };

  const resQuery = (res) => ({ context: res.cluster, kind: res.kind, ns: res.ns, name: res.name,
    group: res.group, version: res.version, resource: res.resource });

  K.drawerTabs = {
    overview(body, res, kind) {
      const row = res.row;
      const dl = h("dl", { class: "kv" });
      const add = (k, v) => { if (v !== "" && v != null) dl.append(h("dt", {}, k), h("dd", {}, v)); };
      add("Cluster", res.cluster); add("Namespace", res.ns);
      if (row) {
        add("Health", K.pill(row.health, row.health + (row.note ? " · " + row.note : "")));
        const cols = res.cols || [];
        cols.forEach((c, i) => { if (c.name !== "Name" && row.cells[i]) add(c.name, row.cells[i]); });
        if (row.hasUsage) { add("CPU now", row.cpuM + "m"); add("Memory now", K.bytes(row.memB)); }
      } else if (res.health) {
        add("Health", K.pill(res.health, res.health + (res.detail ? " · " + res.detail : "")));
        add("Detail", "");
      }
      body.append(dl);
      // Kendra is read-only: the only action is opening a local port, which changes nothing in the cluster.
      const acts = h("div", { class: "actions" });
      const n = kind ? kind.name : res.resource;
      if ((n === "pods" || n === "services") && !res.group) {
        acts.append(h("button", { class: "btn", onclick: () => K.forwardDialog(res, n) }, "Forward port…"));
      }
      if (acts.children.length) body.append(acts);
    },

    async yaml(body, res) {
      const pre = h("pre", { class: "code" }, "Loading…");
      let text = "";
      body.append(h("div", { class: "actions" }, h("button", { class: "btn small", onclick: () => K.copy(text) }, "Copy")), pre);
      try {
        const r = await K.api("/api/yaml", resQuery(res));
        text = r.yaml;
        pre.textContent = text;
        if (r.masked) body.prepend(h("div", { class: "banner warn" }, "Secret values are hidden. Kendra never sends them to the browser."));
      } catch (e) { pre.textContent = e.message; }
    },

    async events(body, res) {
      body.append(h("div", { class: "muted" }, "Loading…"));
      try {
        const ev = await K.api("/api/events", { context: res.cluster, ns: res.ns, name: res.name });
        body.replaceChildren();
        if (!ev.length) body.append(h("div", { class: "empty" }, "No events."));
        ev.forEach((e) => body.append(h("div", { class: "finding " + (e.Type === "Warning" ? "warn" : "ok") },
          h("b", {}, e.Reason + (e.Count > 1 ? " ×" + e.Count : "") + " · " + K.ago(e.Last) + " ago"), e.Message)));
      } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); }
    },

    async related(body, res, kind) {
      body.append(h("div", { class: "muted" }, "Looking at what it uses and what uses it…"));
      try {
        const r = await K.api("/api/related", { context: res.cluster, kind: kind.name, ns: res.ns, name: res.name });
        body.replaceChildren(K.relBlock("Uses", r.uses, res.cluster, "Nothing it refers to."), K.relBlock("Used by", r.usedBy, res.cluster, "Nothing refers to it."));
      } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); }
    },

    async incident(body, res, kind) {
      body.append(h("div", { class: "muted" }, "Gathering what is known about it…"));
      try {
        const r = await K.api("/api/incident", { context: res.cluster, kind: kind.name, ns: res.ns, name: res.name });
        const file = `incident-${res.name}-${new Date().toISOString().slice(0, 10)}.md`;
        body.replaceChildren(
          h("div", { class: "faint dep-note" }, "A Markdown report for a ticket or chat: what is wrong, events, pods, what it uses, what depends on it, recent changes and commands. No logs, YAML or Secret values."),
          h("div", { class: "actions" },
            h("button", { class: "btn small", onclick: () => K.copy(r.markdown) }, "Copy Markdown"),
            h("button", { class: "btn small", onclick: () => K.saveText(file, r.markdown, "text/markdown").catch((e) => K.toast(e.message, true)) }, "Save .md")),
          h("pre", { class: "code" }, r.markdown));
      } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); }
    },

    async impact(body, res, kind) {
      body.append(h("div", { class: "muted" }, "Working out what would be affected…"));
      try {
        const r = await K.api("/api/impact", { context: res.cluster, kind: kind.name, ns: res.ns, name: res.name });
        const worst = [...r.services, ...r.ingresses].some((x) => x.level === "bad");
        body.replaceChildren(h("div", { class: "banner " + (worst ? "err" : "warn") }, h("b", {}, "If this goes away: "), r.summary.join(" · ")),
          h("div", { class: "faint dep-note" }, "Worked out from pod specs and selectors. Nothing is changed."),
          K.relBlock("Workloads that stop", r.workloads, res.cluster, "No workload depends on it."), K.relBlock("Services affected", r.services, res.cluster, "No service loses a ready pod."),
          K.relBlock("Ingresses affected", r.ingresses, res.cluster, "No ingress is affected."), K.relBlock("Volumes involved", r.volumes, res.cluster, "No volume claims."));
      } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); }
    },

    async why(body, res, kind) {
      body.append(h("div", { class: "muted" }, "Looking at status and events…"));
      try {
        const f = await K.api("/api/explain", { context: res.cluster, ns: res.ns, name: res.name, kind: kind ? kind.name : "" });
        body.replaceChildren();
        f.forEach((x) => body.append(h("div", { class: "finding " + x.level }, h("b", {}, x.title), x.detail, x.hint ? h("div", { class: "hint" }, x.hint) : null)));
      } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); }
    },

    logs(body, res) {
      const pre = h("pre", { class: "code wrap" }, "");
      const sel = h("select", {});
      const prev = h("input", { type: "checkbox" });
      const follow = h("input", { type: "checkbox", checked: true });
      const find = h("input", { type: "text", class: "filter", placeholder: "Search in logs", style: "min-width:160px;padding:4px 12px" });
      let ctl = null, lines = [];
      const paint = () => {
        const f = find.value.toLowerCase();
        pre.textContent = (f ? lines.filter((l) => l.toLowerCase().includes(f)) : lines).join("\n");
        pre.scrollTop = pre.scrollHeight;
      };
      const start = async () => {
        if (ctl) ctl.abort();
        ctl = new AbortController();
        lines = [];
        pre.textContent = "";
        let tail = "";
        try {
          const u = new URL("/api/logs", location.origin);
          Object.entries({ context: res.cluster, ns: res.ns, name: res.name, container: sel.value, previous: prev.checked ? "1" : "", follow: follow.checked && !prev.checked ? "1" : "", tail: "500" })
            .forEach(([k, v]) => v && u.searchParams.set(k, v));
          const r = await fetch(u, { signal: ctl.signal, credentials: "same-origin" });
          if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
          const rd = r.body.getReader(), dec = new TextDecoder();
          for (;;) {
            const { value, done } = await rd.read();
            if (done) break;
            tail += dec.decode(value, { stream: true });
            const parts = tail.split("\n");
            tail = parts.pop();
            lines.push(...parts);
            if (lines.length > 5000) lines = lines.slice(-5000);
            paint();
          }
          if (tail) { lines.push(tail); paint(); }
          if (!lines.length) pre.textContent = "(no log output)";
        } catch (e) { if (e.name !== "AbortError") pre.textContent += "\n" + e.message; }
      };
      body.append(h("div", { class: "actions" }, h("label", {}, "Container ", sel),
        h("label", {}, prev, " Previous run"), h("label", {}, follow, " Follow"), find), pre);
      sel.addEventListener("change", start); prev.addEventListener("change", start); follow.addEventListener("change", start);
      find.addEventListener("input", paint);
      K.api("/api/containers", { context: res.cluster, ns: res.ns, name: res.name }).then((cs) => {
        cs.forEach((c) => sel.append(h("option", { value: c }, c)));
        if (cs.length > 1) sel.value = cs[cs.length - 1];
        start();
      }).catch((e) => (pre.textContent = e.message));
      return () => ctl && ctl.abort();
    },
  };
})(globalThis);
