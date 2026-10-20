"use strict";

// Page locale de D3X. Tout le texte venu des torrents passe par textContent :
// un nom de release ne doit jamais pouvoir injecter du HTML.

const $ = (s) => document.querySelector(s);
const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};

const TABS = [
  { id: "todo", label: "À ajouter", statuses: ["add", "queued", "checking", "failed"], selectable: true, empty: "Rien à ajouter : tout ce que V3X a en commun avec ton client y est déjà." },
  { id: "done", label: "Déjà en seed pour V3X", statuses: ["seeding", "in_client", "on_v3x"], empty: "Aucun torrent V3X dans ce client pour l'instant." },
  { id: "renamed", label: "Noms différents", statuses: ["renamed"], selectable: true, empty: "", note: "Mêmes fichiers que sur V3X, sous d'autres noms. D3X ajoute la release V3X à l'arrêt, donne à ses fichiers les noms de ta copie dans le client, puis vérifie : elle ne démarre qu'à 100 %. Rien n'est créé, déplacé ni renommé sur ton disque." },
  { id: "none", label: "Pas sur V3X", statuses: ["none"], selectable: true, empty: "", note: "Ces releases n'existent pas sur V3X : de bonnes candidates à un upload. « Publier » ouvre la fiche d'une release ; coches-en plusieurs pour les passer au crible du Lot." },
  { id: "other", label: "Autres", statuses: ["partial", "error", "removed"], empty: "" },
];

const STATUS = {
  add: ["À ajouter", "add"],
  queued: ["En file", ""],
  checking: ["Vérification…", "warn"],
  failed: ["Vérification incomplète", "bad"],
  seeding: ["Ajouté, en seed", "ok"],
  in_client: ["Déjà dans le client", "ok"],
  on_v3x: ["Seedé pour V3X", "ok"],
  renamed: ["Noms différents", "warn"],
  none: ["Pas sur V3X", ""],
  partial: ["Téléchargé en partie", ""],
  error: ["Illisible", "bad"],
  removed: ["Retiré du client", ""],
};

const PAGE = 400;
const UUID = /^[0-9a-f-]{36}$/i;

const ui = {
  state: null,
  rows: [],
  version: -1,
  tab: "todo",
  shown: PAGE,
  selected: new Set(),
  picked: new Set(), // « Pas sur V3X » : releases choisies pour Publier ou Lot
  reqs: {}, // demandes ouvertes de V3X par ligne « Pas sur V3X »
  reqVersion: -1,
  relays: {}, // relais ouverts sur V3X, par info hash
  relayPrime: 0,
  relVersion: -1,
  view: "cross",
};

// ---------- utilitaires ----------

function fmtSize(n) {
  if (!n || n < 1) return "0 octet";
  const u = ["octets", "Kio", "Mio", "Gio", "Tio", "Pio"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  const d = i === 0 ? 0 : v >= 100 ? 0 : v >= 10 ? 1 : 2;
  return v.toLocaleString("fr-FR", { maximumFractionDigits: d, minimumFractionDigits: 0 }) + " " + u[i];
}

function plural(n, one, many) { return n > 1 ? many : one; }

function toast(msg, kind) {
  const t = el("div", "toast" + (kind ? " " + kind : ""), msg);
  $("#toasts").append(t);
  setTimeout(() => t.remove(), kind === "bad" ? 9000 : 4500);
}

async function api(path, body) {
  const opt = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  const r = await fetch(path, opt);
  if (r.status === 401) {
    $("#session").classList.remove("hidden");
    throw new Error("session");
  }
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || "erreur " + r.status);
  return data;
}

// ---------- apparence (comme sur le site) ----------

const ACCENTS = [
  ["bleu", "Bleu", "#4d8dff"], ["rose", "Rose", "#ff5d8f"], ["vert", "Vert", "#3ddc84"], ["violet", "Violet", "#a78bfa"],
  ["cyan", "Cyan", "#26d0e8"], ["ambre", "Ambre", "#ffb02e"], ["braise", "Braise", "#ff5a1f"], ["lime", "Lime", "#b8e62e"],
];
let previewing = false; // réglages ouverts : l'aperçu prime sur l'état reçu

function applyAppearance(theme, accent) {
  const d = document.documentElement;
  d.setAttribute("data-theme", theme || "dark");
  d.setAttribute("data-accent", accent || "bleu");
  try {
    localStorage.setItem("d3x-theme", theme || "dark");
    localStorage.setItem("d3x-accent", accent || "bleu");
  } catch (e) { /* stockage indisponible */ }
}

// Apparence choisie dans les réglages, appliquée tout de suite à titre d'aperçu.
function previewAppearance() {
  const s = ui.state;
  if (!s) return;
  previewing = true;
  const theme = $("#f-theme").value;
  const accent = ($("#f-accent").querySelector('[aria-checked="true"]') || {}).dataset;
  const a = accent && accent.accent;
  // « Comme sur V3X » : l'apparence du profil, reçue avec l'état.
  applyAppearance(theme || s.appearance.profileTheme || "dark", a || s.appearance.profileAccent || "bleu");
}

function buildSwatches(selected) {
  const box = $("#f-accent");
  box.replaceChildren();
  const add = (value, label, color) => {
    const b = el("button", "swatch" + (value ? "" : " auto"));
    b.type = "button";
    b.setAttribute("role", "radio");
    b.setAttribute("aria-checked", String(value === selected));
    b.setAttribute("aria-label", label);
    b.title = label;
    b.dataset.accent = value;
    if (color) b.style.setProperty("--c", color);
    else b.textContent = "V3X";
    b.addEventListener("click", () => {
      for (const x of box.children) x.setAttribute("aria-checked", String(x === b));
      previewAppearance();
    });
    box.append(b);
  };
  add("", "Comme sur V3X", null);
  for (const [v, l, c] of ACCENTS) add(v, l, c);
}

// ---------- connexions ----------

