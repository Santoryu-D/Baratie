"use strict";

// Page de Baratie. Tout le texte venu d'une release, de TMDB ou du tracker
// passe par textContent : un nom de release ne doit jamais pouvoir injecter
// du HTML.

const $ = (s) => document.querySelector(s);
const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};

const ui = {
  state: null,
  view: "publish",
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
  const data = await r.json().catch(() => ({}));
  if (r.status === 401 && data.error === "session") {
    $("#session").textContent = data.remote
      ? "Ouvre Baratie avec son lien d'accès, qui finit par /auth?t=… : il est écrit dans son journal (docker logs baratie 2>&1 | grep auth). Le lien reste le même d'un redémarrage à l'autre."
      : "La session a expiré : rouvre Baratie avec le lien écrit dans son terminal.";
    $("#session").classList.remove("hidden");
    throw new Error("session");
  }
  if (!r.ok) throw new Error(data.error || "erreur " + r.status);
  return data;
}

function applyTheme(theme) {
  const t = theme === "dark" ? "dark" : "light";
  document.documentElement.setAttribute("data-theme", t);
  try { localStorage.setItem("baratie-theme", t); } catch (e) { /* stockage indisponible */ }
}

// ---------- état ----------

function trackerName() {
  return (ui.state && ui.state.tracker && ui.state.tracker.name) || "le tracker";
}

function renderState(s) {
  ui.state = s;
  applyTheme(s.theme);
  $("#ver").textContent = s.version === "dev" ? "dev" : "v" + s.version;

  const ct = $("#chip-trk");
  const tk = s.tracker || {};
  ct.classList.toggle("ok", !!tk.ok);
  ct.classList.toggle("bad", !!tk.configured && !tk.ok);
  ct.querySelector(".txt").textContent = tk.ok ? tk.name + " · " + tk.username : tk.configured ? tk.name + " · erreur" : "Tracker · à régler";
  ct.title = tk.error || (tk.group ? "Groupe : " + tk.group : "");
  for (const n of document.querySelectorAll(".trk-n")) n.textContent = trackerName();

  renderClientChips(s.clients || []);
  document.documentElement.dataset.mode = s.mode || "";
  $("#onboard").classList.toggle("hidden", !!tk.configured || (s.clients || []).length > 0);
  if (window.trkScope) window.trkScope();
}

// ---------- clients torrent ----------

const KINDS = {
  qbittorrent: { name: "qBittorrent", url: "http://127.0.0.1:8080", user: true, help: "Outils ▸ Options ▸ Interface Web : coche « Interface Web », note le port, l'utilisateur et le mot de passe. Version 4.5 ou plus." },
  transmission: { name: "Transmission", url: "http://127.0.0.1:9091", user: true, help: "Préférences ▸ Distant : autorise l'accès distant. Utilisateur et mot de passe seulement si tu les as activés." },
  deluge: { name: "Deluge", url: "http://127.0.0.1:8112", user: false, help: "L'interface web de Deluge (greffon WebUI) : seul son mot de passe est demandé, pas d'utilisateur." },
  rtorrent: { name: "rTorrent", label: "rTorrent / ruTorrent", url: "https://ta-seedbox/RPC2", user: true, help: "L'adresse XML-RPC de rTorrent (souvent /RPC2), avec l'identification de ta seedbox." },
};

function kindName(kind) {
  return (KINDS[kind] || KINDS.qbittorrent).name;
}

// Plusieurs clients réglés : on les nomme.
function multiClients() {
  return ((ui.state && ui.state.clients) || []).length > 1;
}

