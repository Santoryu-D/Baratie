// Fond et accent appliqués avant le premier affichage (pas de flash de
// couleurs) : dernière apparence connue, gardée par app.js.
(function () {
  try {
    var d = document.documentElement;
    d.setAttribute("data-theme", localStorage.getItem("d3x-theme") || "dark");
    d.setAttribute("data-accent", localStorage.getItem("d3x-accent") || "bleu");
  } catch (e) {
    /* stockage indisponible : défaut du site */
  }
})();
