#!/usr/bin/env python3
"""Картинки интерфейса Windows из ресурсов приложения iOS/Mac.

    python3 scripts/make-ui-assets.py [путь к RustleBoost]

Берёт из RustleBoost/RustleBoost/Resources/Assets.xcassets кадры талисмана
(сидит, встаёт, бежит, тормозит — в двух вариантах: тёмная собака для светлой
темы и светлая для тёмной), планету, знак приложения и значок строки меню
Mac, и кладёт их в src/assets/ в формате WebP — тот же облик, что на iPhone и
Mac, при весе в десять раз меньше PNG.
"""
import json
import pathlib
import sys

from PIL import Image, ImageChops, ImageFilter

ROOT = pathlib.Path(__file__).resolve().parent.parent
SRC = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path.home() / "Developer/RustleBoost")
ASSETS = SRC / "RustleBoost/Resources/Assets.xcassets"
OUT = ROOT / "src/assets"

# Высота показа талисмана — 132 точки; кадры вдвое крупнее для чётких
# экранов с масштабом 200%.
MASCOT_SIZE = 264
PHASES = {"sit": "MascotSit", "rise": "MascotRise", "run": "MascotRun", "stop": "MascotStop"}


def variants(imageset: pathlib.Path):
    """(светлая тема, тёмная тема) из Contents.json набора."""
    data = json.loads((imageset / "Contents.json").read_text())
    light = dark = None
    for item in data["images"]:
        name = item.get("filename")
        if not name:
            continue
        if any(a.get("value") == "dark" for a in item.get("appearances", [])):
            dark = imageset / name
        else:
            light = imageset / name
    return light, dark or light


def save_webp(img: Image.Image, path: pathlib.Path, quality=86):
    path.parent.mkdir(parents=True, exist_ok=True)
    img.save(path, "WEBP", quality=quality, method=6)


def mascot():
    manifest = {}
    for phase, prefix in PHASES.items():
        count = 0
        while (ASSETS / f"{prefix}{count + 1}.imageset").exists():
            count += 1
            light, dark = variants(ASSETS / f"{prefix}{count}.imageset")
            for theme, src in (("light", light), ("dark", dark)):
                im = Image.open(src).convert("RGBA")
                im = im.resize((MASCOT_SIZE, MASCOT_SIZE), Image.LANCZOS)
                save_webp(im, OUT / "mascot" / phase / theme / f"{count:03d}.webp")
        manifest[phase] = count
    (OUT / "mascot" / "frames.json").write_text(json.dumps(manifest))
    print("талисман:", manifest)


def planet():
    im = Image.open(ASSETS / "PlanetEarth.imageset/PlanetEarth.png").convert("RGBA")
    save_webp(im.resize((400, 400), Image.LANCZOS), OUT / "planet.webp", quality=90)


def logo():
    light, dark = variants(ASSETS / "AppLogo.imageset")
    for theme, src in (("light", light), ("dark", dark)):
        im = Image.open(src).convert("RGBA")
        im.thumbnail((256, 256), Image.LANCZOS)
        save_webp(im, OUT / f"logo-{theme}.webp", quality=92)


def tray():
    """Собака для трея: белая с тёмной обводкой и зелёная (подключено)."""
    src = Image.open(ASSETS / "MenuBarDog.imageset/MenuBarDog@3x.png").convert("RGBA")
    alpha = src.getchannel("A")
    size = 64
    pad = 4
    w, h = src.size
    scale = (size - pad * 2) / max(w, h)
    alpha = alpha.resize((round(w * scale), round(h * scale)), Image.LANCZOS)
    canvas_alpha = Image.new("L", (size, size), 0)
    canvas_alpha.paste(alpha, ((size - alpha.width) // 2, (size - alpha.height) // 2))
    outline = canvas_alpha.filter(ImageFilter.MaxFilter(3))
    for name, color in (("tray-idle.png", (255, 255, 255)), ("tray-connected.png", (52, 209, 148))):
        img = Image.new("RGBA", (size, size), (20, 22, 28, 0))
        img.putalpha(ImageChops.multiply(outline, Image.new("L", (size, size), 140)))
        fill = Image.new("RGBA", (size, size), color + (255,))
        fill.putalpha(canvas_alpha)
        img.alpha_composite(fill)
        path = ROOT / "src-tauri/icons" / name
        img.save(path)
        print("трей:", path.name)


if __name__ == "__main__":
    mascot()
    planet()
    logo()
    tray()
