#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "ERROR: missing required command: $cmd"
    exit 1
  fi
}

require_env() {
  local key="$1"
  if [[ -z "${!key:-}" ]]; then
    echo "ERROR: required env var is missing: $key"
    exit 1
  fi
}

trim() {
  local s="$1"
  s="${s#"${s%%[![:space:]]*}"}"
  s="${s%"${s##*[![:space:]]}"}"
  printf "%s" "$s"
}

uri_encode() {
  jq -nr --arg v "$1" '$v|@uri'
}

API_BODY_FILE="$(mktemp)"
trap 'rm -f "$API_BODY_FILE"' EXIT
API_STATUS=""

api_call() {
  local method="$1"
  local path="$2"
  local data="${3:-}"
  local url="${JIRA_BASE_URL}${path}"
  local args=(
    -sS
    -u "${JIRA_EMAIL}:${JIRA_API_TOKEN}"
    -H "Accept: application/json"
    -X "$method"
    "$url"
    -o "$API_BODY_FILE"
    -w "%{http_code}"
  )

  if [[ -n "$data" ]]; then
    args+=(-H "Content-Type: application/json" --data "$data")
  fi

  API_STATUS="$(curl "${args[@]}")"
}

print_api_error() {
  local title="$1"
  echo "ERROR: $title (HTTP $API_STATUS)"
  if [[ -s "$API_BODY_FILE" ]]; then
    cat "$API_BODY_FILE"
    echo
  fi
}

require_cmd curl
require_cmd jq

require_env JIRA_BASE_URL
require_env JIRA_EMAIL
require_env JIRA_API_TOKEN
require_env JIRA_PROJECT_KEY
require_env JIRA_PROJECT_NAME

JIRA_BASE_URL="${JIRA_BASE_URL%/}"
JIRA_PROJECT_TYPE_KEY="${JIRA_PROJECT_TYPE_KEY:-software}"
JIRA_PROJECT_TEMPLATE_KEY="${JIRA_PROJECT_TEMPLATE_KEY:-com.pyxis.greenhopper.jira:gh-kanban-template}"
JIRA_PROJECT_DESCRIPTION="${JIRA_PROJECT_DESCRIPTION:-Managed by setup_jira_minimal.sh}"
JIRA_FILTER_NAME="${JIRA_FILTER_NAME:-${JIRA_PROJECT_KEY} Progress Filter}"
JIRA_FILTER_JQL="${JIRA_FILTER_JQL:-project = ${JIRA_PROJECT_KEY} AND issuetype not in subTaskIssueTypes() ORDER BY Rank ASC, created DESC}"
JIRA_BOARD_NAME="${JIRA_BOARD_NAME:-${JIRA_PROJECT_KEY} Kanban}"

if [[ "$JIRA_BASE_URL" != https://* ]]; then
  echo "ERROR: JIRA_BASE_URL must start with https:// (example: https://your-domain.atlassian.net)"
  exit 1
fi

echo "Step 1/6: verifying Jira authentication..."
api_call GET "/rest/api/3/myself"
if [[ "$API_STATUS" != "200" ]]; then
  print_api_error "unable to authenticate to Jira"
  exit 1
fi

ACCOUNT_ID="$(jq -r '.accountId // empty' "$API_BODY_FILE")"
DISPLAY_NAME="$(jq -r '.displayName // empty' "$API_BODY_FILE")"

if [[ -z "$ACCOUNT_ID" ]]; then
  echo "ERROR: authenticated, but accountId is empty."
  exit 1
fi

echo "Authenticated as: ${DISPLAY_NAME:-unknown} (${ACCOUNT_ID})"

echo "Step 2/6: ensuring Jira project exists..."
api_call GET "/rest/api/3/project/${JIRA_PROJECT_KEY}"
if [[ "$API_STATUS" == "200" ]]; then
  PROJECT_ID="$(jq -r '.id // empty' "$API_BODY_FILE")"
  PROJECT_KEY="$(jq -r '.key // empty' "$API_BODY_FILE")"
  echo "Project exists: ${PROJECT_KEY} (id=${PROJECT_ID})"
elif [[ "$API_STATUS" == "404" ]]; then
  PROJECT_PAYLOAD="$(jq -n \
    --arg key "$JIRA_PROJECT_KEY" \
    --arg name "$JIRA_PROJECT_NAME" \
    --arg desc "$JIRA_PROJECT_DESCRIPTION" \
    --arg lead "$ACCOUNT_ID" \
    --arg type "$JIRA_PROJECT_TYPE_KEY" \
    --arg tpl "$JIRA_PROJECT_TEMPLATE_KEY" \
    '{
      key: $key,
      name: $name,
      description: $desc,
      leadAccountId: $lead,
      projectTypeKey: $type,
      projectTemplateKey: $tpl
    }'
  )"

  api_call POST "/rest/api/3/project" "$PROJECT_PAYLOAD"
  if [[ "$API_STATUS" != "201" && "$API_STATUS" != "200" ]]; then
    print_api_error "failed to create project"
    exit 1
  fi
  PROJECT_ID="$(jq -r '.id // empty' "$API_BODY_FILE")"
  PROJECT_KEY="$(jq -r '.key // empty' "$API_BODY_FILE")"
  echo "Project created: ${PROJECT_KEY} (id=${PROJECT_ID})"
else
  print_api_error "failed to query project"
  exit 1
fi

echo "Step 3/6: ensuring Jira filter exists..."
ENC_FILTER_NAME="$(uri_encode "$JIRA_FILTER_NAME")"
api_call GET "/rest/api/3/filter/search?filterName=${ENC_FILTER_NAME}&expand=owner"
if [[ "$API_STATUS" != "200" ]]; then
  print_api_error "failed to search filters"
  exit 1
fi

FILTER_ID="$(jq -r --arg name "$JIRA_FILTER_NAME" '.values[] | select(.name == $name) | .id' "$API_BODY_FILE" | head -n1)"
if [[ -z "$FILTER_ID" ]]; then
  FILTER_PAYLOAD="$(jq -n \
    --arg name "$JIRA_FILTER_NAME" \
    --arg jql "$JIRA_FILTER_JQL" \
    --arg desc "Filter for ${JIRA_PROJECT_KEY} kanban board" \
    '{name: $name, jql: $jql, description: $desc}'
  )"
  api_call POST "/rest/api/3/filter" "$FILTER_PAYLOAD"
  if [[ "$API_STATUS" != "201" && "$API_STATUS" != "200" ]]; then
    print_api_error "failed to create filter"
    exit 1
  fi
  FILTER_ID="$(jq -r '.id // empty' "$API_BODY_FILE")"
  echo "Filter created: ${JIRA_FILTER_NAME} (id=${FILTER_ID})"
