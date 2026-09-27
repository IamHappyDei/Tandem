import re
from pathlib import Path
from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parent.parent
SVG = (ROOT / "TEXTLOGO.svg").read_text(encoding="utf-8")
OUT = ROOT / "app" / "packaging"
OUT.mkdir(parents=True, exist_ok=True)

VB = re.search(r'viewBox="0 0 (\d+) (\d+)"', SVG)
W, H = float(VB.group(1)), float(VB.group(2))

# The T of the wordmark, straight from TEXTLOGO.svg - the icon is a piece of the
# logo, not an invented mark.
T = ("M1.19707 98.5797L2.39414 161.639L94.9676 162.836L187.94 163.634L188.738 430.238"
     "L189.935 696.443H259.764H329.593L330.79 429.839L331.588 163.634H423.364H515.139"
     "V99.777V35.9197H257.769H0L1.19707 98.5797Z")


def parse(d):
    pts, cur = [], [0.0, 0.0]
    for cmd, rest in re.findall(r"([MLHVZmlhvz])([^MLHVZmlhvz]*)", d):
        vals = [float(x) for x in re.findall(r"-?\d+\.?\d*(?:[eE][-+]?\d+)?", rest)]
        if cmd in "ML":
            for i in range(0, len(vals) - 1, 2):
                cur = [vals[i], vals[i + 1]]
                pts.append(tuple(cur))
        elif cmd == "H":
            for v in vals:
                cur = [v, cur[1]]
                pts.append(tuple(cur))
        elif cmd == "V":
            for v in vals:
                cur = [cur[0], v]
                pts.append(tuple(cur))
    return pts


def tile(size, ink=(255, 255, 255, 255), pad=0.14):
    im = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    pts = parse(T)
    xs, ys = [p[0] for p in pts], [p[1] for p in pts]
    w, h = max(xs) - min(xs), max(ys) - min(ys)
    s = size * (1 - 2 * pad) / max(w, h)
    ox = (size - w * s) / 2 - min(xs) * s
    oy = (size - h * s) / 2 - min(ys) * s
    ImageDraw.Draw(im).polygon([(ox + x * s, oy + y * s) for x, y in pts], fill=ink)
    return im


big = tile(2048)
for n in (256, 128, 64, 48, 32, 24, 16):
    big.resize((n, n), Image.LANCZOS).save(OUT / f"tandem-{n}.png")

SIZES = [(256, 256), (48, 48), (32, 32), (24, 24), (16, 16)]
big.resize((256, 256), Image.LANCZOS).save(OUT / "tandem.ico", format="ICO", sizes=SIZES)
big.resize((32, 32), Image.LANCZOS).save(ROOT / "app" / "internal" / "tray" / "icon.ico",
                                         format="ICO", sizes=[(32, 32), (16, 16)])

chk = Image.open(OUT / "tandem.ico")
print("ico entries:", len(SIZES), "· alpha extrema:", chk.convert("RGBA").getchannel("A").getextrema())
print("wrote", OUT)
