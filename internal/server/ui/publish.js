"use strict";

// Vue « Publier ». Utilise les outils de app.js ($, el, api, toast, fmtSize).
// Tout texte venu d'une release ou du site passe par textContent, sauf l'aperçu
// de la présentation : c'est le HTML rendu par V3X lui-même (le même que sur le
// site), et la politique de sécurité de la page bloque tout script en ligne.

const pub = {
  ctx: null, // contexte du membre (catégories, équipe, langues, modèles)
  ctxError: "",
  dir: "",
  source: "local", // local ou ssh
  job: null,
  fiche: null,
  mediainfo: "",
  miState: "idle", // idle, running, done, none, error
  tmdb: null, // { type, id, title, year, posterUrl }
  dirty: {}, // champs retouchés par le membre : jamais écrasés par la fiche
  ficheSeq: 0,
  previewSeq: 0,
  timer: null,
  shown: false,
  published: false,
};

const ERRORS = {
  validation: "un champ de la fiche est refusé",
  rights_not_declared: "il faut déclarer avoir le droit de partager ce contenu",
  invalid_category: "choisis une sous-catégorie",
  invalid_torrent: "le .torrent est refusé par V3X",
  file_too_large: "le .torrent dépasse 32 Mio (trop de fichiers)",
  archive_only: "une release qui ne contient que des archives n'est pas acceptée",
  review_quota: "tu as déjà trop de releases en attente de validation : attends qu'elles soient revues",
  tmdb_required: "une fiche TMDB est obligatoire pour un film ou une série (ou coche « Pas de fiche TMDB » si elle n'existe vraiment pas)",
  tmdb_link_invalid: "le lien TMDB n'est pas valide",
  nfo_required: "le NFO est obligatoire pour un film ou une série",
  mediainfo_required: "le NFO doit contenir un vrai MediaInfo (pistes vidéo et audio mesurées)",
  resolution_too_low: "la résolution est sous le minimum de V3X",
  ddl_source: "la release porte la marque d'un site de téléchargement direct",
  free_software: "les logiciels libres et gratuits ne se publient pas sur V3X",
  audio_naming: "le nom d'une release musicale doit suivre le nommage du site",
  name_missing_disc: "le nom doit indiquer le disque d'origine",
  name_missing_gpu: "le nom doit indiquer l'encodeur matériel",
  exclusive_group_mismatch: "l'exclusivité est réservée aux releases de ta team",
  team_reupload_forbidden: "cette team refuse les réuploads de ses releases",
  team_reupload_ask: "cette team demande son accord avant un réupload",
  duplicate: "cette release est déjà sur V3X (même .torrent)",
  duplicate_removed: "cette release a déjà été publiée puis retirée",
  duplicate_content: "le même contenu est déjà sur V3X",
  invalid_key_or_scope: "ta clé API n'a pas le droit « Upload » (Réglages ▸ Intégrations sur V3X)",
};

// Codes de contrôle renvoyés par V3X avec la fiche (les remarques sur le nom
// arrivent déjà en clair). BLOCKING : refusés à la publication, le lot n'y va pas.
const WARNINGS = {
  mediainfo_missing: "Pas de MediaInfo : il est obligatoire pour un film ou une série.",
  mediainfo_not_measured: "Le MediaInfo ne contient pas de pistes mesurées : V3X le refusera.",
  resolution_too_low: "Résolution sous le minimum de V3X : la publication sera refusée.",
  name_missing_gpu: "Encodage matériel détecté : le nom doit l'indiquer (NVENC, QSV…).",
  gpu_name_conflict: "Le nom et le MediaInfo ne disent pas le même encodeur.",
  tmdb_missing: "Pas encore de fiche TMDB choisie.",
  tmdb_not_found: "Cette fiche TMDB est introuvable.",
  mb_not_found: "Cette édition MusicBrainz est introuvable.",
  audio_naming: "Le nom d'une release musicale doit suivre le nommage du site.",
  category_unknown: "Catégorie inconnue sur V3X.",
};
const BLOCKING = new Set(["mediainfo_missing", "mediainfo_not_measured", "resolution_too_low", "name_missing_gpu", "gpu_name_conflict", "tmdb_not_found", "audio_naming", "category_unknown"]);

const TOKENS = { resolution: "Résolution", source: "Source", codec: "Codec", audio: "Audio", language: "Langue", hdr: "HDR", year: "Année", group: "Team" };

function pubErr(code, body) {
  if (code && code.startsWith("upscale")) return "les releases agrandies par IA ne sont pas acceptées telles quelles";
  let msg = ERRORS[code] || code;
  try {
    const b = JSON.parse(body || "{}");
    if (code === "validation" && b.field) msg += " : " + b.field + (b.detail ? " (" + b.detail + ")" : "");
    if (b.existing && b.existing.id) msg += ". Fiche existante : https://v3x.club/torrents/" + b.existing.id;
  } catch (e) { /* corps illisible */ }
  return msg;
}

// ---------- contexte ----------

