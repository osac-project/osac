#!/usr/bin/env bash
# my-dashboard.sh — Personal dashboard: sprint tickets, PRs (open + merged 24h), and review requests.
#
# Usage: bash tools/my-dashboard.sh [user]
#   user: optional — GitHub username of a team member. Defaults to @me.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/common.sh"

# ─── OSAC Configuration ──────────────────────────────────────
JIRA_PROJECT="OSAC"
JIRA_BOARD_ID="4269"
JIRA_URL="https://redhat.atlassian.net"
GH_ORG="osac-project"
GH_REPOS=(
    "osac-project/osac"
    "osac-project/osac-test-infra"
    "osac-project/osac-ui"
    "osac-project/enhancement-proposals"
    "osac-project/docs"
)

BOLD='\033[1m'
DIM='\033[2m'
WHITE='\033[1;37m'

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

# ─── Target User Resolution ──────────────────────────────────
TARGET_INPUT="${1:-}"

if [ -n "$TARGET_INPUT" ]; then
    DASHBOARD_TITLE="Dashboard for $TARGET_INPUT"
    POSSESSIVE="${TARGET_INPUT}'s"
    GH_AUTHOR_FILTER="$TARGET_INPUT"
    # For Jira JQL, try to resolve email; fall back to GitHub username
    TARGET_EMAIL=$(gh api "users/$TARGET_INPUT" --jq '.email // empty' 2>/dev/null || echo "")
    if [ -n "$TARGET_EMAIL" ]; then
        JIRA_ASSIGNEE="\"$TARGET_EMAIL\""
    else
        JIRA_ASSIGNEE="\"$TARGET_INPUT\""
    fi
    GH_REVIEW_FILTER="$TARGET_INPUT"
else
    DASHBOARD_TITLE="Personal Dashboard"
    POSSESSIVE="My"
    GH_AUTHOR_FILTER="@me"
    JIRA_ASSIGNEE="currentUser()"
    GH_REVIEW_FILTER="@me"
fi

# ─── Prerequisites ────────────────────────────────────────────
HAS_GH=false; command_exists gh && HAS_GH=true
HAS_JIRA=false; command_exists jira && HAS_JIRA=true

if ! command_exists jq; then
    print_error "jq is required but not installed."
    exit 1
fi

if ! $HAS_GH; then
    print_error "gh (GitHub CLI) is required but not installed."
    exit 1
fi

# ─── Helpers ──────────────────────────────────────────────────

render_bar() {
    local pct=${1:-0} width=${2:-25}
    if (( pct < 0 )); then pct=0; fi
    if (( pct > 100 )); then pct=100; fi
    local filled=$(( pct * width / 100 ))
    local empty=$(( width - filled ))
    local color
    if (( pct >= 80 )); then color="$GREEN"
    elif (( pct >= 40 )); then color="$YELLOW"
    else color="$RED"
    fi
    printf "${color}"
    for ((i=0; i<filled; i++)); do printf "█"; done
    printf "${NC}${DIM}"
    for ((i=0; i<empty; i++)); do printf "░"; done
    printf "${NC}"
}

days_ago() {
    local ts="${1%%T*}"
    local then_s now_s
    if date -d "2000-01-01" +%s &>/dev/null; then
        then_s=$(date -d "$ts" +%s 2>/dev/null || echo 0)
    else
        then_s=$(date -j -f "%Y-%m-%d" "$ts" +%s 2>/dev/null || echo 0)
    fi
    now_s=$(date +%s)
    echo $(( (now_s - then_s) / 86400 ))
}

