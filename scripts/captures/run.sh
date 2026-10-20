#!/bin/sh
# Images du README : la démo de D3X pilotée dans un navigateur sans fenêtre.
#   sh scripts/captures/run.sh
# Il faut Go, Node 22 ou plus, Python 3 avec Pillow, Edge ou Chrome.
# D3X_DEMO_ROOT : dossier vide aux chemins neutres pour les releases de
# démonstration (ils apparaissent sur les captures), C:/Medias par défaut sous
# Windows, /srv/medias ailleurs.
set -e
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
out="$here/out"
mkdir -p "$out"
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) root="${D3X_DEMO_ROOT:-C:/Medias}"; exe=.exe ;; *) root="${D3X_DEMO_ROOT:-/srv/medias}"; exe= ;; esac
(cd "$repo" && go test -c -o "$out/demo.test$exe" ./internal/server)

scene() {
  (cd "$repo/internal/server" && D3X_DEMO=3000 D3X_DEMO_ROOT="$root" D3X_DEMO_VERSION="${D3X_DEMO_VERSION:-1.0.0}" "$out/demo.test$exe" -test.run 'TestDemo$' -test.timeout 2h >"$out/demo.log" 2>&1) &
  demo=$!
  for i in $(seq 1 60); do curl -s http://127.0.0.1:8799/health | grep -q D3X && break; sleep 0.5; done
  sleep 2
  node "$here/capture.mjs" "$out" "$1" || rc=$?
  kill $demo 2>/dev/null || true
  taskkill //IM "demo.test.exe" //F >/dev/null 2>&1 || pkill -f demo.test 2>/dev/null || true
  sleep 1
  return ${rc:-0}
}

rm -rf "$out/gif"
scene gif
scene shots
python3 "$here/makegif.py" "$out/gif" "$out/d3x-1180.gif" 0 15 2>/dev/null || python "$here/makegif.py" "$out/gif" "$out/d3x-1180.gif" 0 15
python3 "$here/finish.py" "$out" "$repo/docs/images" 2>/dev/null || python "$here/finish.py" "$out" "$repo/docs/images"
rm -rf "$root/Releases" "$root/Seed" "$root/Mediatheque"
rmdir "$root" 2>/dev/null || true
echo "Images à jour dans docs/images (vérifie-les avant de committer)."
