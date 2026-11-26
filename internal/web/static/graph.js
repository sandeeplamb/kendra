// Layered topology renderer: nodes in columns by layer, curved edges, hover to trace.
// No dependencies. Everything is built with the DOM API (no innerHTML), so names that
// come from a cluster can never run as markup.
(function (root) {
  const NS = "http://www.w3.org/2000/svg";
  const W = 214, H = 54, GAPY = 16, PAD = 24, HEAD = 34;

  function el(tag, attrs, ...kids) {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
    for (const k of kids) if (k != null) e.append(k);
    return e;
  }
  const clip = (s, n) => (s && s.length > n ? s.slice(0, n - 1) + "…" : s || "");

  // graph: {nodes:[{id,kind,name,namespace,layer,health,detail,missing,external,count}], edges:[{from,to,type,label}]}
  // opts: {titles:{layer:"Gateways"}, onClick(node), empty:"text"}
  function render(host, graph, opts) {
    opts = opts || {};
    host.replaceChildren();
    if (!graph.nodes.length) {
      const d = document.createElement("div");
      d.className = "empty";
      d.textContent = opts.empty || "Nothing to draw.";
      host.append(d);
      return;
    }
    const GAPX = graph.edges.some((e) => e.label) ? 190 : 96; // room for edge labels
    const layers = [...new Set(graph.nodes.map((n) => n.layer))].sort((a, b) => a - b);
    const cols = layers.map((l) => graph.nodes.filter((n) => n.layer === l));
    const incoming = {};
    graph.edges.forEach((e) => (incoming[e.to] = (incoming[e.to] || []).concat(e.from)));
    const pos = {};
    cols.forEach((col, ci) => {
      if (ci > 0) {
        const idx = {};
        cols[ci - 1].forEach((n, i) => (idx[n.id] = i));
        const score = (n) => {
          const ps = (incoming[n.id] || []).map((p) => idx[p]).filter((x) => x !== undefined);
          return ps.length ? ps.reduce((a, b) => a + b, 0) / ps.length : 1e6;
        };
        col.sort((a, b) => score(a) - score(b));
      }
      col.forEach((n, ri) => (pos[n.id] = { x: PAD + ci * (W + GAPX), y: PAD + HEAD + ri * (H + GAPY) }));
    });
    const width = PAD * 2 + cols.length * W + (cols.length - 1) * GAPX;
    const height = PAD * 2 + HEAD + Math.max(...cols.map((c) => c.length)) * (H + GAPY);
    const svg = el("svg", { class: "graph", width, height, viewBox: `0 0 ${width} ${height}` });
    let zoom = 1;

    layers.forEach((l, ci) => {
      const t = (opts.titles && opts.titles[l]) || "";
      const g = el("text", { x: PAD + ci * (W + GAPX), y: PAD + 8, class: "g-title" });
      g.textContent = t.toUpperCase();
      svg.append(g);
    });

    const edgeEls = [];
    const edgeLayer = el("g", {});
    graph.edges.forEach((e) => {
      const a = pos[e.from], b = pos[e.to];
      if (!a || !b) return;
      const x1 = a.x + W, y1 = a.y + H / 2, x2 = b.x, y2 = b.y + H / 2;
      const back = x2 <= x1;
      const dx = back ? 60 : (x2 - x1) / 2;
      const path = el("path", { class: "g-edge t-" + e.type, d: `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}` });
      edgeLayer.append(path);
      const item = { e, path, label: null };
      if (e.label) {
        const mx = (x1 + x2) / 2, my = (y1 + y2) / 2;
        const txt = clip(e.label, 32);
        const w = txt.length * 5.6 + 10;
        const bg = el("rect", { class: "g-lbl-bg", x: mx - w / 2, y: my - 8, width: w, height: 15, rx: 7 });
        const t = el("text", { class: "g-lbl", x: mx, y: my + 3, "text-anchor": "middle" });
        t.textContent = txt;
        const title = el("title", {});
        title.textContent = e.label;
        const grp = el("g", { class: "g-lblg" }, bg, t, title);
        edgeLayer.append(grp);
        item.label = grp;
      }
      edgeEls.push(item);
    });
    svg.append(edgeLayer);

    const nodeEls = {};
    graph.nodes.forEach((n) => {
      const p = pos[n.id];
      const cls = ["g-node", "h-" + (n.health || "ok")];
      if (n.missing) cls.push("missing");
      if (n.external) cls.push("external");
      const title = el("title", {});
      title.textContent = [n.kind + " " + (n.namespace ? n.namespace + "/" : "") + n.name, n.detail].filter(Boolean).join("\n");
      const label = n.count ? `${n.count} pods` : n.name;
      const g = el("g", { class: cls.join(" "), transform: `translate(${p.x},${p.y})`, tabindex: 0 },
        title,
        el("rect", { class: "g-box", width: W, height: H, rx: 11 }),
        el("rect", { class: "g-bar", x: 0, y: 8, width: 4, height: H - 16, rx: 2 }));
      const k = el("text", { class: "g-kind", x: 14, y: 17 });
      k.textContent = n.missing ? n.kind + " · missing" : n.external ? n.kind + (opts.externalLabel === "" ? "" : " · " + (opts.externalLabel || "outside app")) : n.kind + (n.namespace ? " · " + clip(n.namespace, 16) : "");
      const nm = el("text", { class: "g-name", x: 14, y: 33 });
      nm.textContent = clip(label, 28);
      const dt = el("text", { class: "g-detail", x: 14, y: 47 });
      dt.textContent = clip(n.detail || "", 36);
      g.append(k, nm, dt);
      const open = () => opts.onClick && opts.onClick(n);
      g.addEventListener("click", open);
      g.addEventListener("keydown", (ev) => { if (ev.key === "Enter") open(); });
      const trace = (on) => {
        const near = new Set([n.id]);
        graph.edges.forEach((e) => { if (e.from === n.id) near.add(e.to); if (e.to === n.id) near.add(e.from); });
        Object.entries(nodeEls).forEach(([id, ne]) => ne.classList.toggle("dim", on && !near.has(id)));
        edgeEls.forEach((x) => {
          const hot = x.e.from === n.id || x.e.to === n.id;
          x.path.classList.toggle("dim", on && !hot);
          x.path.classList.toggle("hot", on && hot);
          if (x.label) x.label.classList.toggle("dim", on && !hot);
        });
      };
      g.addEventListener("mouseenter", () => trace(true));
      g.addEventListener("mouseleave", () => trace(false));
      g.addEventListener("focus", () => trace(true));
      g.addEventListener("blur", () => trace(false));
      nodeEls[n.id] = g;
      svg.append(g);
    });

    const scroller = document.createElement("div");
    scroller.className = "graph-scroll";
    scroller.append(svg);
    const bar = document.createElement("div");
    bar.className = "graph-tools";
    const setZoom = (z) => {
      zoom = Math.min(2, Math.max(0.4, z));
      svg.setAttribute("width", width * zoom);
      svg.setAttribute("height", height * zoom);
    };
    for (const [txt, f] of [["−", () => setZoom(zoom - 0.15)], ["+", () => setZoom(zoom + 0.15)], ["Fit", () => setZoom(Math.min(1, (scroller.clientWidth - 8) / width))]]) {
      const b = document.createElement("button");
      b.className = "btn small";
      b.textContent = txt;
      b.addEventListener("click", f);
      bar.append(b);
    }
    host.append(bar, scroller);
    requestAnimationFrame(() => { if (width > scroller.clientWidth) setZoom(Math.max(0.6, (scroller.clientWidth - 8) / width)); });
  }

  root.KGraph = { render };
})(globalThis);