function renderState(s) {
  ui.state = s;
  if (s.appearance && !previewing) applyAppearance(s.appearance.theme, s.appearance.accent);
  $("#ver").textContent = s.version === "dev" ? "dev" : "v" + s.version;

  const cv = $("#chip-v3x");
  cv.classList.toggle("ok", s.v3x.ok);
  cv.classList.toggle("bad", s.v3x.configured && !s.v3x.ok);
  cv.querySelector(".txt").textContent = s.v3x.ok ? "V3X · " + s.v3x.username : s.v3x.configured ? "V3X · erreur" : "V3X · à régler";
  cv.title = s.v3x.error || "";

  const cq = $("#chip-qbit");
  cq.classList.toggle("ok", s.qbit.ok);
  cq.classList.toggle("bad", s.qbit.configured && !s.qbit.ok);
  const kindName = KINDS[s.qbit.kind] ? KINDS[s.qbit.kind].name : "Client";
  cq.querySelector(".txt").textContent = s.qbit.ok ? s.qbit.version : s.qbit.configured ? kindName + " · erreur" : "Client torrent · à régler";
  cq.title = s.qbit.error || "";

  const cs = $("#chip-ssh");
  const ssh = s.ssh || {};
  cs.classList.toggle("hidden", !ssh.configured);
  cs.classList.toggle("ok", !!ssh.ok);
  cs.classList.toggle("bad", !!ssh.configured && !ssh.ok);
  cs.querySelector(".txt").textContent = ssh.ok ? "Seedbox · " + ssh.label : "Seedbox · " + (ssh.error ? "erreur" : "à connecter");
  cs.title = ssh.ok ? ssh.system || "" : ssh.error || "Mot de passe ou phrase de passe à redonner";
  if (window.pubSources) window.pubSources();
  if (window.lotSources) window.lotSources();
  if (window.libSources) window.libSources();

  // Mode serveur : les fichiers sont ceux de la machine qui fait tourner D3X.
  document.documentElement.dataset.mode = s.mode || "";
  $("#detect").classList.toggle("hidden", s.mode === "docker");

  const ready = s.v3x.ok && s.qbit.ok;
  $("#onboard").classList.toggle("hidden", s.v3x.configured || s.qbit.configured);
  $("#scan").disabled = !ready;
  $("#scan").title = ready ? "" : "Règle d'abord V3X et ton client torrent";

  const up = $("#update");
  if (s.update) {
    up.replaceChildren();
    up.classList.remove("bad");
    if (s.update.ready) {
      up.append(document.createTextNode("D3X " + s.update.version + " est prête, signature et empreinte vérifiées. "));
      const b = el("button", "btn small primary", "Redémarrer pour l'installer");
      b.type = "button";
      b.addEventListener("click", installUpdate);
      up.append(b);
    } else if (s.update.managed === "docker") {
      up.append(document.createTextNode("D3X " + s.update.version + " est sortie. Mets à jour l'image : docker compose pull, puis docker compose up -d. "));
    } else if (s.update.error) {
      up.classList.add("bad");
      up.append(document.createTextNode("D3X " + s.update.version + " est sortie, mais D3X ne l'installera pas : " + s.update.error + " "));
    } else {
      up.append(document.createTextNode("D3X " + s.update.version + " est sortie : téléchargement et vérification en cours… "));
    }
    if (!s.update.ready) {
      const a = el("a", null, "Voir la version");
      a.href = s.update.url;
      a.target = "_blank";
      a.rel = "noopener";
      up.append(a);
    }
    up.classList.remove("hidden");
  } else {
    up.classList.add("hidden");
  }
  renderWatch(s);
  if (lastVersion && lastVersion !== s.version) toast("D3X est passé en " + s.version + ".", "ok");
  lastVersion = s.version;
}

let lastVersion = "";

function hourOf(iso) {
  return new Date(iso).toLocaleTimeString("fr-FR", { hour: "2-digit", minute: "2-digit" }).replace(":", " h ");
}

// Ligne « Surveillance » sous le titre du cross-seed.
function renderWatch(s) {
  const w = s.watch || {};
  const line = $("#watch-line");
  line.classList.toggle("hidden", !w.on);
  if (!w.on) return;
  const parts = [];
  if (w.running) parts.push("Surveillance : analyse en cours");
  else parts.push("Surveillance active" + (w.next ? " : prochaine analyse vers " + hourOf(w.next) : ""));
  const l = w.last;
  line.classList.toggle("bad", !!(l && l.error));
  if (l && l.error) parts.push("la dernière a échoué (" + l.error + ")");
  else if (l && l.at) {
    const bits = [];
    bits.push(l.new ? l.new + " " + plural(l.new, "nouvelle", "nouvelles") : "rien de nouveau");
    if (l.added) bits.push(l.added + " " + plural(l.added, "ajoutée", "ajoutées"));
    if (l.danger) bits.push(l.danger + " hit and run en vue");
    parts.push("dernière à " + hourOf(l.at) + " : " + bits.join(", "));
  }
  $("#watch-txt").textContent = parts.join(". ") + ".";
}

function watchHint() {
  const s = ui.state || {};
  const w = s.watch || {};
  let t = "Seuls les torrents arrivés depuis la dernière fois sont relus : ton client n'est presque pas sollicité.";
  if (s.mode) t += " D3X tourne en continu sur ce serveur : la surveillance ne s'arrête pas.";
  else if (w.tray) t += " Fenêtre fermée, D3X reste dans la zone de notification (clic droit sur son icône ▸ Quitter pour l'arrêter).";
  else t += " Elle tourne tant que D3X est lancé.";
  $("#w-hint").textContent = t;
}

// Installe la mise à jour vérifiée : D3X se relance sur la même session, la
// page se reconnecte seule.
async function installUpdate() {
  try {
    await api("/ui/update/restart", {});
    toast("Installation de la mise à jour, D3X redémarre…", "ok");
    const want = ui.state && ui.state.update ? ui.state.update.version : "";
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 1000));
      try {
        const s = await api("/ui/state");
        if (s.version === want || s.version !== (ui.state && ui.state.version)) {
          renderState(s);
          return;
        }
      } catch (e) { /* redémarrage en cours */ }
    }
    toast("D3X ne répond plus : relance-le à la main.", "bad");
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