async function loadCtx() {
  try {
    pub.ctx = await api("/ui/pub/context");
    pub.ctxError = "";
  } catch (e) {
    if (e.message === "session") return;
    pub.ctx = null;
    pub.ctxError = e.message;
  }
  const scope = $("#pub-scope");
  scope.textContent = pub.ctxError ? "Publication impossible pour l'instant : " + pub.ctxError + ". Ta clé API doit avoir les droits « Torznab » et « Upload » (Réglages ▸ Intégrations sur V3X)." : "";
  scope.classList.toggle("hidden", !pub.ctxError);
  if (!pub.ctx) return;
  // Feuille de style de l'aperçu (celle du site) : relue maintenant que V3X
  // répond ; chargée avec la page, elle peut être arrivée vide.
  $("#bbcode-css").href = "/ui/bbcode.css?" + Date.now();
  const root = $("#f-root");
  root.replaceChildren(new Option("Choisir…", ""));
  for (const c of pub.ctx.categories || []) root.append(new Option(c.name, c.slug));
  const lang = $("#f-lang");
  lang.replaceChildren();
  for (const l of pub.ctx.languages || []) lang.append(new Option(l || "Non précisée", l));
  const tpl = $("#f-tpl");
  tpl.replaceChildren(new Option("Présentation du site", ""));
  for (const t of pub.ctx.templates || []) tpl.append(new Option(t.name, t.id));
  const team = pub.ctx.user && pub.ctx.user.team;
  $("#team-opt").classList.toggle("hidden", !team);
  if (team) $("#team-label").textContent = "Release de ma team (" + team.name + ")";
  $("#f-anon").checked = !!(pub.ctx.user && pub.ctx.user.anonymousDefault);
}

function categoryBySlug(slug) {
  for (const c of (pub.ctx && pub.ctx.categories) || []) {
    if (c.slug === slug) return { root: c, leaf: null };
    for (const k of c.children || []) if (k.slug === slug) return { root: c, leaf: k };
  }
  return null;
}

function fillLeaves(rootSlug, leafSlug) {
  const leaf = $("#f-leaf");
  leaf.replaceChildren(new Option("Sous-catégorie…", ""));
  const found = categoryBySlug(rootSlug);
  if (found) for (const k of found.root.children || []) leaf.append(new Option(k.name, k.slug));
  if (leafSlug) leaf.value = leafSlug;
  const isSeries = rootSlug === "series";
  $("#season-field").classList.toggle("hidden", !isSeries);
}

function currentCategory() {
  const leafSlug = $("#f-leaf").value;
  const f = leafSlug ? categoryBySlug(leafSlug) : null;
  return f && f.leaf ? f.leaf : null;
}

function isFilmSeries() {
  const r = $("#f-root").value;
  return r === "films" || r === "series";
}

// Rapport VirusTotal exigé par V3X (même règle que le site, lib/virustotal.ts) :
// applications Windows, macOS, smartphone et « autre », et tous les jeux ; un
// jeu console seulement s'il contient un exécutable PC (retour de Luci).
function needsVirusTotal() {
  const slug = $("#f-leaf").value;
  if (!/^applications-(windows|macos|smartphone|autre)$/.test(slug) && !/^jeux-video-/.test(slug)) return false;
  if (!/^jeux-video-(nintendo|sony|microsoft)$/.test(slug)) return true;
  const files = (pub.job && pub.job.files) || [];
  return files.some((f) => /\.(exe|dll|msi|bat|cmd|ps1|scr|com|jar|vbs|msix|appx)$/i.test(f.rel || ""));
}

// ---------- sélecteur de dossiers ----------

