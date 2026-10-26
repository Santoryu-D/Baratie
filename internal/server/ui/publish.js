"use strict";

// Vue « Publier » : choix de la release, hachage, MediaInfo (lu dans la page)
// et client qui seedera. La fiche du tracker est dans trk.js.

const pub = {
  dir: "",
  job: null,
  mediainfo: "",
  miState: "idle", // idle, running, done, none, error
  localNfo: "", // .nfo de la release, lu sur le disque
  timer: null,
  shown: false,
};

// ---------- .nfo de la release ----------

// Les 128 caractères hauts de la page de code 437 (NFO à dessins ASCII).
const CP437 = "ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ ";

function decodeNfo(bytes) {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch (e) {
    let s = "";
    for (const b of bytes) s += b < 128 ? String.fromCharCode(b) : CP437[b - 128];
    return s;
  }
}

// Le .nfo de la release : celui de son dossier, de préférence au nom de la
// release ; 256 Kio au plus. "" s'il n'y en a pas.
async function localNfoOf(j) {
  const nfos = (j.files || []).map((f, i) => ({ f, i })).filter((x) => /\.nfo$/i.test(x.f.rel) && x.f.size > 0 && x.f.size <= 262144);
  if (!nfos.length) return "";
  const depth = (x) => x.f.rel.split("/").length;
  const top = Math.min(...nfos.map(depth));
  const want = String(j.name).toLowerCase().replace(/\.[a-z0-9]{2,4}$/, "");
  const named = (x) => (x.f.rel.toLowerCase().includes(want) ? 1 : 0);
  const pick = nfos.filter((x) => depth(x) === top).sort((a, b) => named(b) - named(a))[0];
  try {
    const r = await fetch("/ui/pub/" + j.id + "/file/" + pick.i + "?offset=0&size=" + pick.f.size);
    if (!r.ok) return "";
    return decodeNfo(new Uint8Array(await r.arrayBuffer())).replace(/\r\n/g, "\n").replace(/\s+$/, "");
  } catch (e) {
    return "";
  }
}

// ---------- sélecteur de dossiers ----------

