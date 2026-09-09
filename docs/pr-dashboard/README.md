# PR dashboard

This directory is the OSAC home for the static pull-request dashboard.
The GitHub Pages workflow at `.github/workflows/pr-dashboard.yml` publishes
this dashboard together with the presentation site.

`tools/pr-notify/` generates `data.json` for this page and `dashboards.json`
for the site index. The workflow obtains its GitHub credential from a
repository secret; no credential belongs in this directory.

The dashboard template and its `.nojekyll` marker are source files. Generated
dashboard data and presentation HTML are deployment artifacts and are not
committed.
