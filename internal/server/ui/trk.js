"use strict";

// La fiche du tracker dans la vue Publier : TMDB, champs, règles, captures,
// simulation et publication. Le choix de la release, le hachage, le MediaInfo
// et le client qui seedera sont dans publish.js.
// Tout texte venu d'une release, de TMDB ou du site passe par textContent.

const trk = {
  jobId: "",
  ctx: null, // catégories, types, résolutions
  fiche: null, // proposition du serveur (internal/fiche)
  tmdb: null, // { id, title, year, poster, imdb }
  dirty: {}, // champs retouchés : jamais écrasés par une nouvelle fiche
  seq: 0,
  done: false,
  searched: false,
};

// Bandeau d'état du tracker au-dessus de Publier.
function trkScope() {
  const t = (ui.state && ui.state.tracker) || {};
  const box = $("#pub-scope");
  let msg = "";
  if (ui.state && !t.configured) msg = "Baratie n'est relié à aucun tracker : indique son adresse et ton token API dans les réglages.";
  else if (t.configured && !t.ok && t.error) msg = "Connexion à " + (t.name || "ton tracker") + " impossible : " + t.error;
  else if (t.rulesError) msg = "Fichier de règles illisible : " + t.rulesError;
  else if (t.ok && !t.tmdb) msg = "Clé TMDB manquante : ajoute-la dans les réglages pour retrouver les films.";
  box.textContent = msg;
  box.classList.toggle("hidden", !msg);
}
window.trkScope = trkScope;
function trkName() {
  return trackerName();
}

window.trkReset = () => {
  trk.jobId = "";
  trk.fiche = null;
  trk.tmdb = null;
  trk.dirty = {};
  trk.done = false;
  trk.searched = false;
  for (const id of ["#trk-f-name", "#trk-f-desc", "#trk-f-mi", "#trk-f-tmdb", "#trk-f-imdb", "#trk-tmdb-q"]) $(id).value = "";
  for (const id of ["#trk-f-anon", "#trk-f-stream", "#trk-f-sd", "#trk-f-personal"]) $(id).checked = false;
  $("#trk-f-nfo").checked = true;
  $("#trk-tmdb-results").replaceChildren();
  $("#trk-tmdb-picked").classList.add("hidden");
  $("#trk-form").classList.add("hidden");
  $("#trk-done").classList.add("hidden");
  $("#trk-done").replaceChildren();
  $("#trk-shots").replaceChildren();
  $("#trk-shots-info").replaceChildren();
  $("#trk-shots-host").classList.add("hidden");
  $("#trk-shots-state").textContent = "";
};

async function trkCtx() {
  if (trk.ctx) return trk.ctx;
  trk.ctx = await api("/ui/trk/context");
  const fill = (sel, list) => {
    const s = $(sel);
    s.replaceChildren(new Option("Choisir…", ""));
    for (const o of list || []) s.append(new Option(o.name, String(o.id)));
  };
  fill("#trk-f-cat", trk.ctx.categories);
  fill("#trk-f-type", trk.ctx.types);
  fill("#trk-f-res", trk.ctx.resolutions);
  return trk.ctx;
}

// Un champ prend la valeur de la fiche, sauf s'il a été retouché.
function trkSet(id, value) {
  if (trk.dirty[id]) return;
  const n = $(id);
  if (n.type === "checkbox") n.checked = !!value;
  else n.value = value == null || value === 0 ? "" : String(value);
}

// ---------- fiche ----------

