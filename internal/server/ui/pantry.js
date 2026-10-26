"use strict";

// La vue Garde-manger. Utilise les outils de app.js ($, el, api,
// toast, fmtSize, setView) et window.pubOpen (publish.js). Rien n'est publié
// d'ici : « Préparer la fiche » ouvre Publier.

const pm = {
  libs: [],
  root: "/data",
  items: [],
  state: null,
  tracker: "le tracker",
  cls: "todo", // filtre de classe
  limit: 150,
  timer: null,
  shown: false,
};

const PM_CLASSES = [
  ["todo", "À publier", (it) => !it.ignored && (it.class === "absent" || it.class === "complement")],
  ["absent", "Absents", (it) => !it.ignored && it.class === "absent"],
  ["complement", "Nouvelles combinaisons", (it) => !it.ignored && it.class === "complement"],
  ["maybe", "Doublons possibles", (it) => !it.ignored && it.class === "maybe"],
  ["evaluate", "À évaluer", (it) => !it.ignored && it.class === "evaluate"],
  ["unknown", "À identifier", (it) => !it.ignored && it.class === "unknown"],
  ["pending", "En attente", (it) => !it.ignored && it.class === "pending"],
  ["covered", "Doublons", (it) => !it.ignored && it.class === "covered"],
  ["identical", "Identiques", (it) => !it.ignored && it.class === "identical"],
  ["excluded", "Exclus", (it) => !it.ignored && it.class === "excluded"],
  ["ignored", "Ignorés", (it) => it.ignored],
  ["all", "Tout", () => true],
];

const PM_LABEL = { absent: "Absent", complement: "Nouvelle combinaison", maybe: "Doublon possible", evaluate: "À évaluer", unknown: "À identifier", pending: "En attente", covered: "Doublon", identical: "Identique", excluded: "Exclu" };

