#!/usr/bin/env python3
"""Generate index.html for the GitHub Pages site from presentation frontmatter."""

import glob
import html
import os
import re
import sys


def parse_frontmatter(path):
    """Extract key-value pairs from a YAML frontmatter block (``---`` delimited).

    Given a file like::

        ---
        marp: true
        title: AI-Assisted SDLC
        description: How OSAC uses Claude Code
        ---

    Returns ``{"marp": "true", "title": "AI-Assisted SDLC",
    "description": "How OSAC uses Claude Code"}``.
    """
    with open(path) as f:
        text = f.read()
    block = re.match(r"^---\s*\n(.*?)\n---", text, re.DOTALL)
    if not block:
        if text.startswith("---"):
            raise ValueError("has an unclosed frontmatter block")
        return {}
    fields = {}
    for line in block.group(1).splitlines():
        kv = re.match(r"^(\w[\w-]*):\s*(.+)$", line)
        if kv:
            value = kv.group(2).strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in ('"', "'"):
                value = value[1:-1]
            fields[kv.group(1)] = value
    return fields


def card_html(href, title, description):
    return (
        f'    <a class="card" href="{html.escape(href)}">\n'
        f"      <h2>{html.escape(title)}</h2>\n"
        f"      <p>{html.escape(description)}</p>\n"
        f"    </a>"
    )


def main():
    presentations_dir = sys.argv[1] if len(sys.argv) > 1 else "presentations"
    output_path = sys.argv[2] if len(sys.argv) > 2 else "index.html"

    cards = []
    for md in sorted(glob.glob(os.path.join(presentations_dir, "*.md"))):
        try:
            fm = parse_frontmatter(md)
        except ValueError as error:
            print(f"error: {md} {error}", file=sys.stderr)
            sys.exit(1)
        if fm.get("marp") != "true":
            continue
        slug = os.path.splitext(os.path.basename(md))[0]
        missing = [k for k in ("title", "description") if not fm.get(k)]
        if missing:
            print(f"error: {md} is missing frontmatter: {', '.join(missing)}", file=sys.stderr)
            sys.exit(1)
        title = fm["title"]
        description = fm["description"]
        cards.append(card_html(f"presentations/{slug}.html", title, description))

    if not cards:
        print("warning: no presentations found", file=sys.stderr)

    page = f"""\
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>OSAC</title>
  <link href="https://fonts.googleapis.com/css2?family=Red+Hat+Display:wght@400;700&family=Red+Hat+Text:wght@400;500&display=swap" rel="stylesheet">
  <style>
    * {{ margin: 0; padding: 0; box-sizing: border-box; }}
    body {{
      font-family: 'Red Hat Text', sans-serif;
      background: #f2f2f2;
      color: #151515;
      min-height: 100vh;
    }}
    header {{
      background: #151515;
      border-top: 4px solid #ee0000;
      padding: 32px 40px;
    }}
    header h1 {{
      font-family: 'Red Hat Display', sans-serif;
      color: #fff;
      font-size: 1.8em;
      font-weight: 700;
    }}
    header p {{
      color: #a0a0a0;
      margin-top: 6px;
      font-size: 0.95em;
    }}
    .cards {{
      max-width: 900px;
      margin: 40px auto;
      padding: 0 24px;
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
      gap: 20px;
    }}
    .card {{
      background: #fff;
      border-radius: 8px;
      padding: 28px 24px;
      text-decoration: none;
      color: inherit;
      border: 1px solid #e0e0e0;
      transition: box-shadow 0.15s, border-color 0.15s;
    }}
    .card:hover {{
      border-color: #ee0000;
      box-shadow: 0 4px 12px rgba(0,0,0,0.08);
    }}
    .card h2 {{
      font-family: 'Red Hat Display', sans-serif;
      font-size: 1.2em;
      margin-bottom: 8px;
      color: #ee0000;
    }}
    .card p {{
      font-size: 0.9em;
      color: #4d4d4d;
      line-height: 1.5;
    }}
    .section-title {{
      max-width: 900px;
      margin: 40px auto 12px;
      padding: 0 24px;
      font-family: 'Red Hat Display', sans-serif;
      font-size: 0.85em;
      text-transform: uppercase;
      letter-spacing: 2px;
      color: #707070;
    }}
  </style>
</head>
<body>
  <header>
    <h1>OSAC</h1>
    <p>Developer tools and presentations</p>
  </header>

  <div class="section-title">Tools</div>
  <div class="cards" id="tools-cards"><p style="color:#6a6e73">Loading dashboards...</p></div>

  <div class="section-title">Presentations</div>
  <div class="cards">
{"".join(chr(10) + c for c in cards)}
  </div>
  <script>
    fetch("dashboards.json")
      .then(function(r) {{
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.json();
      }})
      .then(function(dashboards) {{
        var container = document.getElementById("tools-cards");
        if (!dashboards.length) {{
          container.innerHTML = '<p style="color:#6a6e73">No dashboards configured.</p>';
          return;
        }}
        container.innerHTML = "";
        dashboards.forEach(function(d) {{
          var a = document.createElement("a");
          a.className = "card";
          a.href = d.slug + "/";
          var h2 = document.createElement("h2");
          h2.textContent = d.title;
          var p = document.createElement("p");
          p.textContent = d.description;
          a.appendChild(h2);
          a.appendChild(p);
          container.appendChild(a);
        }});
      }})
      .catch(function() {{
        document.getElementById("tools-cards").innerHTML =
          '<p style="color:#6a6e73">Could not load dashboards.</p>';
      }});
  </script>
</body>
</html>
"""

    with open(output_path, "w") as f:
        f.write(page)
    print(f"wrote {output_path} with {len(cards)} presentation(s)")


if __name__ == "__main__":
    main()