relative_ago() {
    local ts="${1:-}"
    local then_s=0 now_s
    now_s=$(date +%s)
    if date -d "2000-01-01" +%s &>/dev/null; then
        then_s=$(date -d "$ts" +%s 2>/dev/null || echo 0)
    else
        local compact
        compact=$(printf '%s' "$ts" | sed -E 's/\.[0-9]+//; s/[Zz]$//; s/[+-][0-9]{2}:[0-9]{2}$//')
        then_s=$(date -j -f "%Y-%m-%dT%H:%M:%S" "$compact" +%s 2>/dev/null || echo 0)
    fi
    if (( then_s == 0 )); then
        echo "?"
        return
    fi
    local diff=$(( now_s - then_s ))
    if (( diff < 0 )); then diff=0; fi
    if (( diff < 60 )); then
        echo "just now"
    elif (( diff < 3600 )); then
        echo "$((diff / 60))m ago"
    else
        echo "$((diff / 3600))h ago"
    fi
}

days_between() {
    local d1 d2
    if date -d "2000-01-01" +%s &>/dev/null; then
        d1=$(date -d "$1" +%s 2>/dev/null || echo 0)
        d2=$(date -d "$2" +%s 2>/dev/null || echo 0)
    else
        d1=$(date -j -f "%Y-%m-%d" "$1" +%s 2>/dev/null || echo 0)
        d2=$(date -j -f "%Y-%m-%d" "$2" +%s 2>/dev/null || echo 0)
    fi
    echo $(( (d2 - d1) / 86400 ))
}

gh_unresolved_threads() {
    local repo="$1" number="$2"
    local owner="${repo%%/*}" name="${repo##*/}"
    gh api graphql \
        -f query='query($o:String!,$n:String!,$pr:Int!){repository(owner:$o,name:$n){pullRequest(number:$pr){reviewThreads(first:100){nodes{isResolved}}}}}' \
        -f o="$owner" -f n="$name" -F pr="$number" \
        --jq '[.data.repository.pullRequest.reviewThreads.nodes[]|select(.isResolved==false)]|length' \
        2>/dev/null || echo "?"
}

section_header() {
    local icon="$1" title="$2"
    echo ""
    echo -e "${BOLD}${WHITE}╔══════════════════════════════════════════════════════════════════╗${NC}"
    printf  "${BOLD}${WHITE}║  %s  %-59s ║${NC}\n" "$icon" "$title"
    echo -e "${BOLD}${WHITE}╚══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
}

print_table() {
    column -t -s '§' | awk -v dim='\033[2m' -v nc='\033[0m' '
        NR == 1 {
            printf "  %s%s%s\n", dim, $0, nc
            n = length($0)
            s = ""
            for (i = 1; i <= n; i++) s = s "-"
            printf "  %s%s%s\n", dim, s, nc
            next
        }
        { printf "  %s\n", $0 }
    '
}

# ─── Resolve GitHub user info ─────────────────────────────────
GH_USER=""
GH_USER=$(gh api user --jq '.login' 2>/dev/null || echo "")

echo ""
echo -e "${BOLD}${WHITE}  $DASHBOARD_TITLE — $(date '+%A, %B %d %Y — %H:%M')${NC}"

# ══════════════════════════════════════════════════════════════
# SECTION 1: JIRA SPRINT TASKS
# ══════════════════════════════════════════════════════════════

section_header "📋" "$POSSESSIVE Sprint Tasks"

