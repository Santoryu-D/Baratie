"use strict";

// Vue « Mon seed » : ce que V3X attend de ton seed (hit and run à régler,
// relais à reprendre), comparé à l'état réel de ton client.

const seedView = { data: null, loading: false };

const SEED_CLIENT = {
  seeding: ["En seed", "ok"],
  stopped: ["Arrêtée", "warn"],
  incomplete: ["Incomplète, arrêtée", "bad"],
  checking: ["Vérification", ""],
  downloading: ["Téléchargement", ""],
  missing: ["Absente du client", "bad"],
};

function fmtHours(sec) {
  const h = Math.floor(sec / 3600);
  const m = Math.round((sec % 3600) / 60);
  if (h >= 48) return Math.round(sec / 3600) + " h";
  return h + " h " + String(m).padStart(2, "0");
}

function fmtDeadline(iso) {
  const d = new Date(iso);
  return d.toLocaleDateString("fr-FR", { weekday: "short", day: "2-digit", month: "2-digit" }) + ", " + d.toLocaleTimeString("fr-FR", { hour: "2-digit", minute: "2-digit" });
}

function seedLink(id, name) {
  const a = el("a", null, name);
  a.href = "https://v3x.club/torrents/" + id;
  a.target = "_blank";
  a.rel = "noopener";
  return a;
}

async function seedLoad(fresh) {
  if (seedView.loading) return;
  seedView.loading = true;
  $("#seed-refresh").disabled = true;
  try {
    seedView.data = await api("/ui/seed" + (fresh ? "?fresh=1" : ""));
    seedRender();
  } catch (e) {
    if (e.message !== "session") {
      $("#seed-summary").replaceChildren(el("p", "notice bad", "État du seed indisponible : " + e.message));
    }
  } finally {
    seedView.loading = false;
    $("#seed-refresh").disabled = false;
  }
}

function seedStat(label, value, kind, sub) {
  const box = el("div", "stat " + (kind || ""));
  box.append(el("span", "stat-label", label), el("b", "stat-value", value));
  if (sub) box.append(el("span", "stat-sub", sub));
  return box;
}