function fillSettings() {
  const s = ui.state;
  if (!s) return;
  $("#f-key").value = "";
  $("#f-key").placeholder = s.v3x.configured ? "Clé enregistrée (laisse vide pour la garder)" : "Colle ta clé ici";
  $("#f-kind").value = s.qbit.kind || "qbittorrent";
  kindHint();
  $("#f-url").value = s.qbit.url || "";
  $("#f-user").value = s.qbit.user || "";
  $("#f-pass").value = "";
  $("#f-pass").placeholder = s.qbit.hasPass ? "Enregistré (laisse vide pour le garder)" : "";
  $("#f-insecure").checked = !!s.qbit.insecure;
  $("#f-cat").value = s.category || "";
  $("#f-tag").value = s.tag || "";
  $("#f-updates").checked = !!s.checkUpdates;
  $("#f-theme").value = (s.appearance && s.appearance.uiTheme) || "";
  buildSwatches((s.appearance && s.appearance.uiAccent) || "");
  renderMaps(s.pathMap || []);
  $("#l-on").checked = !!(s.links && s.links.on);
  $("#l-dir").value = (s.links && s.links.dir) || "";
  const w = s.watch || {};
  $("#w-on").checked = !!w.on;
  $("#w-min").value = String([15, 30, 60, 180].includes(w.minutes) ? w.minutes : 30);
  $("#w-notify").checked = !!w.notify;
  $("#w-auto").checked = !!w.autoAdd;
  $("#w-start").checked = !!w.autostart;
  $("#w-start-row").classList.toggle("hidden", !w.canAutostart);
  $("#w-notify-row").classList.toggle("hidden", !!s.mode);
  watchHint();
  setStatus("#v3x-status", s.v3x.configured, s.v3x.ok, s.v3x.ok ? "Connecté en tant que " + s.v3x.username : s.v3x.error);
  setStatus("#qbit-status", s.qbit.configured, s.qbit.ok, s.qbit.ok ? "Connecté à " + s.qbit.version : s.qbit.error);
  fillSSH();
}

// Dossiers du client : une ligne « ici → dans le client » par dossier.
function mapRow(rule) {
  const row = el("div", "map-row");
  const from = el("input");
  from.type = "text";
  from.spellcheck = false;
  from.autocomplete = "off";
  from.placeholder = ui.state && ui.state.mode ? "/media/films (ici)" : "Z:\\Films (ce PC)";
  from.setAttribute("aria-label", "Dossier sur cette machine");
  from.value = rule.from || "";
  from.dataset.side = "from";
  const to = el("input");
  to.type = "text";
  to.spellcheck = false;
  to.autocomplete = "off";
  to.placeholder = "/data/films (le client)";
  to.setAttribute("aria-label", "Le même dossier vu par le client");
  to.value = rule.to || "";
  to.dataset.side = "to";
  const del = el("button", "btn small", "Retirer");
  del.type = "button";
  del.setAttribute("aria-label", "Retirer cette ligne");
  del.addEventListener("click", () => row.remove());
  row.append(from, el("span", "arrow", "→"), to, del);
  return row;
}

function renderMaps(rules) {
  const box = $("#f-maps");
  box.replaceChildren(...rules.map(mapRow));
}

function collectMaps() {
  return [...$("#f-maps").children].map((r) => ({
    from: r.querySelector('[data-side="from"]').value.trim(),
    to: r.querySelector('[data-side="to"]').value.trim(),
  })).filter((r) => r.from || r.to);
}

// ---------- seedbox (SSH) ----------

function fillSSH() {
  const x = (ui.state && ui.state.ssh) || {};
  $("#s-host").value = x.host || "";
  $("#s-port").value = x.port || "";
  $("#s-user").value = x.user || "";
  $("#s-key").value = x.keyPath || "";
  $("#s-secret").value = "";
  $("#s-forget").classList.toggle("hidden", !x.configured);
  $("#s-hostkey").classList.add("hidden");
  setStatus("#ssh-status", x.configured, x.ok, x.ok ? "Connecté à " + x.label + (x.system ? " (" + x.system + ")" : "") : x.error || "Pas connectée : redonne le mot de passe (ou la phrase de passe de ta clé), puis « Se connecter ».");
}

// Réponse du serveur : état complet (connecté) ou clé d'hôte à vérifier.
function sshResult(r) {
  if (r && r.hostKey) {
    const box = $("#s-hostkey");
    box.classList.toggle("bad", !!r.hostKey.changed);
    $("#s-hostkey-txt").textContent = r.hostKey.changed
      ? "La clé de la seedbox a CHANGÉ depuis ta dernière connexion. Ça arrive après une réinstallation, mais c'est aussi le signe d'une interception. Vérifie auprès de ton hébergeur avant d'accepter :"
      : "Première connexion à cette seedbox. Vérifie que cette empreinte est bien celle de ta seedbox (ton hébergeur la donne, ou « ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub » sur la seedbox) :";
    $("#s-hostkey-fp").textContent = r.hostKey.fingerprint;
    box.dataset.fp = r.hostKey.fingerprint;
    box.classList.remove("hidden");
    return;
  }
  renderState(r);
  fillSSH();
  if (r.ssh && r.ssh.ok) toast("Seedbox connectée : " + r.ssh.label + ".", "ok");
}

async function sshCall(path, body, btn) {
  const label = btn.textContent;
  btn.disabled = true;
  btn.textContent = "Connexion…";
  try {
    sshResult(await api(path, body));
  } catch (e) {
    if (e.message !== "session") setStatus("#ssh-status", true, false, e.message);
  } finally {
    btn.disabled = false;
    btn.textContent = label;
  }
}

function bindSSH() {
  $("#s-connect").addEventListener("click", () => sshCall("/ui/ssh/test", {
    host: $("#s-host").value,
    port: Number($("#s-port").value) || 0,
    user: $("#s-user").value,
    keyPath: $("#s-key").value,
    secret: $("#s-secret").value,
  }, $("#s-connect")));
  $("#s-secret").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); $("#s-connect").click(); } });
  $("#s-accept").addEventListener("click", () => sshCall("/ui/ssh/accept", { fingerprint: $("#s-hostkey").dataset.fp || "" }, $("#s-accept")));
  $("#s-reject").addEventListener("click", () => {
    $("#s-hostkey").classList.add("hidden");
    setStatus("#ssh-status", true, false, "Connexion abandonnée : empreinte refusée.");
  });
  $("#s-forget").addEventListener("click", async () => {
    try { renderState(await api("/ui/ssh/forget", {})); fillSSH(); toast("Seedbox oubliée.", "ok"); } catch (e) { if (e.message !== "session") toast(e.message, "bad"); }
  });
}