if $HAS_JIRA; then
    # Get sprint metadata via Jira REST API (jira CLI doesn't support --board on sprint list)
    SPRINT_DATA=$(curl -sn "${JIRA_URL}/rest/agile/1.0/board/${JIRA_BOARD_ID}/sprint?state=active" 2>/dev/null \
        | jq '[.values // [] | .[] | select(.state=="active")] | sort_by(.startDate) | last // {}')
    SPRINT_NAME=$(echo "$SPRINT_DATA" | jq -r '.name//"Unknown"')
    SPRINT_START=$(echo "$SPRINT_DATA" | jq -r '.startDate//""' | cut -dT -f1)
    SPRINT_END=$(echo "$SPRINT_DATA" | jq -r '.endDate//""' | cut -dT -f1)

    # Get tasks via jira CLI (supports JQL via --raw)
    TASKS_RAW=$(jira issue list --raw \
        -q "project = $JIRA_PROJECT AND sprint in openSprints() AND assignee = $JIRA_ASSIGNEE" \
        --paginate "0:50" 2>/dev/null || echo '[]')

    TASKS=$(echo "$TASKS_RAW" | jq '
        if type=="array" then
            [.[]|{
                key,
                summary: (.fields.summary//""),
                status: (.fields.status.name//""),
                priority: (.fields.priority.name//""),
                issuetype: (.fields.issueType.name//"")
            }]
        else [] end')

    TOTAL=$(echo "$TASKS" | jq 'length')
    DONE=$(echo "$TASKS" | jq '[.[]|select(.status=="Closed" or .status=="Release Pending")]|length')
    IN_REVIEW=$(echo "$TASKS" | jq '[.[]|select(.status=="Code Review" or .status=="Review")]|length')
    IN_PROGRESS=$(echo "$TASKS" | jq '[.[]|select(.status=="In Progress")]|length')
    TODO=$(echo "$TASKS" | jq '[.[]|select(.status=="To Do" or .status=="Backlog" or .status=="New")]|length')

    TODAY=$(date +%Y-%m-%d)
    S_TOTAL=0; S_ELAPSED=0; S_PCT=0
    if [ -n "$SPRINT_START" ] && [ -n "$SPRINT_END" ]; then
        S_TOTAL=$(days_between "$SPRINT_START" "$SPRINT_END")
        S_ELAPSED=$(days_between "$SPRINT_START" "$TODAY")
        if (( S_ELAPSED < 0 )); then S_ELAPSED=0; fi
        if (( S_ELAPSED > S_TOTAL )); then S_ELAPSED=$S_TOTAL; fi
        if (( S_TOTAL > 0 )); then
            S_PCT=$(( S_ELAPSED * 100 / S_TOTAL ))
        fi
    fi

    T_PCT=0
    if (( TOTAL > 0 )); then
        T_PCT=$(( (DONE * 100 + IN_REVIEW * 50) / TOTAL ))
    fi

    echo -e "  ${BOLD}$SPRINT_NAME${NC}"
    if [ -n "$SPRINT_START" ] && [ -n "$SPRINT_END" ]; then
        echo -e "  ${DIM}$SPRINT_START → $SPRINT_END${NC}"
    fi
    echo ""
    printf "  Sprint timeline  "; render_bar "$S_PCT"; printf "  %3d%%  (day %d/%d)\n" "$S_PCT" "$S_ELAPSED" "$S_TOTAL"
    printf "  Tasks progress   "; render_bar "$T_PCT"; printf "  %3d%%  (%d done, %d review, %d wip, %d todo)\n" "$T_PCT" "$DONE" "$IN_REVIEW" "$IN_PROGRESS" "$TODO"
    echo ""

    if (( TOTAL > 0 )); then
        printf "  ${DIM}%-18s %-12s %-18s %-12s %s${NC}\n" \
            "Key" "Type" "Status" "Priority" "Summary"
        printf "  ${DIM}%-18s %-12s %-18s %-12s %s${NC}\n" \
            "──────────────────" "────────────" "──────────────────" "────────────" "──────────────────────────────"

        echo "$TASKS" | jq -r '
            def status_order: {"In Progress":0,"Code Review":1,"Review":2,"To Do":3,"Backlog":4,"New":5,"Release Pending":6,"Closed":7}[.] // 8;
            sort_by(.status | status_order) | .[] | [.key,.issuetype,.status,.priority,.summary] | @tsv
        ' | \
        while IFS=$'\t' read -r key type status priority summary; do
            summary_short="${summary:0:40}"
            if [ "${#summary}" -gt 40 ]; then summary_short="${summary_short}…"; fi

            case "$status" in
                "In Progress")                  st_icon="🔵" ;;
                "Code Review")                  st_icon="🟡" ;;
                "Review")                       st_icon="🟠" ;;
                "To Do"|"Backlog"|"New")        st_icon="⬜" ;;
                "Closed"|"Release Pending")     st_icon="✅" ;;
                *)                              st_icon="❓" ;;
            esac

            printf "  %-18s %-12s %s %-16s %-12s %s\n" \
                "$key" "$type" "$st_icon" "$status" "$priority" "$summary_short"
        done

        # --- PR ↔ Jira Status Check ---
        mkdir -p "$TMP_DIR/ticket_prs"

        while IFS= read -r tkey; do
            (
                gh api graphql \
                    -f query='query($q:String!){search(query:$q,type:ISSUE,first:5){nodes{...on PullRequest{number title body state reviewDecision repository{name}url}}}}' \
                    -f q="${tkey} org:${GH_ORG} is:pr" \
                    --jq '[.data.search.nodes[] | select(.title != null)]' \
                    2>/dev/null || echo '[]'
            ) > "$TMP_DIR/ticket_prs/${tkey}.json" &
        done < <(echo "$TASKS" | jq -r '.[].key')
        wait || true

        > "$TMP_DIR/status_suggestions.txt"
        while IFS=$'\t' read -r tkey tstatus; do
            pr_file="$TMP_DIR/ticket_prs/${tkey}.json"
            [ -s "$pr_file" ] || continue

            matched_prs=$(jq -c --arg key "$tkey" \
                '[.[] | select((.title + " " + (.body // "")) | test($key + "([^0-9]|$)"))]' \
                "$pr_file" 2>/dev/null || echo '[]')
            matched_count=$(echo "$matched_prs" | jq 'length')
            [ "$matched_count" -gt 0 ] || continue

            has_open=$(echo "$matched_prs" | jq '[.[] | select(.state == "OPEN")] | length')
            has_merged=$(echo "$matched_prs" | jq '[.[] | select(.state == "MERGED")] | length')
            all_closed=$(echo "$matched_prs" | jq 'all(.state == "CLOSED")')
            has_approved=$(echo "$matched_prs" | jq '[.[] | select(.state == "OPEN" and .reviewDecision == "APPROVED")] | length')
            has_changes=$(echo "$matched_prs" | jq '[.[] | select(.state == "OPEN" and .reviewDecision == "CHANGES_REQUESTED")] | length')

            open_list=$(echo "$matched_prs" | jq -r '[.[] | select(.state == "OPEN") | "#\(.number) \(.repository.name)"] | join(", ")')
            merged_list=$(echo "$matched_prs" | jq -r '[.[] | select(.state == "MERGED") | "#\(.number) \(.repository.name)"] | join(", ")')
            closed_list=$(echo "$matched_prs" | jq -r '[.[] | select(.state == "CLOSED") | "#\(.number) \(.repository.name)"] | join(", ")')
            approved_list=$(echo "$matched_prs" | jq -r '[.[] | select(.state == "OPEN" and .reviewDecision == "APPROVED") | "#\(.number) \(.repository.name)"] | join(", ")')
            changes_list=$(echo "$matched_prs" | jq -r '[.[] | select(.state == "OPEN" and .reviewDecision == "CHANGES_REQUESTED") | "#\(.number) \(.repository.name)"] | join(", ")')

            suggestion=""
            if [ "$has_open" -gt 0 ]; then
                case "$tstatus" in
                    "To Do"|"Backlog")
                        suggestion="open PRs (${open_list}) → move ticket to In Progress" ;;
                    "Code Review"|"Review")
                        if [ "$has_changes" -gt 0 ]; then
                            suggestion="changes requested (${changes_list}) → address comments, move to In Progress"
                        fi ;;
                    "In Progress")
                        if [ "$has_approved" -gt 0 ]; then
                            suggestion="approved (${approved_list}) → merge PR or move to Code Review"
                        fi ;;
                esac
            elif [ "$has_merged" -gt 0 ]; then
                case "$tstatus" in
                    "To Do"|"Backlog"|"In Progress"|"Code Review")
                        suggestion="merged (${merged_list}) → move ticket to Review" ;;
                esac
            elif [ "$all_closed" = "true" ]; then
                case "$tstatus" in
                    "In Progress"|"Code Review"|"Review")
                        suggestion="all ${matched_count} PR(s) closed without merge (${closed_list}) → move to Backlog or close" ;;
                esac
            fi

            if [ -n "$suggestion" ]; then
                echo "${tkey}|${suggestion}" >> "$TMP_DIR/status_suggestions.txt"
            fi
        done < <(echo "$TASKS" | jq -r '.[] | "\(.key)\t\(.status)"')

        if [ -s "$TMP_DIR/status_suggestions.txt" ]; then
            echo ""
            echo -e "  ${BOLD}⚠ Jira ↔ PR Status Suggestions:${NC}"
            echo ""
            while IFS='|' read -r skey suggestion; do
                printf "    ${YELLOW}⚠${NC} %-18s %s\n" "$skey" "$suggestion"
            done < "$TMP_DIR/status_suggestions.txt"
        fi
    else
        echo -e "  ${DIM}No tasks assigned in current sprint.${NC}"
    fi