function seedRender() {
  const d = seedView.data;
  if (!d) return;
  const items = d.items || [];
  const atRisk = items.filter((x) => x.risk === "bad").length;
  const sum = $("#seed-summary");
  sum.replaceChildren(
    seedStat("Hit and run", d.hnr.active + " / " + d.hnr.maxActive, d.hnr.active ? "bad" : "ok", d.hnr.exempt ? "exempté : tu seedes assez" : d.hnr.minSeedHours + " h de seed dans les " + d.hnr.windowDays + " jours"),
    seedStat("À seeder encore", String(items.length), atRisk ? "bad" : items.length ? "" : "ok", atRisk ? atRisk + " en danger" : items.length ? "dans les temps" : "rien à faire"),
    seedStat("Relais pour toi", String((d.relays || []).length), (d.relays || []).length ? "add" : "", "+" + d.relayPrime + " jetons chacun"),
    seedStat("V3X arrêtées", String((d.stopped || []).length), (d.stopped || []).length ? "warn" : "ok", (d.stopped || []).length ? "complètes mais sans seed" : "tout seede"),
  );

  // À seeder encore.
  const tb = $("#seed-tbody");
  tb.replaceChildren();
  for (const x of items) {
    const tr = el("tr", "risk-" + x.risk);
    const tdN = el("td");
    const name = el("div", "name");
    name.append(seedLink(x.torrentId, x.name));
    tdN.append(name);
    const subs = [];
    if (x.uploader) subs.push("ton upload");
    if (x.hnr) subs.push("hit and run marqué : seede-la pour le lever");
    if (subs.length) tdN.append(el("div", "sub", subs.join(" · ")));
    const tdS = el("td");
    const done = Math.min(x.seedSeconds, x.requiredSeconds);
    tdS.append(el("div", "small", fmtHours(done) + " / " + fmtHours(x.requiredSeconds)));
    const bar = el("div", "mini");
    const i = el("i");
    i.style.width = Math.round((done / Math.max(1, x.requiredSeconds)) * 100) + "%";
    bar.append(i);
    tdS.append(bar);
    const tdD = el("td", "small", fmtDeadline(x.deadline));
    const tdC = el("td");
    const [lab, kind] = SEED_CLIENT[x.client] || [x.client, ""];
    tdC.append(el("span", "pill " + kind, lab));
    if (x.client === "seeding" && !x.seedingNow) tdC.append(el("div", "sub", "pas encore vue par le tracker"));
    const tdA = el("td");
    if (x.client === "stopped") {
      const b = el("button", "btn small", "Démarrer");
      b.type = "button";
      b.addEventListener("click", () => seedStart([x.infoHash], b));
      tdA.append(b);
    } else if (x.client === "missing") {
      tdA.append(el("span", "sub", "remets-la dans ton client"));
    } else if (x.client === "incomplete") {
      tdA.append(el("span", "sub", "à reprendre toi-même"));
    }
    tr.append(tdN, tdS, tdD, tdC, tdA);
    tb.append(tr);
  }
  $("#seed-empty").textContent = d.hnr.enabled ? "Rien à seeder : aucun téléchargement en attente de seed." : "Le hit and run est coupé sur V3X.";
  $("#seed-empty").classList.toggle("hidden", items.length > 0);
  $("#seed-hnr-sub").textContent = items.length ? "les plus urgentes d'abord" : "";

  // Relais.
  const rl = d.relays || [];
  $("#seed-relay-card").classList.toggle("hidden", rl.length === 0);
  const rb = $("#seed-relay-list");
  rb.replaceChildren();
  for (const x of rl) {
    const li = el("li");
    const left = el("div");
    left.append(seedLink(x.torrentId, x.name), el("div", "sub", x.seeders + (x.seeders > 1 ? " seeders" : " seeder") + " · relais ouvert jusqu'au " + fmtDeadline(x.until)));
    li.append(left);
    if (x.addKey) {
      const b = el("button", "btn small primary", "Ajouter en cross-seed");
      b.type = "button";
      b.addEventListener("click", async () => {
        b.disabled = true;
        try {
          await api("/ui/apply", { keys: [x.addKey] });
          toast("Ajoutée : D3X la vérifie puis la met en seed.", "ok");
          poll();
        } catch (e) {
          if (e.message !== "session") toast(e.message, "bad");
          b.disabled = false;
        }
      });
      li.append(b);
    } else if (x.client === "stopped") {
      const b = el("button", "btn small", "Démarrer");
      b.type = "button";
      b.addEventListener("click", () => seedStart([x.infoHash], b));
      li.append(b);
    } else {
      const [lab, kind] = SEED_CLIENT[x.client] || [x.client, ""];
      li.append(el("span", "pill " + kind, lab));
    }
    rb.append(li);
  }

  // Torrents V3X arrêtés.
  const st = d.stopped || [];
  $("#seed-stopped-card").classList.toggle("hidden", st.length === 0);
  $("#seed-stopped-txt").textContent = st.length + (st.length > 1 ? " releases V3X complètes sont arrêtées" : " release V3X complète est arrêtée") + " dans ton client : elles ne seedent pas, ni ton ratio ni tes jetons n'en profitent.";
  const sl = $("#seed-stopped-list");
  sl.replaceChildren();
  for (const x of st.slice(0, 200)) sl.append(el("li", null, x.name + " · " + fmtSize(x.size)));
}

async function seedStart(hashes, btn) {
  if (btn) btn.disabled = true;
  try {
    const r = await api("/ui/seed/start", { hashes });
    toast(r.started ? (r.started > 1 ? r.started + " releases démarrées." : "Release démarrée.") : "Rien à démarrer.", "ok");
    setTimeout(() => seedLoad(true), 1500);
  } catch (e) {
    if (e.message !== "session") toast(e.message, "bad");
    if (btn) btn.disabled = false;
  }
}

window.seedShow = (on) => {
  if (on) seedLoad(false);
};

document.addEventListener("DOMContentLoaded", () => {
  $("#seed-refresh").addEventListener("click", () => seedLoad(true));
  $("#seed-start-all").addEventListener("click", () => {
    const st = (seedView.data && seedView.data.stopped) || [];
    if (st.length) seedStart(st.map((x) => x.hash), $("#seed-start-all"));
  });
});
