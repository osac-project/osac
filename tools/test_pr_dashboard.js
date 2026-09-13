const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");

const html = fs.readFileSync("docs/pr-dashboard/index.html", "utf8");
function extractInlineScript(documentHtml) {
  const match = documentHtml.match(/<script>([\s\S]*?)<\/script>/i);
  assert.ok(match, "Dashboard HTML must contain an inline script");
  return match[1];
}

assert.equal(
  extractInlineScript("<SCRIPT>const uppercaseTag = true;</SCRIPT>"),
  "const uppercaseTag = true;",
);

const script = extractInlineScript(html)
  .replace(/\nfetch\("data\.json"\)[\s\S]*$/, "");

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

const context = {
  URL,
  document: {
    createElement() {
      let textContent = "";
      return {
        set textContent(value) {
          textContent = value;
        },
        get innerHTML() {
          return escapeHtml(textContent);
        },
      };
    },
  },
};

vm.runInNewContext(
  `${script}\nglobalThis.checkRunRows = checkRunRows; globalThis.renderRepo = renderRepo;`,
  context,
);

const unsafeCheckRows = context.checkRunRows([
  { name: "Unsafe check", conclusion: "SUCCESS", details_url: "javascript:alert(1)" },
]);
assert.doesNotMatch(unsafeCheckRows, /<a /);
assert.match(unsafeCheckRows, /Unsafe check/);

const safeCheckRows = context.checkRunRows([
  { name: "Safe check", conclusion: "SUCCESS", details_url: "https://ci.example/run" },
]);
assert.match(safeCheckRows, /href="https:\/\/ci\.example\/run"/);

const unsafeRepository = context.renderRepo({
  name: "osac-project/osac",
  pulls_url: "javascript:alert(1)",
  prs: [{
    status: "needs_review",
    url: "javascript:alert(1)",
    title: "Unsafe PR",
    author: "alice",
    age_days: 1,
    check_runs: [],
  }],
});
assert.doesNotMatch(unsafeRepository, /href="javascript:/);
assert.match(unsafeRepository, /Unsafe PR/);

console.log("PR dashboard link validation tests passed");