// Pastilles de la barre du haut : une par client.
function renderClientChips(list) {
  const box = $("#chips-clients");
  const chip = (cls, text, title) => {
    const b = el("button", "chip" + (cls ? " " + cls : ""));
    b.type = "button";
    b.append(el("span", "dot"), el("span", "txt", text));
    b.title = title || "";
    b.addEventListener("click", openSettings);
    return b;
  };
  const cls = (c) => (c.ok ? "ok" : c.pending ? "" : "bad");
  box.replaceChildren();
  if (!list.length) box.append(chip("", "Client torrent · à régler", ""));
  else if (list.length === 1) {
    const c = list[0];
    box.append(chip(cls(c), c.ok ? c.version : kindName(c.kind) + (c.pending ? " · connexion…" : " · erreur"), c.error || ""));
  } else {
    for (const c of list) box.append(chip(cls(c), c.label + (c.ok ? "" : c.pending ? " · connexion…" : " · injoignable"), c.label + " : " + (c.ok ? c.version : c.error || "connexion en cours")));
  }
}

// Les clients en cours de réglage (rien n'est enregistré avant « Enregistrer
// et tester ») ; open : ceux dont le formulaire est déplié.
const edit = { clients: [], open: new Set(), seq: 0 };
const MAX_CLIENTS = 8;

function newClient(kind, url) {
  return { id: "nouveau-" + ++edit.seq, isNew: true, name: "", kind: kind || "qbittorrent", url: url || "", user: "", pass: "", insecure: false, pathMap: [], hasPass: false, test: null };
}

function editName(c) {
  return (c.name || "").trim() || kindName(c.kind);
}

function editState(c) {
  if (c.test) return c.test.ok ? ["ok", "Connecté à " + c.test.version] : ["bad", c.test.error + testHint(c.test.error)];
  if (c.isNew) return ["", ""];
  if (c.pending) return ["", "Connexion en cours…"];
  return c.ok ? ["ok", "Connecté à " + c.version] : ["bad", (c.error || "injoignable") + testHint(c.error)];
}

// Que vérifier quand le test échoue faute de réponse.
function testHint(err) {
  if (!/pas de réponse|connexion impossible/.test(err || "")) return "";
  if (ui.state && ui.state.mode) {
    return ". Baratie tourne sur un serveur (Docker, NAS) : l'adresse doit être joignable depuis lui (le nom du conteneur du client, ou l'adresse de la machine), jamais 127.0.0.1 s'ils sont dans des conteneurs différents.";
  }
  return ". Ouvre cette adresse dans un navigateur sur la machine où tourne Baratie : si l'interface du client ne s'affiche pas, le souci est entre les deux (adresse, port, pare-feu).";
}

function input(id, value, placeholder, onInput, type) {
  const n = el("input");
  n.type = type || "text";
  if (id) n.id = id;
  n.autocomplete = "off";
  n.spellcheck = false;
  n.value = value || "";
  if (placeholder) n.placeholder = placeholder;
  n.addEventListener("input", () => onInput(n.value));
  return n;
}

function labelFor(id, text) {
  const l = el("label", null, text);
  l.htmlFor = id;
  return l;
}

function renderClients() {
  const single = edit.clients.length <= 1;
  $("#clients-legend").textContent = single ? "Client torrent" : "Clients torrent";
  $("#clients-hint").textContent = single
    ? "Le client qui seedera tes releases publiées. Plusieurs (une seedbox et un NAS) ? Ajoute l'autre : tu choisis le client à chaque publication."
    : "Baratie propose pour chaque publication le client qui voit déjà le dossier de la release ; tu peux en choisir un autre avant d'envoyer.";
  $("#clients").replaceChildren(...edit.clients.map((c, i) => clientCard(c, i, single)));
  clientAddButton();
}

function clientAddButton() {
  const b = $("#client-add");
  const single = edit.clients.length <= 1;
  b.textContent = single ? "Ajouter un autre client" : "Ajouter un client";
  b.disabled = edit.clients.length >= MAX_CLIENTS || (single && !(edit.clients[0] && edit.clients[0].url.trim()));
  b.title = edit.clients.length >= MAX_CLIENTS ? MAX_CLIENTS + " clients au plus" : "";
}