async function browse(path) {
  try {
    const r = await api("/ui/fs?source=" + pub.source + "&path=" + encodeURIComponent(path || ""));
    pub.dir = r.path;
    $("#fs-path").value = r.path;
    $("#fs-up").disabled = r.parent === null;
    $("#fs-up").dataset.parent = r.parent || "";
    const ul = $("#fs-list");
    ul.replaceChildren();
    if (!r.entries.length) ul.append(el("li", "empty", "Dossier vide."));
    for (const e of r.entries) {
      const li = el("li", e.skip ? "skip" : "");
      const icon = el("span", "fs-ico " + (e.dir ? "dir" : "file"));
      icon.setAttribute("aria-hidden", "true");
      const name = el("button", "fs-name", e.name);
      name.type = "button";
      if (e.dir) name.addEventListener("click", () => browse(e.path));
      else name.addEventListener("click", () => choose(e.path));
      li.append(icon, name, el("span", "fs-size", e.dir ? "" : fmtSize(e.size)));
      if (r.path !== "" && !e.skip) {
        const pick = el("button", "btn small", e.dir ? "Publier ce dossier" : "Publier ce fichier");
        pick.type = "button";
        pick.addEventListener("click", () => choose(e.path));
        li.append(pick);
      } else if (e.skip) {
        li.append(el("span", "muted small", "écarté"));
      }
      ul.append(li);
    }
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

// ---------- tâche ----------

async function choose(path, source) {
  if (source && source !== pub.source) {
    pub.source = source;
    pub.dir = "";
    window.pubSources();
  }
  try {
    const j = await api("/ui/pub/start", { path, source: pub.source });
    resetForm();
    pub.job = j;
    pub.published = false;
    $("#pub-pick").classList.add("hidden");
    $("#pub-src").classList.add("hidden");
    $("#pub-job").classList.remove("hidden");
    $("#pub-form").classList.remove("hidden");
    $("#pub-done").classList.add("hidden");
    $("#f-name").value = stripExt(j.name, j.single);
    renderJob();
    runMediaInfo();
    window.musicSetup();
    loadFiche();
    pollJob();
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

function stripExt(name, single) {
  return single ? name.replace(/\.(mkv|mp4|avi|m2ts|ts|mov|wmv|webm|mpe?g|m4v|iso|flac|mp3|m4a|ogg|opus|wav|dsf)$/i, "") : name;
}

function resetForm() {
  pub.fiche = null;
  pub.mediainfo = "";
  pub.miState = "idle";
  pub.tmdb = null;
  pub.dirty = {};
  for (const id of ["#f-name", "#f-title", "#f-season", "#f-desc", "#f-nfo", "#f-vt", "#tmdb-q"]) $(id).value = "";
  $("#f-notmdb").checked = false;
  $("#f-excl").checked = false;
  $("#f-team").checked = false;
  $("#f-root").value = "";
  fillLeaves("", "");
  $("#tmdb-results").replaceChildren();
  $("#tmdb-picked").classList.add("hidden");
  $("#preview").replaceChildren();
  $("#pub-error").classList.add("hidden");
  $("#name-sugg").classList.add("hidden");
  $("#name-tokens").replaceChildren();
}

async function cancelJob() {
  if (pub.job && !pub.job.published) {
    try { await api("/ui/pub/" + pub.job.id + "/cancel", {}); } catch (e) { /* rien */ }
  }
  pub.job = null;
  clearTimeout(pub.timer);
  $("#pub-pick").classList.remove("hidden");
  $("#pub-job").classList.add("hidden");
  $("#pub-form").classList.add("hidden");
  $("#pub-done").classList.add("hidden");
  $("#pub-bar").classList.add("hidden");
  window.pubSources();
  browse(pub.dir);
}

async function pollJob() {
  clearTimeout(pub.timer);
  if (!pub.job) return;
  try {
    pub.job = await api("/ui/pub/" + pub.job.id);
  } catch (e) {
    if (e.message === "session") return;
  }
  renderJob();
  const j = pub.job;
  const seedBusy = j.published && (!j.seed || j.seed.status === "checking") && !j.seedError;
  if (j.state === "hashing" || j.state === "publishing" || j.state === "seeding" || seedBusy) {
    pub.timer = setTimeout(pollJob, 700);
  }
}

function setStep(id, state, text) {
  const s = $(id);
  s.className = "step " + state;
  s.querySelector(".sub").textContent = text;
}

function renderJob() {
  const j = pub.job;
  if (!j) return;
  $("#job-name").textContent = j.name;
  const facts = $("#job-facts");
  facts.replaceChildren();
  const add = (label, value) => {
    const d = el("div", "fact");
    d.append(el("span", "muted", label), el("b", null, value));
    facts.append(d);
  };
  add("Fichiers", String(j.files.length));
  add("Taille", fmtSize(j.total));
  add("Pièces", (j.pieceLength >= 1048576 ? j.pieceLength / 1048576 + " Mio" : j.pieceLength / 1024 + " Kio"));
  if (j.source && j.source !== "Ce PC") add("Sur", j.source);
  if (j.skipped && j.skipped.length) add("Écartés", String(j.skipped.length));

  const dup = $("#job-dup");
  if (j.duplicate) {
    const m = j.duplicate;
    dup.replaceChildren(document.createTextNode(m.kind === "exact" || m.kind === "self" ? "Cette release est déjà sur V3X, à l'identique : " : "Les mêmes fichiers sont déjà sur V3X, sous un autre nom : "));
    const a = el("a", null, m.name);
    a.href = "https://v3x.club/torrents/" + m.id;
    a.target = "_blank";
    a.rel = "noopener";
    dup.append(a, document.createTextNode(". Pas besoin de la publier : seede-la depuis l'onglet Cross-seed."));
    dup.classList.add("bad");
    dup.classList.remove("hidden");
  } else {
    dup.classList.add("hidden");
  }

  if (j.state === "error") setStep("#st-hash", "bad", j.error);
  else if (j.state === "hashing") {
    setStep("#st-hash", "run", "Hachage : " + fmtSize(j.hashed) + " sur " + fmtSize(j.total));
  } else setStep("#st-hash", "ok", j.fromCache ? "Repris du cache : ces fichiers ont déjà été hachés." : "Prêt.");
  $("#st-hash-bar").style.width = (j.total ? Math.round((j.hashed / j.total) * 100) : 0) + "%";
  $("#st-hash .mini").classList.toggle("hidden", j.state !== "hashing");

  const miText = { idle: "En attente.", running: "Lecture du fichier vidéo…", done: "Lu sur le plus gros fichier vidéo.", none: "Aucun fichier vidéo : pas de MediaInfo.", error: "Lecture impossible : colle-le à la main dans le NFO." }[pub.miState];
  setStep("#st-mi", { running: "run", done: "ok", error: "bad", none: "", idle: "" }[pub.miState], miText);

  pubClients(j);
  let mapText;
  const multi = multiClients();
  const who = multi ? "« " + j.clientName + " »" : "Ton client";
  if (j.mapGuess) mapText = who + " verra ce dossier comme « " + j.clientDir + " » (déduit de " + j.mapVotes + " torrent" + (j.mapVotes > 1 ? "s" : "") + " de " + (multi ? "ce client" : "ton client") + "). ";
  else mapText = who + " cherchera les fichiers dans « " + j.clientDir + " ». ";
  setStep("#st-map", j.mapGuess || j.mapVotes ? "ok" : "", mapText);
  const fix = el("button", "link", "Corriger");
  fix.type = "button";
  fix.addEventListener("click", () => {
    $("#map-edit").classList.toggle("hidden");
    $("#map-dir").value = j.clientDir;
  });
  $("#st-map-txt").append(fix);

  // Publication terminée.
  if (j.published) renderDone();
  renderBar();
}

// Plusieurs clients : celui qui seedera la release, au choix jusqu'à l'envoi.
function pubClients(j) {
  const list = (ui.state && ui.state.clients) || [];
  const row = $("#pub-client-row");
  const show = list.length > 1 && !j.published && j.state !== "publishing";
  row.classList.toggle("hidden", !show);
  if (!show) return;
  const sel = $("#pub-client");
  if (document.activeElement === sel) return;
  const key = list.map((c) => c.id + (c.ok ? "+" : "-")).join(",");
  if (sel.dataset.key !== key) {
    sel.dataset.key = key;
    sel.replaceChildren(...list.map((c) => {
      const o = el("option", null, c.label + (c.ok ? "" : " (injoignable)"));
      o.value = c.id;
      o.disabled = !c.ok;
      return o;
    }));
  }
  sel.value = j.client;
}

async function pickClient(ev) {
  const sel = ev.target;
  sel.disabled = true;
  try {
    pub.job = await api("/ui/pub/" + pub.job.id + "/client", { client: sel.value });
    $("#map-edit").classList.add("hidden");
    renderJob();
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
    if (pub.job) sel.value = pub.job.client;
  } finally {
    sel.disabled = false;
  }
}

async function saveMap() {
  try {
    pub.job = await api("/ui/pub/" + pub.job.id + "/clientdir", { dir: $("#map-dir").value, remember: $("#map-remember").checked });
    $("#map-edit").classList.add("hidden");
    renderJob();
    toast("Dossier du client enregistré.", "ok");
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

// ---------- MediaInfo (dans la page, même bibliothèque que le site) ----------

let miFactory = null;

// MediaInfo du plus gros fichier vidéo d'une tâche, lu par morceaux via D3X.
// Sert aussi au mode lot. Renvoie "" s'il n'y a pas de vidéo.
async function mediaInfoOf(j) {
  if (j.mainFile < 0) return "";
  if (!miFactory) {
    miFactory = await window.MediaInfo.mediaInfoFactory({ format: "text", locateFile: () => "/vendor/mediainfo/MediaInfoModule.wasm" });
  }
  const file = j.files[j.mainFile];
  const read = async (size, offset) => {
    const r = await fetch("/ui/pub/" + j.id + "/file/" + j.mainFile + "?offset=" + offset + "&size=" + size);
    if (!r.ok) throw new Error("lecture " + r.status);
    return new Uint8Array(await r.arrayBuffer());
  };
  const text = await miFactory.analyzeData(() => file.size, read);
  return String(text || "").trim();
}

async function runMediaInfo() {
  const j = pub.job;
  if (!j) return;
  if (j.mainFile < 0) {
    pub.miState = "none";
    renderJob();
    return;
  }
  pub.miState = "running";
  renderJob();
  try {
    const text = await mediaInfoOf(j);
    if (!pub.job || pub.job.id !== j.id) return;
    pub.mediainfo = text;
    pub.miState = text ? "done" : "error";
  } catch (e) {
    if (!pub.job || pub.job.id !== j.id) return;
    pub.miState = "error";
  }
  renderJob();
  loadFiche();
}

// ---------- fiche proposée par V3X ----------

let ficheTimer = null;
function loadFicheSoon() {
  clearTimeout(ficheTimer);
  ficheTimer = setTimeout(loadFiche, 400);
}

async function loadFiche(force) {
  const j = pub.job;
  if (!j || !pub.ctx) return;
  const seq = ++pub.ficheSeq;
  $("#fiche-state").textContent = "Préparation de la fiche par V3X…";
  const leaf = currentCategory();
  try {
    const f = await api("/ui/pub/" + j.id + "/fiche", {
      releaseName: $("#f-name").value.trim() || stripExt(j.name, j.single),
      mediainfo: pub.mediainfo,
      tmdbRef: pub.tmdb ? pub.tmdb.type + "/" + pub.tmdb.id : null,
      mbRef: pub.mb ? mbURL(pub.mb) : null,
      categorySlug: leaf ? leaf.slug : null,
      templateId: $("#f-tpl").value || null,
    });
    if (seq !== pub.ficheSeq) return;
    pub.fiche = f;
    applyFiche(f, force);
    $("#fiche-state").textContent = "";
  } catch (e) {
    if (seq !== pub.ficheSeq || e.message === "session") return;
    $("#fiche-state").textContent = "Fiche indisponible : " + e.message;
  }
}

function setField(id, key, value, force) {
  if (value == null) return;
  if (pub.dirty[key] && !force) return;
  $(id).value = value;
}

function applyFiche(f, force) {
  if (f.category && (!pub.dirty.category || force)) {
    const found = categoryBySlug(f.category.slug);
    if (found) {
      $("#f-root").value = found.root.slug;
      fillLeaves(found.root.slug, found.leaf ? found.leaf.slug : "");
    }
  }
  setField("#f-lang", "language", f.language, force);
  setField("#f-season", "season", f.season, force);
  setField("#f-title", "title", f.title, force);
  if (!pub.dirty.team || force) $("#f-team").checked = !!f.teamRelease;
  setField("#f-nfo", "nfo", f.nfo, force);
  if (!pub.dirty.desc || force) {
    $("#f-desc").value = f.presentation || "";
    previewSoon(0);
  }
  if (!$("#tmdb-q").value && f.tmdbQuery && !pub.tmdb && !isAlbum()) {
    $("#tmdb-q").value = f.tmdbQuery;
    searchTmdb();
  }
  // Nom suggéré par le site (année, piste audio…), adopté d'un clic.
  const sugg = $("#name-sugg");
  if (f.suggestedName && f.suggestedName !== $("#f-name").value) {
    sugg.replaceChildren(document.createTextNode("Suggestion de V3X : "));
    sugg.append(el("code", null, f.suggestedName), document.createTextNode(" "));
    const b = el("button", "link", "Adopter");
    b.type = "button";
    b.addEventListener("click", () => {
      $("#f-name").value = f.suggestedName;
      pub.dirty.name = true;
      sugg.classList.add("hidden");
      loadFicheSoon();
    });
    sugg.append(b);
    sugg.classList.remove("hidden");
  } else {
    sugg.classList.add("hidden");
  }
  const tok = $("#name-tokens");
  tok.replaceChildren();
  for (const t of (f.analysis && f.analysis.tokens) || []) {
    const c = el("span", "tok");
    c.append(el("span", "muted", (TOKENS[t.kind] || t.kind) + " "), el("b", null, t.value));
    tok.append(c);
  }
  // La liste warnings reprend les remarques sur le nom (en clair) et les codes.
  for (const w of f.warnings || []) {
    if (w === "tmdb_missing" && pub.tmdb) continue;
    tok.append(el("span", "tok warn" + (BLOCKING.has(w) ? " block" : ""), WARNINGS[w] || w));
    if (w === "audio_naming") window.musicNameFix(tok);
  }
  renderBar();
}

// ---------- TMDB ----------

async function searchTmdb() {
  const q = $("#tmdb-q").value.trim();
  if (q.length < 2) return;
  const ul = $("#tmdb-results");
  ul.replaceChildren(el("li", "muted", "Recherche…"));
  try {
    const isRef = /themoviedb\.org\/(movie|tv)\/\d+|^(movie|tv)\/\d+$/i.test(q);
    const r = isRef ? await api("/ui/pub/tmdb/lookup?ref=" + encodeURIComponent(q)) : await api("/ui/pub/tmdb/search?q=" + encodeURIComponent(q));
    ul.replaceChildren();
    const list = (r.results || []).filter((x) => x.type === "movie" || x.type === "tv").slice(0, 12);
    if (!list.length) ul.append(el("li", "muted", "Aucun résultat. Essaie le titre original, ou colle le lien de la fiche TMDB."));
    for (const it of list) {
      const li = el("li");
      const b = el("button", "tmdb-item");
      b.type = "button";
      if (it.posterUrl && /^https:\/\//.test(it.posterUrl)) {
        const img = el("img");
        img.src = it.posterUrl;
        img.alt = "";
        img.loading = "lazy";
        b.append(img);
      } else {
        b.append(el("span", "noposter", "?"));
      }
      const meta = el("span", "tmdb-meta");
      meta.append(el("b", null, it.title), el("span", "muted", (it.type === "tv" ? "Série" : "Film") + (it.year ? " · " + it.year : "")));
      if (it.originalTitle && it.originalTitle !== it.title) meta.append(el("span", "muted small", it.originalTitle));
      b.append(meta);
      b.addEventListener("click", () => pickTmdb(it));
      li.append(b);
      ul.append(li);
    }
  } catch (e) {
    if (e.message !== "session") ul.replaceChildren(el("li", "bad", e.message));
  }
}

function pickTmdb(it) {
  pub.tmdb = { type: it.type, id: it.id, title: it.title, year: it.year, posterUrl: it.posterUrl };
  $("#f-notmdb").checked = false;
  $("#tmdb-results").replaceChildren();
  const p = $("#tmdb-picked");
  p.replaceChildren();
  if (it.posterUrl && /^https:\/\//.test(it.posterUrl)) {
    const img = el("img");
    img.src = it.posterUrl;
    img.alt = "";
    p.append(img);
  }
  const t = el("div");
  const a = el("a", null, it.title + (it.year ? " (" + it.year + ")" : ""));
  a.href = "https://www.themoviedb.org/" + it.type + "/" + it.id;
  a.target = "_blank";
  a.rel = "noopener";
  t.append(a, el("div", "muted small", it.type === "tv" ? "Série" : "Film"));
  const ch = el("button", "link", "Changer");
  ch.type = "button";
  ch.addEventListener("click", () => {
    pub.tmdb = null;
    p.classList.add("hidden");
    searchTmdb();
    loadFicheSoon();
  });
  t.append(ch);
  p.append(t);
  p.classList.remove("hidden");
  loadFiche();
}

// ---------- éditeur de présentation ----------

const TOOLS = [
  ["B", "Gras", "[b]", "[/b]"],
  ["I", "Italique", "[i]", "[/i]"],
  ["U", "Souligné", "[u]", "[/u]"],
  ["S", "Barré", "[s]", "[/s]"],
  ["Centrer", "Centrer", "[center]", "[/center]", true],
  ["Justifier", "Justifier", "[justify]", "[/justify]", true],
  ["Citation", "Citation", "[quote]", "[/quote]", true],
  ["Code", "Code / NFO (garde l'alignement)", "[code]", "[/code]", true],
  ["Spoiler", "Spoiler", "[spoiler=Spoiler]", "[/spoiler]", true],
  ["Avis", "Avis perso (bloc mis en valeur)", "[avis]", "[/avis]", true],
  ["Lien", "Lien", "[url=https://]", "[/url]"],
  ["Image", "Image (lien direct)", "[img]", "[/img]"],
  ["Vidéo", "Vidéo YouTube (colle le lien entre les balises)", "[youtube]", "[/youtube]"],
  ["Liste", "Liste", "[list]\n[*] ", "\n[/list]", true],
  ["Tableau", "Tableau", "[table]\n[tr][th]Colonne 1[/th][th]Colonne 2[/th][/tr]\n[tr][td]", "[/td][td]…[/td][/tr]\n[/table]", true],
];

function wrap(open, close, block) {
  const ta = $("#f-desc");
  const { selectionStart: a, selectionEnd: b, value } = ta;
  const sel = value.slice(a, b);
  const pre = block && a > 0 && value[a - 1] !== "\n" ? "\n" : "";
  ta.value = value.slice(0, a) + pre + open + sel + close + value.slice(b);
  const pos = a + pre.length + open.length;
  ta.focus();
  ta.setSelectionRange(pos, pos + sel.length);
  pub.dirty.desc = true;
  previewSoon();
}

function buildToolbar() {
  const bar = $("#toolbar");
  for (const [label, title, open, close, block] of TOOLS) {
    const b = el("button", "tool", label);
    b.type = "button";
    b.title = title;
    if (label.length === 1) b.classList.add("tool-" + label.toLowerCase());
    b.addEventListener("click", () => wrap(open, close, block));
    bar.append(b);
  }
  const size = el("select", "tool");
  size.setAttribute("aria-label", "Taille du texte");
  size.append(new Option("Taille", ""));
  for (const n of [1, 2, 3, 4, 5, 6, 7]) size.append(new Option(String(n), String(n)));
  size.addEventListener("change", () => {
    if (size.value) wrap("[size=" + size.value + "]", "[/size]");
    size.value = "";
  });
  const color = el("input", "tool color");
  color.type = "color";
  color.value = "#3360e8";
  color.title = "Couleur";
  color.addEventListener("change", () => wrap("[color=" + color.value + "]", "[/color]"));
  bar.append(size, color);
}

let prevTimer = null;
function previewSoon(delay) {
  clearTimeout(prevTimer);
  prevTimer = setTimeout(renderPreview, delay == null ? 500 : delay);
}

async function renderPreview() {
  const seq = ++pub.previewSeq;
  const bb = $("#f-desc").value;
  if (!bb.trim()) {
    $("#preview").replaceChildren(el("p", "muted", "La présentation s'affichera ici."));
    return;
  }
  try {
    const r = await api("/ui/pub/preview", { bbcode: bb });
    if (seq !== pub.previewSeq) return; // une réponse plus récente arrive
    $("#preview").innerHTML = r.html;
  } catch (e) {
    if (seq !== pub.previewSeq || e.message === "session") return;
    $("#preview").replaceChildren(el("p", "bad", "Aperçu indisponible : " + e.message));
  }
}

function edTab(prev) {
  $("#ed-tab-edit").setAttribute("aria-selected", String(!prev));
  $("#ed-tab-prev").setAttribute("aria-selected", String(prev));
  $("#ed-panes").classList.toggle("show-prev", prev);
}

// ---------- publication ----------

function missing() {
  const out = [];
  const j = pub.job;
  if (!j) return ["release"];
  if (j.state === "error") out.push("le .torrent n'a pas pu être fabriqué");
  if (!currentCategory()) out.push("la sous-catégorie");
  if (isFilmSeries() && !pub.tmdb && !$("#f-notmdb").checked) out.push("la fiche TMDB");
  if (isFilmSeries() && !$("#f-nfo").value.trim()) out.push("le NFO (MediaInfo)");
  if (!$("#f-name").value.trim()) out.push("le nom de la release");
  // Musique : V3X refuse un nom sans la qualité (format, bits, fréquence).
  if (pub.fiche && (pub.fiche.warnings || []).includes("audio_naming")) out.push("la qualité dans le nom");
  if (needsVirusTotal() && !$("#f-vt").value.trim()) out.push("le rapport VirusTotal");
  return out;
}

function renderBar() {
  const bar = $("#pub-bar");
  const j = pub.job;
  const show = pub.shown && j && !j.published;
  bar.classList.toggle("hidden", !show);
  $("#vt-field").classList.toggle("hidden", !needsVirusTotal() && !$("#f-vt").value.trim());
  if (!show) return;
  const miss = missing();
  const go = $("#pub-go");
  let txt;
  if (j.state === "publishing") txt = "Publication en cours…";
  else if (miss.length) txt = "Il manque : " + miss.join(", ");
  else if (j.state === "hashing") txt = "Fiche prête. Le .torrent se termine : " + Math.round((j.hashed / j.total) * 100) + " %";
  else txt = "Tout est prêt.";
  $("#pub-bar-txt").textContent = txt;
  go.disabled = miss.length > 0 || j.state !== "ready";
}

function fields() {
  const leaf = currentCategory();
  const f = {
    categoryId: leaf ? String(leaf.id) : "",
    description: $("#f-desc").value,
    descriptionFormat: "standard",
    title: $("#f-title").value.trim(),
    releaseName: $("#f-name").value.trim(),
    nfo: $("#f-nfo").value,
    language: $("#f-lang").value,
    anonymous: String($("#f-anon").checked),
    exclusive: String($("#f-excl").checked),
  };
  if ($("#f-root").value === "series" && $("#f-season").value.trim()) f.season = $("#f-season").value.trim();
  if (pub.tmdb) f.tmdbUrl = "https://www.themoviedb.org/" + pub.tmdb.type + "/" + pub.tmdb.id;
  // Musique : le lien MusicBrainz, comme sur la page d'upload (titre, jaquette).
  if (isAlbum() && pub.mb) {
    f.tmdbUrl = mbURL(pub.mb);
    f.creator = (pub.fiche && pub.fiche.creator) || pub.mb.artist || "";
  }
  if ($("#f-notmdb").checked) f.noTmdb = "true";
  const vt = $("#f-vt").value.trim();
  if (vt) f.virusTotalUrl = vt;
  if (pub.ctx && pub.ctx.user && pub.ctx.user.team) f.teamRelease = String($("#f-team").checked);
  return f;
}

function askPublish() {
  const f = fields();
  const dl = $("#pub-confirm-list");
  dl.replaceChildren();
  const leaf = currentCategory();
  const rows = [
    ["Release", f.releaseName],
    ["Catégorie", leaf ? leaf.name : ""],
    isAlbum()
      ? ["Album", pub.mb ? pub.mb.title + (pub.mb.date ? " (" + pub.mb.date.slice(0, 4) + ")" : "") + " · MusicBrainz" : "aucune fiche MusicBrainz"]
      : ["Œuvre", pub.tmdb ? pub.tmdb.title + (pub.tmdb.year ? " (" + pub.tmdb.year + ")" : "") : "aucune"],
    ["Taille", fmtSize(pub.job.total)],
    ["Visibilité", f.anonymous === "true" ? "anonyme" : "sous ton pseudo"],
    ["Seed", "ajoutée à " + (multiClients() ? "« " + pub.job.clientName + " »" : "ton client") + " dans « " + pub.job.clientDir + " »"],
  ];
  for (const [k, v] of rows) dl.append(el("dt", null, k), el("dd", null, v));
  $("#f-rights").checked = false;
  $("#pub-confirm-yes").disabled = true;
  $("#pub-confirm").showModal();
}

async function doPublish() {
  $("#pub-confirm").close();
  $("#pub-error").classList.add("hidden");
  try {
    pub.job = await api("/ui/pub/" + pub.job.id + "/publish", { fields: fields() });
    renderJob();
    pollJob();
    watchPublish();
  } catch (e) {
    if (e.message !== "session") showPubError(e.message);
  }
}

// Attend la réponse du site : erreur (on reste sur la fiche) ou publiée.
function watchPublish() {
  const tick = async () => {
    if (!pub.job) return;
    try { pub.job = await api("/ui/pub/" + pub.job.id); } catch (e) { return; }
    const j = pub.job;
    if (j.state === "publishing") return setTimeout(tick, 700);
    if (!j.published && j.error) showPubError(pubErr(j.error, j.errorBody));
    renderJob();
  };
  setTimeout(tick, 700);
}

function showPubError(msg) {
  const n = $("#pub-error");
  n.textContent = "V3X a refusé la publication : " + msg + ".";
  n.classList.remove("hidden");
  n.scrollIntoView({ block: "center", behavior: "smooth" });
}

function renderDone() {
  const j = pub.job;
  const box = $("#pub-done");
  box.replaceChildren();
  const h = el("h2", null, j.published.status === "active" ? "Publiée sur V3X" : "Envoyée, en attente de validation");
  const a = el("a", "btn primary", "Voir la fiche");
  a.href = "https://v3x.club/torrents/" + j.published.id;
  a.target = "_blank";
  a.rel = "noopener";
  const p = el("p", "muted");
  if (j.published.uploadBonus) p.textContent = "Prime d'upload : " + j.published.uploadBonus.toLocaleString("fr-FR") + " jetons. ";
  let seed;
  if (j.seedError) seed = "Seed : " + j.seedError + ". Ajoute le .torrent à ton client à la main, depuis la fiche.";
  else if (!j.seed || j.seed.status === "checking") seed = "Seed : ton client vérifie les fichiers" + (j.seed && j.seed.progress ? " (" + Math.round(j.seed.progress * 100) + " %)" : "") + "…";
  else if (j.seed.status === "seeding") seed = "Seed : vérifié à 100 %, ta release est en seed.";
  else seed = "Seed : " + (j.seed.detail || j.seed.status);
  const s = el("p", null, seed);
  const again = el("button", "btn", "Publier une autre release");
  again.type = "button";
  again.addEventListener("click", cancelJob);
  const row = el("div", "row");
  row.append(a, again);
  box.append(h, p, s, row);
  const reqBox = el("div", "pub-reqs");
  box.append(reqBox);
  if (!pub.reqsFor || pub.reqsFor !== j.id) {
    pub.reqsFor = j.id;
    pub.reqHits = null;
    api("/ui/requests/for?name=" + encodeURIComponent(j.published.name || $("#f-name").value))
      .then((r) => { pub.reqHits = (r.requests || []).filter((x) => !x.mine); if (pub.job && pub.job.id === j.id) renderDone(); })
      .catch(() => { pub.reqHits = []; });
  }
  if (pub.reqHits && pub.reqHits.length) {
    reqBox.append(el("h3", null, plural(pub.reqHits.length, "Un membre la demande", "Des membres la demandent")));
    if (j.published.status === "active") {
      const r = el("div", "row wrap");
      for (const x of pub.reqHits.slice(0, 3)) r.append(proposeButton(x, j.published.id));
      reqBox.append(r, el("p", "hint", "Vérifie que c'est bien ce qui est demandé (qualité, langue) : le demandeur valide, et la prime t'est versée."));
    } else {
      reqBox.append(el("p", "hint", "Une fois ta release validée, propose-la depuis la demande sur V3X :"), el("div", "reqs", null));
      reqBox.lastChild.append(...requestBadges(pub.reqHits));
    }
  }
  box.classList.remove("hidden");
  $("#pub-form").classList.add("hidden");
  if (!pub.published) {
    pub.published = true;
    toast(j.published.status === "active" ? "Release publiée." : "Release envoyée en validation.", "ok");
  }
}

// ---------- branchements ----------

// Ce PC ou la seedbox : changer vide le sélecteur et repart de l'accueil
// de la source (dossier personnel sur la seedbox).
window.pubSources = () => {
  const box = $("#pub-src");
  if (pub.source === "ssh" && !(ui.state && ui.state.ssh && ui.state.ssh.ok)) pub.source = "local";
  const shown = renderSourceSwitch(box, pub.source, (id) => {
    pub.source = id;
    window.pubSources();
    if (!pub.job) browse(id === "local" ? (ui.state && ui.state.lastDir) || "" : "");
  });
  box.classList.toggle("hidden", !shown || !!pub.job);
};

// Ouvre une release dans Publier (depuis « Pas sur V3X »).
window.pubOpen = async (path, source) => {
  if (pub.job && !pub.job.published) {
    setView("publish");
    return toast("Une fiche est déjà ouverte dans Publier : publie-la ou « Changer de release » d'abord.", "bad");
  }
  if (pub.job) await cancelJob();
  setView("publish");
  await choose(path, source);
};

window.pubShow = async (on) => {
  pub.shown = on;
  renderBar();
  if (!on) return;
  if (!pub.ctx) await loadCtx();
  if (!pub.job && !$("#fs-list").children.length) {
    let start = "";
    try { const s = await api("/ui/state"); start = s.lastDir || ""; } catch (e) { /* rien */ }
    browse(start);
  }
};

function bindPublish() {
  buildToolbar();
  $("#fs-up").addEventListener("click", () => browse($("#fs-up").dataset.parent || ""));
  $("#fs-go").addEventListener("click", () => browse($("#fs-path").value));
  $("#fs-path").addEventListener("keydown", (e) => { if (e.key === "Enter") browse($("#fs-path").value); });
  $("#job-cancel").addEventListener("click", cancelJob);
  $("#map-save").addEventListener("click", saveMap);
  $("#pub-client").addEventListener("change", pickClient);
  $("#tmdb-go").addEventListener("click", searchTmdb);
  $("#tmdb-q").addEventListener("keydown", (e) => { if (e.key === "Enter") searchTmdb(); });
  $("#f-notmdb").addEventListener("change", () => { if ($("#f-notmdb").checked) { pub.tmdb = null; $("#tmdb-picked").classList.add("hidden"); loadFicheSoon(); } renderBar(); });
  $("#f-root").addEventListener("change", () => { pub.dirty.category = true; fillLeaves($("#f-root").value, ""); renderBar(); });
  $("#f-leaf").addEventListener("change", () => { pub.dirty.category = true; loadFicheSoon(); renderBar(); });
  $("#f-vt").addEventListener("input", renderBar);
  $("#f-name").addEventListener("input", () => { pub.dirty.name = true; loadFicheSoon(); renderBar(); });
  for (const [id, key] of [["#f-title", "title"], ["#f-season", "season"], ["#f-nfo", "nfo"], ["#f-lang", "language"]]) {
    $(id).addEventListener("input", () => { pub.dirty[key] = true; renderBar(); });
  }
  $("#f-team").addEventListener("change", () => { pub.dirty.team = true; loadFicheSoon(); });
  $("#f-tpl").addEventListener("change", () => loadFiche(false));
  $("#regen").addEventListener("click", () => { pub.dirty.desc = false; loadFiche(false); });
  $("#f-desc").addEventListener("input", () => { pub.dirty.desc = true; previewSoon(); });
  $("#f-desc").addEventListener("keydown", (e) => {
    if (!(e.ctrlKey || e.metaKey)) return;
    const k = e.key.toLowerCase();
    if (k === "b" || k === "i" || k === "u") {
      e.preventDefault();
      wrap("[" + k + "]", "[/" + k + "]");
    }
  });
  $("#ed-tab-edit").addEventListener("click", () => edTab(false));
  $("#ed-tab-prev").addEventListener("click", () => edTab(true));
  $("#pub-go").addEventListener("click", askPublish);
  $("#f-rights").addEventListener("change", () => { $("#pub-confirm-yes").disabled = !$("#f-rights").checked; });
  $("#pub-confirm-no").addEventListener("click", () => $("#pub-confirm").close());
  $("#pub-confirm-yes").addEventListener("click", doPublish);
}

bindPublish();