else
    echo -e "  ${DIM}jira CLI not installed — skipping Jira section.${NC}"
    echo -e "  ${DIM}Install: https://github.com/ankitpokhrel/jira-cli${NC}"
fi

# ══════════════════════════════════════════════════════════════
# SECTION 2: MY OPEN PRs (+ merged in last 24h)
# ══════════════════════════════════════════════════════════════

section_header "🔀" "$POSSESSIVE Open PRs"

mkdir -p "$TMP_DIR/my_prs" "$TMP_DIR/my_merged" "$TMP_DIR/threads"

CUTOFF_24H=$(date -u -v-24H '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '24 hours ago' '+%Y-%m-%dT%H:%M:%SZ')

for i in "${!GH_REPOS[@]}"; do
    repo="${GH_REPOS[$i]}"
    (
        gh pr list --repo "$repo" --author "$GH_AUTHOR_FILTER" --state open \
            --json number,title,url,labels,reviewDecision,reviewRequests,createdAt,isDraft 2>/dev/null \
        | jq --arg repo "$repo" \
            '[.[]|{
                number,
                title,
                url,
                repo: $repo,
                created_at: .createdAt,
                is_draft: .isDraft,
                labels: [.labels[].name],
                review_decision: (.reviewDecision // ""),
                requested_reviewers: [(.reviewRequests // [])[] | (.login // .slug // .name // "?")]
            }]' \
        || echo '[]'
    ) > "$TMP_DIR/my_prs/gh_$i.json" 2>/dev/null &
    (
        gh pr list --repo "$repo" --author "$GH_AUTHOR_FILTER" --state merged --limit 50 \
            --json number,title,url,mergedAt,labels 2>/dev/null \
        | jq --arg repo "$repo" --arg cutoff "$CUTOFF_24H" \
            '[.[] | select(.mergedAt != null and .mergedAt > $cutoff) | {
                number,
                title,
                url,
                repo: $repo,
                merged_at: .mergedAt,
                labels: [.labels[].name]
            }]' \
        || echo '[]'
    ) > "$TMP_DIR/my_merged/gh_$i.json" 2>/dev/null &
