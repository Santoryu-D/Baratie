"use strict";

// Vue « Lot ». Mène les releases d'un dossier une par une, avec les mêmes
// routes que la vue Publier (liste, hachage, MediaInfo, fiche, publication).
// Ne publie seule une release que si tout est sûr ; le reste est « à revoir ».

const lot = {
  dir: "",
  source: "local", // local ou ssh
  entries: [], // { name, path, size, files, video, exec, flat, error, last, state, detail, checked }
  tab: "pending",
  running: false,
  stop: false,
  current: "",
  published: 0,
  done: 0,
  total: 0,
};

// États du lot, dans l'ordre des onglets.
const LOT_TABS = [
  ["pending", "En attente", "Pas encore traitées par un lot."],
  ["review", "À revoir", "Le lot n'a pas osé : la raison est sur la ligne. « Revoir » l'ouvre dans Publier."],
  ["simulated", "Simulé", "Le lot l'aurait publiée. Décoche Simulation pour le faire pour de vrai."],
  ["published", "Publié", "Publiée sur V3X et mise en seed."],
  ["duplicate", "Déjà présent", "Déjà sur V3X : rien à publier, seede-la depuis Cross-seed."],
  ["ignored", "Ignoré", "Écartée à la main : les lots la sautent."],
  ["error", "Erreur", "Illisible ou refusée par V3X."],
];
const LOT_STATE_OF_KIND = { published: "published", batch_review: "review", batch_simulated: "simulated", batch_duplicate: "duplicate", batch_ignored: "ignored", batch_error: "error" };

// ---------- ressemblance des titres ----------

