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

BODY_FILE="$(mktemp)"
trap 'rm -f "$BODY_FILE"' EXIT
HTTP_STATUS=""

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
    -o "$BODY_FILE"
    -w "%{http_code}"
  )
  if [[ -n "$data" ]]; then
    args+=(-H "Content-Type: application/json" --data "$data")
  fi
  HTTP_STATUS="$(curl "${args[@]}")"
}

fail_with_body() {
  local msg="$1"
  echo "ERROR: ${msg} (HTTP ${HTTP_STATUS})"
  if [[ -s "$BODY_FILE" ]]; then
    cat "$BODY_FILE"
    echo
  fi
  exit 1
}

require_cmd curl
require_cmd jq

require_env JIRA_BASE_URL
require_env JIRA_EMAIL
require_env JIRA_API_TOKEN
require_env JIRA_PROJECT_KEY

JIRA_BASE_URL="${JIRA_BASE_URL%/}"
JIRA_TASK_TEMPLATES="${JIRA_TASK_TEMPLATES:-项目规划与范围定义,里程碑拆解与排期,执行与推进,测试验收与收尾}"
JIRA_SUBTASK_TEMPLATES="${JIRA_SUBTASK_TEMPLATES:-本周动作,风险与阻塞}"

echo "Step 1/5: verifying Jira authentication..."
api_call GET "/rest/api/3/myself"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "unable to authenticate to Jira"

echo "Step 2/5: detecting issue types..."
api_call GET "/rest/api/3/issue/createmeta/${JIRA_PROJECT_KEY}/issuetypes"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to query project issue types"

EPIC_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.hierarchyLevel == 1)) | .id' "$BODY_FILE" | head -n1)"
TASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName == "Task" or .name == "Task" or .name == "任务")) | .id' "$BODY_FILE" | head -n1)"
SUBTASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select(.subtask == true) | .id' "$BODY_FILE" | head -n1)"

if [[ -z "$TASK_TYPE_ID" ]]; then
  TASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.hierarchyLevel == 0)) | .id' "$BODY_FILE" | head -n1)"
fi

if [[ -z "$EPIC_TYPE_ID" || -z "$TASK_TYPE_ID" || -z "$SUBTASK_TYPE_ID" ]]; then
  fail_with_body "unable to detect Epic/Task/Sub-task issue type ids"
fi

echo "Step 3/5: loading epics..."
EPIC_JQL="project = \"${JIRA_PROJECT_KEY}\" AND issuetype = ${EPIC_TYPE_ID} ORDER BY created ASC"
ENC_EPIC_JQL="$(uri_encode "$EPIC_JQL")"
api_call GET "/rest/api/3/search/jql?jql=${ENC_EPIC_JQL}&fields=summary&maxResults=200"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to query epics"

mapfile -t EPIC_KEYS < <(jq -r '.issues[].key' "$BODY_FILE")
if [[ "${#EPIC_KEYS[@]}" -eq 0 ]]; then
  echo "No epics found in project ${JIRA_PROJECT_KEY}. Nothing to do."
  exit 0
fi

echo "Step 4/5: ensuring task templates under each epic..."
IFS=',' read -r -a TASK_TEMPLATES <<< "$JIRA_TASK_TEMPLATES"
IFS=',' read -r -a SUBTASK_TEMPLATES <<< "$JIRA_SUBTASK_TEMPLATES"

CREATED_TASKS=0
SKIPPED_TASKS=0
CREATED_SUBTASKS=0
SKIPPED_SUBTASKS=0