done

wait

shopt -s nullglob
pr_files=("$TMP_DIR"/my_prs/*.json)
merged_files=("$TMP_DIR"/my_merged/*.json)
shopt -u nullglob

if [ ${#pr_files[@]} -gt 0 ]; then
    MY_PRS=$(jq -s 'map(select(. != null and type == "array")) | add // []' "${pr_files[@]}")
else
    MY_PRS='[]'
fi
MY_PR_COUNT=$(echo "$MY_PRS" | jq 'length')

if [ ${#merged_files[@]} -gt 0 ]; then
    MY_MERGED=$(jq -s 'map(select(. != null and type == "array")) | add // []' "${merged_files[@]}")
else
    MY_MERGED='[]'
fi
MY_MERGED_COUNT=$(echo "$MY_MERGED" | jq 'length')

if (( MY_PR_COUNT > 0 )); then
    while IFS=$'\t' read -r repo number; do
        safe="${repo//\//_}_${number}"
        (gh_unresolved_threads "$repo" "$number" > "$TMP_DIR/threads/${safe}.txt") &
    done < <(echo "$MY_PRS" | jq -r '.[]|"\(.repo)\t\(.number)"')
    wait

    echo "$MY_PRS" | jq -c 'sort_by(.created_at)|.[]' | while read -r row; do
        repo=$(echo "$row" | jq -r '.repo')
        number=$(echo "$row" | jq -r '.number')
        decision=$(echo "$row" | jq -r '.review_decision')
        draft=$(echo "$row" | jq -r '.is_draft')
        reviewers=$(echo "$row" | jq -r '(.requested_reviewers|join(", ")) // ""')
        labels=$(echo "$row" | jq -r '(.labels|join(", ")) // ""')
        created_at=$(echo "$row" | jq -r '.created_at')

        repo_short="${repo##*/}"
        age=$(days_ago "$created_at")

        case "$decision" in
            APPROVED)          approval="✓ Approved" ;;
            CHANGES_REQUESTED) approval="✗ Changes" ;;
            REVIEW_REQUIRED)   approval="◷ Pending" ;;
            *)                 approval="—" ;;
        esac

        safe="${repo//\//_}_${number}"
        threads="—"
        if [ -f "$TMP_DIR/threads/${safe}.txt" ]; then
            threads=$(cat "$TMP_DIR/threads/${safe}.txt")
        fi
        if [ "$threads" = "0" ]; then
            threads_fmt="✓ none"
        elif [ "$threads" != "?" ] && [ "$threads" != "—" ] && [ "$threads" -gt 0 ] 2>/dev/null; then
            threads_fmt="⚠ $threads open"
        else
            threads_fmt="—"
        fi

        if [ "$draft" = "true" ]; then draft_fmt="yes"; else draft_fmt="no"; fi

        labels_short="${labels:0:20}"
        if [ "${#labels}" -gt 20 ]; then labels_short="${labels_short}…"; fi

        echo "${repo_short}§#${number}§${approval}§${threads_fmt}§${draft_fmt}§${reviewers:---}§${age}d§${labels_short:---}"
    done > "$TMP_DIR/my_prs_table.txt"

    {
        echo "Repo§#§Approved§Unresolved§Draft§Reviewers§Age§Labels"
        cat "$TMP_DIR/my_prs_table.txt"
    } | print_table

    echo ""
    echo -e "  ${BOLD}Links:${NC}"
    echo "$MY_PRS" | jq -r 'sort_by(.created_at)|.[]|"  \(.url)"'