else
  echo "Filter exists: ${JIRA_FILTER_NAME} (id=${FILTER_ID})"
fi

echo "Step 4/6: ensuring kanban board exists..."
ENC_BOARD_NAME="$(uri_encode "$JIRA_BOARD_NAME")"
api_call GET "/rest/agile/1.0/board?projectKeyOrId=${JIRA_PROJECT_KEY}&name=${ENC_BOARD_NAME}&maxResults=50"
if [[ "$API_STATUS" != "200" ]]; then
  print_api_error "failed to query boards"
  exit 1
fi

BOARD_ID="$(jq -r --arg name "$JIRA_BOARD_NAME" '.values[] | select(.name == $name) | .id' "$API_BODY_FILE" | head -n1)"
BOARD_SELF="$(jq -r --arg name "$JIRA_BOARD_NAME" '.values[] | select(.name == $name) | .self' "$API_BODY_FILE" | head -n1)"
if [[ -z "$BOARD_ID" ]]; then
  BOARD_PAYLOAD="$(jq -n \
    --arg name "$JIRA_BOARD_NAME" \
    --arg key "$JIRA_PROJECT_KEY" \
    --arg fid "$FILTER_ID" \
    '{
      name: $name,
      type: "kanban",
      filterId: ($fid | tonumber),
      location: {type: "project", projectKeyOrId: $key}
    }'
  )"

  api_call POST "/rest/agile/1.0/board" "$BOARD_PAYLOAD"
  if [[ "$API_STATUS" != "201" && "$API_STATUS" != "200" ]]; then
    print_api_error "failed to create board"
    exit 1
  fi
  BOARD_ID="$(jq -r '.id // empty' "$API_BODY_FILE")"
  BOARD_SELF="$(jq -r '.self // empty' "$API_BODY_FILE")"
  echo "Board created: ${JIRA_BOARD_NAME} (id=${BOARD_ID})"
else
  echo "Board exists: ${JIRA_BOARD_NAME} (id=${BOARD_ID})"
fi

echo "Step 5/6: checking issue hierarchy (Epic/Task/Sub-task)..."
api_call GET "/rest/api/3/issue/createmeta/${JIRA_PROJECT_KEY}/issuetypes"
if [[ "$API_STATUS" != "200" ]]; then
  print_api_error "failed to query project issue types"
  exit 1
fi

EPIC_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.hierarchyLevel == 1)) | .id' "$API_BODY_FILE" | head -n1)"
EPIC_TYPE_NAME="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.hierarchyLevel == 1)) | .name' "$API_BODY_FILE" | head -n1)"
TASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName == "Task" or .name == "Task" or .name == "任务")) | .id' "$API_BODY_FILE" | head -n1)"
TASK_TYPE_NAME="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName == "Task" or .name == "Task" or .name == "任务")) | .name' "$API_BODY_FILE" | head -n1)"
SUBTASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select(.subtask == true) | .id' "$API_BODY_FILE" | head -n1)"
SUBTASK_TYPE_NAME="$(jq -r '(.values // .issueTypes // [])[] | select(.subtask == true) | .name' "$API_BODY_FILE" | head -n1)"

