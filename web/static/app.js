(() => {
  "use strict";

  const els = {
    form: document.getElementById("search-form"),
    query: document.getElementById("query-input"),
    sort: document.getElementById("sort-select"),
    order: document.getElementById("order-select"),
    pageSize: document.getElementById("page-size-select"),
    examples: document.getElementById("examples"),
    error: document.getElementById("query-error"),
    resultsMeta: document.getElementById("results-meta"),
    headRow: document.getElementById("results-head-row"),
    resultsBody: document.getElementById("results-body"),
    emptyState: document.getElementById("empty-state"),
    loadingState: document.getElementById("loading-state"),
    pagination: document.getElementById("pagination"),
    datasetStats: document.getElementById("dataset-stats"),
    overlay: document.getElementById("detail-overlay"),
    detailContent: document.getElementById("detail-content"),
    detailClose: document.getElementById("detail-close"),
    columnChips: document.getElementById("column-chips"),
    addColumnForm: document.getElementById("add-column-form"),
    addColumnInput: document.getElementById("add-column-input"),
    attrSuggestions: document.getElementById("attr-key-suggestions"),
  };

  // Example queries double as a quick syntax reference: hovering/reading
  // them teaches the query language faster than a paragraph of docs would.
  const EXAMPLES = [
    "type:purchase",
    "owner_id:usr_00042",
    "type:book AND owner_id:usr_00042",
    "amount:>250",
    "type:err* OR type:fail*",
    "NOT source:web",
    "usr_00042",
    "user.is_active:true",
  ];

  // The three columns every row always has. Extra columns (state.columns)
  // are user-managed and show one flattened attribute each — see the
  // column-management functions below.
  const FIXED_COLUMNS = [
    { key: "created_at", label: "Time", className: "col-time" },
    { key: "event_type", label: "Type", className: "col-type" },
    { key: "source", label: "Source", className: "col-source" },
  ];

  const COLUMNS_STORAGE_KEY = "eventExplorer.columns";

  let state = {
    q: "",
    sort: "created_at",
    order: "desc",
    page: 1,
    page_size: 50,
    columns: [], // attribute keys the user has added as their own column
  };
  let lastResult = null;

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[c]));
  }

  function fmtTime(iso) {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z");
  }

  function quoteIfNeeded(v) {
    return /\s/.test(v) ? `"${v}"` : v;
  }

  // ---- column management (add/remove attribute columns) -----------------

  function loadRememberedColumns() {
    try {
      const raw = localStorage.getItem(COLUMNS_STORAGE_KEY);
      const parsed = raw ? JSON.parse(raw) : [];
      return Array.isArray(parsed) ? parsed.filter((c) => typeof c === "string") : [];
    } catch (e) {
      return [];
    }
  }

  function saveRememberedColumns() {
    try {
      localStorage.setItem(COLUMNS_STORAGE_KEY, JSON.stringify(state.columns));
    } catch (e) {
      /* storage disabled/full — columns just won't be remembered next visit */
    }
  }

  function addColumn(key) {
    key = (key || "").trim();
    if (!key || state.columns.includes(key)) return;
    state.columns.push(key);
    onColumnsChanged();
  }

  function removeColumn(key) {
    state.columns = state.columns.filter((c) => c !== key);
    onColumnsChanged();
  }

  function onColumnsChanged() {
    saveRememberedColumns();
    renderColumnChips();
    renderTableHead();
    if (lastResult) renderRows(lastResult.items || []);
    // A column change doesn't change which events matched, just how
    // they're displayed — replace, don't push, so it's not its own
    // back-button stop.
    writeStateToURL(false);
  }

  function renderColumnChips() {
    els.columnChips.innerHTML = "";
    if (state.columns.length === 0) {
      const hint = document.createElement("span");
      hint.className = "columns-toolbar-hint";
      hint.textContent = "none — add one below, or click \"+ col\" on an attribute in an event's detail panel";
      els.columnChips.appendChild(hint);
      return;
    }
    state.columns.forEach((key) => {
      const chip = document.createElement("span");
      chip.className = "column-chip";
      chip.textContent = key;
      const remove = document.createElement("button");
      remove.type = "button";
      remove.textContent = "×";
      remove.setAttribute("aria-label", `Remove column ${key}`);
      remove.addEventListener("click", () => removeColumn(key));
      chip.appendChild(remove);
      els.columnChips.appendChild(chip);
    });
  }

  function renderTableHead() {
    els.headRow.innerHTML = "";
    FIXED_COLUMNS.forEach((col) => {
      const th = document.createElement("th");
      th.className = col.className;
      th.textContent = col.label;
      els.headRow.appendChild(th);
    });
    state.columns.forEach((key) => {
      const th = document.createElement("th");
      th.className = "col-attr";
      th.textContent = key;
      els.headRow.appendChild(th);
    });
  }

  // Populates the "add column" input's suggestion list from attribute keys
  // actually present in the current page of results, minus ones already
  // added — so the common case is picking from a short, relevant list
  // instead of having to know/remember exact key names.
  function updateColumnSuggestions(items) {
    const keys = new Set();
    items.forEach((item) => {
      Object.keys(item.attributes || {}).forEach((k) => keys.add(k));
    });
    state.columns.forEach((k) => keys.delete(k));
    els.attrSuggestions.innerHTML = Array.from(keys).sort()
      .map((k) => `<option value="${escapeHtml(k)}"></option>`)
      .join("");
  }

  // ---- url/local state ----------------------------------------------------

  function readStateFromURL() {
    const p = new URLSearchParams(location.search);
    state.q = p.get("q") || "";
    state.sort = p.get("sort") || "created_at";
    state.order = p.get("order") || "desc";
    state.page = parseInt(p.get("page") || "1", 10) || 1;
    state.page_size = parseInt(p.get("page_size") || "50", 10) || 50;
    const colsParam = p.get("cols");
    state.columns = colsParam !== null
      ? colsParam.split(",").map((c) => c.trim()).filter(Boolean)
      : loadRememberedColumns();
  }

  function writeStateToURL(push) {
    const p = new URLSearchParams();
    if (state.q) p.set("q", state.q);
    if (state.sort !== "created_at") p.set("sort", state.sort);
    if (state.order !== "desc") p.set("order", state.order);
    if (state.page !== 1) p.set("page", state.page);
    if (state.page_size !== 50) p.set("page_size", state.page_size);
    if (state.columns.length) p.set("cols", state.columns.join(","));
    const qs = p.toString();
    const url = location.pathname + (qs ? "?" + qs : "");
    if (push) history.pushState(state, "", url);
    else history.replaceState(state, "", url);
  }

  function syncFormFromState() {
    els.query.value = state.q;
    els.sort.value = state.sort;
    els.order.value = state.order;
    els.pageSize.value = String(state.page_size);
  }

  // ---- dataset stats header ------------------------------------------------

  async function loadStats() {
    try {
      const res = await fetch("/api/stats");
      if (!res.ok) throw new Error("stats request failed");
      const s = await res.json();
      const range = s.total_events > 0
        ? `${fmtTime(s.earliest_at)} &rarr; ${fmtTime(s.latest_at)}`
        : "no data yet";
      const topTypes = (s.event_types || []).slice(0, 6)
        .map((f) => `<span class="facet-chip">${escapeHtml(f.value)} (${f.count.toLocaleString()})</span>`)
        .join("");
      els.datasetStats.innerHTML = `
        <span><strong>${s.total_events.toLocaleString()}</strong> events</span>
        <span>${range}</span>
        <span class="facets">${topTypes}</span>
      `;
    } catch (e) {
      els.datasetStats.innerHTML = `<span class="stat-skel">stats unavailable</span>`;
    }
  }

  // ---- results table --------------------------------------------------------

  function renderRows(items) {
    els.resultsBody.innerHTML = "";
    items.forEach((item) => {
      const tr = document.createElement("tr");
      let cells = `
        <td class="col-time">${fmtTime(item.created_at)}</td>
        <td class="col-type"><span class="type-badge">${escapeHtml(item.event_type)}</span></td>
        <td class="col-source"><span class="source-badge">${escapeHtml(item.source)}</span></td>
      `;
      state.columns.forEach((key) => {
        const val = (item.attributes || {})[key];
        cells += val === undefined
          ? `<td class="col-attr"><span class="col-attr-empty">&mdash;</span></td>`
          : `<td class="col-attr">${escapeHtml(val)}</td>`;
      });
      tr.innerHTML = cells;
      tr.addEventListener("click", () => showDetail(item));
      els.resultsBody.appendChild(tr);
    });
  }

  function prettyBody(body) {
    try {
      return JSON.stringify(body, null, 2);
    } catch (e) {
      return String(body);
    }
  }

  function showDetail(item) {
    const topFields = [
      { label: "event_id", value: item.event_id, query: "id" },
      { label: "event_type", value: item.event_type, query: "type" },
      { label: "source", value: item.source, query: "source" },
      { label: "created_at", value: fmtTime(item.created_at), query: null },
    ];
    const topFieldsHtml = topFields.map((f) => `
      <tr>
        <td class="attr-key">${escapeHtml(f.label)}</td>
        <td>${escapeHtml(String(f.value))}${
          f.query
            ? `<button type="button" class="row-btn" data-query="${escapeHtml(f.query)}:${escapeHtml(quoteIfNeeded(String(f.value)))}">search</button>`
            : ""
        }</td>
      </tr>`).join("");

    const attrRows = Object.entries(item.attributes || {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([k, v]) => `
        <tr>
          <td class="attr-key">
            ${escapeHtml(k)}
            <button type="button" class="row-btn" data-query="${escapeHtml(k)}:${escapeHtml(quoteIfNeeded(v))}">search</button>
            <button type="button" class="row-btn row-btn-col" data-add-column="${escapeHtml(k)}"${state.columns.includes(k) ? " disabled" : ""}>${state.columns.includes(k) ? "in table" : "+ col"}</button>
          </td>
          <td>${escapeHtml(v)}</td>
        </tr>`)
      .join("");

    const bodyText = prettyBody(item.body);

    els.detailContent.innerHTML = `
      <h2>${escapeHtml(item.event_type)}</h2>
      <h3>Event</h3>
      <table class="attr-table"><tbody>${topFieldsHtml}</tbody></table>
      <h3>Attributes (flattened, queryable)</h3>
      <table class="attr-table"><tbody>${attrRows || "<tr><td>none</td></tr>"}</tbody></table>
      <h3>Raw JSON body</h3>
      <pre id="detail-json-pre">${escapeHtml(bodyText)}</pre>
    `;

    els.detailContent.querySelectorAll("button[data-query]").forEach((btn) => {
      btn.addEventListener("click", () => {
        state.q = btn.getAttribute("data-query");
        state.page = 1;
        closeDetail();
        syncFormFromState();
        runSearch(true);
      });
    });
    els.detailContent.querySelectorAll("button[data-add-column]").forEach((btn) => {
      btn.addEventListener("click", () => {
        addColumn(btn.getAttribute("data-add-column"));
        showDetail(item); // re-render so the button flips to "in table"
      });
    });

    els.overlay.hidden = false;

    highlightJson(document.getElementById("detail-json-pre"), bodyText);
  }

  function closeDetail() {
    els.overlay.hidden = true;
    els.detailContent.innerHTML = "";
  }

  // ---- syntax-highlighted JSON (Shiki, loaded on demand from esm.sh) -------
  //
  // No bundler/npm in this project (see README: no build step), and Shiki's
  // engine is WASM + per-language grammar files that aren't practical to
  // hand-vendor. It's loaded as a real ES module straight from esm.sh, lazily
  // on first use and cached after that. The <pre> already contains plain
  // escaped JSON text before this runs (see showDetail), so a blocked/absent
  // network connection just means no highlighting, not a broken page.

  let shikiCodeToHtmlPromise = null;
  function getShikiCodeToHtml() {
    if (!shikiCodeToHtmlPromise) {
      shikiCodeToHtmlPromise = import("https://esm.sh/shiki@4.4.3").then((mod) => mod.codeToHtml);
    }
    return shikiCodeToHtmlPromise;
  }

  async function highlightJson(preEl, jsonText) {
    if (!preEl) return;
    try {
      const codeToHtml = await getShikiCodeToHtml();
      const html = await codeToHtml(jsonText, { lang: "json", theme: "github-dark" });
      // The panel may have been closed/replaced while this was in flight.
      if (document.body.contains(preEl)) {
        preEl.outerHTML = html;
      }
    } catch (e) {
      console.warn("shiki highlighting unavailable, showing plain text:", e);
    }
  }

  // ---- pagination / meta -----------------------------------------------------

  function renderPagination() {
    const r = lastResult;
    els.pagination.innerHTML = "";
    if (!r || r.total === 0) return;
    const totalPages = Math.max(1, Math.ceil(r.total / r.page_size));
    const prev = document.createElement("button");
    prev.textContent = "← Prev";
    prev.disabled = r.page <= 1;
    prev.addEventListener("click", () => { state.page = Math.max(1, state.page - 1); runSearch(true); });

    const next = document.createElement("button");
    next.textContent = "Next →";
    next.disabled = r.page >= totalPages;
    next.addEventListener("click", () => { state.page = state.page + 1; runSearch(true); });

    const label = document.createElement("span");
    label.textContent = `Page ${r.page} of ${totalPages}`;

    els.pagination.append(prev, label, next);
  }

  function renderMeta() {
    const r = lastResult;
    if (!r) { els.resultsMeta.textContent = ""; return; }
    const from = r.total === 0 ? 0 : (r.page - 1) * r.page_size + 1;
    const to = Math.min(r.page * r.page_size, r.total);
    els.resultsMeta.textContent =
      `${r.total.toLocaleString()} match${r.total === 1 ? "" : "es"}` +
      (r.total > 0 ? ` — showing ${from.toLocaleString()}–${to.toLocaleString()}` : "") +
      ` — ${r.took_ms}ms`;
  }

  async function runSearch(push) {
    els.error.hidden = true;
    els.loadingState.hidden = false;
    els.emptyState.hidden = true;
    writeStateToURL(!!push);

    const params = new URLSearchParams({
      q: state.q,
      sort: state.sort,
      order: state.order,
      page: String(state.page),
      page_size: String(state.page_size),
    });

    try {
      const res = await fetch(`/api/events?${params.toString()}`);
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.detail || data.title || `request failed (${res.status})`);
      }
      lastResult = data;
      renderRows(data.items || []);
      renderMeta();
      renderPagination();
      updateColumnSuggestions(data.items || []);
      els.emptyState.hidden = (data.items || []).length !== 0;
    } catch (e) {
      lastResult = null;
      els.resultsBody.innerHTML = "";
      els.resultsMeta.textContent = "";
      els.pagination.innerHTML = "";
      els.error.textContent = e.message;
      els.error.hidden = false;
    } finally {
      els.loadingState.hidden = true;
    }
  }

  function renderExamples() {
    els.examples.innerHTML = "";
    EXAMPLES.forEach((ex) => {
      const chip = document.createElement("button");
      chip.type = "button";
      chip.className = "example-chip";
      chip.textContent = ex;
      chip.addEventListener("click", () => {
        state.q = ex;
        state.page = 1;
        syncFormFromState();
        runSearch(true);
      });
      els.examples.appendChild(chip);
    });
  }

  els.form.addEventListener("submit", (e) => {
    e.preventDefault();
    state.q = els.query.value;
    state.sort = els.sort.value;
    state.order = els.order.value;
    state.page_size = parseInt(els.pageSize.value, 10) || 50;
    state.page = 1;
    runSearch(true);
  });

  [els.sort, els.order, els.pageSize].forEach((el) => {
    el.addEventListener("change", () => {
      state.sort = els.sort.value;
      state.order = els.order.value;
      state.page_size = parseInt(els.pageSize.value, 10) || 50;
      state.page = 1;
      runSearch(true);
    });
  });

  els.addColumnForm.addEventListener("submit", (e) => {
    e.preventDefault();
    addColumn(els.addColumnInput.value);
    els.addColumnInput.value = "";
    els.addColumnInput.focus();
  });

  els.detailClose.addEventListener("click", closeDetail);
  els.overlay.addEventListener("click", (e) => { if (e.target === els.overlay) closeDetail(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeDetail(); });

  window.addEventListener("popstate", () => {
    readStateFromURL();
    syncFormFromState();
    renderColumnChips();
    renderTableHead();
    runSearch(false);
  });

  readStateFromURL();
  syncFormFromState();
  renderExamples();
  renderColumnChips();
  renderTableHead();
  loadStats();
  runSearch(false);
})();