for epic_key in "${EPIC_KEYS[@]}"; do
  epic_key="$(trim "$epic_key")"
  [[ -z "$epic_key" ]] && continue

  TASK_JQL="project = \"${JIRA_PROJECT_KEY}\" AND parent = ${epic_key} ORDER BY created ASC"
  ENC_TASK_JQL="$(uri_encode "$TASK_JQL")"
  api_call GET "/rest/api/3/search/jql?jql=${ENC_TASK_JQL}&fields=summary&maxResults=200"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to query child tasks for ${epic_key}"

  declare -A TASK_BY_SUMMARY=()
  while IFS=$'\t' read -r task_key task_summary; do
    [[ -z "$task_key" || -z "$task_summary" ]] && continue
    TASK_BY_SUMMARY["$task_summary"]="$task_key"
  done < <(jq -r '.issues[] | [.key, .fields.summary] | @tsv' "$BODY_FILE")

  declare -a TARGET_TASK_KEYS=()
  for raw_task in "${TASK_TEMPLATES[@]}"; do
    task_summary="$(trim "$raw_task")"
    [[ -z "$task_summary" ]] && continue

    if [[ -n "${TASK_BY_SUMMARY[$task_summary]:-}" ]]; then
      task_key="${TASK_BY_SUMMARY[$task_summary]}"
      echo "Task exists: ${task_key} - ${task_summary} (parent=${epic_key})"
      TARGET_TASK_KEYS+=("$task_key")
      SKIPPED_TASKS=$((SKIPPED_TASKS + 1))
      continue
    fi

    TASK_PAYLOAD="$(jq -n \
      --arg projectKey "$JIRA_PROJECT_KEY" \
      --arg issueTypeId "$TASK_TYPE_ID" \
      --arg summary "$task_summary" \
      --arg parentKey "$epic_key" \
      '{
        fields: {
          project: {key: $projectKey},
          issuetype: {id: $issueTypeId},
          summary: $summary,
          parent: {key: $parentKey}
        }
      }'
    )"
    api_call POST "/rest/api/3/issue" "$TASK_PAYLOAD"
    [[ "$HTTP_STATUS" == "201" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to create task ${task_summary} under ${epic_key}"
    task_key="$(jq -r '.key // empty' "$BODY_FILE")"
    echo "Task created: ${task_key} - ${task_summary} (parent=${epic_key})"
    TARGET_TASK_KEYS+=("$task_key")
    CREATED_TASKS=$((CREATED_TASKS + 1))
  done

  for task_key in "${TARGET_TASK_KEYS[@]}"; do
    SUBTASK_JQL="project = \"${JIRA_PROJECT_KEY}\" AND parent = ${task_key} ORDER BY created ASC"
    ENC_SUBTASK_JQL="$(uri_encode "$SUBTASK_JQL")"
    api_call GET "/rest/api/3/search/jql?jql=${ENC_SUBTASK_JQL}&fields=summary&maxResults=200"
    [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to query subtasks for ${task_key}"

    declare -A SUBTASK_BY_SUMMARY=()
    while IFS=$'\t' read -r subtask_key subtask_summary; do
      [[ -z "$subtask_key" || -z "$subtask_summary" ]] && continue
      SUBTASK_BY_SUMMARY["$subtask_summary"]="$subtask_key"
    done < <(jq -r '.issues[] | [.key, .fields.summary] | @tsv' "$BODY_FILE")

    for raw_subtask in "${SUBTASK_TEMPLATES[@]}"; do
      subtask_summary="$(trim "$raw_subtask")"
      [[ -z "$subtask_summary" ]] && continue

      if [[ -n "${SUBTASK_BY_SUMMARY[$subtask_summary]:-}" ]]; then
        echo "Sub-task exists: ${SUBTASK_BY_SUMMARY[$subtask_summary]} - ${subtask_summary} (parent=${task_key})"
        SKIPPED_SUBTASKS=$((SKIPPED_SUBTASKS + 1))
        continue
      fi

      SUBTASK_PAYLOAD="$(jq -n \
        --arg projectKey "$JIRA_PROJECT_KEY" \
        --arg issueTypeId "$SUBTASK_TYPE_ID" \
        --arg summary "$subtask_summary" \
        --arg parentKey "$task_key" \
        '{
          fields: {
            project: {key: $projectKey},
            issuetype: {id: $issueTypeId},
            summary: $summary,
            parent: {key: $parentKey}
          }
        }'
      )"
      api_call POST "/rest/api/3/issue" "$SUBTASK_PAYLOAD"
      [[ "$HTTP_STATUS" == "201" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to create sub-task ${subtask_summary} under ${task_key}"
      echo "Sub-task created: $(jq -r '.key // empty' "$BODY_FILE") - ${subtask_summary} (parent=${task_key})"
      CREATED_SUBTASKS=$((CREATED_SUBTASKS + 1))
    done
  done
done

echo "Step 5/5: done."
echo "Tasks: created=${CREATED_TASKS}, skipped=${SKIPPED_TASKS}"
echo "Sub-tasks: created=${CREATED_SUBTASKS}, skipped=${SKIPPED_SUBTASKS}"
