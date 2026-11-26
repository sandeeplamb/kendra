// Row filter for the resource lists. Pure functions, no DOM: tested with `node`.
//   api            words are ANDed; matches name, namespace, cluster and every column
//   !ok            only unhealthy rows (warning or failing)
//   ns:shop        namespace contains "shop"        cluster:prod   cluster contains "prod"
//   health:bad     exact health                     status:crash|error  column contains any
//   restarts>3     numeric column                   age<1h         created within the last hour
//   -kube-system   hide anything containing it
(function (root) {
  const UNITS = { s: 1, m: 60, h: 3600, d: 86400, w: 604800 };

  function parseAge(s) {
    const m = /^(\d+(?:\.\d+)?)([smhdw])$/.exec(s || "");
    return m ? parseFloat(m[1]) * UNITS[m[2]] : NaN;
  }

  function colValue(cols, row, name) {
    const i = cols.findIndex((c) => c.toLowerCase() === name || c.toLowerCase().startsWith(name));
    return i >= 0 ? row.cells[i] || "" : "";
  }

  function haystack(row, cols) {
    return [row.name, row.namespace, row.cluster || "", ...row.cells].join("\u0001").toLowerCase();
  }

  function compile(text) {
    const terms = (text || "").trim().split(/\s+/).filter(Boolean);
    return terms.map((t) => {
      const lower = t.toLowerCase();
      if (lower === "!ok") return (row) => row.health === "warn" || row.health === "bad";
      if (lower.length > 1 && lower[0] === "-") {
        const w = lower.slice(1);
        return (row, cols) => !haystack(row, cols).includes(w);
      }
      let m = /^([a-z_-]+)(>=|<=|>|<)(.+)$/.exec(lower);
      if (m) {
        const [, key, op, val] = m;
        return (row, cols, now) => {
          let a, b;
          if (key === "age") {
            a = (now - Date.parse(row.created)) / 1000;
            b = parseAge(val);
          } else {
            a = parseFloat(colValue(cols, row, key));
            b = parseFloat(val);
          }
          if (Number.isNaN(a) || Number.isNaN(b)) return false;
          return op === ">" ? a > b : op === "<" ? a < b : op === ">=" ? a >= b : a <= b;
        };
      }
      m = /^([a-z_-]+):(.+)$/.exec(lower);
      if (m) {
        const [, key, val] = m;
        const alts = val.split("|").filter(Boolean);
        return (row, cols) => {
          let v;
          if (key === "ns" || key === "namespace") v = (row.namespace || "").toLowerCase();
          else if (key === "cluster") v = (row.cluster || "").toLowerCase();
          else if (key === "health") return alts.includes(row.health);
          else v = colValue(cols, row, key).toLowerCase();
          return alts.some((a) => v.includes(a));
        };
      }
      return (row, cols) => haystack(row, cols).includes(lower);
    });
  }

  function apply(rows, cols, text, now) {
    const preds = compile(text);
    if (!preds.length) return rows;
    now = now || Date.now();
    return rows.filter((r) => preds.every((p) => p(r, cols, now)));
  }

  const api = { parseAge, compile, apply };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else root.KFilter = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
