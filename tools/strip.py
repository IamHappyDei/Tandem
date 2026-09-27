import re
from pathlib import Path

SKIP = ("node_modules", "dist/", ".smoke", "capture/", "tmp/", "fscopilot/", "run/")


def whole_line(text):
    """Drop lines whose only content is a comment. A // or # inside a string, or
    inside a regex literal such as /^tdm1\\//, is never touched - only lines that
    begin with the marker."""
    out = []
    for i, ln in enumerate(text.split("\n")):
        t = ln.strip()
        if i > 0 and (t.startswith("//") or t.startswith("*") and "/*" not in t and "*/" not in t):
            continue
        if (t.startswith("//") or t.startswith("/*")) and i > 0:
            continue
        out.append(ln)
    return "\n".join(out)


def scripts(text):
    return re.sub(r"(<script[^>]*>)(.*?)(</script>)",
                  lambda m: m.group(1) + whole_line(m.group(2)) + m.group(3), text, flags=re.S | re.I)


def styles(text):
    return re.sub(r"(<style[^>]*>)(.*?)(</style>)",
                  lambda m: m.group(1) + re.sub(r"(?m)^\s*/\*.*?\*/\s*$", "", m.group(2), flags=re.S) + m.group(3),
                  text, flags=re.S | re.I)


def shell(text):
    lines = text.split("\n")
    return "\n".join(l for i, l in enumerate(lines) if not (i and l.lstrip().startswith("#")))


def batch(text):
    return "\n".join(l for l in text.split("\n") if not re.match(r"\s*(rem\b|::)", l, re.I))


def collapse(text):
    out, blank = [], 0
    for l in text.split("\n"):
        l = l.rstrip()
        if l == "":
            blank += 1
            if blank > 1:
                continue
        else:
            blank = 0
        out.append(l)
    while out and out[-1] == "":
        out.pop()
    return "\n".join(out) + "\n"


def main():
    seen, changed = set(), []
    for pat in ("**/*.js", "**/*.html", "**/*.sh", "**/*.bat"):
        for p in sorted(Path(".").glob(pat)):
            s = str(p).replace("\\", "/")
            if s in seen or any(x in "/" + s for x in SKIP):
                continue
            seen.add(s)
            src = p.read_text(encoding="utf-8", errors="replace")
            ext = p.suffix.lower()
            t = src
            if ext in (".js", ".mjs"):
                t = whole_line(t)
            elif ext == ".html":
                t = styles(scripts(t))
            elif ext == ".sh":
                t = shell(t)
            elif ext == ".bat":
                t = batch(t)
            t = collapse(t)
            if t != src:
                p.write_text(t, encoding="utf-8")
                changed.append(s)
    print("touched", len(changed))
    for c in changed:
        print("   ", c)


if __name__ == "__main__":
    main()