window.trkFiche = async () => {
  const j = pub.job;
  if (!j) return;
  if (trk.jobId !== j.id) {
    window.trkReset();
    trk.jobId = j.id;
    // Ouvert depuis le Garde-manger : le film TMDB est déjà connu.
    const pre = window.trkPresetTMDB;
    window.trkPresetTMDB = null;
    if (pre && pre.id && pre.path === j.path) {
      trk.searched = true;
      return trkPick({ id: pre.id });
    }
  }
  trkScope();
  if (j.posted) return;
  $("#trk-form").classList.remove("hidden");
  for (const n of document.querySelectorAll(".trk-n")) n.textContent = trkName();
  $("#trk-name").textContent = trkName();
  const seq = ++trk.seq;
  $("#trk-state").textContent = "Préparation de la fiche…";
  try {
    await trkCtx();
    const f = await api("/ui/pub/" + j.id + "/fiche-trk", {
      mediainfo: pub.mediainfo || "",
      tmdb: trk.tmdb ? trk.tmdb.id : 0,
      imdb: trk.tmdb ? trk.tmdb.imdb || 0 : 0,
      title: $("#trk-f-name").value.trim(),
      tmdbTitle: trk.tmdb ? trk.tmdb.titleEN || trk.tmdb.title : "",
      tmdbYear: trk.tmdb ? trk.tmdb.year || "" : "",
      tmdbLang: trk.tmdb ? trk.tmdb.origLang || "" : "",
      tmdbAnim: trk.tmdb ? !!trk.tmdb.animation : false,
    });
    if (seq !== trk.seq || !pub.job || pub.job.id !== j.id) return;
    trk.fiche = f;
    trkApply(f);
    $("#trk-state").textContent = "";
    // Première fois : recherche TMDB lancée d'après le nom.
    if (!trk.tmdb && !trk.searched && f.tmdbQuery) {
      trk.searched = true;
      $("#trk-tmdb-q").value = f.tmdbQuery;
      trkSearch();
    }
  } catch (e) {
    if (seq !== trk.seq || e.message === "session") return;
    $("#trk-state").textContent = "Fiche indisponible : " + e.message;
  }
  window.trkBar();
};

function trkApply(f) {
  const x = f.fields || {};
  trkSet("#trk-f-name", x.name);
  trkSet("#trk-f-cat", x.categoryId);
  trkSet("#trk-f-type", x.typeId);
  trkSet("#trk-f-res", x.resolutionId);
  trkSet("#trk-f-tmdb", x.tmdb);
  trkSet("#trk-f-imdb", x.imdb);
  trkSet("#trk-f-anon", x.anonymous);
  trkSet("#trk-f-sd", x.sd);
  trkSet("#trk-f-desc", x.description);
  trkSet("#trk-f-mi", x.mediainfo);

  // NFO de la release (lu par publish.js sur le disque).
  $("#trk-nfo-l").classList.toggle("hidden", !pub.localNfo);

  // Jetons du nom.
  const tk = $("#trk-tokens");
  tk.replaceChildren();
  const r = f.release || {};
  const show = [["Année", r.year], ["Langue", [r.lang, r.langVar].filter(Boolean).join(" ")], ["Résolution", r.res], ["Source", [r.provider, r.source].filter(Boolean).join(" ")], ["Codec", r.codec], ["HDR", r.hdr], ["Team", r.group]];
  if (r.remux) show.push(["Remux", "oui"]);
  for (const [k, v] of show) {
    if (!v) continue;
    const s = el("span", "tok", k + " ");
    s.append(el("b", null, v));
    tk.append(s);
  }

  // Conformité du nom.
  const sg = $("#trk-suggest");
  sg.replaceChildren();
  const conf = f.conform || [];
  if (conf.length || f.suggested) {
    sg.append(document.createTextNode("Convention " + trkName() + " : " + conf.join(" ; ") + ". "));
    if (f.suggested) {
      sg.append(document.createTextNode("Proposé : "));
      sg.append(el("code", null, f.suggested), document.createTextNode(" "));
      const b = el("button", "link", "Adopter");
      b.type = "button";
      b.addEventListener("click", () => {
        $("#trk-f-name").value = f.suggested;
        trk.dirty["#trk-f-name"] = true;
        window.trkFiche();
      });
      sg.append(b);
      if (/\[(SOURCE|TEAM)\]/.test(f.suggested)) sg.append(el("span", null, " (complète ensuite [SOURCE] : WEB, BluRay, HDTV… et [TEAM] dans le titre)"));
    }
    sg.classList.remove("hidden");
  } else sg.classList.add("hidden");

  // Où la release sera seedée.
  const sl = $("#trk-seedlink");
  sl.replaceChildren();
  if (f.seedError) sl.append(el("span", "bad", "Seed : " + f.seedError));
  else if (f.seedLink) {
    sl.append(document.createTextNode("Seed depuis un lien physique : "), el("code", null, f.seedLink), document.createTextNode(". Ton fichier d'origine garde son nom et n'est jamais modifié."));
  } else sl.append(document.createTextNode("Le titre n'est que celui de la fiche : aucun fichier n'est renommé."));

  trkList("#trk-blockers", "Publication bloquée :", f.blockers || []);
  trkList("#trk-warnings", "À vérifier :", f.warnings || []);

  const why = $("#trk-why");
  why.replaceChildren(...(f.reasons || []).map((t) => el("li", null, t)));
  trkVersions(f.versions || []);
}

