#!/usr/bin/env python3
"""Generate the GitHub Wiki from the repo's docs/ (single source of truth).

Usage: build-wiki.py <src-docs-dir> <dst-wiki-dir>

The wiki is a *mirror* of docs/: edit the markdown in docs/, never the wiki
directly. This script is run locally for the first push and by the
wiki-sync GitHub Action on every change to docs/.

Transform rules
  - docs/README.md            -> Home.md          (the wiki landing page)
  - docs/<name>.md            -> <name>.md        (page slug == <name>)
  - relative links  ](./x.md#anchor) / ](x.md)    -> ](x#anchor) / ](x)
    with README -> Home; http(s)/mailto/pure-#anchor links are left as-is;
    links inside fenced code blocks are never rewritten.
  - ```mermaid fences are PRE-RENDERED to committed SVGs (<slug>-diagram-N.svg)
    and replaced with an image link — GitHub's wiki renderer does not render
    mermaid, so we bake the diagrams. Needs `mmdc` (@mermaid-js/mermaid-cli) on
    PATH; without it the raw mermaid block is kept (local-dev fallback).
  - a curated _Sidebar.md nav and a _Footer.md "generated" note are written.
"""
import os
import re
import shutil
import subprocess
import sys
import tempfile

# Reading order for the sidebar; (page-slug, label). "Home" is implicit first.
NAV = [
    ("Home", "Home"),
    ("architecture", "Architecture"),
    ("acquire", "The acquire service"),
    ("download-gateway", "Download gateway & clients"),
    ("indexers-and-nzb", "Indexer search & NZB-first"),
    ("deploying", "Deploying the addon"),
]

SOURCE_TREE = "https://github.com/laedeli/acquire/tree/main/docs"
LINK_RE = re.compile(r"\]\(([^)]+)\)")

# Mermaid rendering (bake diagrams the wiki can't render itself).
MMDC = shutil.which("mmdc")
MMDC_EXTRA = os.environ.get("MMDC_ARGS", "").split()  # e.g. "-p puppeteer.json"


def render_mermaid(source, out_path):
    """Render one mermaid block to out_path (svg). Returns True on success."""
    if not MMDC:
        return False
    with tempfile.NamedTemporaryFile("w", suffix=".mmd", delete=False) as fh:
        fh.write(source)
        mmd = fh.name
    try:
        cmd = [MMDC, "-i", mmd, "-o", out_path, "-b", "white"] + MMDC_EXTRA
        proc = subprocess.run(cmd, capture_output=True, text=True)
        if proc.returncode != 0:
            sys.stderr.write(f"  ! mmdc failed for {os.path.basename(out_path)}: "
                             f"{proc.stderr.strip()[:200]}\n")
            return False
        return os.path.exists(out_path)
    finally:
        os.unlink(mmd)


def page_for(md_filename):
    """docs filename (README.md / acquire.md) -> wiki page slug."""
    base = md_filename[:-3] if md_filename.endswith(".md") else md_filename
    return "Home" if base == "README" else base


def transform_target(target):
    if target.startswith(("http://", "https://", "mailto:", "#")):
        return target
    t = target[2:] if target.startswith("./") else target
    path, sep, anchor = t.partition("#")
    if not path.endswith(".md"):
        return target  # not a doc link (e.g. bare anchor already handled)
    slug = page_for(path)
    return slug + (("#" + anchor) if sep else "")


def transform_markdown(text, slug, dst):
    """Rewrite doc links, and bake ```mermaid fences into committed SVGs."""
    lines = text.splitlines()
    out, i, diagram_n = [], 0, 0
    while i < len(lines):
        stripped = lines[i].lstrip()
        # ```mermaid — capture the block, render it, swap in an image.
        if stripped.startswith(("```mermaid", "~~~mermaid")):
            fence = stripped[:3]
            body, i = [], i + 1
            while i < len(lines) and not lines[i].lstrip().startswith(fence):
                body.append(lines[i])
                i += 1
            i += 1  # skip the closing fence
            diagram_n += 1
            svg = f"{slug}-diagram-{diagram_n}.svg"
            if render_mermaid("\n".join(body), os.path.join(dst, svg)):
                out.append(f"![{slug} diagram {diagram_n}]({svg})")
                print(f"    rendered {svg}")
            elif MMDC:
                # Renderer is present but this diagram failed — abort rather than
                # publish a raw (unrendered) block and regress the wiki.
                sys.exit(f"  ! mermaid render failed for {svg}; aborting")
            else:  # no renderer (local dev): keep the raw block
                out.append(fence + "mermaid")
                out.extend(body)
                out.append(fence)
            continue
        # other fenced code — copy verbatim (never rewrite links inside).
        if stripped.startswith(("```", "~~~")):
            fence = stripped[:3]
            out.append(lines[i])
            i += 1
            while i < len(lines):
                out.append(lines[i])
                closed = lines[i].lstrip().startswith(fence)
                i += 1
                if closed:
                    break
            continue
        out.append(LINK_RE.sub(lambda m: "](" + transform_target(m.group(1)) + ")", lines[i]))
        i += 1
    return "\n".join(out) + ("\n" if text.endswith("\n") else "")


def build_sidebar():
    lines = ["### acquire docs", ""]
    lines += [f"- [{label}]({slug})" for slug, label in NAV]
    lines += [
        "",
        "---",
        f"_Generated from [`docs/`]({SOURCE_TREE}) — edit there, not here._",
        "",
    ]
    return "\n".join(lines)


def build_footer():
    return (
        f"_These pages are generated from [`docs/`]({SOURCE_TREE}) in the main "
        "repo. Edit the docs there; a GitHub Action syncs the wiki automatically._\n"
    )


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: build-wiki.py <src-docs-dir> <dst-wiki-dir>")
    src, dst = sys.argv[1], sys.argv[2]

    if not MMDC:
        sys.stderr.write("  ! mmdc not found — mermaid diagrams kept as raw "
                         "fences (install @mermaid-js/mermaid-cli to bake them)\n")

    # Clear previously-generated markdown + baked diagrams (keep .git / other assets).
    for name in os.listdir(dst):
        if name == ".git":
            continue
        if name.endswith(".md") or re.match(r".+-diagram-\d+\.svg$", name):
            os.remove(os.path.join(dst, name))

    for name in sorted(os.listdir(src)):
        if not name.endswith(".md"):
            continue
        slug = page_for(name)
        with open(os.path.join(src, name), encoding="utf-8") as fh:
            text = fh.read()
        with open(os.path.join(dst, f"{slug}.md"), "w", encoding="utf-8") as fh:
            fh.write(transform_markdown(text, slug, dst))
        print(f"  {name} -> {slug}.md")

    with open(os.path.join(dst, "_Sidebar.md"), "w", encoding="utf-8") as fh:
        fh.write(build_sidebar())
    with open(os.path.join(dst, "_Footer.md"), "w", encoding="utf-8") as fh:
        fh.write(build_footer())
    print("  + _Sidebar.md, _Footer.md")


if __name__ == "__main__":
    main()