function clientCard(c, i, single) {
  const card = el("div", "client-card" + (single ? " single" : ""));
  const open = single || edit.open.has(c.id);
  if (!single) {
    const [cls, text] = editState(c);
    const head = el("div", "client-head");
    const dot = el("span", "dot " + cls);
    dot.setAttribute("aria-hidden", "true");
    const title = el("div", "client-title");
    const sub = cls === "ok" ? [c.test ? c.test.version : c.version, c.url] : [kindName(c.kind), c.url, text];
    title.append(el("b", null, editName(c)), el("span", "sub", sub.filter(Boolean).join(" · ")));
    title.title = text;
    const btn = (label, aria, fn) => {
      const b = el("button", "btn small", label);
      b.type = "button";
      if (aria) b.setAttribute("aria-label", aria);
      b.addEventListener("click", fn);
      return b;
    };
    head.append(dot, title);
    if (i > 0) head.append(btn("Monter", "Monter " + editName(c) + " dans la liste", () => {
      edit.clients.splice(i, 1);
      edit.clients.splice(i - 1, 0, c);
      renderClients();
    }));
    head.append(btn(open ? "Fermer" : "Modifier", null, () => {
      if (open) edit.open.delete(c.id);
      else edit.open.add(c.id);
      renderClients();
    }));
    head.append(btn("Retirer", "Retirer " + editName(c), () => {
      edit.clients = edit.clients.filter((x) => x !== c);
      if (!edit.clients.length) edit.clients.push(newClient());
      renderClients();
    }));
    card.append(head);
  }
  if (open) card.append(clientBody(c, i, single));
  return card;
}

function clientBody(c, i, single) {
  const body = el("div", "client-body");
  const id = (k) => "c" + i + "-" + k;
  if (!single) {
    body.append(labelFor(id("name"), "Nom"), input(id("name"), c.name, kindName(c.kind) + " (par exemple Seedbox, NAS)", (v) => { c.name = v; }));
  }
  const kind = el("select");
  kind.id = id("kind");
  for (const [k, v] of Object.entries(KINDS)) {
    const o = el("option", null, v.label || v.name);
    o.value = k;
    kind.append(o);
  }
  kind.value = c.kind;
  const hint = el("p", "hint");
  const url = input(id("url"), c.url, "", (v) => {
    c.url = v;
    c.test = null;
    clientAddButton();
  });
  const user = input(id("user"), c.user, "", (v) => { c.user = v; c.test = null; });
  const pass = input(id("pass"), "", c.hasPass ? "Enregistré (laisse vide pour le garder)" : "", (v) => { c.pass = v; c.test = null; }, "password");
  const kindHint = () => {
    const k = KINDS[c.kind] || KINDS.qbittorrent;
    url.placeholder = k.url;
    hint.textContent = k.help + (ui.state && ui.state.mode ? " Dans Docker, mets l'adresse du conteneur du client (http://qbittorrent:8080) ou celle de la machine." : "");
    user.disabled = !k.user;
    if (!k.user) { user.value = ""; c.user = ""; }
  };
  kind.addEventListener("change", () => {
    c.kind = kind.value;
    c.test = null;
    kindHint();
  });
  kindHint();
  const kindBox = el("div");
  kindBox.append(labelFor(id("kind"), "Client"), kind);
  body.append(kindBox, hint, labelFor(id("url"), "Adresse"), url);
  const two = el("div", "two");
  const u = el("div");
  u.append(labelFor(id("user"), "Utilisateur"), user);
  const pw = el("div");
  pw.append(labelFor(id("pass"), "Mot de passe"), pass);
  two.append(u, pw);
  const ins = el("label", "check");
  const cb = el("input");
  cb.type = "checkbox";
  cb.checked = !!c.insecure;
  cb.addEventListener("change", () => { c.insecure = cb.checked; });
  ins.append(cb, document.createTextNode(" Accepter un certificat non vérifié (seedbox en https auto-signé)"));
  body.append(two, ins);

  // Essai de connexion de ce client seul, sans rien enregistrer.
  const status = el("p", "status");
  const [cls, text] = editState(c);
  status.className = "status" + (cls ? " " + cls : "");
  status.textContent = text;
  const test = el("button", "btn small", "Tester la connexion");
  test.type = "button";
  test.addEventListener("click", async () => {
    if (!c.url.trim()) return toast("Indique l'adresse du client.", "bad");
    test.disabled = true;
    test.textContent = "Test en cours…";
    try {
      c.test = await api("/ui/client/test", clientPayload(c));
    } catch (e) {
      if (e.message === "session") return;
      c.test = { ok: false, error: e.message };
    }
    renderClients();
  });
  const actions = el("div", "row-actions");
  actions.append(test);
  body.append(actions, status);

  // Dossiers de ce client : une ligne « ici → dans le client » par dossier.
  body.append(el("p", "sublegend", "Dossiers du client"), el("p", "hint", "Si ce client ne voit pas les fichiers sous les mêmes chemins que Baratie (autre conteneur, autre machine), une ligne par dossier : à gauche son chemin pour Baratie, à droite le même dossier vu par le client. Inutile s'ils voient tous les deux les mêmes chemins."));
  const maps = el("div", "maps");
  const addRow = (rule) => maps.append(mapRow(c, rule));
  for (const r of c.pathMap) addRow(r);
  const add = el("button", "btn small", "Ajouter un dossier");
  add.type = "button";
  add.addEventListener("click", () => {
    const r = { from: "", to: "" };
    c.pathMap.push(r);
    addRow(r);
    maps.lastChild.querySelector("input").focus();
  });
  body.append(maps, add);
  return body;
}