if [[ -z "$TASK_TYPE_ID" ]]; then
  TASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.hierarchyLevel == 0)) | .id' "$API_BODY_FILE" | head -n1)"
  TASK_TYPE_NAME="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.hierarchyLevel == 0)) | .name' "$API_BODY_FILE" | head -n1)"
fi

if [[ -z "$EPIC_TYPE_ID" || -z "$TASK_TYPE_ID" || -z "$SUBTASK_TYPE_ID" ]]; then
  echo "ERROR: failed to detect full hierarchy in this project."
  echo "Detected issue types:"
  jq -r '(.values // .issueTypes // [])[] | "- \(.name) (id=\(.id), subtask=\(.subtask), hierarchyLevel=\(.hierarchyLevel))"' "$API_BODY_FILE"
  exit 1
fi

echo "Hierarchy detected:"
echo "- Epic: ${EPIC_TYPE_NAME} (id=${EPIC_TYPE_ID})"
echo "- Task: ${TASK_TYPE_NAME} (id=${TASK_TYPE_ID})"
echo "- Sub-task: ${SUBTASK_TYPE_NAME} (id=${SUBTASK_TYPE_ID})"

echo "Step 6/6: optional Epic initialization..."
if [[ -n "${JIRA_EPICS:-}" ]]; then
  api_call GET "/rest/api/3/field"
  if [[ "$API_STATUS" != "200" ]]; then
    print_api_error "failed to load fields"
    exit 1
  fi

  EPIC_NAME_FIELD_ID="$(jq -r '.[] | select((.schema.custom // "") | contains("epic-label")) | .id' "$API_BODY_FILE" | head -n1)"

  SEARCH_JQL="project = \"${JIRA_PROJECT_KEY}\" AND issuetype = ${EPIC_TYPE_ID} ORDER BY created DESC"
  ENC_SEARCH_JQL="$(uri_encode "$SEARCH_JQL")"
  api_call GET "/rest/api/3/search/jql?jql=${ENC_SEARCH_JQL}&fields=summary&maxResults=200"
  if [[ "$API_STATUS" != "200" ]]; then
    print_api_error "failed to load existing epics"
    exit 1
  fi

  declare -A EXISTING_EPICS=()
  while IFS=$'\t' read -r summary key; do
    [[ -z "$summary" || -z "$key" ]] && continue
    EXISTING_EPICS["$summary"]="$key"
  done < <(jq -r '.issues[] | [.fields.summary, .key] | @tsv' "$API_BODY_FILE")

  IFS=',' read -r -a EPIC_LIST <<< "$JIRA_EPICS"
  CREATED_COUNT=0
  SKIPPED_COUNT=0
  for raw in "${EPIC_LIST[@]}"; do
    epic="$(trim "$raw")"
    [[ -z "$epic" ]] && continue

    if [[ -n "${EXISTING_EPICS[$epic]:-}" ]]; then
      echo "Epic exists, skip: ${EXISTING_EPICS[$epic]} - $epic"
      SKIPPED_COUNT=$((SKIPPED_COUNT + 1))
      continue
    fi

    ISSUE_PAYLOAD="$(jq -n \
      --arg key "$JIRA_PROJECT_KEY" \
      --arg issueTypeId "$EPIC_TYPE_ID" \
      --arg summary "$epic" \
      --arg epicField "$EPIC_NAME_FIELD_ID" \
      '{
        fields: {
          project: {key: $key},
          issuetype: {id: $issueTypeId},
          summary: $summary
        }
      }
      | if $epicField != "" then .fields += {($epicField): $summary} else . end'
    )"

    api_call POST "/rest/api/3/issue" "$ISSUE_PAYLOAD"
    if [[ "$API_STATUS" != "201" && "$API_STATUS" != "200" ]]; then
      print_api_error "failed to create epic: $epic"
      exit 1
    fi

    issue_key="$(jq -r '.key // empty' "$API_BODY_FILE")"
    echo "Epic created: ${issue_key} - $epic"
    CREATED_COUNT=$((CREATED_COUNT + 1))
  done
  echo "Epic init done: created=${CREATED_COUNT}, skipped=${SKIPPED_COUNT}"
else
  echo "JIRA_EPICS is empty; skip Epic initialization."
fi

PROJECT_URL="${JIRA_BASE_URL}/jira/software/projects/${JIRA_PROJECT_KEY}/summary"
BOARD_URL="${JIRA_BASE_URL}/jira/software/projects/${JIRA_PROJECT_KEY}/boards/${BOARD_ID}"

echo
echo "Setup complete."
echo "- Project URL: ${PROJECT_URL}"
echo "- Board URL: ${BOARD_URL}"
echo
echo "Recommended next UI step:"
echo "- Add a 'Blocked' column on the board and map it to a matching status."
