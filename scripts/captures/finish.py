"""Prépare les images du README : captures réduites, planche des thèmes, GIF.

python -I finish.py <dossier des captures> <dossier docs/images du dépôt>
"""
import os
import shutil
import sys

from PIL import Image, ImageDraw

src, dst = sys.argv[1], sys.argv[2]
os.makedirs(dst, exist_ok=True)
WIDTH = 2000


def save(im, name, palette=False):
    # Palette de 256 couleurs pour les vues d'interface (deux fois plus
    # légères) ; pleines couleurs quand un dégradé (affiche, pochette) est
    # visible.
    if palette:
        method = Image.Quantize.FASTOCTREE if im.mode == "RGBA" else Image.Quantize.MEDIANCUT
        im = im.quantize(colors=256, method=method, dither=Image.Dither.FLOYDSTEINBERG)
    p = os.path.join(dst, name)
    im.save(p, optimize=True)
    print(name, im.size, round(os.path.getsize(p) / 1e3), "Ko")


for name, palette in [("publier-apercu", False), ("musique", False), ("lot", False), ("mediatheque", False)]:
    im = Image.open(os.path.join(src, name + ".png")).convert("RGB")
    im = im.resize((WIDTH, round(im.height * WIDTH / im.width)), Image.LANCZOS)
    save(im, name + ".png", palette)


# Planche des thèmes : le haut de la vue Cross-seed, dans trois fonds.
def rounded(im, r):
    mask = Image.new("L", im.size, 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, im.width - 1, im.height - 1), r, fill=255)
    out = Image.new("RGBA", im.size)
    out.paste(im, (0, 0), mask)
    return out


tiles = []
for theme in ["dark", "light", "blue"]:
    im = Image.open(os.path.join(src, "theme-" + theme + ".png")).convert("RGB")
    im = im.crop((0, 0, im.width, round(im.width * 0.62)))
    im = im.resize((900, round(im.height * 900 / im.width)), Image.LANCZOS)
    tiles.append(rounded(im, 22))
gap = 28
sheet = Image.new("RGBA", (len(tiles) * 900 + (len(tiles) - 1) * gap, tiles[0].height), (0, 0, 0, 0))
for i, t in enumerate(tiles):
    sheet.paste(t, (i * (900 + gap), 0), t)
save(sheet, "themes.png")

shutil.copy(os.path.join(src, "d3x-1180.gif"), os.path.join(dst, "cross-seed.gif"))
print("cross-seed.gif", round(os.path.getsize(os.path.join(dst, "cross-seed.gif")) / 1e3), "Ko")