else
    echo -e "  ${DIM}No open PRs.${NC}"
fi

echo ""
echo -e "  ${BOLD}Merged in last 24h:${NC}"
echo ""

if (( MY_MERGED_COUNT > 0 )); then
    echo "$MY_MERGED" | jq -c 'sort_by(.merged_at)|reverse|.[]' | while read -r row; do
        repo=$(echo "$row" | jq -r '.repo')
        number=$(echo "$row" | jq -r '.number')
        title=$(echo "$row" | jq -r '.title')
        merged_at=$(echo "$row" | jq -r '.merged_at')

        repo_short="${repo##*/}"
        age=$(relative_ago "$merged_at")
        title_short="${title:0:50}"
        if [ "${#title}" -gt 50 ]; then title_short="${title_short}…"; fi
        title_short="${title_short//§/-}"

        echo "${repo_short}§#${number}§${title_short}§${age}"
    done > "$TMP_DIR/my_merged_table.txt"

    {
        echo "Repo§#§Title§Merged"
        cat "$TMP_DIR/my_merged_table.txt"
    } | print_table

    echo ""
    echo -e "  ${BOLD}Merged Links:${NC}"
    echo "$MY_MERGED" | jq -r 'sort_by(.merged_at)|reverse|.[]|"  \(.url)"'
else
    echo -e "  ${DIM}No PRs merged in the last 24 hours.${NC}"
fi

# ══════════════════════════════════════════════════════════════
# SECTION 3: REVIEW REQUESTS (excluding WIP / on-hold)
# ══════════════════════════════════════════════════════════════