// Nom des fichiers locaux : ce PC, ou la machine de D3X en mode serveur.
function localLabel() {
  return ui.state && ui.state.mode ? "Ce serveur" : "Ce PC";
}

// Sélecteur « Ce PC / Seedbox » d'une vue. Caché tant qu'aucune seedbox
// n'est réglée ; une seedbox pas connectée ouvre les réglages.
function renderSourceSwitch(box, current, onPick) {
  const x = (ui.state && ui.state.ssh) || {};
  box.replaceChildren();
  if (!x.configured) return false;
  const mk = (id, parts) => {
    const b = el("button");
    b.type = "button";
    b.setAttribute("role", "radio");
    b.setAttribute("aria-checked", String(current === id));
    b.append(...parts);
    b.addEventListener("click", () => {
      if (id === "ssh" && !x.ok) {
        openSettings();
        $("#s-secret").focus();
        toast("Connecte d'abord la seedbox (mot de passe ou phrase de passe de ta clé).", "bad");
        return;
      }
      if (id !== current) onPick(id);
    });
    return b;
  };
  const dot = el("span", "dot " + (x.ok ? "ok" : "bad"));
  dot.setAttribute("aria-hidden", "true");
  box.append(mk("local", [document.createTextNode(localLabel())]), mk("ssh", [dot, el("span", "host", x.label)]));
  return true;
}

function setStatus(sel, configured, ok, text) {
  const n = $(sel);
  n.className = "status" + (configured ? (ok ? " ok" : " bad") : "");
  n.textContent = configured ? text || "" : "";
}

function openSettings() {
  fillSettings();
  $("#settings").showModal();
}

async function saveSettings(ev) {
  ev.preventDefault();
  const btn = $("#settings-save");
  btn.disabled = true;
  btn.textContent = "Test en cours…";
  try {
    const s = await api("/ui/settings", {
      v3xKey: $("#f-key").value,
      clientKind: $("#f-kind").value,
      qbitUrl: $("#f-url").value,
      qbitUser: $("#f-user").value,
      qbitPass: $("#f-pass").value,
      qbitInsecure: $("#f-insecure").checked,
      category: $("#f-cat").value,
      tag: $("#f-tag").value,
      checkUpdates: $("#f-updates").checked,
      uiTheme: $("#f-theme").value,
      uiAccent: (($("#f-accent").querySelector('[aria-checked="true"]') || {}).dataset || {}).accent || "",
      pathMap: collectMaps(),
      links: { on: $("#l-on").checked, dir: $("#l-dir").value.trim() },
      watch: {
        on: $("#w-on").checked,
        minutes: Number($("#w-min").value) || 30,
        notify: $("#w-notify").checked,
        autoAdd: $("#w-auto").checked,
        autostart: $("#w-start-row").classList.contains("hidden") ? null : $("#w-start").checked,
      },
    });
    previewing = false;
    renderState(s);
    fillSettings();
    if (s.v3x.ok && s.qbit.ok) {
      toast("Réglages enregistrés, les deux connexions marchent.", "ok");
      $("#settings").close();
    }
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  } finally {
    btn.disabled = false;
    btn.textContent = "Enregistrer et tester";
  }
}

// Aide propre à chaque client.
const KINDS = {
  qbittorrent: { name: "qBittorrent", url: "http://127.0.0.1:8080", user: true, help: "Outils ▸ Options ▸ Interface Web : coche « Interface Web », note le port, l'utilisateur et le mot de passe. Version 4.5 ou plus." },
  transmission: { name: "Transmission", url: "http://127.0.0.1:9091", user: true, help: "Préférences ▸ Distant : autorise l'accès distant. Utilisateur et mot de passe seulement si tu les as activés." },
  deluge: { name: "Deluge", url: "http://127.0.0.1:8112", user: false, help: "L'interface web de Deluge (greffon WebUI) : seul son mot de passe est demandé, pas d'utilisateur." },
  rtorrent: { name: "rTorrent", url: "https://ta-seedbox/RPC2", user: true, help: "L'adresse XML-RPC de rTorrent (souvent /RPC2), avec l'identification de ta seedbox. ruTorrent n'est pas nécessaire." },
};

function kindHint() {
  const k = KINDS[$("#f-kind").value] || KINDS.qbittorrent;
  $("#f-url").placeholder = k.url;
  $("#client-hint").textContent = k.help + (ui.state && ui.state.mode === "docker" ? " Dans Docker, mets l'adresse du conteneur du client (http://qbittorrent:8080) ou celle de la machine, jamais 127.0.0.1." : "");
  $("#f-user").disabled = !k.user;
  if (!k.user) $("#f-user").value = "";
}

async function detectClients() {
  const ul = $("#detected");
  ul.replaceChildren(el("li", "muted", "Recherche sur ce PC…"));
  ul.classList.remove("hidden");
  try {
    const r = await api("/ui/client/detect");
    ul.replaceChildren();
    if (!r.found || !r.found.length) {
      ul.append(el("li", "muted", "Aucun client trouvé sur les ports habituels. Active son interface web, ou saisis son adresse à la main (NAS, seedbox)."));
      return;
    }
    for (const f of r.found) {
      const li = el("li");
      const b = el("button", "btn small", "Utiliser");
      b.type = "button";
      b.addEventListener("click", () => {
        $("#f-kind").value = f.kind;
        kindHint();
        $("#f-url").value = f.url;
        ul.classList.add("hidden");
      });
      li.append(el("span", null, f.name + " · " + f.url), b);
      ul.append(li);
    }
  } catch (e) {
    if (e.message !== "session") ul.replaceChildren(el("li", "bad", e.message));
  }
}

// ---------- analyse ----------

let lastStatus = null;