function trkList(sel, title, items) {
  const n = $(sel);
  n.replaceChildren();
  n.classList.toggle("hidden", items.length === 0);
  if (!items.length) return;
  n.append(el("b", null, title));
  const ul = el("ul");
  for (const t of items) ul.append(el("li", null, t));
  n.append(ul);
}

function trkVersions(list) {
  const box = $("#trk-versions");
  box.classList.toggle("hidden", list.length === 0);
  const ul = $("#trk-versions-list");
  ul.replaceChildren();
  for (const v of list.slice(0, 12)) {
    const li = el("li", v.identical || v.same ? "same" : null);
    const a = el("a", null, v.name);
    if (v.url && /^https?:\/\//.test(v.url)) {
      a.href = v.url;
      a.target = "_blank";
      a.rel = "noopener";
    }
    li.append(a);
    if (v.identical) li.append(el("span", "tag", "ta release, à l'identique"));
    else if (v.same) li.append(el("span", "tag", "même nom"));
    else if (v.dupe === "same") li.append(el("span", "tag dupe", "doublon"));
    else if (v.dupe === "maybe") li.append(el("span", "tag maybe", "doublon possible"));
    if (v.dupeWhy) li.title = v.dupeWhy;
    li.append(el("span", "num", [v.type, v.resolution, fmtSize(v.size), v.seeders + " " + plural(v.seeders, "source", "sources")].filter(Boolean).join(" · ")));
    ul.append(li);
  }
  $("#trk-versions-hint").textContent =
    (list.length > 12 ? "Et " + (list.length - 12) + " autre(s). " : "") +
    "Règle de " + trkName() + " : une même source (plateforme), résolution et codec fait un doublon, sauf amélioration nette. Survole « doublon » pour le détail.";
}

// ---------- TMDB ----------

async function trkSearch() {
  const q = $("#trk-tmdb-q").value.trim();
  if (q.length < 2) return;
  const ul = $("#trk-tmdb-results");
  ul.replaceChildren(el("li", "muted", "Recherche…"));
  try {
    const isRef = /themoviedb\.org\/movie\/\d+|imdb\.com\/title\/tt\d+|^tt\d{5,}$|^\d+$/i.test(q);
    let r;
    if (isRef) r = await api("/ui/trk/tmdb/lookup?ref=" + encodeURIComponent(q));
    else {
      const m = q.match(/^(.*?)\s+((?:19|20)\d{2})$/);
      r = await api("/ui/trk/tmdb/search?q=" + encodeURIComponent(m ? m[1] : q) + (m ? "&year=" + m[2] : ""));
    }
    ul.replaceChildren();
    const list = r.results || [];
    if (!list.length) ul.append(el("li", "muted", "Aucun résultat. Essaie le titre original, ou colle le lien TMDB ou IMDB du film."));
    for (const it of list) {
      const li = el("li");
      const b = el("button", "tmdb-item");
      b.type = "button";
      if (it.poster && /^https:\/\//.test(it.poster)) {
        const img = el("img");
        img.src = it.poster;
        img.alt = "";
        img.loading = "lazy";
        b.append(img);
      } else b.append(el("span", "noposter", "?"));
      const meta = el("span", "tmdb-meta");
      meta.append(el("b", null, it.title), el("span", "muted", "Film" + (it.year ? " · " + it.year : "")));
      if (it.originalTitle && it.originalTitle !== it.title) meta.append(el("span", "muted small", it.originalTitle));
      b.append(meta);
      b.addEventListener("click", () => trkPick(it));
      li.append(b);
      ul.append(li);
    }
  } catch (e) {
    if (e.message !== "session") ul.replaceChildren(el("li", "bad", e.message));
  }
}