section_header "👀" "PRs Requesting $POSSESSIVE Review"

mkdir -p "$TMP_DIR/reviews"

for i in "${!GH_REPOS[@]}"; do
    repo="${GH_REPOS[$i]}"
    (
        gh pr list --repo "$repo" --search "review-requested:$GH_REVIEW_FILTER" \
            --json number,title,author,url,createdAt,labels 2>/dev/null \
        | jq --arg repo "$repo" \
            '[.[] |
                select((.labels | map(.name) | any(. == "work-in-progress" or . == "on-hold" or . == "auto-label/work-in-progress" or . == "auto-label/on-hold")) | not) |
                {
                    number,
                    title,
                    author: .author.login,
                    url,
                    repo: $repo,
                    created_at: .createdAt,
                    labels: [.labels[].name]
                }
            ]' \
        || echo '[]'
    ) > "$TMP_DIR/reviews/gh_$i.json" 2>/dev/null &
done

wait

shopt -s nullglob
review_files=("$TMP_DIR"/reviews/*.json)
shopt -u nullglob

if [ ${#review_files[@]} -gt 0 ]; then
    REVIEWS=$(jq -s 'map(select(. != null and type == "array")) | add // []' "${review_files[@]}")
else
    REVIEWS='[]'
fi
REVIEW_COUNT=$(echo "$REVIEWS" | jq 'length')

if (( REVIEW_COUNT > 0 )); then
    echo "$REVIEWS" | jq -c 'sort_by(.created_at)|.[]' | while read -r row; do
        repo=$(echo "$row" | jq -r '.repo')
        number=$(echo "$row" | jq -r '.number')
        author=$(echo "$row" | jq -r '.author')
        created=$(echo "$row" | jq -r '.created_at')
        title=$(echo "$row" | jq -r '.title')

        repo_short="${repo##*/}"
        age=$(days_ago "$created")
        title_short="${title:0:50}"
        if [ "${#title}" -gt 50 ]; then title_short="${title_short}…"; fi
        title_short="${title_short//§/-}"

        age_fmt="${age}d"
        if (( age > 3 )); then age_fmt="${age}d ⚠"; fi

        echo "${repo_short}§#${number}§${author}§${age_fmt}§${title_short}"
    done > "$TMP_DIR/reviews_table.txt"

    {
        echo "Repo§#§Author§Age§Title"
        cat "$TMP_DIR/reviews_table.txt"
    } | print_table

    echo ""
    echo -e "  ${BOLD}Review Links:${NC}"
    echo "$REVIEWS" | jq -r 'sort_by(.created_at)|.[]|"  \(.url)"'
else
    echo -e "  ${DIM}No pending review requests.${NC}"
fi

REVIEW_GH_LOGIN="$GH_REVIEW_FILTER"
if [ "$REVIEW_GH_LOGIN" = "@me" ] && [ -n "$GH_USER" ]; then
    REVIEW_GH_LOGIN="$GH_USER"
fi
echo ""
echo -e "  ${DIM}Excluded labels: work-in-progress, on-hold, auto-label/work-in-progress, auto-label/on-hold${NC}"
if [ -n "$REVIEW_GH_LOGIN" ]; then
    echo -e "  ${DIM}All review requests: https://github.com/pulls?q=is%3Aopen+is%3Apr+org%3Aosac-project+review-requested%3A${REVIEW_GH_LOGIN}+-label%3Awork-in-progress+-label%3Aon-hold+-label%3Aauto-label%2Fwork-in-progress+-label%3Aauto-label%2Fon-hold${NC}"
fi

# ══════════════════════════════════════════════════════════════
# SHARED FETCH: ALL OPEN PRs (for sections 4 & 5)
# ══════════════════════════════════════════════════════════════

mkdir -p "$TMP_DIR/all_prs"