function mapRow(c, rule) {
  const row = el("div", "map-row");
  const from = input("", rule.from, "/data/films (Baratie)", (v) => { rule.from = v; });
  from.setAttribute("aria-label", "Dossier vu par Baratie");
  const to = input("", rule.to, "/downloads/films (le client)", (v) => { rule.to = v; });
  to.setAttribute("aria-label", "Le même dossier vu par le client");
  const del = el("button", "btn small", "Retirer");
  del.type = "button";
  del.setAttribute("aria-label", "Retirer cette ligne");
  del.addEventListener("click", () => {
    c.pathMap = c.pathMap.filter((x) => x !== rule);
    row.remove();
  });
  row.append(from, el("span", "arrow", "→"), to, del);
  return row;
}

// Ce que la page envoie d'un client (identifiant vide : client neuf).
function clientPayload(c) {
  return {
    id: c.isNew ? "" : c.id,
    name: (c.name || "").trim(),
    kind: c.kind,
    url: c.url.trim(),
    user: c.user.trim(),
    pass: c.pass,
    insecure: !!c.insecure,
    pathMap: c.pathMap.map((r) => ({ from: r.from.trim(), to: r.to.trim() })).filter((r) => r.from || r.to),
  };
}

async function detectClients() {
  const ul = $("#detected");
  ul.replaceChildren(el("li", "muted", "Recherche sur cette machine…"));
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
      const known = edit.clients.some((c) => c.url.trim().replace(/\/+$/, "") === f.url);
      const b = el("button", "btn small", known ? "Déjà réglé" : "Ajouter");
      b.type = "button";
      b.disabled = known;
      b.addEventListener("click", () => {
        const only = edit.clients.length === 1 && !edit.clients[0].url.trim() ? edit.clients[0] : null;
        if (only) {
          only.kind = f.kind;
          only.url = f.url;
        } else if (edit.clients.length < MAX_CLIENTS) {
          const c = newClient(f.kind, f.url);
          edit.clients.push(c);
          edit.open.add(c.id);
        } else return toast(MAX_CLIENTS + " clients au plus.", "bad");
        ul.classList.add("hidden");
        renderClients();
      });
      li.append(el("span", null, f.name + " · " + f.url), b);
      ul.append(li);
    }
  } catch (e) {
    if (e.message !== "session") ul.replaceChildren(el("li", "bad", e.message));
  }
}

// ---------- réglages ----------

function setStatus(sel, configured, ok, text) {
  const n = $(sel);
  n.className = "status" + (ok ? " ok" : configured ? " bad" : "");
  n.textContent = configured ? text || "" : "";
}