async function trkPick(it) {
  $("#trk-tmdb-results").replaceChildren(el("li", "muted", "Lecture de la fiche TMDB…"));
  try {
    const m = await api("/ui/trk/tmdb/movie/" + it.id);
    trk.tmdb = { id: m.id, title: m.title, titleEN: m.titleEN || "", year: m.year, poster: m.poster || it.poster, imdb: m.imdb || 0, origLang: m.origLang || it.origLang || "", animation: !!(m.animation || it.animation) };
  } catch (e) {
    if (e.message === "session") return;
    trk.tmdb = { id: it.id, title: it.title, year: it.year, poster: it.poster, imdb: it.imdb || 0, origLang: it.origLang || "", animation: !!it.animation };
    toast("IMDB introuvable : " + e.message, "bad");
  }
  delete trk.dirty["#trk-f-tmdb"];
  delete trk.dirty["#trk-f-imdb"];
  $("#trk-tmdb-results").replaceChildren();
  trkPicked();
  window.trkFiche();
}

function trkPicked() {
  const it = trk.tmdb;
  const p = $("#trk-tmdb-picked");
  p.replaceChildren();
  if (!it) {
    p.classList.add("hidden");
    return;
  }
  if (it.poster && /^https:\/\//.test(it.poster)) {
    const img = el("img");
    img.src = it.poster;
    img.alt = "";
    p.append(img);
  }
  const t = el("div");
  const a = el("a", null, it.title + (it.year ? " (" + it.year + ")" : ""));
  a.href = "https://www.themoviedb.org/movie/" + it.id;
  a.target = "_blank";
  a.rel = "noopener";
  t.append(a, el("div", "muted small", "TMDB " + it.id + (it.imdb ? " · IMDB tt" + String(it.imdb).padStart(7, "0") : " · pas d'IMDB")));
  const ch = el("button", "link", "Changer");
  ch.type = "button";
  ch.addEventListener("click", () => {
    trk.tmdb = null;
    trkPicked();
    trkSearch();
    window.trkFiche();
  });
  t.append(ch);
  p.append(t);
  p.classList.remove("hidden");
}

// ---------- captures d'écran ----------

function trkFmtTime(s) {
  s = Math.round(s);
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = s % 60;
  return (h ? h + ":" + String(m).padStart(2, "0") : m) + ":" + String(x).padStart(2, "0");
}

