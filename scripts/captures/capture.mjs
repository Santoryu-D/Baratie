// Pilote Edge sans fenêtre (protocole DevTools) sur la démo de D3X
// (http://127.0.0.1:8799) : captures du README et images du GIF.
//   node capture.mjs <dossier de sortie> <gif|shots>   (voir run.sh)
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";

const OUT = process.argv[2];
const MODE = process.argv[3] || "shots";
// Navigateur Chromium sans fenêtre : BROWSER, sinon Edge (Windows) ou Chrome.
const EDGE =
  process.env.BROWSER ||
  (process.platform === "win32" ? "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe" : process.platform === "darwin" ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" : "google-chrome");
const PORT = 9333;
const DEMO = "http://127.0.0.1:8799";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

mkdirSync(OUT, { recursive: true });
const profile = OUT + "/edge-profile-" + MODE + "-" + Date.now();
const edge = spawn(EDGE, ["--headless=new", `--remote-debugging-port=${PORT}`, `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--hide-scrollbars", "--mute-audio", "--lang=fr-FR", "about:blank"], { stdio: "ignore" });

let page;
for (let i = 0; i < 50 && !page; i++) {
  await sleep(300);
  try {
    const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
    page = list.find((t) => t.type === "page");
  } catch {}
}
if (!page) throw new Error("Edge ne répond pas");

const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((r) => ws.addEventListener("open", r, { once: true }));
let seq = 0;
const pending = new Map();
const handlers = {};
ws.addEventListener("message", (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) {
    const { resolve, reject } = pending.get(m.id);
    pending.delete(m.id);
    m.error ? reject(new Error(m.error.message)) : resolve(m.result);
  } else if (m.method && handlers[m.method]) handlers[m.method](m.params);
});
const send = (method, params = {}) =>
  new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });
const js = async (expression) => {
  const r = await send("Runtime.evaluate", { expression: `(async () => { ${expression} })()`, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error("JS : " + (r.exceptionDetails.exception?.description || r.exceptionDetails.text));
  return r.result.value;
};
const until = async (cond, ms = 30000) => {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    if (await js(`return !!(${cond});`)) return;
    await sleep(200);
  }
  throw new Error("délai dépassé : " + cond);
};

async function setup(width, height, dsf) {
  await send("Page.enable");
  await send("Runtime.enable");
  await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: dsf, mobile: false });
  await send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "no-preference" }] });
  await send("Page.navigate", { url: DEMO + "/auth?t=demo" });
  await sleep(1500);
  await until(`typeof ui !== "undefined" && ui.state && document.fonts.status === "loaded"`);
  await sleep(2600); // connexions vérifiées au lancement
}

async function shot(name, clip) {
  const p = { format: "png", captureBeyondViewport: false };
  if (clip) p.clip = { ...clip, scale: 1 };
  const r = await send("Page.captureScreenshot", p);
  writeFileSync(`${OUT}/${name}.png`, Buffer.from(r.data, "base64"));
  console.log("capture", name);
}

// Curseur de démonstration : la page est pilotée par script, le GIF montre
// où l'on clique.
const CURSOR = `
  if (!document.getElementById("demo-cursor")) {
    const c = document.createElement("div");
    c.id = "demo-cursor";
    c.style.cssText = "position:fixed;left:0;top:0;z-index:2147483647;pointer-events:none;transition:transform .7s cubic-bezier(.25,.8,.3,1);transform:translate(820px,560px)";
    c.innerHTML = '<svg width="26" height="26" viewBox="0 0 24 24"><path d="M4 2.5l15.5 8.2-6.6 1.9-3.2 6.4z" fill="#fff" stroke="#0b0c0e" stroke-width="1.6" stroke-linejoin="round"/></svg>';
    const ring = document.createElement("div");
    ring.style.cssText = "position:absolute;left:-14px;top:-14px;width:30px;height:30px;border-radius:50%;border:2px solid #fff;opacity:0;transform:scale(.4);transition:opacity .35s,transform .35s";
    c.append(ring);
    document.body.append(c);
    window.demoMove = (el) => {
      const r = el.getBoundingClientRect();
      c.style.transform = "translate(" + (r.left + Math.min(r.width / 2, 60)) + "px," + (r.top + r.height / 2) + "px)";
    };
    window.demoPress = () => {
      ring.style.transition = "none"; ring.style.opacity = "0.9"; ring.style.transform = "scale(.4)";
      requestAnimationFrame(() => { ring.style.transition = "opacity .45s, transform .45s"; ring.style.opacity = "0"; ring.style.transform = "scale(1.6)"; });
    };
  }`;

async function clickOn(selector, pause = 900) {
  await js(`${CURSOR}; const el = ${selector}; el.scrollIntoView({ block: "nearest" }); demoMove(el);`);
  await sleep(pause);
  await js(`const el = ${selector}; demoPress(); el.click();`);
  await sleep(350);
}

// ---------- GIF : le cross-seed de bout en bout ----------

async function gif() {
  const W = 1180, H = 740;
  await setup(W, H, 1);
  const frames = [];
  handlers["Page.screencastFrame"] = async (p) => {
    frames.push({ t: p.metadata.timestamp, data: p.data });
    send("Page.screencastFrameAck", { sessionId: p.sessionId }).catch(() => {});
  };
  await js(CURSOR);
  await sleep(400);
  await send("Page.startScreencast", { format: "png", maxWidth: W, maxHeight: H, everyNthFrame: 1 });
  await sleep(1200);
  await clickOn(`document.querySelector("#scan")`);
  await until(`lastStatus && lastStatus.scan.phase === "done" && document.querySelectorAll("#view-cross tr.selectable").length > 3`, 60000);
  await sleep(1800);
  // Trois releases cochées, puis « Ajouter ».
  for (let i = 0; i < 3; i++) await clickOn(`document.querySelectorAll("#view-cross tr.selectable input[type=checkbox]")[${i}]`, 650);
  await sleep(600);
  await clickOn(`document.querySelector("#apply")`);
  await until(`lastStatus && !lastStatus.applying && !(lastStatus.counts.checking > 0) && !(lastStatus.counts.queued > 0) && ui.rows.some((r) => r.status === "seeding")`, 60000);
  await sleep(1600);
  // Les releases ajoutées sont passées dans l'onglet « Déjà en seed ».
  await clickOn(`[...document.querySelectorAll("#view-cross .tab")].find((b) => b.textContent.includes("Déjà en seed"))`, 800);
  await sleep(2600);
  await send("Page.stopScreencast");
  await sleep(300);
  mkdirSync(`${OUT}/gif`, { recursive: true });
  const meta = [];
  frames.forEach((f, i) => {
    const n = String(i).padStart(4, "0");
    writeFileSync(`${OUT}/gif/${n}.png`, Buffer.from(f.data, "base64"));
    meta.push({ file: `${n}.png`, t: f.t });
  });
  writeFileSync(`${OUT}/gif/frames.json`, JSON.stringify(meta));
  console.log(frames.length, "images");
}

// ---------- captures ----------

async function shots() {
  await setup(1360, 860, 2);
  // Cross-seed analysé.
  await js(`document.querySelector("#scan").click();`);
  await until(`lastStatus && lastStatus.scan.phase === "done" && document.querySelectorAll("#view-cross tr.selectable").length > 3`, 60000);
  await js(`const rows = document.querySelectorAll("#view-cross tr.selectable"); rows[1].click(); rows[2].click();`);
  await sleep(800);
  await shot("cross-seed");

  // Les fonds et accents, sur la même vue.
  for (const [theme, accent] of [["light", "vert"], ["blue", "violet"], ["dark", "braise"]]) {
    await js(`applyAppearance(${JSON.stringify(theme)}, ${JSON.stringify(accent)});`);
    await sleep(600);
    await shot(`theme-${theme}`);
  }
  await js(`applyAppearance(ui.state.appearance.theme, ui.state.appearance.accent);`);

  // Publier un film : fiche TMDB, présentation.
  await js(`setView("publish");`);
  await until(`[...document.querySelectorAll("#fs-list li")].some((l) => l.textContent.includes("Demonstration.2024"))`);
  await js(`[...document.querySelectorAll("#fs-list li")].find((l) => l.textContent.includes("Demonstration.2024")).querySelector(".btn").click();`);
  await until(`pub.job && document.querySelector("#tmdb-results .tmdb-item")`, 30000);
  await js(`document.querySelector("#tmdb-results .tmdb-item").click();`);
  await until(`pub.job && pub.job.state === "ready" && pub.miState === "done"`, 60000);
  await sleep(2500);
  await js(`window.scrollTo(0, 0);`);
  await shot("publier");
  await js(`edTab(true); document.querySelector(".editor").scrollIntoView({ block: "start" }); window.scrollBy(0, -70); document.querySelector("#pub-bar").style.visibility = "hidden";`);
  await sleep(1500);
  await shot("publier-apercu");
  await js(`document.querySelector("#pub-bar").style.visibility = "";`);

  // Musique : album retrouvé dans MusicBrainz.
  await js(`await cancelJob();`);
  await until(`[...document.querySelectorAll("#fs-list li")].some((l) => l.textContent.includes("Orchestre"))`);
  await js(`[...document.querySelectorAll("#fs-list li")].find((l) => l.textContent.includes("Orchestre")).querySelector(".btn").click();`);
  await until(`pub.mb && pub.fiche && pub.fiche.creator`, 60000);
  await sleep(1500);
  await js(`document.querySelector("#pub-form").scrollIntoView({ block: "start" }); window.scrollBy(0, -16);`);
  await sleep(800);
  await shot("musique");

  // Lot : un dossier passé au crible, en simulation.
  await js(`await cancelJob(); setView("batch"); $("#b-path").value = ui.state.lastDir; await lotList();`);
  await until(`lot.entries.length > 3`);
  await js(`$("#b-sim").checked = true; lotRender(); lotRun();`);
  await until(`!lot.running && lot.entries.every((e) => e.state !== "pending")`, 180000);
  await js(`lot.tab = "review"; lotRender(); window.scrollTo(0, 0); document.querySelector("#b-bar").style.visibility = "hidden"; document.querySelectorAll(".toast").forEach((t) => t.remove());`);
  await sleep(1200);
  await shot("lot");

  // Médiathèque : des films renommés retrouvés et vérifiés sur le disque.
  await js(`setView("library");`);
  await until(`libView.data && libView.data.folders.length > 0`);
  await js(`document.querySelector("#lib-scan").click();`);
  await until(`libView.data && libView.data.state.scan.phase === "done" && libView.data.rows.length > 0`, 120000);
  await js(`libView.tab = "add"; for (const r of libView.data.rows) if (r.status === "add") libView.selected.add(r.key); libRender(); window.scrollTo(0, 0); document.querySelectorAll(".toast").forEach((t) => t.remove());`);
  await sleep(1200);
  await shot("mediatheque");
}

try {
  if (MODE === "gif") await gif();
  else await shots();
} finally {
  ws.close();
  // Fermeture propre du navigateur (tous ses processus), puis l'arbre au cas où.
  try {
    const v = await (await fetch(`http://127.0.0.1:${PORT}/json/version`)).json();
    const b = new WebSocket(v.webSocketDebuggerUrl);
    await new Promise((r) => b.addEventListener("open", r, { once: true }));
    b.send(JSON.stringify({ id: 1, method: "Browser.close" }));
    await sleep(800);
  } catch {}
  if (process.platform === "win32") spawn("taskkill", ["/PID", String(edge.pid), "/T", "/F"], { stdio: "ignore" });
  else edge.kill();
}