function fillSettings() {
  const s = ui.state;
  if (!s) return;
  const tk = s.tracker || {};
  $("#f-trk-url").value = tk.url || "";
  $("#f-trk-key").value = "";
  $("#f-trk-key").placeholder = tk.hasKey ? "Token enregistré (laisse vide pour le garder)" : "Colle ton token ici";
  setStatus("#trk-status", tk.configured, tk.ok, tk.ok ? "Connecté à " + tk.name + " en tant que " + tk.username + (tk.group ? " (" + tk.group + ")" : "") : tk.error);
  const rh = $("#rules-hint");
  rh.replaceChildren();
  if (tk.rulesError) rh.append(el("span", "bad", "Fichier de règles : " + tk.rulesError));
  else if (tk.rulesFound) rh.append(document.createTextNode("Règles du tracker lues dans "), el("code", null, tk.rulesPath || "regles.json"), document.createTextNode("."));
  else rh.append(document.createTextNode("Pas de fichier de règles : identifiants UNIT3D par défaut et aucune règle d'upload. Pour ceux de ton tracker, dépose un "), el("code", null, "regles.json"), document.createTextNode(" dans " + (tk.rulesPath ? tk.rulesPath.replace(/[^/\\]+$/, "") : "le dossier de Baratie") + " (modèle dans le README)."));
  $("#f-tmdb").value = "";
  $("#f-tmdb").placeholder = tk.tmdb ? "Clé enregistrée (laisse vide pour la garder)" : "Colle ta clé ici";
  $("#f-imgbb").value = "";
  $("#f-imgbb").placeholder = tk.imgbb ? "Clé enregistrée (laisse vide pour la garder)" : "Colle ta clé ici";
  edit.clients = (s.clients || []).map((c) => ({ ...c, pass: "", name: c.name || "", url: c.url || "", user: c.user || "", pathMap: (c.pathMap || []).map((r) => ({ from: r.from || "", to: r.to || "" })), test: null }));
  if (!edit.clients.length) edit.clients.push(newClient());
  edit.open.clear();
  $("#detected").classList.add("hidden");
  renderClients();
  $("#f-cat").value = s.category || "";
  $("#f-tag").value = s.tag || "";
  $("#seeddir-hint").textContent = s.seedDir
    ? "Dossier de seed : " + s.seedDir + ". Chaque release publiée y devient un lien physique au nom de la fiche ; tes fichiers d'origine ne sont jamais renommés ni modifiés."
    : "Pas de dossier de seed (BARATIE_SEED_DIR) : le client seede directement les fichiers d'origine, sous le nom de la release.";
  const pm = s.pantry || {};
  $("#f-pm-radarr").value = pm.radarrUrl || "";
  $("#f-pm-radarr-key").value = "";
  $("#f-pm-radarr-key").placeholder = pm.radarrKey ? "Clé enregistrée (laisse vide pour la garder)" : "Colle ta clé ici";
  $("#f-pm-rate").value = pm.rate || 20;
  $("#f-theme").value = s.theme === "dark" ? "dark" : "light";
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
      trackerUrl: $("#f-trk-url").value,
      trackerKey: $("#f-trk-key").value,
      tmdbKey: $("#f-tmdb").value,
      imgbbKey: $("#f-imgbb").value,
      pantry: {
        libs: (ui.state && ui.state.pantry && ui.state.pantry.libs) || [],
        radarrUrl: $("#f-pm-radarr").value,
        radarrKey: $("#f-pm-radarr-key").value,
        rate: Number($("#f-pm-rate").value) || 0,
      },
      clients: edit.clients.map(clientPayload),
      category: $("#f-cat").value,
      tag: $("#f-tag").value,
      theme: $("#f-theme").value,
    });
    renderState(s);
    fillSettings();
    const cl = s.clients || [];
    const tk = s.tracker || {};
    if (tk.ok && cl.length && cl.every((c) => c.ok)) {
      toast("Réglages enregistrés : " + tk.name + (cl.length > 1 ? " et tes " + cl.length + " clients répondent." : " et ton client répondent."), "ok");
      $("#settings").close();
    }
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  } finally {
    btn.disabled = false;
    btn.textContent = "Enregistrer et tester";
  }
}