function trkShotsRender(r) {
  const box = $("#trk-shots");
  box.replaceChildren();
  const list = r.shots || [];
  for (const sh of list) {
    const card = el("div", "trk-shot");
    if (sh.error) card.append(el("div", "bad small", "Capture " + (sh.index + 1) + " : " + sh.error));
    else {
      const a = el("a");
      a.href = sh.url;
      a.target = "_blank";
      a.rel = "noopener";
      const img = el("img");
      img.src = sh.url;
      img.alt = "Capture " + (sh.index + 1);
      img.loading = "lazy";
      a.append(img);
      card.append(a);
    }
    const row = el("div", "row");
    row.append(el("span", "muted", "à " + trkFmtTime(sh.at) + (sh.size ? " · " + fmtSize(sh.size) : "")));
    const redo = el("button", "btn small", "Refaire");
    redo.type = "button";
    redo.title = "Une autre image, à un autre moment du film";
    redo.addEventListener("click", async () => {
      redo.disabled = true;
      try {
        trkShotsRender(await api("/ui/shots/" + pub.job.id + "/" + sh.index + "/redo", {}));
      } catch (e) {
        redo.disabled = false;
        if (e.message !== "session") toast(e.message, "bad");
      }
    });
    row.append(redo);
    card.append(row);
    box.append(card);
  }
  const info = $("#trk-shots-info");
  info.replaceChildren();
  for (const n of r.notes || []) info.append(el("div", "muted small", "• " + n));
  if (r.warn) info.append(el("div", "warn small", "⚠ " + r.warn));
  info.classList.toggle("hidden", !info.childElementCount);
  $("#trk-shots-host").classList.toggle("hidden", !list.length || list.some((s) => s.error));
}

async function trkShotsMake() {
  const j = pub.job;
  if (!j) return;
  const go = $("#trk-shots-go");
  go.disabled = true;
  $("#trk-shots-state").textContent = "Captures en cours (ffmpeg lit le film)…";
  try {
    trkShotsRender(await api("/ui/shots/" + j.id, { n: 4 }));
    $("#trk-shots-state").textContent = "Vérifie-les : « Refaire » si une image est noire ou floue.";
    go.textContent = "Tout refaire";
  } catch (e) {
    $("#trk-shots-state").textContent = "";
    if (e.message !== "session") toast(e.message, "bad");
  } finally {
    go.disabled = false;
  }
}

async function trkShotsHost() {
  const j = pub.job;
  if (!j) return;
  const b = $("#trk-shots-host");
  b.disabled = true;
  $("#trk-shots-state").textContent = "Envoi sur imgbb et vérification à travers le relais de " + trkName() + "…";
  try {
    const r = await api("/ui/shots/" + j.id + "/host", {});
    const d = $("#trk-f-desc");
    d.value = d.value.replace(/\s*\[center\](\[url=https:\/\/(?:[a-z0-9.]*pixhost\.to|i\.ibb\.co)[^\]]*\]\[img\][^\[]*\[\/img\]\[\/url\][\s]*)+\[\/center\]/g, "").trimEnd() + "\n\n" + r.bbcode;
    trk.dirty["#trk-f-desc"] = true;
    $("#trk-shots").replaceChildren();
    $("#trk-shots-info").replaceChildren();
    b.classList.add("hidden");
    $("#trk-shots-state").textContent = (r.hosted || []).length + " captures hébergées sur imgbb, vérifiées et ajoutées à la description.";
    if (r.warn) toast(r.warn, "warn");
    $("#trk-shots-go").textContent = "Générer 4 captures";
    window.trkBar();
    return true;
  } catch (e) {
    $("#trk-shots-state").textContent = "";
    if (e.message !== "session") toast(e.message, "bad");
    return false;
  } finally {
    b.disabled = false;
  }
}

// Captures générées mais pas encore hébergées (0.4.3) : elles ne seraient
// pas sur la fiche.
function trkShotsPending() {
  const n = $("#trk-shots").childElementCount;
  return { n, hostable: n > 0 && !$("#trk-shots-host").classList.contains("hidden") };
}

