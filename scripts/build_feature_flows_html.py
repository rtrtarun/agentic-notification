"""Build docs/feature-flows.html from docs/feature-flows.md.

The HTML embeds the markdown and renders it in the browser with marked (markdown)
and mermaid (diagrams), both loaded from jsDelivr at pinned versions. Re-run after
editing the markdown:

    python scripts/build_feature_flows_html.py
"""
import html
import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
SRC = ROOT / "docs" / "feature-flows.md"
OUT = ROOT / "docs" / "feature-flows.html"

TEMPLATE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Feature flows and reverse-engineering report</title>
<style>
  :root { --bg:#fff; --fg:#1b1f24; --muted:#586069; --line:#d8dee4; --code:#f4f6f8; --accent:#1f6feb; --warn:#fff8e1; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#0d1117; --fg:#e6edf3; --muted:#9aa4af; --line:#30363d; --code:#161b22; --accent:#58a6ff; --warn:#2a2410; }
  }
  body { margin:0; background:var(--bg); color:var(--fg); font:16px/1.6 system-ui, -apple-system, "Segoe UI", sans-serif; }
  main { max-width: 62rem; margin: 0 auto; padding: 1.5rem 1rem 4rem; }
  h1,h2,h3 { line-height:1.25; }
  h2 { margin-top:2.5rem; padding-top:.5rem; border-top:1px solid var(--line); }
  a { color:var(--accent); }
  code { background:var(--code); padding:.1em .35em; border-radius:4px; font-size:.9em; }
  pre { background:var(--code); padding:1rem; border-radius:6px; overflow:auto; }
  pre code { background:none; padding:0; }
  .table-wrap { overflow-x:auto; }
  table { border-collapse:collapse; width:100%; margin:1rem 0; font-size:.93rem; }
  th, td { border:1px solid var(--line); padding:.45rem .6rem; text-align:left; vertical-align:top; }
  th { background:var(--code); }
  blockquote { margin:1rem 0; padding:.5rem 1rem; border-left:4px solid var(--accent); background:var(--warn); }
  pre.mermaid { background:transparent; text-align:center; overflow:auto; }
  #toc-note { color:var(--muted); font-size:.9rem; }
  noscript { display:block; padding:1rem; background:var(--warn); }
</style>
</head>
<body>
<main>
<noscript>This page renders its diagrams with JavaScript. The same content is in feature-flows.md.</noscript>
<p id="toc-note">Generated from <code>docs/feature-flows.md</code> by <code>scripts/build_feature_flows_html.py</code>. Edit the markdown, then re-run the script.</p>
<div id="out">Loading...</div>
</main>
<script type="text/markdown" id="src">__MARKDOWN__</script>
<script src="https://cdn.jsdelivr.net/npm/marked@12.0.2/marked.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/mermaid@10.9.1/dist/mermaid.min.js"></script>
<script>
  const md = document.getElementById("src").textContent;
  const out = document.getElementById("out");
  const renderer = new marked.Renderer();
  const baseCode = renderer.code.bind(renderer);
  renderer.code = function (code, lang) {
    const text = typeof code === "object" ? code.text : code;
    const language = typeof code === "object" ? code.lang : lang;
    if (language === "mermaid") {
      const esc = text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
      return '<pre class="mermaid">' + esc + "</pre>";
    }
    return baseCode(code, lang);
  };
  out.innerHTML = marked.parse(md, { renderer, gfm: true, headerIds: true, mangle: false });
  out.querySelectorAll("table").forEach(function (t) {
    const w = document.createElement("div");
    w.className = "table-wrap";
    t.parentNode.insertBefore(w, t);
    w.appendChild(t);
  });
  out.querySelectorAll("a[href$='.md']").forEach(function (a) {
    a.setAttribute("href", a.getAttribute("href").replace(/feature-flows\\.md$/, "feature-flows.html"));
  });
  const dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  mermaid.initialize({ startOnLoad: false, theme: dark ? "dark" : "default", securityLevel: "strict" });
  mermaid.run({ querySelector: "pre.mermaid" });
</script>
</body>
</html>
"""


def main() -> None:
    md = SRC.read_text(encoding="utf-8")
    # The markdown sits inside a script element: only a closing script tag could end it early.
    safe = md.replace("</script", "<\\/script")
    OUT.write_text(TEMPLATE.replace("__MARKDOWN__", safe), encoding="utf-8")
    print(f"wrote {OUT.relative_to(ROOT)} ({len(md)} characters of markdown)")


if __name__ == "__main__":
    main()