function renderStatus(st) {
  lastStatus = st;
  const sc = st.scan;
  const running = sc.phase === "list" || sc.phase === "read" || sc.phase === "match";
  $("#scan").classList.toggle("hidden", running || st.applying);
  $("#stop").classList.toggle("hidden", !running && !st.applying);
  $("#scan").textContent = sc.phase === "idle" ? "Analyser mon client" : "Analyser à nouveau";

  const prog = $("#progress");
  if (running || st.applying) {
    prog.classList.remove("hidden");
    let label = "";
    let pct = 0;
    if (st.applying) {
      label = "Ajout " + Math.min(st.batchDone + 1, st.batch) + " / " + st.batch + " : " + (st.current || "…");
      pct = st.batch ? (st.batchDone / st.batch) * 100 : 0;
    } else if (sc.phase === "list") {
      label = "Lecture de la liste de ton client…";
    } else if (sc.phase === "read") {
      label = "Lecture des .torrent : " + sc.done + " / " + sc.total;
      pct = sc.total ? (sc.done / sc.total) * 100 : 0;
    } else {
      label = "Comparaison avec le catalogue de V3X…";
      pct = sc.total ? (sc.done / sc.total) * 100 : 100;
    }
    $("#progress-label").textContent = label;
    $("#progress-detail").textContent = sc.detail || "";
    $("#progress-fill").style.width = Math.max(3, Math.min(100, pct)) + "%";
  } else {
    prog.classList.add("hidden");
  }

  const err = $("#scan-error");
  err.textContent = sc.phase === "error" ? sc.error : "";
  err.classList.toggle("hidden", sc.phase !== "error");

  if (st.version !== ui.version) loadRows();
}