async function pmLoad(stateOnly) {
  try {
    const r = await api("/ui/pantry" + (stateOnly ? "?state=1" : ""));
    pm.state = r.state;
    pm.tracker = r.tracker || "le tracker";
    pm.libs = r.libs || [];
    pm.root = r.root || "/data";
    pmLibsSum();
    if (!stateOnly) pm.items = r.items || [];
    pmRender(stateOnly);
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

function pmMinutes(n, rate) {
  const m = Math.ceil(n / Math.max(rate || 20, 1));
  return m < 60 ? m + " min" : Math.floor(m / 60) + " h " + String(m % 60).padStart(2, "0");
}

function pmRenderState() {
  const s = pm.state || {};
  const box = $("#pm-status");
  const parts = [];
  if (s.phase === "scan") parts.push("Lecture des bibliothèques…");
  else if (s.phase === "radarr") parts.push("Lecture de Radarr…");
  else if (s.phase === "identify") parts.push("Identification sur TMDB : " + s.done + " / " + s.total);
  if (s.queue > 0) {
    parts.push((s.paused ? "En pause : " : "Recherche sur " + pm.tracker + " : ") + s.queue + " film" + (s.queue > 1 ? "s" : "") + " à chercher (" + pmMinutes(s.queue, s.rate) + " à " + s.rate + " par minute)" + (s.checking && !s.paused ? " · en cours : " + s.checking : ""));
  } else if (s.phase === "idle" && s.lastScan && !String(s.lastScan).startsWith("0001")) {
    parts.push("À jour. Dernier scan : " + new Date(s.lastScan).toLocaleString("fr-FR"));
  } else if (s.phase === "idle") {
    parts.push("Pas encore scanné : lance « Scanner ma médiathèque ».");
  }
  if (s.toProbe > 0) parts.push("Lecture des fichiers (règles de " + pm.tracker + ") : " + s.probed + " / " + (s.probed + s.toProbe) + (s.probing ? " · en cours : " + s.probing : ""));
  if (s.probeOff) parts.push(s.probeOff);
  if (s.radarr) parts.push("Radarr : " + s.radarr);
  for (const e of s.errors || []) parts.push(e);
  if (s.lastError) parts.push("Dernière erreur : " + s.lastError);
  box.textContent = parts.join(" — ");
  box.classList.toggle("hidden", !parts.length);
  box.classList.toggle("warn", !!s.lastError || (s.errors || []).length > 0);
  $("#pm-pause").textContent = s.paused ? "Reprendre les recherches" : "Mettre en pause";
  $("#pm-scan").disabled = s.phase !== "idle";
}

function pmRender(stateOnly) {
  for (const n of document.querySelectorAll(".trk-n")) n.textContent = pm.tracker;
  pmRenderState();
  if (stateOnly) return;

  // Pastilles de classes, avec leurs nombres.
  const chips = $("#pm-chips");
  chips.replaceChildren();
  for (const [key, label, test] of PM_CLASSES) {
    const n = pm.items.filter(test).length;
    if (n === 0 && key !== "todo" && key !== pm.cls && key !== "all") continue;
    const b = el("button", "pm-chip", label);
    b.type = "button";
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", String(key === pm.cls));
    b.append(el("b", null, String(n)));
    b.addEventListener("click", () => {
      pm.cls = key;
      pm.limit = 150;
      pmRender();
    });
    chips.append(b);
  }

  // Bibliothèques.
  const libSel = $("#pm-lib");
  const libs = [...new Set(pm.items.map((it) => it.lib))].sort();
  const cur = libSel.value;
  libSel.replaceChildren(new Option("Toutes les bibliothèques", ""), ...libs.map((l) => new Option(l, l)));
  libSel.value = libs.includes(cur) ? cur : "";

  const test = (PM_CLASSES.find((c) => c[0] === pm.cls) || PM_CLASSES[0])[2];
  const q = $("#pm-q").value.trim().toLowerCase();
  const list = pm.items.filter((it) => test(it) && (!libSel.value || it.lib === libSel.value) && (!q || (it.name + " " + (it.tmdbTitle || "")).toLowerCase().includes(q)));
  const ul = $("#pm-list");
  ul.replaceChildren();
  if (!list.length) ul.append(el("li", "muted", pm.items.length ? "Aucun film dans cette catégorie." : "Lance un scan pour remplir le Garde-manger."));
  for (const it of list.slice(0, pm.limit)) ul.append(pmRow(it));
  const more = $("#pm-more");
  more.classList.toggle("hidden", list.length <= pm.limit);
  more.textContent = "Afficher plus (" + (list.length - pm.limit) + " restants)";
}

function pmVersions(it) {
  const vs = it.versions || [];
  if (!vs.length) return it.checkedAt && it.checkedAt[0] !== "0" ? "aucune version sur " + pm.tracker : "";
  const seen = new Set();
  for (const v of vs) seen.add([v.resolution, /VOSTFR/i.test(v.category || "") ? "VOSTFR" : "FR"].filter(Boolean).join(" "));
  return "sur " + pm.tracker + " : " + vs.length + " version" + (vs.length > 1 ? "s" : "") + " (" + [...seen].join(", ") + ")";
}

function pmRow(it) {
  const li = el("li", "pm-item" + (it.ignored ? " ignored" : ""));
  li.append(el("span", "pm-dot " + it.class, ""));
  const main = el("div", "pm-main");
  main.append(el("div", "pm-name", it.name));
  const film = it.tmdb ? (it.tmdbTitle ? it.tmdbTitle + (it.tmdbYear ? " (" + it.tmdbYear + ")" : "") : "TMDB " + it.tmdb) + (it.source === "radarr" ? " · Radarr" : it.source === "manual" ? " · choisi" : "") : "film non identifié";
  main.append(el("div", "pm-sub", [it.lib, fmtSize(it.size), film, pmVersions(it)].filter(Boolean).join(" · ")));
  main.append(el("div", "pm-why", (PM_LABEL[it.class] || it.class) + " : " + (it.why || "")));
  if (it.err) main.append(el("div", "pm-why bad", it.err));
  li.append(main);

  const acts = el("div", "pm-acts");
  const btn = (label, cls, fn) => {
    const b = el("button", "btn small" + (cls ? " " + cls : ""), label);
    b.type = "button";
    b.addEventListener("click", fn);
    acts.append(b);
    return b;
  };
  if (!it.ignored && it.class !== "excluded" && it.class !== "identical") {
    btn("Préparer la fiche", it.class === "absent" || it.class === "complement" ? "primary" : "", () => pmPrepare(it));
  }
  if (it.class !== "excluded") {
    if (it.tmdb) btn("Changer le film", "", () => pmPick(li, it));
    else btn("Identifier", "", () => pmPick(li, it));
  }
  btn(it.ignored ? "Remettre" : "Ignorer", "", async () => {
    try {
      const r = await api("/ui/pantry/" + it.id + "/ignore", { on: !it.ignored });
      Object.assign(it, r);
      pmRender();
    } catch (e) {
      if (e.message !== "session") toast(e.message, "bad");
    }
  });
  li.append(acts);

  // Film ambigu : les candidats tout de suite.
  if (!it.tmdb && it.class !== "excluded" && (it.candidates || []).length) pmCandidates(li, it, it.candidates);
  return li;
}

function pmCandidates(li, it, list) {
  let box = li.querySelector(".pm-cands");
  if (!box) {
    box = el("div", "pm-cands");
    li.append(box);
  }
  box.replaceChildren(el("span", "muted small", list.length ? "C'est :" : "Aucun résultat."));
  for (const mv of list.slice(0, 6)) {
    const b = el("button", "btn small", mv.title + (mv.year ? " (" + mv.year + ")" : ""));
    b.type = "button";
    b.title = mv.originalTitle || "";
    b.addEventListener("click", () => pmSet(it, mv.id));
    box.append(b);
  }
  const q = el("input");
  q.type = "search";
  q.placeholder = "Titre, ou lien TMDB / IMDB";
  q.addEventListener("keydown", async (e) => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    const v = q.value.trim();
    if (v.length < 2) return;
    try {
      const isRef = /themoviedb\.org\/movie\/\d+|imdb\.com\/title\/tt\d+|^tt\d{5,}$|^\d+$/i.test(v);
      const m = v.match(/^(.*?)\s+((?:19|20)\d{2})$/);
      const r = isRef ? await api("/ui/trk/tmdb/lookup?ref=" + encodeURIComponent(v)) : await api("/ui/trk/tmdb/search?q=" + encodeURIComponent(m ? m[1] : v) + (m ? "&year=" + m[2] : ""));
      pmCandidates(li, it, r.results || []);
    } catch (err) {
      if (err.message !== "session") toast(err.message, "bad");
    }
  });
  box.append(q);
}

function pmPick(li, it) {
  pmCandidates(li, it, it.candidates || []);
  const q = li.querySelector(".pm-cands input");
  if (q) q.focus();
}

async function pmSet(it, tmdbID) {
  try {
    const r = await api("/ui/pantry/" + it.id + "/movie", { tmdb: tmdbID });
    Object.assign(it, r);
    pmRender();
    toast("Film choisi : il sera cherché sur " + pm.tracker + " au prochain tour.", "ok");
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

// Ouvre le fichier dans Publier, avec le film TMDB déjà choisi.
async function pmPrepare(it) {
  window.trkPresetTMDB = it.tmdb ? { id: it.tmdb, path: it.path } : null;
  await window.pubOpen(it.path, "local");
}

window.pantryShow = (on) => {
  pm.shown = on;
  clearTimeout(pm.timer);
  if (!on) return;
  pmLoad();
  const tick = async () => {
    if (!pm.shown) return;
    const busy = pm.state && (pm.state.phase !== "idle" || pm.state.toProbe > 0 || (pm.state.queue > 0 && !pm.state.paused));
    await pmLoad(!busy);
    pm.timer = setTimeout(tick, busy ? 4000 : 15000);
  };
  pm.timer = setTimeout(tick, 3000);
};

// ---------- bibliothèques scannées ----------

function pmLibsSum() {
  const short = pm.libs.map((l) => (l.startsWith(pm.root + "/") ? l.slice(pm.root.length + 1) : l));
  $("#pm-libs-sum").textContent = short.length ? short.join(", ") : "aucune";
}

function pmLibRow(path, label, on) {
  const l = el("label", "check");
  const c = el("input");
  c.type = "checkbox";
  c.checked = on;
  c.dataset.path = path;
  l.append(c, document.createTextNode(" " + label));
  return l;
}

async function pmLibsOpen() {
  const box = $("#pm-libs-box");
  const list = $("#pm-libs-list");
  list.replaceChildren(el("span", "muted", "Lecture de " + pm.root + "…"));
  box.classList.remove("hidden");
  let dirs = [];
  try {
    const r = await api("/ui/fs?path=" + encodeURIComponent(pm.root));
    dirs = (r.entries || []).filter((e) => e.dir);
  } catch (e) {
    if (e.message === "session") return;
  }
  list.replaceChildren();
  const seen = new Set();
  for (const d of dirs) {
    seen.add(d.path);
    list.append(pmLibRow(d.path, d.name, pm.libs.includes(d.path)));
  }
  for (const l of pm.libs) if (!seen.has(l)) list.append(pmLibRow(l, l, true));
  if (!list.children.length) list.append(el("span", "muted", "Aucun dossier dans " + pm.root + " : ajoute un chemin ci-dessous."));
}

async function pmLibsSave() {
  const libs = [...document.querySelectorAll("#pm-libs-list input:checked")].map((c) => c.dataset.path);
  try {
    const r = await api("/ui/pantry/libs", { libs });
    pm.libs = r.libs || libs;
    pmLibsSum();
    $("#pm-libs-box").classList.add("hidden");
    toast("Bibliothèques enregistrées : lance « Scanner ma médiathèque ».", "ok");
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

(function bindPantry() {
  $("#pm-libs-edit").addEventListener("click", pmLibsOpen);
  $("#pm-libs-cancel").addEventListener("click", () => $("#pm-libs-box").classList.add("hidden"));
  $("#pm-libs-save").addEventListener("click", pmLibsSave);
  $("#pm-libs-add").addEventListener("click", () => {
    const v = $("#pm-libs-other").value.trim();
    if (!v.startsWith("/")) return toast("Chemin complet attendu, par exemple /data/films", "bad");
    $("#pm-libs-list").append(pmLibRow(v, v, true));
    $("#pm-libs-other").value = "";
  });
  $("#pm-scan").addEventListener("click", async () => {
    try {
      const r = await api("/ui/pantry/scan", {});
      pm.state = r.state;
      pmRenderState();
      window.pantryShow(true);
    } catch (e) {
      if (e.message !== "session") toast(e.message, "bad");
    }
  });
  $("#pm-pause").addEventListener("click", async () => {
    try {
      const r = await api("/ui/pantry/pause", { on: !(pm.state && pm.state.paused) });
      pm.state = r.state;
      pmRenderState();
    } catch (e) {
      if (e.message !== "session") toast(e.message, "bad");
    }
  });
  let qTimer = null;
  $("#pm-q").addEventListener("input", () => {
    clearTimeout(qTimer);
    qTimer = setTimeout(() => pmRender(), 200);
  });
  $("#pm-lib").addEventListener("change", () => pmRender());
  $("#pm-more").addEventListener("click", () => {
    pm.limit += 150;
    pmRender();
  });
})();
