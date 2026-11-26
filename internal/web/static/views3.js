// Insights: certificates, image inventory, usage vs requests, compare two clusters.
(function () {
  const { h } = K;
  const V = K.views;
  const F = K.fleet;
  const enc = encodeURIComponent;
  const head = (title, sub, ...right) => h("div", { class: "page-head" }, h("h1", {}, title), sub, h("span", { class: "grow" }), right);
  const refresh = () => h("button", { class: "btn small", onclick: () => K.refresh() }, "Refresh");
  const filterBox = (ph) => h("input", { class: "filter", id: "filter", placeholder: ph, spellcheck: "false" });
  const chip = (cls, on, label, n, click) => h("button", { class: "fl-chip " + cls + (on ? " on" : ""), onclick: click }, h("b", {}, n), " " + label);
  // clusters where an add-on is missing are one quiet line, not one banner each
  const issues = (names, res) => {
    const absent = names.filter((n) => res[n] && res[n].error && /is not installed/.test(res[n].error));
    const failed = names.filter((n) => res[n] && res[n].error && !/is not installed/.test(res[n].error)).map((n) => res[n]);
    return [...F.errBanners(failed), absent.length ? h("div", { class: "faint dep-clean" }, "Not installed in: " + absent.join(", ")) : null];
  };
  const day = (iso) => (iso ? iso.slice(0, 10) : "");

  // ---------------- certificates (cert-manager) ----------------
  V.certs = async function (main, _a, live) {
    const names = F.scopeNames();
    const filter = filterBox("Filter by cluster, namespace, host, issuer   ( / )");
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    const res = {}; let doneN = 0, bucket = "", shown = [];
    const bucketOf = (c) => (c.ready === "False" ? "notready" : !c.hasExpiry ? "none" : c.daysLeft < 0 ? "expired" : c.daysLeft <= 30 ? "soon" : "ok");
    const exportTable = () => ({
      head: ["Cluster", "Namespace", "Certificate", "Days left", "Expires", "Renews", "Issuer", "Hosts", "Ready", "Reason"],
      rows: shown.map((c) => [c.cluster, c.namespace, c.name, c.hasExpiry ? c.daysLeft : "", day(c.notAfter), day(c.renewal), c.issuer, (c.dnsNames || []).join("; "), c.ready, c.reason || ""]),
    });
    main.append(head("Certificates", sub, K.exportBar(exportTable, "kendra-certificates"), refresh()),
      h("div", { class: "tools" }, filter), strip, body);
    const draw = () => {
      const all = names.flatMap((n) => (res[n] && res[n].data ? res[n].data.map((c) => ({ ...c, cluster: n })) : []));
      const cnt = { expired: 0, soon: 0, ok: 0, notready: 0, none: 0 };
      all.forEach((c) => cnt[bucketOf(c)]++);
      const f = filter.value.trim().toLowerCase();
      shown = all.filter((c) => (!bucket || bucketOf(c) === bucket) && (!f || [c.cluster, c.namespace, c.name, c.issuer, ...(c.dnsNames || [])].join(" ").toLowerCase().includes(f)))
        .sort((a, b) => (a.hasExpiry === b.hasExpiry ? a.daysLeft - b.daysLeft : a.hasExpiry ? -1 : 1));
      sub.textContent = `${K.scopeLabel(K.multi() ? K.state.ctx : K.state.ctx)} · ${all.length} certificate${all.length === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      const t = (b) => () => { bucket = bucket === b ? "" : b; draw(); };
      strip.replaceChildren(chip("bad", bucket === "expired", "expired", cnt.expired, t("expired")), chip("warn", bucket === "soon", "expire within 30 days", cnt.soon, t("soon")),
        chip("bad", bucket === "notready", "not ready", cnt.notready, t("notready")), chip("ok", bucket === "ok", "fine", cnt.ok, t("ok")));
      const multi = names.length > 1;
      body.replaceChildren(...issues(names, res), shown.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, [multi ? "Cluster" : null, "Certificate", "Expires in", "Expires", "Renews", "Issuer", "Hosts", "Ready"].filter(Boolean).map((c) => h("th", {}, c)))),
        h("tbody", {}, shown.map((c) => {
          const b = bucketOf(c);
          const left = !c.hasExpiry ? "–" : c.daysLeft < 0 ? `expired ${-c.daysLeft}d ago` : c.daysLeft === 0 ? "today" : c.daysLeft + " days";
          return h("tr", { class: b === "expired" || b === "notready" ? "h-bad" : b === "soon" ? "h-warn" : "h-ok", title: c.message || "" },
            multi ? h("td", { class: "muted" }, c.cluster) : null,
            h("td", { class: "name" }, c.namespace + "/" + c.name),
            h("td", {}, c.hasExpiry ? K.pill(b === "ok" ? "ok" : b === "soon" ? "warn" : "bad", left) : h("span", { class: "faint" }, left)),
            h("td", { class: "mono" }, day(c.notAfter)), h("td", { class: "mono muted" }, day(c.renewal)),
            h("td", {}, c.issuer), h("td", {}, (c.dnsNames || []).slice(0, 3).map((x) => h("span", { class: "chip" }, x)), (c.dnsNames || []).length > 3 ? h("span", { class: "faint" }, " +" + (c.dnsNames.length - 3)) : null),
            h("td", {}, c.ready === "True" ? K.pill("ok", "ready") : K.pill(c.ready === "False" ? "bad" : "warn", c.reason || c.ready)));
        })))) : doneN >= names.length ? h("div", { class: "empty" }, all.length ? "No certificate matches." : "No cert-manager certificates found.") : null);
    };
    filter.addEventListener("input", draw);
    draw();
    await F.loadPer("/api/certificates", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- image inventory ----------------
  V.images = async function (main, _a, live) {
    const names = F.scopeNames();
    const filter = filterBox("Search an image, tag, namespace or cluster   ( / )");
    const spread = h("input", { type: "checkbox" }), loose = h("input", { type: "checkbox" });
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    const res = {}; let doneN = 0, shown = [];
    const open = new Set();
    const exportTable = () => ({
      head: ["Repository", "Tag", "Digest pinned", "Cluster", "Namespace", "Pods"],
      rows: shown.flatMap((g) => g.rows.map((r) => [g.repo, r.tag, r.digest ? "yes" : "no", r.cluster, r.namespace, r.pods])),
    });
    main.append(head("Image inventory", sub, K.exportBar(exportTable, "kendra-images"), refresh()),
      h("div", { class: "tools" }, filter, h("label", { class: "chk" }, spread, " more than one tag in use"), h("label", { class: "chk" }, loose, " untagged or :latest")), strip, body);
    const isLoose = (r) => !r.tag || r.tag === "latest";
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      const rows = names.flatMap((n) => (res[n] && res[n].data ? res[n].data.map((r) => ({ ...r, cluster: n })) : []));
      const by = new Map();
      rows.forEach((r) => {
        if (f && !(r.image + " " + r.namespace + " " + r.cluster).toLowerCase().includes(f)) return;
        let g = by.get(r.repo); if (!g) by.set(r.repo, (g = { repo: r.repo, rows: [], tags: new Map(), clusters: new Set(), pods: 0 }));
        g.rows.push(r); g.clusters.add(r.cluster); g.pods += r.pods;
        const t = r.tag || (r.digest ? "digest" : "(none)");
        g.tags.set(t, (g.tags.get(t) || 0) + r.pods);
      });
      let groups = [...by.values()];
      const total = groups.length;
      if (spread.checked) groups = groups.filter((g) => g.tags.size > 1);
      if (loose.checked) groups = groups.filter((g) => g.rows.some(isLoose));
      groups.sort((a, b) => b.tags.size - a.tags.size || b.pods - a.pods || a.repo.localeCompare(b.repo));
      shown = groups;
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · ${total} image${total === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      const multiTag = [...by.values()].filter((g) => g.tags.size > 1).length, looseN = [...by.values()].filter((g) => g.rows.some(isLoose)).length;
      strip.replaceChildren(h("span", { class: "fl-chip warn" }, h("b", {}, multiTag), " with several tags in use"), h("span", { class: "fl-chip warn" }, h("b", {}, looseN), " untagged or :latest"),
        h("span", { class: "fl-tot faint" }, `${rows.reduce((s, r) => s + r.pods, 0)} pods in ${new Set(rows.map((r) => r.cluster)).size} cluster(s)`));
      const multi = names.length > 1;
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])), groups.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, ["Repository", "Tags in use", multi ? "Clusters" : null, "Pods"].filter(Boolean).map((c) => h("th", {}, c)))),
        h("tbody", {}, groups.slice(0, 400).flatMap((g) => {
          const tr = h("tr", { tabindex: 0, class: g.tags.size > 1 ? "h-warn" : "" },
            h("td", { class: "name mono" }, (open.has(g.repo) ? "▾ " : "▸ ") + g.repo),
            h("td", {}, [...g.tags].sort((a, b) => b[1] - a[1]).map(([t, n]) => h("span", { class: "chip" + (t === "latest" || t === "(none)" ? " state" : "") }, t + " × " + n))),
            multi ? h("td", {}, g.clusters.size) : null, h("td", {}, g.pods));
          const toggle = () => { open.has(g.repo) ? open.delete(g.repo) : open.add(g.repo); draw(); };
          tr.addEventListener("click", toggle);
          tr.addEventListener("keydown", (e) => { if (e.key === "Enter") toggle(); });
          const out = [tr];
          if (open.has(g.repo)) out.push(h("tr", { class: "detail" }, h("td", { colspan: multi ? 4 : 3 }, h("table", { class: "cards inner" },
            h("thead", {}, h("tr", {}, ["Cluster", "Namespace", "Tag", "Pods"].map((c) => h("th", {}, c)))),
            h("tbody", {}, g.rows.sort((a, b) => (a.cluster + a.namespace).localeCompare(b.cluster + b.namespace)).map((r) =>
              h("tr", {}, h("td", { class: "muted" }, r.cluster), h("td", {}, r.namespace), h("td", { class: "mono" }, r.tag || (r.digest ? "digest only" : "(none)")), h("td", {}, r.pods))))))));
          return out;
        })))) : doneN >= names.length ? h("div", { class: "empty" }, "No image matches.") : null,
        groups.length > 400 ? h("div", { class: "faint" }, "Showing the first 400 images. Narrow the search.") : null);
    };
    [filter, spread, loose].forEach((x) => x.addEventListener(x === filter ? "input" : "change", draw));
    draw();
    await F.loadPer("/api/images", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- usage vs requests ----------------
  V.usage = async function (main, _a, live) {
    const names = F.scopeNames();
    const filter = filterBox("Filter by cluster, namespace or workload   ( / )");
    const by = h("select", {}, h("option", { value: "workload" }, "per workload"), h("option", { value: "namespace" }, "per namespace"));
    const sortSel = h("select", {}, h("option", { value: "cpuWaste" }, "most CPU unused"), h("option", { value: "memWaste" }, "most memory unused"),
      h("option", { value: "cpuOver" }, "using most above request"), h("option", { value: "none" }, "no requests set"));
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    const res = {}; let doneN = 0, shown = [], verdictFilter = "";
    const m = (x) => (x >= 1000 ? (x / 1000).toFixed(x % 1000 ? 1 : 0) + " cores" : x + "m");
    const verdict = (r, hasMetrics) => {
      if (r.noRequests > 0 && r.cpuReq === 0 && r.memReq === 0) return "none";
      if (!hasMetrics) return "";
      if ((r.cpuReq && r.cpuUsed > r.cpuReq) || (r.memReq && r.memUsed > r.memReq)) return "starved";
      if (r.pods > 0 && r.cpuReq >= 100 && r.cpuUsed < r.cpuReq * 0.2 && (r.memReq === 0 || r.memUsed < r.memReq * 0.5)) return "over";
      return "ok";
    };
    const exportTable = () => ({
      head: ["Cluster", "Namespace", "Workload", "Pods", "CPU used (m)", "CPU requested (m)", "CPU limit (m)", "Memory used (MiB)", "Memory requested (MiB)", "Memory limit (MiB)", "Verdict"],
      rows: shown.map((r) => [r.cluster, r.namespace, r.workload, r.pods, r.cpuUsed, r.cpuReq, r.cpuLim, Math.round(r.memUsed / 1048576), Math.round(r.memReq / 1048576), Math.round(r.memLim / 1048576), r.verdict]),
    });
    main.append(head("Usage vs requests", sub, K.exportBar(exportTable, "kendra-usage"), refresh()),
      h("div", { class: "tools" }, filter, by, h("span", { class: "faint" }, "sorted by"), sortSel), strip, body);
    const bar = (used, req, lim, fmt) => {
      if (!req && !used) return h("span", { class: "faint" }, "–");
      const pct = req ? Math.round((100 * used) / req) : 0, cls = !req ? "" : pct > 100 ? "bad" : pct < 20 ? "low" : "";
      return h("div", { class: "ubar " + cls, title: `used ${fmt(used)} · requested ${req ? fmt(req) : "none"} · limit ${lim ? fmt(lim) : "none"}` },
        h("i", {}, h("b", { style: `width:${Math.min(100, req ? pct : 100)}%` })), h("span", { class: "mono" }, fmt(used) + " / " + (req ? fmt(req) : "no request"), req ? h("span", { class: "faint" }, " " + pct + "%") : null));
    };
    const draw = () => {
      const noMetrics = names.filter((n) => res[n] && res[n].data && !res[n].data.metrics);
      let rows = names.flatMap((n) => (res[n] && res[n].data ? res[n].data.rows.map((r) => ({ ...r, cluster: n, hasMetrics: res[n].data.metrics })) : []));
      if (by.value === "namespace") {
        const mp = new Map();
        rows.forEach((r) => {
          const k = r.cluster + "|" + r.namespace; let a = mp.get(k);
          if (!a) mp.set(k, (a = { cluster: r.cluster, namespace: r.namespace, workload: "(all)", pods: 0, cpuUsed: 0, cpuReq: 0, cpuLim: 0, memUsed: 0, memReq: 0, memLim: 0, noRequests: 0, hasMetrics: r.hasMetrics, n: 0 }));
          for (const f of ["pods", "cpuUsed", "cpuReq", "cpuLim", "memUsed", "memReq", "memLim", "noRequests"]) a[f] += r[f];
          a.n++;
        });
        rows = [...mp.values()];
      }
      rows.forEach((r) => (r.verdict = verdict(r, r.hasMetrics)));
      const cnt = { over: 0, starved: 0, none: 0 };
      rows.forEach((r) => { if (cnt[r.verdict] !== undefined) cnt[r.verdict]++; });
      const f = filter.value.trim().toLowerCase();
      const key = { cpuWaste: (r) => -(r.cpuReq - r.cpuUsed), memWaste: (r) => -(r.memReq - r.memUsed), cpuOver: (r) => -(r.cpuReq ? r.cpuUsed / r.cpuReq : 0), none: (r) => -r.noRequests }[sortSel.value];
      shown = rows.filter((r) => (!verdictFilter || r.verdict === verdictFilter) && (!f || (r.cluster + " " + r.namespace + " " + r.workload).toLowerCase().includes(f))).sort((a, b) => key(a) - key(b));
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · ${rows.length} ${by.value === "namespace" ? "namespace" : "workload"}${rows.length === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      const t = (v) => () => { verdictFilter = verdictFilter === v ? "" : v; draw(); };
      strip.replaceChildren(chip("warn", verdictFilter === "over", "use under 20% of their CPU request", cnt.over, t("over")), chip("bad", verdictFilter === "starved", "use more than they requested", cnt.starved, t("starved")),
        chip("warn", verdictFilter === "none", "have no requests", cnt.none, t("none")));
      const multi = names.length > 1;
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])),
        noMetrics.length ? h("div", { class: "banner" }, "No metrics-server data in: " + noMetrics.join(", ") + ". Requests are shown, usage is not.") : null,
        shown.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
          h("thead", {}, h("tr", {}, [multi ? "Cluster" : null, "Namespace", by.value === "namespace" ? null : "Workload", "Pods", "CPU used / requested", "Memory used / requested", ""].filter(Boolean).map((c) => h("th", {}, c)))),
          h("tbody", {}, shown.slice(0, 500).map((r) => h("tr", { class: r.verdict === "starved" ? "h-bad" : r.verdict === "over" || r.verdict === "none" ? "h-warn" : "" },
            multi ? h("td", { class: "muted" }, r.cluster) : null, h("td", {}, r.namespace), by.value === "namespace" ? null : h("td", { class: "name" }, r.workload), h("td", {}, r.pods),
            h("td", {}, bar(r.cpuUsed, r.cpuReq, r.cpuLim, m)), h("td", {}, bar(r.memUsed, r.memReq, r.memLim, K.bytes)),
            h("td", {}, r.verdict === "over" ? K.pill("warn", "oversized") : r.verdict === "starved" ? K.pill("bad", "above request") : r.verdict === "none" ? K.pill("warn", "no requests") : null)))))) :
          doneN >= names.length ? h("div", { class: "empty" }, "Nothing matches.") : null,
        shown.length > 500 ? h("div", { class: "faint" }, "Showing the first 500 rows. Filter, or export for all.") : null);
    };
    [filter].forEach((x) => x.addEventListener("input", draw));
    [by, sortSel].forEach((x) => x.addEventListener("change", draw));
    draw();
    await F.loadPer("/api/usage", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- security: risky pod settings and Pod Security labels ----------------
  V.security = async function (main, _a, live) {
    const names = F.scopeNames();
    const filter = filterBox("Filter by cluster, namespace, workload or risk   ( / )");
    const sys = h("input", { type: "checkbox", checked: true });
    const mode = h("select", {}, h("option", { value: "workloads" }, "risky workloads"), h("option", { value: "namespaces" }, "Pod Security labels per namespace"));
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    const res = {}; let doneN = 0, shownW = [], shownN = [], level = "";
    const exportTable = () => mode.value === "workloads"
      ? { head: ["Cluster", "Namespace", "Workload", "Pods", "Level", "Risks"], rows: shownW.map((w) => [w.cluster, w.namespace, w.workload, w.pods, w.risks.some((r) => r.level === "bad") ? "high" : "medium", w.risks.map((r) => r.text).join("; ")]) }
      : { head: ["Cluster", "Namespace", "Enforce", "Audit", "Warn"], rows: shownN.map((n) => [n.cluster, n.namespace, n.enforce || "none", n.audit || "none", n.warn || "none"]) };
    main.append(head("Security", sub, K.exportBar(exportTable, "kendra-security"), refresh()),
      h("div", { class: "tools" }, filter, mode, h("label", { class: "chk" }, sys, " hide kube-* namespaces")), strip, body);
    const isSys = (ns) => ns.startsWith("kube-");
    const draw = () => {
      const f = filter.value.trim().toLowerCase(), hide = (ns) => sys.checked && isSys(ns);
      const wl = names.flatMap((n) => (res[n] && res[n].data ? res[n].data.workloads.map((w) => ({ ...w, cluster: n })) : [])).filter((w) => !hide(w.namespace));
      const ns = names.flatMap((n) => (res[n] && res[n].data ? res[n].data.namespaces.map((x) => ({ ...x, cluster: n })) : [])).filter((x) => !hide(x.namespace));
      const high = (w) => w.risks.some((r) => r.level === "bad");
      const noEnf = ns.filter((x) => !x.enforce);
      shownW = wl.filter((w) => (!level || (level === "high" ? high(w) : !high(w))) && (!f || (w.cluster + " " + w.namespace + " " + w.workload + " " + w.risks.map((r) => r.text).join(" ")).toLowerCase().includes(f)));
      shownN = ns.filter((x) => (level !== "none" || !x.enforce) && (!f || (x.cluster + " " + x.namespace + " " + x.enforce).toLowerCase().includes(f)));
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · ${wl.length} risky workload${wl.length === 1 ? "" : "s"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      const t = (v) => () => { level = level === v ? "" : v; if (v === "none") mode.value = "namespaces"; else if (mode.value !== "workloads") mode.value = "workloads"; draw(); };
      strip.replaceChildren(chip("bad", level === "high", "high risk workloads", wl.filter(high).length, t("high")), chip("warn", level === "medium", "medium risk", wl.filter((w) => !high(w)).length, t("medium")),
        chip("warn", level === "none", "namespaces without enforcement", noEnf.length, t("none")));
      const multi = names.length > 1, errs = F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n]));
      if (mode.value === "workloads") {
        body.replaceChildren(...errs, shownW.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
          h("thead", {}, h("tr", {}, [multi ? "Cluster" : null, "Namespace", "Workload", "Pods", "Risks"].filter(Boolean).map((c) => h("th", {}, c)))),
          h("tbody", {}, shownW.slice(0, 500).map((w) => h("tr", { class: high(w) ? "h-bad" : "h-warn" },
            multi ? h("td", { class: "muted" }, w.cluster) : null, h("td", {}, w.namespace), h("td", { class: "name" }, w.workload), h("td", {}, w.pods),
            h("td", {}, w.risks.map((r) => K.pill(r.level, r.text)).flatMap((x) => [x, " "]))))))) : doneN >= names.length ? h("div", { class: "empty" }, wl.length ? "Nothing matches." : "No risky settings found.") : null);
      } else {
        body.replaceChildren(...errs, shownN.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
          h("thead", {}, h("tr", {}, [multi ? "Cluster" : null, "Namespace", "Enforce", "Audit", "Warn"].filter(Boolean).map((c) => h("th", {}, c)))),
          h("tbody", {}, shownN.slice(0, 800).map((x) => h("tr", { class: x.enforce ? "" : "h-warn" },
            multi ? h("td", { class: "muted" }, x.cluster) : null, h("td", { class: "name" }, x.namespace),
            ...["enforce", "audit", "warn"].map((k) => h("td", {}, x[k] ? K.pill(x[k] === "privileged" ? "warn" : "ok", x[k]) : h("span", { class: "faint" }, "none"))))))))
          : doneN >= names.length ? h("div", { class: "empty" }, "Nothing matches.") : null);
      }
    };
    filter.addEventListener("input", draw);
    [sys, mode].forEach((x) => x.addEventListener("change", draw));
    draw();
    await F.loadPer("/api/security", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- cloud identities: service accounts tied to AWS roles, Azure or GCP identities ----------------
  V.identities = async function (main, _a, live) {
    const names = F.scopeNames();
    const filter = filterBox("Filter by cluster, namespace, service account, role or workload   ( / )");
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    const res = {}; let doneN = 0, shown = [], pf = "";
    const exportTable = () => ({ head: ["Cluster", "Namespace", "Service account", "Provider", "Identity", "Pods", "Workloads"],
      rows: shown.map((x) => [x.cluster, x.namespace, x.serviceAccount, x.provider, x.identity, x.pods, (x.workloads || []).join("; ")]) });
    main.append(head("Cloud identities", sub, K.exportBar(exportTable, "kendra-identities"), refresh()),
      h("div", { class: "faint dep-note" }, "Service accounts that let pods act as an AWS IAM role (IRSA), an Azure workload identity or a Google service account, and the pods that run as them."),
      h("div", { class: "tools" }, filter), strip, body);
    const short = (x) => (x.provider === "AWS" ? x.identity.replace(/^arn:aws[a-z-]*:iam::/, "").replace(":role/", " · ") : x.identity);
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      const all = names.flatMap((n) => (res[n] && res[n].data ? res[n].data.map((x) => ({ ...x, cluster: n })) : []));
      const cnt = (p) => all.filter((x) => x.provider === p).length;
      shown = all.filter((x) => (!pf || (pf === "unused" ? x.pods === 0 : x.provider === pf)) && (!f || [x.cluster, x.namespace, x.serviceAccount, x.identity, ...(x.workloads || [])].join(" ").toLowerCase().includes(f)));
      sub.textContent = `${K.scopeLabel(K.state.ctx)} · ${all.length} identit${all.length === 1 ? "y" : "ies"}${doneN < names.length ? ` · reading ${doneN}/${names.length}…` : ""}`;
      const t = (v) => () => { pf = pf === v ? "" : v; draw(); };
      strip.replaceChildren(chip("ok", pf === "AWS", "AWS roles (IRSA)", cnt("AWS"), t("AWS")), chip("ok", pf === "Azure", "Azure workload identity", cnt("Azure"), t("Azure")),
        chip("ok", pf === "GCP", "GCP", cnt("GCP"), t("GCP")), chip("warn", pf === "unused", "not used by any pod", all.filter((x) => x.pods === 0).length, t("unused")));
      const multi = names.length > 1;
      body.replaceChildren(...F.errBanners(names.filter((n) => res[n] && res[n].error).map((n) => res[n])), shown.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, [multi ? "Cluster" : null, "Namespace", "Service account", "Cloud", "Identity", "Pods", "Used by"].filter(Boolean).map((c) => h("th", {}, c)))),
        h("tbody", {}, shown.slice(0, 600).map((x) => h("tr", { class: x.pods === 0 ? "h-warn" : "" },
          multi ? h("td", { class: "muted" }, x.cluster) : null, h("td", {}, x.namespace), h("td", { class: "name" }, x.serviceAccount),
          h("td", {}, h("span", { class: "ctx-badge " + ({ AWS: "eks", Azure: "aks", GCP: "gke" }[x.provider]) }, x.provider)),
          h("td", { class: "mono", title: x.identity }, short(x)), h("td", {}, x.pods === 0 ? K.pill("warn", "unused") : x.pods),
          h("td", {}, (x.workloads || []).slice(0, 3).map((w) => h("span", { class: "chip" }, w)), (x.workloads || []).length > 3 ? h("span", { class: "faint" }, " +" + (x.workloads.length - 3)) : null))))))
        : doneN >= names.length ? h("div", { class: "empty" }, all.length ? "Nothing matches." : "No service accounts with a cloud identity found.") : null);
    };
    filter.addEventListener("input", draw);
    draw();
    await F.loadPer("/api/identities", names, live, (n, r) => { res[n] = r; doneN++; if (live()) draw(); });
  };

  // ---------------- compare two clusters ----------------
  // #/compare/<cluster A>/<cluster B>/<namespace>
  V.compare = async function (main, [a0, b0, ns0], live) {
    const all = K.state.ctxs.map((c) => c.name);
    const mk = (v) => { const s = h("select", {}, all.map((n) => h("option", { value: n }, n))); s.value = all.includes(v) ? v : all[0]; return s; };
    const pool = K.multi() ? K.members(K.state.ctx) : all;
    const A = mk(a0 || (K.multi() ? pool[0] : K.state.ctx)), B = mk(b0 || pool.find((n) => n !== A.value) || all[0]);
    const ns = h("input", { class: "filter", placeholder: "Namespace (empty = all)", value: ns0 || "", style: "max-width:220px" });
    const sys = h("input", { type: "checkbox", checked: true }), same = h("input", { type: "checkbox" });
    const filter = filterBox("Filter by name or difference   ( / )");
    const sub = h("span", { class: "sub" }), strip = h("div", { class: "fl-strip" }), body = h("div", {});
    let rows = [], status = "", shown = [], names = [];
    const exportTable = () => ({ head: ["Status", "Kind", "Namespace", "Name", "Differences", names[0] || "A", names[1] || "B"],
      rows: shown.map((r) => [r.status, r.kind, r.namespace, r.name, r.diffs.join("; "), r.a ? "present" : "missing", r.b ? "present" : "missing"]) });
    main.append(head("Compare clusters", sub, K.exportBar(exportTable, "kendra-compare"), h("span", {}, "")),
      h("div", { class: "tools" }, h("label", { class: "chk" }, "A ", A), h("span", { class: "faint" }, "against"), h("label", { class: "chk" }, "B ", B), ns,
        h("button", { class: "btn small", onclick: () => go() }, "Compare")),
      h("div", { class: "tools" }, filter, h("label", { class: "chk" }, sys, " hide kube-* namespaces"), h("label", { class: "chk" }, same, " show identical")),
      h("div", { class: "faint dep-note" }, "Compares workloads (replicas, images), services, ingresses and ConfigMap key names. Values of ConfigMaps and Secrets are never read."), strip, body);
    const key = (x) => x.kind + "|" + x.namespace + "|" + x.name;
    const go = async () => {
      if (A.value === B.value) { body.replaceChildren(h("div", { class: "banner" }, "Pick two different clusters.")); return; }
      names = [A.value, B.value];
      history.replaceState(null, "", `#/compare/${enc(A.value)}/${enc(B.value)}/${enc(ns.value.trim())}`);
      body.replaceChildren(h("div", { class: "empty" }, "Reading both clusters…")); strip.replaceChildren();
      let r;
      try { r = await K.api("/api/snapshot", { context: names.join(","), ns: ns.value.trim() }); } catch (e) { body.replaceChildren(h("div", { class: "banner err" }, e.message)); return; }
      if (!live()) return;
      const bad = r.filter((x) => x.error);
      if (bad.length) { body.replaceChildren(...F.errBanners(bad)); return; }
      const ma = new Map(r[0].data.map((x) => [key(x), x])), mb = new Map(r[1].data.map((x) => [key(x), x]));
      rows = [...new Set([...ma.keys(), ...mb.keys()])].map((k) => {
        const x = ma.get(k), y = mb.get(k), ref = x || y, diffs = [];
        let status = "same";
        if (!x) status = "onlyB"; else if (!y) status = "onlyA";
        else {
          for (const p of new Set([...Object.keys(x.props), ...Object.keys(y.props)])) {
            const va = x.props[p], vb = y.props[p];
            if (va !== vb) diffs.push(`${p}: ${va === undefined ? "–" : va || "(empty)"} → ${vb === undefined ? "–" : vb || "(empty)"}`);
          }
          if (diffs.length) status = "diff";
        }
        return { kind: ref.kind, namespace: ref.namespace, name: ref.name, a: x, b: y, status, diffs };
      });
      draw();
    };
    const draw = () => {
      if (!names.length) return;
      const f = filter.value.trim().toLowerCase();
      const vis = rows.filter((r) => !(sys.checked && r.namespace.startsWith("kube-")));
      const cnt = { diff: 0, onlyA: 0, onlyB: 0, same: 0 };
      vis.forEach((r) => cnt[r.status]++);
      shown = vis.filter((r) => (status ? r.status === status : same.checked || r.status !== "same") && (!f || (r.kind + " " + r.namespace + " " + r.name + " " + r.diffs.join(" ")).toLowerCase().includes(f)))
        .sort((x, y) => ({ diff: 0, onlyA: 1, onlyB: 2, same: 3 }[x.status] - { diff: 0, onlyA: 1, onlyB: 2, same: 3 }[y.status]) || (x.namespace + x.name).localeCompare(y.namespace + y.name));
      sub.textContent = `A ${names[0]} · B ${names[1]}`;
      const t = (s) => () => { status = status === s ? "" : s; draw(); };
      strip.replaceChildren(chip("warn", status === "diff", "differ", cnt.diff, t("diff")), chip("bad", status === "onlyA", "only in A", cnt.onlyA, t("onlyA")), chip("bad", status === "onlyB", "only in B", cnt.onlyB, t("onlyB")), chip("ok", status === "same", "identical", cnt.same, t("same")));
      body.replaceChildren(shown.length ? h("div", { class: "tbl-scroll" }, h("table", { class: "cards" },
        h("thead", {}, h("tr", {}, ["", "Kind", "Namespace", "Name", "Difference (A → B)"].map((c) => h("th", {}, c)))),
        h("tbody", {}, shown.slice(0, 800).map((r) => h("tr", { class: r.status === "same" ? "" : r.status === "diff" ? "h-warn" : "h-bad" },
          h("td", {}, r.status === "same" ? K.pill("ok", "same") : r.status === "diff" ? K.pill("warn", "differs") : K.pill("bad", r.status === "onlyA" ? "only A" : "only B")),
          h("td", {}, r.kind), h("td", { class: "muted" }, r.namespace), h("td", { class: "name" }, r.name),
          h("td", { class: "mono diffs" }, r.status === "onlyA" ? "missing in B" : r.status === "onlyB" ? "missing in A" : r.diffs.map((d) => h("div", {}, d)))))))) : h("div", { class: "empty" }, rows.length ? "No differences." : "Nothing found."),
        shown.length > 800 ? h("div", { class: "faint" }, "Showing the first 800 rows. Narrow the namespace, or export for all.") : null);
    };
    filter.addEventListener("input", draw);
    [sys, same].forEach((x) => x.addEventListener("change", draw));
    if (a0 && b0) await go();
  };
})();