async function loadRows() {
  try {
    const r = await api("/ui/rows");
    ui.version = r.version;
    ui.rows = r.rows || [];
    const keys = new Set(ui.rows.filter(canAdd).map((x) => x.key));
    for (const k of ui.selected) if (!keys.has(k)) ui.selected.delete(k);
    const picks = new Set(ui.rows.filter(canPick).map((x) => x.key));
    for (const k of ui.picked) if (!picks.has(k)) ui.picked.delete(k);
    renderRows();
    if (ui.reqVersion !== ui.version && ui.rows.some((x) => x.status === "none")) loadRequests();
    if (ui.relVersion !== ui.version && ui.rows.some((x) => x.status === "add")) loadRelays();
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

function tabOf(status) {
  return TABS.find((t) => t.statuses.includes(status)) || TABS[TABS.length - 1];
}

// Onglet d'une ligne : une release « noms différents » reste dans son onglet
// pendant son ajout et sa vérification.
function tabOfRow(r) {
  if (r.match && r.match.kind === "renamed" && ["queued", "checking", "failed"].includes(r.status)) return TABS.find((t) => t.id === "renamed");
  return tabOf(r.status);
}

// Ligne qu'on peut cocher pour l'ajouter.
function canAdd(r) {
  return r.status === "add" || (r.status === "renamed" && !!r.canAdd);
}

// Ligne « Pas sur V3X » qu'on peut choisir pour la publier.
function canPick(r) {
  return r.status === "none" && !!r.path;
}

function setOf(r) {
  return canPick(r) ? ui.picked : ui.selected;
}

function checkable(r) {
  return canAdd(r) || canPick(r);
}

// Demandes ouvertes sur V3X auxquelles répondent les releases « Pas sur V3X ».
async function loadRequests() {
  const v = ui.version;
  ui.reqVersion = v;
  try {
    const r = await api("/ui/requests/match");
    if (ui.version !== v) return;
    ui.reqs = r.byKey || {};
    renderRows();
  } catch (e) {
    ui.reqs = {};
  }
}

// Relais ouverts (releases rares lâchées, prime au repreneur).
async function loadRelays() {
  const v = ui.version;
  ui.relVersion = v;
  try {
    const r = await api("/ui/relays");
    if (ui.version !== v) return;
    ui.relays = r.byHash || {};
    ui.relayPrime = r.prime || 0;
    renderRows();
  } catch (e) {
    ui.relays = {};
  }
}

function relayOf(r) {
  return r.match && r.match.infoHash ? ui.relays[r.match.infoHash.toLowerCase()] : null;
}

// Réanimation : relais primé, ou release morte qui rapporte la prime de
// résurrection à qui la ramène.
function reanimBadges(r) {
  const out = [];
  const m = r.match;
  if (!m || !["add", "renamed"].includes(r.status)) return out;
  const rl = relayOf(r);
  if (rl) {
    const b = el("span", "req relay", "Relais" + (ui.relayPrime ? " · +" + ui.relayPrime.toLocaleString("fr-FR") + " jetons" : ""));
    const until = rl.until ? new Date(rl.until).toLocaleDateString("fr-FR", { day: "numeric", month: "long" }) : "";
    b.title = "Lâchée par un membre qui ne peut plus la seeder : la prime va aux premiers repreneurs, après 24 h de seed" + (until ? " (relais ouvert jusqu'au " + until + ")" : "") + ".";
    out.push(b);
  }
  if (m.dead) {
    const b = el("span", "req dead", "Morte sur V3X" + (m.prime ? " · +" + m.prime.toLocaleString("fr-FR") + " jetons" : ""));
    b.title = "Plus personne ne la seede depuis 3 jours : ton seed la ramène. La prime de résurrection tombe une fois, après 24 h de seed, dès qu'elle a servi à quelqu'un.";
    out.push(b);
  }
  return out;
}

// Badges « Demandée sur V3X » d'une release (lien vers la demande).
function requestBadges(hits) {
  const out = [];
  for (const h of (hits || []).slice(0, 3)) {
    if (!UUID.test(h.id)) continue;
    const a = el("a", "req" + (h.mine ? " mine" : ""), h.mine ? "Ta demande sur V3X" : "Demandée sur V3X" + (h.reward > 0 ? " · " + h.reward.toLocaleString("fr-FR") + " jetons" : ""));
    a.href = "https://v3x.club/requests/" + h.id;
    a.target = "_blank";
    a.rel = "noopener";
    a.title = h.title + (h.year && !/\(\d{4}\)$/.test(h.title) ? " (" + h.year + ")" : "") + (h.mine ? " : tu ne peux pas remplir ta propre demande" : " : la prime va à qui la remplit");
    out.push(a);
  }
  return out;
}

// Propositions faites pendant cette session (la carte est redessinée souvent).
const proposed = new Set();

// Bouton « Proposer pour la demande » (release publiée et validée).
function proposeButton(h, torrentId) {
  const done = () => { b.textContent = "Proposée pour « " + h.title + " »"; b.disabled = true; };
  const b = el("button", "btn small primary", "Proposer pour « " + h.title + " »" + (h.reward > 0 ? " · " + h.reward.toLocaleString("fr-FR") + " jetons" : ""));
  b.type = "button";
  b.title = "Le demandeur reçoit ta proposition et la valide sur V3X : la prime t'est versée à ce moment-là.";
  if (proposed.has(h.id + "|" + torrentId)) done();
  b.addEventListener("click", async () => {
    b.disabled = true;
    try {
      await api("/ui/requests/propose", { requestId: h.id, torrentId });
      proposed.add(h.id + "|" + torrentId);
      done();
      toast("Proposée : le demandeur la valide sur V3X.", "ok");
    } catch (e) {
      if (e.message === "already_proposed") {
        proposed.add(h.id + "|" + torrentId);
        done();
      } else b.disabled = false;
      const msg = { already_proposed: "déjà proposée pour cette demande", not_open: "la demande n'est plus ouverte", self_fill: "c'est ta propre demande", torrent_not_found: "la release doit d'abord être validée" }[e.message];
      if (e.message !== "session") toast("Pas proposée : " + (msg || e.message) + ".", "bad");
    }
  });
  return b;
}

// Où D3X voit les releases choisies (ce PC ou la seedbox).
async function localPaths(rows) {
  const r = await api("/ui/rows/local", { keys: rows.map((x) => x.key) });
  const ok = [];
  const bad = [];
  for (const row of rows) {
    const p = r[row.key];
    if (p && p.path) ok.push({ row, path: p.path, source: p.source });
    else bad.push({ row, error: (p && p.error) || "emplacement inconnu : relance l'analyse" });
  }
  return { ok, bad };
}

async function publishRow(r, btn) {
  if (btn) btn.disabled = true;
  try {
    const { ok, bad } = await localPaths([r]);
    if (bad.length) return toast(bad[0].error + ".", "bad");
    window.pubOpen(ok[0].path, ok[0].source);
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function lotPicked() {
  const rows = ui.rows.filter((r) => ui.picked.has(r.key));
  if (!rows.length) return;
  const btn = $("#pick-lot");
  btn.disabled = true;
  try {
    const { ok, bad } = await localPaths(rows);
    if (!ok.length) return toast(bad[0].error + ".", "bad");
    if (bad.length) toast(bad.length + " " + plural(bad.length, "release écartée", "releases écartées") + " : " + bad[0].error + ".", "bad");
    // Un lot lit un seul endroit : ce PC d'abord, la seedbox ensuite.
    const local = ok.filter((x) => x.source === "local");
    const use = local.length ? local : ok;
    if (use.length < ok.length) toast("Les releases de la seedbox passeront dans un second lot : celui-ci prend celles de " + localLabel().toLowerCase() + ".");
    const hits = {};
    for (const x of use) if (ui.reqs[x.row.key]) hits[x.path] = ui.reqs[x.row.key];
    window.lotFromPaths(use.map((x) => x.path), use[0].source, hits);
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  } finally {
    btn.disabled = false;
  }
}

function renderRows() {
  const counts = {};
  for (const r of ui.rows) {
    const t = tabOfRow(r).id;
    counts[t] = (counts[t] || 0) + 1;
  }
  const has = ui.rows.length > 0;
  $("#tabs").classList.toggle("hidden", !has);
  $("#table-wrap").classList.toggle("hidden", !has);
  const tabs = $("#tabs");
  tabs.replaceChildren();
  for (const t of TABS) {
    if (t.id !== "todo" && !counts[t.id]) continue;
    const b = el("button", "tab");
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", String(t.id === ui.tab));
    b.append(el("span", null, t.label), el("span", "n", String(counts[t.id] || 0)));
    b.addEventListener("click", () => {
      ui.tab = t.id;
      ui.shown = PAGE;
      renderRows();
    });
    tabs.append(b);
  }
  const tab = TABS.find((t) => t.id === ui.tab) || TABS[0];
  let list = ui.rows.filter((r) => tabOfRow(r).id === tab.id);
  // Pas sur V3X : celles qu'un membre demande d'abord, les mieux primées en tête.
  if (tab.id === "none") {
    const best = (r) => Math.max(-1, ...(ui.reqs[r.key] || []).filter((h) => !h.mine).map((h) => h.reward || 0));
    list = list.map((r, i) => [r, best(r), i]).sort((a, b) => b[1] - a[1] || a[2] - b[2]).map((x) => x[0]);
  }
  // À ajouter : les relais primés, puis les releases mortes, avant le reste.
  if (tab.id === "todo") {
    const rank = (r) => (relayOf(r) ? 0 : r.match && r.match.dead ? 1 : 2);
    list = list.map((r, i) => [r, rank(r), i]).sort((a, b) => a[1] - b[1] || a[2] - b[2]).map((x) => x[0]);
  }
  const tbody = $("#tbody");
  tbody.replaceChildren();
  for (const r of list.slice(0, ui.shown)) tbody.append(rowEl(r));

  const empty = $("#empty");
  let note = tab.note && list.length ? tab.note : "";
  if (tab.id === "none") {
    const asked = list.filter((r) => (ui.reqs[r.key] || []).some((h) => !h.mine)).length;
    if (asked) note = asked + " " + plural(asked, "répond", "répondent") + " à une demande ouverte sur V3X : en tête de liste, avec la prime. " + note;
  }
  if (tab.id === "todo") {
    const relays = list.filter((r) => relayOf(r)).length;
    const dead = list.filter((r) => !relayOf(r) && r.match && r.match.dead).length;
    const parts = [];
    if (relays) parts.push(relays + " " + plural(relays, "relais primé", "relais primés"));
    if (dead) parts.push(dead + " " + plural(dead, "release morte", "releases mortes") + " sur V3X");
    if (parts.length) note = parts.join(" et ") + " : en tête de liste, ton seed leur redonne vie et rapporte une prime.";
  }
  empty.textContent = list.length ? note : tab.empty;
  empty.classList.toggle("hidden", !empty.textContent);
  const more = $("#more");
  if (list.length > ui.shown) {
    more.replaceChildren(document.createTextNode(list.length - ui.shown + " de plus. "));
    const b = el("button", "btn small", "Afficher la suite");
    b.addEventListener("click", () => { ui.shown += PAGE; renderRows(); });
    more.append(b);
    more.classList.remove("hidden");
  } else {
    more.classList.add("hidden");
  }
  const addable = list.filter(checkable);
  const all = $("#check-all");
  all.disabled = !tab.selectable || addable.length === 0;
  all.checked = addable.length > 0 && addable.every((r) => setOf(r).has(r.key));
  renderSelection();
}

function rowEl(r) {
  const tr = el("tr");
  const m = r.match;
  const tdC = el("td", "c-check");
  if (checkable(r)) {
    const cb = el("input");
    cb.type = "checkbox";
    cb.checked = setOf(r).has(r.key);
    cb.setAttribute("aria-label", "Cocher " + r.name);
    cb.addEventListener("click", (ev) => { ev.stopPropagation(); toggle(r.key, cb.checked); });
    tdC.append(cb);
    tr.classList.add("selectable");
    tr.addEventListener("click", (ev) => {
      if (ev.target.closest("a,button")) return;
      cb.checked = !cb.checked;
      toggle(r.key, cb.checked);
    });
  }
  tr.append(tdC);

  const tdN = el("td");
  const name = el("div", "name");
  if (m && UUID.test(m.id)) {
    const a = el("a", null, m.name);
    a.href = "https://v3x.club/torrents/" + m.id;
    a.target = "_blank";
    a.rel = "noopener";
    name.append(a);
  } else {
    name.textContent = r.name;
  }
  if (r.copies > 1) {
    const c = el("span", "copies", "×" + r.copies);
    c.title = r.copies + " copies identiques dans ton client : une seule suffit pour seeder sur V3X";
    name.append(c);
  }
  tdN.append(name);
  if (m && m.name !== r.name) tdN.append(el("div", "sub", "Dans ton client : " + r.name));
  if (r.detail) tdN.append(el("div", "sub", r.detail));
  const rb = reanimBadges(r);
  if (rb.length) {
    const box = el("div", "reqs");
    box.append(...rb);
    tdN.append(box);
  }
  if (r.status === "none" && ui.reqs[r.key]) {
    const reqs = el("div", "reqs");
    reqs.append(...requestBadges(ui.reqs[r.key]));
    if (reqs.children.length) tdN.append(reqs);
  }
  tr.append(tdN);

  tr.append(el("td", "num", fmtSize(m ? m.size : r.size)));

  const tdS = el("td", "num seeds");
  if (m) {
    const b = el("b", m.seeders <= 1 ? "rare" : null, String(m.seeders));
    b.title = "sources";
    tdS.append(b, document.createTextNode(" / " + m.leechers));
    if (m.seeders <= 1 && r.status === "add") tdS.title = "Release presque sans source : ton seed compte double";
  } else {
    tdS.textContent = "";
  }
  tr.append(tdS);

  const tdE = el("td");
  const box = el("div", "state");
  const [label, kind] = r.status === "renamed" && r.canAdd ? ["À ajouter sous tes noms", "add"] : STATUS[r.status] || [r.status, ""];
  box.append(el("span", "pill " + kind, label));
  if (r.status === "checking" && r.progress > 0) {
    const bar = el("div", "mini");
    const i = el("i");
    i.style.width = Math.round(r.progress * 100) + "%";
    bar.append(i);
    bar.title = Math.round(r.progress * 100) + " %";
    box.append(bar);
  }
  if (canPick(r)) {
    const b = el("button", "btn small", "Publier");
    b.type = "button";
    b.title = "Ouvre la fiche de cette release dans Publier : le .torrent est fabriqué sur ses fichiers, puis elle part en seed.";
    b.addEventListener("click", () => publishRow(r, b));
    box.append(b);
  }
  if (r.status === "failed") {
    const b = el("button", "btn small", "Retirer du client");
    b.title = "Retire ce torrent de ton client. Tes fichiers ne sont pas touchés.";
    b.addEventListener("click", () => removeRow(r, b));
    box.append(b);
  }
  tdE.append(box);
  tr.append(tdE);
  return tr;
}

function toggle(key, on) {
  const row = ui.rows.find((r) => r.key === key);
  const set = row ? setOf(row) : ui.selected;
  if (on) set.add(key);
  else set.delete(key);
  renderSelection();
  const tab = TABS.find((t) => t.id === ui.tab);
  const addable = ui.rows.filter((r) => tabOfRow(r).id === tab.id && checkable(r));
  $("#check-all").checked = addable.length > 0 && addable.every((r) => setOf(r).has(r.key));
}

function renderSelection() {
  const sel = ui.rows.filter((r) => ui.selected.has(r.key));
  const n = sel.length;
  const size = sel.reduce((a, r) => a + (r.match ? r.match.size : r.size), 0);
  $("#sel-count").textContent = String(n);
  $("#sel-label").textContent = plural(n, "release cochée", "releases cochées");
  $("#sel-size").textContent = fmtSize(size);
  const none = ui.tab === "none";
  $("#actionbar").classList.toggle("hidden", n === 0 || ui.view !== "cross" || none);
  const picked = ui.rows.filter((r) => ui.picked.has(r.key));
  const p = picked.length;
  $("#pick-count").textContent = String(p);
  $("#pick-label").textContent = plural(p, "release choisie", "releases choisies");
  $("#pick-size").textContent = fmtSize(picked.reduce((a, r) => a + r.size, 0));
  $("#pick-publish").disabled = p !== 1;
  $("#pick-publish").title = p === 1 ? "" : "Publier ouvre une release à la fois : le Lot en prend plusieurs";
  $("#pick-bar").classList.toggle("hidden", p === 0 || ui.view !== "cross" || !none);
}

async function applySelected() {
  const keys = [...ui.selected];
  if (!keys.length) return;
  const btn = $("#apply");
  btn.disabled = true;
  try {
    const r = await api("/ui/apply", { keys });
    ui.selected.clear();
    toast(r.queued + " " + plural(r.queued, "release mise", "releases mises") + " en file. Ton client les vérifie une par une.", "ok");
    poll();
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  } finally {
    btn.disabled = false;
  }
}

async function removeRow(r, btn) {
  btn.disabled = true;
  try {
    await api("/ui/remove", { key: r.key });
    toast("Retiré de ton client, fichiers intacts.", "ok");
    poll();
  } catch (e) {
    btn.disabled = false;
    if (e.message !== "session") toast(e.message, "bad");
  }
}

// ---------- historique ----------

const KIND = { crossseed: "En seed", crossseed_failed: "Laissé à l'arrêt", removed: "Retiré" };

async function loadHistory() {
  const ul = $("#history");
  try {
    const r = await api("/ui/history");
    ul.replaceChildren();
    if (!r.events || !r.events.length) {
      ul.append(el("li", "empty", "Rien pour l'instant."));
      return;
    }
    for (const e of r.events) {
      const li = el("li");
      const t = el("time", null, new Date(e.at).toLocaleString("fr-FR", { dateStyle: "short", timeStyle: "short" }));
      t.dateTime = e.at;
      li.append(t, el("span", "pill " + (e.kind === "crossseed" ? "ok" : e.kind === "crossseed_failed" ? "bad" : ""), KIND[e.kind] || e.kind));
      const w = el("div", "what");
      if (e.id && UUID.test(e.id)) {
        const a = el("a", null, e.name);
        a.href = "https://v3x.club/torrents/" + e.id;
        a.target = "_blank";
        a.rel = "noopener";
        w.append(a);
      } else {
        w.textContent = e.name;
      }
      if (e.detail) w.append(el("div", "sub", e.detail));
      li.append(w);
      ul.append(li);
    }
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

// ---------- vues et boucle ----------

function setView(v) {
  ui.view = v;
  for (const b of document.querySelectorAll(".view")) {
    if (b.dataset.view === v) b.setAttribute("aria-current", "page");
    else b.removeAttribute("aria-current");
  }
  $("#view-cross").classList.toggle("hidden", v !== "cross");
  $("#view-history").classList.toggle("hidden", v !== "history");
  $("#view-publish").classList.toggle("hidden", v !== "publish");
  $("#view-batch").classList.toggle("hidden", v !== "batch");
  $("#view-seed").classList.toggle("hidden", v !== "seed");
  $("#view-library").classList.toggle("hidden", v !== "library");
  if (v === "history") loadHistory();
  if (window.pubShow) window.pubShow(v === "publish");
  if (window.batchShow) window.batchShow(v === "batch");
  if (window.seedShow) window.seedShow(v === "seed");
  if (window.libShow) window.libShow(v === "library");
  renderSelection();
}

let timer = null;
async function poll() {
  clearTimeout(timer);
  let busy = false;
  try {
    const st = await api("/ui/status");
    renderStatus(st);
    const sc = st.scan.phase;
    busy = sc === "list" || sc === "read" || sc === "match" || st.applying || (st.counts.checking || 0) > 0;
  } catch (e) {
    if (e.message === "session") return;
  }
  timer = setTimeout(poll, busy ? 1000 : 5000);
}

// Présence : un signal toutes les 20 s, et un au revoir à la fermeture. Sous
// Windows, D3X s'arrête quand sa fenêtre est fermée (sauf travail en cours).
function presence() {
  setInterval(() => { fetch("/ui/ping").catch(() => {}); }, 20000);
  window.addEventListener("pagehide", () => {
    try { navigator.sendBeacon("/ui/bye", new Blob(["{}"], { type: "application/json" })); } catch (e) { /* rien */ }
  });
}

async function boot() {
  presence();
  for (const b of document.querySelectorAll("[data-open-settings]")) b.addEventListener("click", openSettings);
  for (const b of document.querySelectorAll(".view")) b.addEventListener("click", () => setView(b.dataset.view));
  $("#settings-form").addEventListener("submit", saveSettings);
  bindSSH();
  $("#f-kind").addEventListener("change", kindHint);
  $("#detect").addEventListener("click", detectClients);
  $("#f-map-add").addEventListener("click", () => {
    const row = mapRow({});
    $("#f-maps").append(row);
    row.querySelector("input").focus();
  });
  $("#settings-cancel").addEventListener("click", () => $("#settings").close());
  $("#f-theme").addEventListener("change", previewAppearance);
  // Fermé sans enregistrer : retour à l'apparence en vigueur.
  $("#settings").addEventListener("close", () => {
    if (previewing && ui.state && ui.state.appearance) {
      previewing = false;
      applyAppearance(ui.state.appearance.theme, ui.state.appearance.accent);
    }
  });
  $("#apply").addEventListener("click", applySelected);
  $("#pick-lot").addEventListener("click", lotPicked);
  $("#pick-publish").addEventListener("click", () => {
    const r = ui.rows.find((x) => ui.picked.has(x.key));
    if (r) publishRow(r, $("#pick-publish"));
  });
  $("#check-all").addEventListener("change", (ev) => {
    const tab = TABS.find((t) => t.id === ui.tab);
    for (const r of ui.rows) {
      if (tabOfRow(r).id !== tab.id || !checkable(r)) continue;
      if (ev.target.checked) setOf(r).add(r.key);
      else setOf(r).delete(r.key);
    }
    renderRows();
  });
  $("#scan").addEventListener("click", async () => {
    try {
      renderStatus(await api("/ui/scan", {}));
      poll();
    } catch (e) {
      if (e.message !== "session") toast(e.message, "bad");
    }
  });
  $("#stop").addEventListener("click", async () => {
    try { renderStatus(await api("/ui/stop", {})); } catch (e) { /* rien */ }
  });

  try {
    const s = await api("/ui/state");
    renderState(s);
    // Connexions vérifiées en tâche de fond au lancement : on relit l'état une fois.
    setTimeout(async () => { try { renderState(await api("/ui/state")); } catch (e) { /* rien */ } }, 2500);
    // L'état (dont la mise à jour) est relu toutes les 5 minutes.
    setInterval(async () => { try { renderState(await api("/ui/state")); } catch (e) { /* rien */ } }, 300000);
  } catch (e) {
    return;
  }
  poll();
}

boot();