// Les captures déjà dans la description (vignettes imgbb).
function trkDescShots(desc) {
  return (desc.match(/\[img\]https:\/\/[^\[]*(?:pixhost|ibb\.co)[^\[]*\[\/img\]/g) || []).length;
}

// « Héberger les captures » depuis la confirmation : imgbb, description
// mise à jour, puis la confirmation revient avec la description finale.
async function trkHostThenAsk() {
  const b = $("#trk-dialog-host");
  b.disabled = true;
  b.textContent = "Envoi sur imgbb…";
  const ok = await trkShotsHost();
  b.disabled = false;
  $("#trk-dialog").close();
  if (ok) window.trkAsk();
}

// ---------- envoi ----------

function trkFields() {
  const n = (id) => Number($(id).value) || 0;
  return {
    name: $("#trk-f-name").value.trim(),
    description: $("#trk-f-desc").value,
    mediainfo: $("#trk-f-mi").value,
    categoryId: n("#trk-f-cat"),
    typeId: n("#trk-f-type"),
    resolutionId: n("#trk-f-res"),
    tmdb: n("#trk-f-tmdb"),
    imdb: n("#trk-f-imdb"),
    anonymous: $("#trk-f-anon").checked,
    stream: $("#trk-f-stream").checked,
    sd: $("#trk-f-sd").checked,
    personal: $("#trk-f-personal").checked,
  };
}

function trkNfo() {
  if (!pub.localNfo || !$("#trk-f-nfo").checked) return "";
  // Le NFO lu (texte) repart en octets UTF-8, en base64.
  const bytes = new TextEncoder().encode(pub.localNfo);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

// Ce qui manque ou bloque, en clair.
function trkMissing() {
  const f = trkFields();
  const out = [];
  if (!f.name) out.push("le titre");
  if (/\[(SOURCE|TEAM)\]/.test(f.name)) out.push("la source et la team dans le titre ([SOURCE], [TEAM])");
  if (!f.description.trim()) out.push("la description");
  if (!f.categoryId) out.push("la catégorie");
  if (!f.typeId) out.push("le type");
  if (!f.resolutionId) out.push("la résolution");
  if (!f.tmdb) out.push("le film TMDB");
  return out;
}

window.trkBar = () => {
  const bar = $("#pub-bar");
  const j = pub.job;
  const show = pub.shown && j && !j.posted;
  bar.classList.toggle("hidden", !show);
  $("#trk-sim").classList.remove("hidden");
  bar.querySelector(".how").textContent = "Simuler montre ce qui partirait. Publier envoie la fiche sur " + trkName() + ", puis la release est ajoutée à ton client, vérifiée et mise en seed.";
  const go = $("#pub-go");
  go.textContent = "Publier sur " + trkName();
  if (!show) return;
  const miss = trkMissing();
  const blk = (trk.fiche && trk.fiche.blockers) || [];
  // Les bloquants levés à la main (film TMDB saisi, type choisi) ne comptent
  // plus : seuls restent ceux qui ne dépendent pas de la fiche.
  const hard = blk.filter((b) => !/choisis/.test(b));
  let txt;
  if (j.state === "publishing") txt = "Publication en cours…";
  else if (!trk.fiche) txt = "Préparation de la fiche…";
  else if (hard.length) txt = "Bloqué : " + hard[0];
  else if (miss.length) txt = "Il manque : " + miss.join(", ");
  else if (j.state === "hashing") txt = "Fiche prête. Le .torrent se termine : " + Math.round((j.hashed / j.total) * 100) + " %";
  else txt = "Tout est prêt.";
  $("#pub-bar-txt").textContent = txt;
  bar.classList.toggle("blocked", !!trk.fiche && (hard.length > 0 || miss.length > 0) && j.state !== "publishing");
  go.disabled = !trk.fiche || hard.length > 0 || miss.length > 0 || j.state !== "ready";
  $("#trk-sim").disabled = !trk.fiche || j.state === "publishing";
};

function trkDialog(title, hint, rows, publish) {
  $("#trk-dialog-title").textContent = title;
  $("#trk-dialog-hint").textContent = hint;
  const dl = $("#trk-dialog-list");
  dl.replaceChildren();
  for (const [k, v] of rows) dl.append(el("dt", null, k), el("dd", null, v));
  const yes = $("#trk-dialog-yes");
  yes.classList.toggle("hidden", !publish);
  yes.textContent = "Publier sur " + trkName();
  // Captures en attente : deux choix, héberger d'abord (mis en avant) ou
  // publier sans.
  const host = $("#trk-dialog-host");
  const hostable = publish === "shots";
  host.classList.toggle("hidden", !hostable);
  host.textContent = "Héberger les captures";
  yes.classList.toggle("primary", !!publish && !hostable);
  if (publish === "shots" || publish === "noshots") yes.textContent = "Publier sans captures";
  $("#trk-dialog").showModal();
}

async function trkSimulate() {
  const j = pub.job;
  if (!j) return;
  try {
    const r = await api("/ui/pub/" + j.id + "/simulate-trk", { fields: trkFields(), nfo: trkNfo() });
    const rows = [["Envoi", r.endpoint + " (" + r.tracker + ")"], ["torrent", r.torrent.name + " · " + fmtSize(r.torrent.size) + (r.torrent.ready ? "" : " · pas encore prêt")]];
    rows.push(["nfo", r.nfoBytes ? r.nfoBytes + " octets" : "aucun"]);
    for (const k of Object.keys(r.form).sort()) {
      let v = r.form[k];
      if (k === "mediainfo" && v.length > 300) v = v.slice(0, 300) + "… (" + v.length + " caractères)";
      rows.push([k, v === "" ? "(vide)" : v]);
    }
    rows.push(["Seed", r.seed.link ? "lien physique " + r.seed.link + " · " + (r.seed.client || "ton client") : (r.seed.client || "ton client") + " · " + r.seed.dir]);
    trkDialog("Simulation : rien n'est envoyé", "Voici exactement ce qui partirait sur " + r.tracker + " si tu publiais maintenant.", rows, false);
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

window.trkAsk = () => {
  const f = trkFields();
  const ctxName = (list, id) => ((list || []).find((o) => o.id === id) || {}).name || "?";
  const c = trk.ctx || {};
  const sp = trkShotsPending();
  const inDesc = trkDescShots(f.description);
  let hint = "Un clic, un envoi : la fiche part sur " + trkName() + ", puis la release est mise en seed.";
  let mode = true;
  if (sp.n) {
    hint = sp.hostable
      ? "Tes " + sp.n + " captures ne sont pas encore hébergées : elles ne seraient pas sur la fiche. « Héberger les captures » les envoie sur imgbb, vérifie qu'elles s'affichent sur " + trkName() + " et les ajoute à la description ; tu verras la description finale avant de publier."
      : "Tes captures ne sont pas hébergées (une est en erreur : « Refaire » dans la fiche) : elles ne seraient pas sur la fiche.";
    mode = sp.hostable ? "shots" : "noshots";
  }
  const desc = f.description.trim();
  trkDialog(sp.n ? "Captures non hébergées" : "Publier cette release ?", hint, [
    ["Description", (inDesc ? inDesc + " captures · " : "sans captures · ") + (desc.length > 160 ? desc.slice(0, 160) + "…" : desc)],
    ["Titre", f.name],
    ["Film", trk.tmdb ? trk.tmdb.title + (trk.tmdb.year ? " (" + trk.tmdb.year + ")" : "") : "TMDB " + f.tmdb],
    ["Fiche", [ctxName(c.categories, f.categoryId), ctxName(c.types, f.typeId), ctxName(c.resolutions, f.resolutionId)].join(" · ")],
    ["Taille", fmtSize(pub.job.total)],
    ["Visibilité", f.anonymous ? "anonyme" : "sous ton pseudo"],
    ["NFO", trkNfo() ? "joint" : "aucun"],
    ["Seed", trk.fiche && trk.fiche.seedLink ? "lien physique « " + trk.fiche.seedLink + " », ajouté à " + (pub.job.clientName || "ton client") : "ajoutée à " + (pub.job.clientName || "ton client") + " dans « " + pub.job.clientDir + " »"],
  ], mode);
};

async function trkPublish() {
  $("#trk-dialog").close();
  const j = pub.job;
  if (!j) return;
  try {
    pub.job = await api("/ui/pub/" + j.id + "/publish-trk", { fields: trkFields(), nfo: trkNfo() });
    renderJob();
    pollJob();
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

// ---------- état de la tâche ----------

window.trkRenderJob = (j) => {
  // Doublon de nom trouvé au départ : signalé dans l'encart de la release.
  const dup = $("#job-dup");
  const same = (j.existing || []).find((v) => v.same);
  if (same && !j.posted) {
    dup.textContent = "Une release de ce nom est déjà sur " + trkName() + " : " + same.name + ".";
    dup.classList.add("bad");
    dup.classList.remove("hidden");
  }
  const err = $("#trk-error");
  err.textContent = j.error && j.state === "ready" && !j.posted ? j.error : "";
  err.classList.toggle("hidden", !err.textContent);
  if (j.posted) trkDone(j);
};

function trkDone(j) {
  const box = $("#trk-done");
  box.replaceChildren();
  box.append(el("h2", null, "Publiée sur " + trkName()));
  const row = el("div", "row");
  if (j.posted.url && /^https?:\/\//.test(j.posted.url)) {
    const a = el("a", "btn primary", "Voir la fiche");
    a.href = j.posted.url;
    a.target = "_blank";
    a.rel = "noopener";
    row.append(a);
  }
  let seed;
  if (j.seedError) seed = "Seed : " + j.seedError + ". Ajoute le .torrent à ton client à la main, depuis la fiche.";
  else if (j.state !== "done" || !j.seed || (j.seed.status === "checking" && !j.seed.detail)) seed = "Seed : ton client vérifie les fichiers" + (j.seed && j.seed.progress ? " (" + Math.round(j.seed.progress * 100) + " %)" : "") + "…";
  else if (j.seed.status === "seeding") seed = "Seed : vérifié à 100 %, ta release est en seed.";
  else seed = "Seed : " + (j.seed.detail || j.seed.status);
  const again = el("button", "btn", "Publier une autre release");
  again.type = "button";
  again.addEventListener("click", cancelJob);
  row.append(again);
  box.append(el("p", null, seed), row);
  box.classList.remove("hidden");
  $("#trk-form").classList.add("hidden");
  $("#pub-bar").classList.add("hidden");
  if (!trk.done) {
    trk.done = true;
    toast("Release publiée sur " + trkName() + ".", "ok");
  }
}

// ---------- branchements ----------

(function bindTrk() {
  $("#trk-tmdb-go").addEventListener("click", trkSearch);
  $("#trk-tmdb-q").addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      trkSearch();
    }
  });
  for (const id of ["#trk-f-name", "#trk-f-cat", "#trk-f-type", "#trk-f-res", "#trk-f-tmdb", "#trk-f-imdb", "#trk-f-anon", "#trk-f-sd", "#trk-f-desc", "#trk-f-mi"]) {
    const n = $(id);
    const on = () => {
      trk.dirty[id] = true;
      window.trkBar();
    };
    n.addEventListener("input", on);
    n.addEventListener("change", on);
  }
  for (const id of ["#trk-f-stream", "#trk-f-personal", "#trk-f-nfo"]) $(id).addEventListener("change", window.trkBar);
  // Titre retouché : conformité et lien de seed recalculés.
  let titleTimer = null;
  $("#trk-f-name").addEventListener("input", () => {
    clearTimeout(titleTimer);
    titleTimer = setTimeout(window.trkFiche, 700);
  });
  $("#trk-sim").addEventListener("click", trkSimulate);
  $("#trk-shots-go").addEventListener("click", trkShotsMake);
  $("#trk-shots-host").addEventListener("click", trkShotsHost);
  $("#trk-dialog-no").addEventListener("click", () => $("#trk-dialog").close());
  $("#trk-dialog-yes").addEventListener("click", trkPublish);
  $("#trk-dialog-host").addEventListener("click", trkHostThenAsk);
})();