// ---------- historique ----------

const KIND = { published: ["Publiée", "ok"], seed: ["En seed", "ok"], seed_failed: ["Laissée à l'arrêt", "bad"] };

async function loadHistory() {
  const ul = $("#history");
  try {
    const r = await api("/ui/history");
    ul.replaceChildren();
    if (!r.events || !r.events.length) {
      ul.append(el("li", "empty", "Rien pour l'instant : tes publications apparaîtront ici."));
      return;
    }
    for (const e of r.events) {
      const li = el("li");
      const t = el("time", null, new Date(e.at).toLocaleString("fr-FR", { dateStyle: "short", timeStyle: "short" }));
      t.dateTime = e.at;
      const [label, cls] = KIND[e.kind] || [e.kind, ""];
      li.append(t, el("span", "pill " + cls, label));
      const w = el("div", "what", e.name);
      if (e.detail) w.append(el("div", "sub", e.detail));
      li.append(w);
      ul.append(li);
    }
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
  }
}

// ---------- vues ----------

const VIEW_HASH = { publish: "publier", pantry: "garde-manger", history: "historique" };

function viewFromHash() {
  const h = decodeURIComponent(location.hash.slice(1));
  return Object.keys(VIEW_HASH).find((v) => VIEW_HASH[v] === h) || null;
}

function setView(v, fromHistory) {
  if (!VIEW_HASH[v]) v = "publish";
  if (!fromHistory && viewFromHash() !== v) history.pushState(null, "", "#" + VIEW_HASH[v]);
  ui.view = v;
  for (const b of document.querySelectorAll(".view")) {
    if (b.dataset.view === v) b.setAttribute("aria-current", "page");
    else b.removeAttribute("aria-current");
  }
  for (const k of Object.keys(VIEW_HASH)) $("#view-" + k).classList.toggle("hidden", v !== k);
  if (v === "history") loadHistory();
  if (window.pubShow) window.pubShow(v === "publish");
  if (window.pantryShow) window.pantryShow(v === "pantry");
}

async function boot() {
  for (const b of document.querySelectorAll("[data-open-settings]")) b.addEventListener("click", openSettings);
  for (const b of document.querySelectorAll(".view")) b.addEventListener("click", () => setView(b.dataset.view));
  window.addEventListener("popstate", () => setView(viewFromHash() || "publish", true));
  $("#settings-form").addEventListener("submit", saveSettings);
  $("#detect").addEventListener("click", detectClients);
  $("#client-add").addEventListener("click", () => {
    if (edit.clients.length >= MAX_CLIENTS) return;
    const c = newClient();
    edit.clients.push(c);
    edit.open.add(c.id);
    renderClients();
    const inp = $("#clients").lastChild.querySelector("input");
    if (inp) inp.focus();
  });
  $("#settings-cancel").addEventListener("click", () => $("#settings").close());
  $("#f-theme").addEventListener("change", () => applyTheme($("#f-theme").value));
  $("#settings").addEventListener("close", () => { if (ui.state) applyTheme(ui.state.theme); });
  try {
    renderState(await api("/ui/state"));
  } catch (e) {
    return;
  }
  const start = viewFromHash();
  setView(start || "publish", true);
  if (!start) history.replaceState(null, "", "#" + VIEW_HASH.publish);
  // Connexions vérifiées en tâche de fond au lancement : l'état est relu.
  for (const ms of [2500, 10000]) {
    setTimeout(async () => { try { renderState(await api("/ui/state")); } catch (e) { /* rien */ } }, ms);
  }
  setInterval(async () => { try { renderState(await api("/ui/state")); } catch (e) { /* rien */ } }, 120000);
}

boot();