for i in "${!GH_REPOS[@]}"; do
    repo="${GH_REPOS[$i]}"
    (
        gh pr list --repo "$repo" --state open \
            --json number,title,author,url,createdAt,labels 2>/dev/null \
        | jq --arg repo "$repo" \
            '[.[]|{number, title, author: .author.login, url, repo: $repo, created_at: .createdAt, labels: [.labels[].name]}]' \
        || echo '[]'
    ) > "$TMP_DIR/all_prs/gh_$i.json" 2>/dev/null &
done

wait

shopt -s nullglob
all_pr_files=("$TMP_DIR"/all_prs/*.json)
shopt -u nullglob
if [ ${#all_pr_files[@]} -gt 0 ]; then
    ALL_PRS=$(jq -s 'map(select(. != null and type == "array")) | add // []' "${all_pr_files[@]}")
else
    ALL_PRS='[]'
fi

# Fetch org members dynamically for team/external categorization
ORG_MEMBERS=$(gh api orgs/$GH_ORG/members --paginate --jq '.[].login' 2>/dev/null | jq -R -s '[split("\n")[]|select(length>0)]')

# ══════════════════════════════════════════════════════════════
# SECTION 4: BOT PRs
# ══════════════════════════════════════════════════════════════

section_header "🤖" "Bot PRs"

BOT_PRS=$(echo "$ALL_PRS" | jq '[.[] | select(.author | test("\\[bot\\]$|^app/|bot$|^dependabot|^renovate|^github-actions"))]')
BOT_PR_COUNT=$(echo "$BOT_PRS" | jq 'length')

if (( BOT_PR_COUNT > 0 )); then
    echo "$BOT_PRS" | jq -r '
        group_by(.author) | .[] |
        (.[0].author) as $bot |
        group_by(.repo) | .[] |
        "\($bot)§\((.[0].repo // "") | split("/") | last)§\(length)"
    ' | sort > "$TMP_DIR/bot_table.txt"

    {
        echo "Bot§Repo§Open PRs"
        cat "$TMP_DIR/bot_table.txt"
    } | print_table

    echo ""
    echo -e "  ${BOLD}Total: $BOT_PR_COUNT open bot PRs${NC}"
else
    echo -e "  ${DIM}No open bot PRs.${NC}"
fi

# ══════════════════════════════════════════════════════════════
# SECTION 5: EXTERNAL CONTRIBUTOR PRs
# ══════════════════════════════════════════════════════════════

section_header "🌐" "External Contributor PRs"

EXTERNAL_PRS=$(echo "$ALL_PRS" | jq \
    --argjson members "$ORG_MEMBERS" \
    --arg me "$GH_USER" \
    '[.[] | select(
        .author as $a |
        ($members | any(. == $a) | not) and
        ($a | test("\\[bot\\]$|^app/|bot$|^dependabot|^renovate|^github-actions") | not) and
        ($a != $me)
    )]')
EXTERNAL_COUNT=$(echo "$EXTERNAL_PRS" | jq 'length')

if (( EXTERNAL_COUNT > 0 )); then
    echo "$EXTERNAL_PRS" | jq -c 'sort_by(.created_at)|.[]' | while read -r row; do
        repo=$(echo "$row" | jq -r '.repo')
        number=$(echo "$row" | jq -r '.number')
        author=$(echo "$row" | jq -r '.author')
        created=$(echo "$row" | jq -r '.created_at')
        title=$(echo "$row" | jq -r '.title')

        repo_short="${repo##*/}"
        age=$(days_ago "$created")
        title_short="${title:0:50}"
        if [ "${#title}" -gt 50 ]; then title_short="${title_short}…"; fi
        title_short="${title_short//§/-}"

        echo "${repo_short}§#${number}§${author}§${age}d§${title_short}"
    done > "$TMP_DIR/external_table.txt"

    {
        echo "Repo§#§Author§Age§Title"
        cat "$TMP_DIR/external_table.txt"
    } | print_table

    echo ""
    echo -e "  ${BOLD}Links:${NC}"
    echo "$EXTERNAL_PRS" | jq -r 'sort_by(.created_at)|.[]|"  \(.url)"'
else
    echo -e "  ${DIM}No open external contributor PRs.${NC}"
fi

echo ""
