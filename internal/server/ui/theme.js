(function () {
  try {
    document.documentElement.setAttribute("data-theme", localStorage.getItem("baratie-theme") || "light");
    document.documentElement.setAttribute("data-accent", "braise");
  } catch (e) {
    document.documentElement.setAttribute("data-theme", "light");
  }
})();