function words(s) {
  const out = new Set();
  const n = String(s || "").normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/['’]/g, "").toLowerCase();
  for (const t of n.split(/[^a-z0-9]+/)) if (t.length > 1) out.add(t);
  return out;
}

function similarity(a, b) {
  const A = words(a), B = words(b);
  if (!A.size || !B.size) return 0;
  let c = 0;
  for (const t of A) if (B.has(t)) c++;
  return c / (A.size + B.size - c);
}

// Œuvre certaine : même titre (ou titre original) à 60 % au moins, même année
// à un an près, et le bon type (série si le nom porte une saison).
function certainWork(results, title, year, isSeries) {
  const ok = results.filter((r) => {
    if ((r.type === "tv") !== !!isSeries) return false;
    if (year && r.year && Math.abs(Number(r.year) - Number(year)) > 1) return false;
    return Math.max(similarity(title, r.title), similarity(title, r.originalTitle || "")) >= 0.6;
  });
  return ok.length === 1 ? ok[0] : null;
}

// ---------- liste ----------

function lotEntries(list, hits) {
  return list.map((e) => {
    const st = e.error ? "error" : (e.last && LOT_STATE_OF_KIND[e.last.kind]) || "pending";
    return { ...e, state: st, detail: e.error || (e.last && e.last.detail) || "", checked: st === "pending" || st === "review" || st === "simulated", requests: (hits && hits[e.path]) || null };
  });
}

async function lotList() {
  const dir = $("#b-path").value.trim();
  if (!dir) return toast("Choisis le dossier qui contient les releases.", "bad");
  try {
    const r = await api("/ui/batch/list?source=" + lot.source + "&path=" + encodeURIComponent(dir));
    lot.dir = r.path;
    lot.picked = false;
    $("#b-from").classList.add("hidden");
    lot.entries = lotEntries(r.entries);
    $("#b-fs").classList.add("hidden");
    lot.tab = "pending";
    lotRender();
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

async function lotBrowse(path) {
  const ul = $("#b-fs");
  try {
    const r = await api("/ui/fs?source=" + lot.source + "&path=" + encodeURIComponent(path || ""));
    ul.replaceChildren();
    if (r.parent !== null) {
      const up = el("li");
      const b = el("button", "fs-name", "Dossier parent");
      b.type = "button";
      b.addEventListener("click", () => lotBrowse(r.parent));
      up.append(el("span", "fs-ico dir"), b);
      ul.append(up);
    }
    for (const e of r.entries.filter((x) => x.dir && !x.skip)) {
      const li = el("li");
      const b = el("button", "fs-name", e.name);
      b.type = "button";
      b.addEventListener("click", () => lotBrowse(e.path));
      const pick = el("button", "btn small", "Choisir ce dossier");
      pick.type = "button";
      pick.addEventListener("click", () => { $("#b-path").value = e.path; lotList(); });
      li.append(el("span", "fs-ico dir"), b, pick);
      ul.append(li);
    }
    if (r.path) $("#b-path").value = r.path;
    ul.classList.remove("hidden");
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

// Releases choisies dans « Pas sur V3X » : le lot les prend telles quelles.
window.lotFromPaths = async (paths, source, hits) => {
  if (lot.running) {
    setView("batch");
    return toast("Un lot tourne : attends sa fin ou arrête-le d'abord.", "bad");
  }
  try {
    const r = await api("/ui/batch/entries", { paths, source });
    lot.source = source;
    lot.dir = "";
    lot.picked = true;
    $("#b-path").value = "";
    $("#b-fs").classList.add("hidden");
    lot.entries = lotEntries(r.entries, hits);
    const from = $("#b-from");
    from.textContent = lot.entries.length + " " + plural(lot.entries.length, "release choisie", "releases choisies") + " dans Cross-seed ▸ Pas sur V3X" + (source === "ssh" ? ", sur ta seedbox." : ".");
    from.classList.remove("hidden");
    lot.tab = "pending";
    window.lotSources();
    setView("batch");
    lotRender();
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
};

// Ce PC ou la seedbox : changer de source vide la liste.
window.lotSources = () => {
  if (lot.source === "ssh" && !(ui.state && ui.state.ssh && ui.state.ssh.ok)) lot.source = "local";
  const shown = renderSourceSwitch($("#b-src"), lot.source, (id) => {
    if (lot.running) return toast("Un lot tourne : arrête-le avant de changer de source.", "bad");
    lot.source = id;
    lot.entries = [];
    lot.dir = "";
    lot.picked = false;
    $("#b-from").classList.add("hidden");
    $("#b-path").value = "";
    $("#b-fs").classList.add("hidden");
    window.lotSources();
    lotRender();
    if (id === "ssh") lotBrowse("");
  });
  $("#b-src-row").classList.toggle("hidden", !shown);
};

// ---------- affichage ----------

function lotRender() {
  const has = lot.entries.length > 0;
  $("#b-tabs").classList.toggle("hidden", !has);
  $("#b-wrap").classList.toggle("hidden", !has);
  const counts = {};
  for (const e of lot.entries) counts[e.state] = (counts[e.state] || 0) + 1;
  const tabs = $("#b-tabs");
  tabs.replaceChildren();
  for (const [id, label, help] of LOT_TABS) {
    const b = el("button", "tab");
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", String(id === lot.tab));
    b.title = help;
    b.append(el("span", null, label), el("span", "n", String(counts[id] || 0)));
    b.addEventListener("click", () => { lot.tab = id; lotRender(); });
    tabs.append(b);
  }
  const list = lot.entries.filter((e) => e.state === lot.tab);
  const tb = $("#b-tbody");
  tb.replaceChildren();
  for (const e of list) tb.append(lotRow(e));
  const empty = $("#b-empty");
  const help = LOT_TABS.find((t) => t[0] === lot.tab);
  empty.textContent = list.length ? "" : "Rien ici. " + (help ? help[2] : "");
  empty.classList.toggle("hidden", list.length > 0);
  const checkable = list.filter((e) => e.state !== "published" && e.state !== "duplicate" && e.state !== "error");
  $("#b-all").checked = checkable.length > 0 && checkable.every((e) => e.checked);
  $("#b-all").disabled = lot.running || checkable.length === 0;
  lotBar();
}

function lotRow(e) {
  const tr = el("tr", e.path === lot.current ? "current" : "");
  const td = el("td", "c-check");
  if (e.state !== "published" && e.state !== "duplicate" && e.state !== "error") {
    const cb = el("input");
    cb.type = "checkbox";
    cb.checked = e.checked;
    cb.disabled = lot.running;
    cb.setAttribute("aria-label", "Inclure " + e.name);
    cb.addEventListener("change", () => { e.checked = cb.checked; lotBar(); });
    td.append(cb);
  }
  tr.append(td);
  const name = el("td");
  name.append(el("div", "name", e.name));
  const tags = [];
  if (e.files > 1) tags.push(e.files + " fichiers");
  if (e.audio) tags.push("album");
  else if (!e.video) tags.push("pas de vidéo");
  if (e.exec) tags.push("contient un exécutable");
  if (tags.length) name.append(el("div", "sub", tags.join(" · ")));
  const asked = (e.requests || []).filter((h) => !h.mine);
  if (asked.length) {
    const reqs = el("div", "reqs");
    if (e.state === "published" && e.active && e.last && e.last.id) for (const h of asked.slice(0, 2)) reqs.append(proposeButton(h, e.last.id));
    else reqs.append(...requestBadges(asked));
    name.append(reqs);
  }
  if (e.last && e.last.id && /^[0-9a-f-]{36}$/i.test(e.last.id)) {
    const a = el("a", "sub", "Voir la fiche sur V3X");
    a.href = "https://v3x.club/torrents/" + e.last.id;
    a.target = "_blank";
    a.rel = "noopener";
    name.append(a);
  }
  tr.append(name, el("td", "num", e.size ? fmtSize(e.size) : ""));
  const st = el("td");
  const label = (LOT_TABS.find((t) => t[0] === e.state) || [e.state, e.state])[1];
  const kind = { review: "warn", simulated: "add", published: "ok", duplicate: "ok", error: "bad" }[e.state] || "";
  st.append(el("span", "pill " + kind, e.path === lot.current ? "En cours…" : label));
  if (e.detail) st.append(el("div", "sub", e.detail));
  tr.append(st);
  const act = el("td");
  const box = el("div", "b-actions");
  if (e.state === "review" || e.state === "simulated" || e.state === "pending") {
    const r = el("button", "btn small", "Revoir");
    r.type = "button";
    r.title = "Ouvrir dans Publier pour finir la fiche à la main";
    r.disabled = lot.running;
    r.addEventListener("click", () => { setView("publish"); choose(e.path, lot.source); });
    box.append(r);
  }
  if (e.state !== "published" && e.state !== "duplicate") {
    const ig = el("button", "btn small", e.state === "ignored" ? "Ne plus ignorer" : "Ignorer");
    ig.type = "button";
    ig.disabled = lot.running;
    ig.addEventListener("click", () => lotIgnore(e));
    box.append(ig);
  }
  act.append(box);
  tr.append(act);
  return tr;
}

function lotBar() {
  const bar = $("#b-bar");
  const show = $("#view-batch") && !$("#view-batch").classList.contains("hidden") && lot.entries.length > 0;
  bar.classList.toggle("hidden", !show);
  if (!show) return;
  const n = lot.entries.filter((e) => e.checked && (e.state === "pending" || e.state === "review" || e.state === "simulated")).length;
  $("#b-bar-txt").textContent = lot.running ? "Lot en cours : " + lot.done + " / " + lot.total : n + (n > 1 ? " releases cochées" : " release cochée");
  $("#b-bar-how").textContent = $("#b-sim").checked ? "Simulation : rien ne sera publié, tu verras ce que le lot ferait." : "Pour de vrai : les releases sûres seront publiées et mises en seed.";
  $("#b-run").classList.toggle("hidden", lot.running);
  $("#b-run").disabled = n === 0;
  $("#b-run").textContent = $("#b-sim").checked ? "Simuler le lot" : "Lancer le lot";
  $("#b-stop").classList.toggle("hidden", !lot.running);
}

function lotProgress(label, detail) {
  const p = $("#b-progress");
  p.classList.toggle("hidden", !lot.running);
  $("#b-progress-label").textContent = label || "";
  $("#b-progress-detail").textContent = detail || "";
  $("#b-progress-fill").style.width = (lot.total ? Math.max(3, (lot.done / lot.total) * 100) : 0) + "%";
}

async function decide(e, state, detail, id) {
  e.state = state;
  e.detail = detail || "";
  e.checked = state === "review" || state === "simulated";
  const kind = { review: "batch_review", simulated: "batch_simulated", duplicate: "batch_duplicate", ignored: "batch_ignored", error: "batch_error" }[state];
  if (kind) {
    try { await api("/ui/batch/decision", { path: e.path, source: lot.source, name: e.name, kind, detail: e.detail, id: id || "" }); } catch (err) { /* le lot continue */ }
  }
  lotRender();
}

async function lotIgnore(e) {
  if (e.state === "ignored") {
    e.state = "pending";
    e.detail = "";
    e.checked = true;
    try { await api("/ui/batch/decision", { path: e.path, source: lot.source, name: e.name, kind: "batch_unignored", detail: "" }); } catch (err) { /* rien */ }
    lotRender();
    return;
  }
  await decide(e, "ignored", "écartée à la main");
}

// ---------- traitement d'une release ----------

const wait = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitJob(id, done) {
  for (;;) {
    const j = await api("/ui/pub/" + id);
    if (done(j)) return j;
    if (lot.stop) return j;
    lotProgress("Lot : " + (lot.done + 1) + " / " + lot.total, j.state === "hashing" ? "hachage " + Math.round((j.hashed / Math.max(j.total, 1)) * 100) + " %" : "");
    await wait(700);
  }
}

async function processEntry(e, opts) {
  if (e.exec) return decide(e, "review", "contient un exécutable : jamais publié par un lot");
  if (opts.video && !e.video && !e.audio) return decide(e, "review", "ni vidéo ni album (option « Vidéos et albums seulement »)");
  if (opts.cap > 0 && e.size > opts.cap * 1073741824) return decide(e, "review", "au-delà du plafond de " + opts.cap + " Gio : pas hachée");
  if (e.flat) return decide(e, "review", "dossier sans fichier à sa racine (collection, plusieurs releases ?)");

  let j = await api("/ui/pub/start", { path: e.path, source: lot.source });
  const cancel = async () => { try { await api("/ui/pub/" + j.id + "/cancel", {}); } catch (err) { /* rien */ } };
  if (j.duplicate) {
    await cancel();
    return decide(e, "duplicate", "déjà sur V3X : " + j.duplicate.name, j.duplicate.id);
  }
  // MediaInfo pendant le hachage.
  let mi = "";
  try { mi = await mediaInfoOf(j); } catch (err) { mi = ""; }
  let name = j.single ? j.name.replace(/\.(mkv|mp4|avi|m2ts|ts|mov|wmv|webm|mpe?g|m4v|iso|flac|mp3|m4a)$/i, "") : j.name;
  const album = !!(j.audio && j.audio.tracks && j.audio.tracks.length && !e.video);
  // Album : Audio ▸ Musique et l'édition MusicBrainz, si elle est certaine.
  const mc = album ? musicCategory() : null;
  let mbw = null;
  if (album) mbw = await lotAlbumWork(j);
  const ficheOf = (ref) =>
    api("/ui/pub/" + j.id + "/fiche", {
      releaseName: name,
      mediainfo: mi,
      tmdbRef: ref,
      mbRef: mbw && mbw.mb ? mbURL(mbw.mb) : null,
      categorySlug: mc ? mc.leaf.slug : null,
      templateId: null,
    });
  let f = await ficheOf(null);

  // Œuvre : recherche TMDB, retenue seulement si elle est certaine.
  const year = ((f.analysis && f.analysis.tokens) || []).find((t) => t.kind === "year");
  const isSeries = !!f.season || /\bS\d{1,2}(E\d{1,3})?\b/i.test(name);
  let work = null;
  if (f.tmdbQuery && !album) {
    try {
      const r = await api("/ui/pub/tmdb/search?q=" + encodeURIComponent(f.tmdbQuery));
      work = certainWork((r.results || []).filter((x) => x.type === "movie" || x.type === "tv"), (f.analysis && f.analysis.title) || f.tmdbQuery, year && year.value, isSeries);
    } catch (err) { work = null; }
  }
  if (work) f = await ficheOf(work.type + "/" + work.id);

  // Album sans la qualité dans le nom : complété si l'option est cochée.
  let renamed = false;
  if (album && opts.fixName && j.audio.token && (f.warnings || []).includes("audio_naming")) {
    const fixed = nameWithToken(name, j.audio.token);
    if (fixed !== name) {
      name = fixed;
      renamed = true;
      f = await ficheOf(null);
    }
  }

  const reasons = [];
  const cat = f.category;
  const filmSeries = cat && (cat.slug.startsWith("films-videos") || cat.slug.startsWith("films") || cat.slug.startsWith("series"));
  if (!cat) reasons.push("catégorie à choisir");
  if (!work && !album && (filmSeries || e.video)) reasons.push("œuvre TMDB incertaine");
  if (album && !(mbw && mbw.mb)) reasons.push("album incertain : " + ((mbw && mbw.why) || "introuvable"));
  if (album && j.audio.mixed) reasons.push("pistes de qualités différentes");
  if (e.video && !mi) reasons.push("MediaInfo illisible");
  // Seuls les contrôles qui feraient refuser la publication arrêtent le lot ;
  // les remarques sur le nom (espaces, année…) restent des conseils, comme sur le site.
  for (const w of f.warnings || []) {
    if (!BLOCKING.has(w)) continue;
    if (w === "audio_naming" && j.audio && j.audio.token) reasons.push("nom sans la qualité : « " + nameWithToken(name, j.audio.token) + " » ?");
    else reasons.push(WARNINGS[w].replace(/\.$/, ""));
  }
  if (reasons.length) {
    await cancel();
    return decide(e, "review", reasons.join(" · "));
  }

  const found = categoryBySlug(cat.slug);
  const catName = found && found.leaf ? found.root.name + " ▸ " + found.leaf.name : cat.slug;
  const workName = album
    ? mbw.mb.title + (mbw.mb.date ? " (" + mbw.mb.date.slice(0, 4) + ")" : "")
    : work.title + (work.year ? " (" + work.year + ")" : "");
  const summary = [catName, workName, renamed ? "nom complété : " + name : ""].filter(Boolean).join(" · ");
  if (opts.sim) {
    await cancel(); // le .torrent reste en cache : le vrai lot ne rehachera pas
    return decide(e, "simulated", "serait publiée : " + summary);
  }

  j = await waitJob(j.id, (x) => x.state !== "hashing");
  if (j.state !== "ready") {
    await cancel();
    return decide(e, "error", j.error || "le .torrent n'a pas pu être fabriqué");
  }
  const team = pub.ctx && pub.ctx.user && pub.ctx.user.team;
  const fields = {
    categoryId: String(cat.id),
    description: f.presentation || "",
    descriptionFormat: "standard",
    title: f.title || "",
    releaseName: name,
    nfo: f.nfo || "",
    language: f.language || "",
    anonymous: String(!!(pub.ctx && pub.ctx.user && pub.ctx.user.anonymousDefault)),
    exclusive: "false",
    tmdbUrl: album ? mbURL(mbw.mb) : "https://www.themoviedb.org/" + work.type + "/" + work.id,
  };
  if (album) fields.creator = f.creator || mbw.mb.artist || "";
  if (team) fields.teamRelease = String(!!f.teamRelease);
  if (f.season && cat.slug.indexOf("serie") >= 0) fields.season = f.season;
  try {
    await api("/ui/pub/" + j.id + "/publish", { fields });
  } catch (err) {
    return decide(e, "error", err.message);
  }
  j = await waitJob(j.id, (x) => x.state !== "publishing");
  if (!j.published) return decide(e, "error", pubErr(j.error, j.errorBody));
  lot.published++;
  e.last = { id: j.published.id };
  e.active = j.published.status === "active";
  // Une demande ouverte attend peut-être cette release.
  try {
    const q = await api("/ui/requests/for?name=" + encodeURIComponent(fields.releaseName || e.name));
    if (q.requests && q.requests.length) e.requests = q.requests;
  } catch (err) { /* rien */ }
  e.state = "published";
  e.detail = (j.published.status === "active" ? "publiée" : "en attente de validation") + " · " + summary;
  e.checked = false;
  lotRender();
}

async function lotRun() {
  if (lot.running) return;
  if (!pub.ctx) await loadCtx();
  if (!pub.ctx) return toast("Publication impossible : " + (pub.ctxError || "V3X injoignable"), "bad");
  const opts = { sim: $("#b-sim").checked, video: $("#b-video").checked, fixName: $("#b-fixname").checked, cap: Number($("#b-cap").value) || 0, max: Number($("#b-max").value) || 0 };
  // Seulement ce qui est coché ET affiché à l'écran : un dossier apparu entre-temps
  // n'est jamais publié sans avoir été vu.
  const todo = lot.entries.filter((e) => e.checked && (e.state === "pending" || e.state === "review" || e.state === "simulated"));
  lot.running = true;
  lot.stop = false;
  lot.done = 0;
  lot.published = 0;
  lot.total = todo.length;
  lot.tab = "pending";
  lotRender();
  for (const e of todo) {
    if (lot.stop) break;
    if (opts.max > 0 && lot.published >= opts.max && !opts.sim) {
      e.detail = "plafond de publications atteint";
      continue;
    }
    lot.current = e.path;
    lotRender();
    lotProgress("Lot : " + (lot.done + 1) + " / " + lot.total, e.name);
    try {
      await processEntry(e, opts);
    } catch (err) {
      if (err.message === "session") break;
      await decide(e, "error", err.message);
    }
    lot.done++;
    // Le site n'aime pas les rafales : une pause entre deux publications.
    if (!opts.sim && e.state === "published") {
      for (let i = 0; i < 7 && !lot.stop; i++) await wait(1000);
    }
  }
  lot.running = false;
  lot.current = "";
  lotProgress("", "");
  const counts = {};
  for (const e of lot.entries) counts[e.state] = (counts[e.state] || 0) + 1;
  lot.tab = opts.sim ? "simulated" : counts.published ? "published" : "review";
  lotRender();
  toast(lot.stop ? "Lot arrêté." : "Lot terminé : " + (opts.sim ? (counts.simulated || 0) + " seraient publiées" : lot.published + " publiées") + ", " + (counts.review || 0) + " à revoir.", "ok");
}

window.batchShow = async (on) => {
  lotBar();
  if (!on) return;
  if (!pub.ctx) loadCtx();
  if (!$("#b-path").value && !lot.picked) {
    try { const s = await api("/ui/state"); $("#b-path").value = s.lastDir || ""; } catch (e) { /* rien */ }
  }
};

function bindBatch() {
  $("#b-list").addEventListener("click", lotList);
  $("#b-path").addEventListener("keydown", (e) => { if (e.key === "Enter") lotList(); });
  $("#b-browse").addEventListener("click", () => lotBrowse($("#b-path").value));
  $("#b-run").addEventListener("click", lotRun);
  $("#b-stop").addEventListener("click", () => { lot.stop = true; toast("Le lot s'arrête après la release en cours."); });
  $("#b-sim").addEventListener("change", lotBar);
  $("#b-all").addEventListener("change", (ev) => {
    for (const e of lot.entries) if (e.state === lot.tab && e.state !== "published" && e.state !== "duplicate" && e.state !== "error") e.checked = ev.target.checked;
    lotRender();
  });
}

bindBatch();
