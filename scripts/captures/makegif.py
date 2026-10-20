"""Monte le GIF du README à partir des images de capture.mjs.

python -I makegif.py <dossier des images> <sortie.gif> [largeur] [ips]
"""
import json
import sys

from PIL import Image

src, out = sys.argv[1], sys.argv[2]
width = int(sys.argv[3]) if len(sys.argv) > 3 else 0
fps = float(sys.argv[4]) if len(sys.argv) > 4 else 15

meta = json.load(open(src + "/frames.json"))
step = 1.0 / fps

# Images gardées : au plus une par pas de temps ; chacune dure jusqu'à la
# suivante.
kept = []
for i, f in enumerate(meta):
    if kept and f["t"] - kept[-1]["t"] < step and i != len(meta) - 1:
        continue
    kept.append(f)
durations = []
for i, f in enumerate(kept):
    nxt = kept[i + 1]["t"] if i + 1 < len(kept) else f["t"] + 2.5
    durations.append(max(20, int(round((nxt - f["t"]) * 1000))))
durations[0] += 400


def load(f):
    im = Image.open(src + "/" + f["file"]).convert("RGB")
    if width and im.width != width:
        im = im.resize((width, round(im.height * width / im.width)), Image.LANCZOS)
    return im


frames = [load(f) for f in kept]
# Palette commune tirée d'images variées (début, résultats, ajout, fin).
sample_idx = sorted({0, len(frames) // 4, len(frames) // 2, 3 * len(frames) // 4, len(frames) - 1})
w, h = frames[0].size
sheet = Image.new("RGB", (w, h * len(sample_idx)))
for k, i in enumerate(sample_idx):
    sheet.paste(frames[i], (0, h * k))
pal = sheet.quantize(colors=255, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
q = [f.quantize(palette=pal, dither=Image.Dither.NONE) for f in frames]
q[0].save(out, save_all=True, append_images=q[1:], duration=durations, loop=0, optimize=False, disposal=1)
import os

print(len(q), "images,", round(sum(durations) / 1000, 1), "s,", round(os.path.getsize(out) / 1e6, 2), "Mo")