async function browse(path) {
  try {
    const r = await api("/ui/fs?path=" + encodeURIComponent(path || ""));
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

async function choose(path) {
  try {
    const j = await api("/ui/pub/start", { path });
    resetJob();
    pub.job = j;
    $("#pub-pick").classList.add("hidden");
    $("#pub-job").classList.remove("hidden");
    renderJob();
    runMediaInfo();
    pub.localNfo = await localNfoOf(j);
    window.trkFiche();
    pollJob();
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

function resetJob() {
  pub.mediainfo = "";
  pub.miState = "idle";
  pub.localNfo = "";
  window.trkReset();
}

async function cancelJob() {
  if (pub.job && !pub.job.posted) {
    try { await api("/ui/pub/" + pub.job.id + "/cancel", {}); } catch (e) { /* rien */ }
  }
  pub.job = null;
  clearTimeout(pub.timer);
  $("#pub-pick").classList.remove("hidden");
  $("#pub-job").classList.add("hidden");
  $("#pub-bar").classList.add("hidden");
  window.trkReset();
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
  const seedBusy = j.posted && (!j.seed || j.seed.status === "checking") && !j.seedError;
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
  add("Pièces", j.pieceLength >= 1048576 ? j.pieceLength / 1048576 + " Mio" : j.pieceLength / 1024 + " Kio");
  if (j.skipped && j.skipped.length) add("Écartés", String(j.skipped.length));

  const dup = $("#job-dup");
  const same = (j.existing || []).find((v) => v.same);
  if (same && !j.posted) {
    dup.textContent = "Une release de ce nom est déjà sur " + trackerName() + " : " + same.name + ".";
    dup.classList.add("bad");
    dup.classList.remove("hidden");
  } else dup.classList.add("hidden");

  if (j.state === "error") setStep("#st-hash", "bad", j.error);
  else if (j.state === "hashing") setStep("#st-hash", "run", "Hachage : " + fmtSize(j.hashed) + " sur " + fmtSize(j.total));
  else setStep("#st-hash", "ok", j.fromCache ? "Repris du cache : ces fichiers ont déjà été hachés." : "Prêt.");
  $("#st-hash-bar").style.width = (j.total ? Math.round((j.hashed / j.total) * 100) : 0) + "%";
  $("#st-hash .mini").classList.toggle("hidden", j.state !== "hashing");

  const miText = { idle: "En attente.", running: "Lecture du fichier vidéo…", done: "Lu sur le plus gros fichier vidéo.", none: "Aucun fichier vidéo : pas de MediaInfo.", error: "Lecture impossible : colle-le à la main dans la fiche." }[pub.miState];
  setStep("#st-mi", { running: "run", done: "ok", error: "bad", none: "", idle: "" }[pub.miState], miText);

  pubClients(j);
  const multi = multiClients();
  const who = multi ? "« " + j.clientName + " »" : "Ton client";
  let mapText;
  if (j.posted && j.seedLink) mapText = who + " seede le lien « " + j.seedLink + " ». ";
  else if (ui.state && ui.state.seedDir) mapText = who + " seedera un lien physique de la release, au nom de la fiche, dans « " + ui.state.seedDir + " » (tes fichiers d'origine ne bougent pas). ";
  else if (j.mapGuess) mapText = who + " verra ce dossier comme « " + j.clientDir + " » (déduit de " + j.mapVotes + " torrent" + (j.mapVotes > 1 ? "s" : "") + " de " + (multi ? "ce client" : "ton client") + "). ";
  else mapText = who + " cherchera les fichiers dans « " + j.clientDir + " ». ";
  setStep("#st-map", j.mapGuess || j.mapVotes ? "ok" : "", mapText);
  if (!j.posted && !(ui.state && ui.state.seedDir)) {
    const fix = el("button", "link", "Corriger");
    fix.type = "button";
    fix.addEventListener("click", () => {
      $("#map-edit").classList.toggle("hidden");
      $("#map-dir").value = j.clientDir;
    });
    $("#st-map-txt").append(fix);
  }
  window.trkRenderJob(j);
  window.trkBar();
}

// Plusieurs clients : celui qui seedera la release, au choix jusqu'à l'envoi.
function pubClients(j) {
  const list = (ui.state && ui.state.clients) || [];
  const row = $("#pub-client-row");
  const show = list.length > 1 && !j.posted && j.state !== "publishing";
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

// ---------- MediaInfo (dans la page) ----------

let miFactory = null;

// MediaInfo du plus gros fichier vidéo, lu par morceaux. "" sans vidéo.
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
  window.trkFiche();
}

// ---------- entrées ----------

// Ouvre une release dans Publier (depuis le Garde-manger).
window.pubOpen = async (path) => {
  if (pub.job && !pub.job.posted) {
    setView("publish");
    return toast("Une fiche est déjà ouverte dans Publier : publie-la ou « Changer de release » d'abord.", "bad");
  }
  if (pub.job) await cancelJob();
  setView("publish");
  await choose(path);
};

window.pubShow = async (on) => {
  pub.shown = on;
  if (window.trkBar) window.trkBar();
  if (!on) return;
  if (!pub.job && !$("#fs-list").children.length) browse((ui.state && ui.state.lastDir) || "");
};

(function bindPublish() {
  $("#fs-up").addEventListener("click", () => browse($("#fs-up").dataset.parent || ""));
  $("#fs-go").addEventListener("click", () => browse($("#fs-path").value));
  $("#fs-path").addEventListener("keydown", (e) => { if (e.key === "Enter") browse($("#fs-path").value); });
  $("#job-cancel").addEventListener("click", cancelJob);
  $("#map-save").addEventListener("click", saveMap);
  $("#pub-client").addEventListener("change", pickClient);
  $("#pub-go").addEventListener("click", () => window.trkAsk());
})();
