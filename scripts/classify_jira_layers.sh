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

strip_layer_prefix() {
  local s="$1"
  s="$(printf "%s" "$s" | sed -E 's/^\[(路线图|里程碑|计划|任务|执行)\][[:space:]]*//')"
  printf "%s" "$s"
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
  local title="$1"
  echo "ERROR: ${title} (HTTP ${HTTP_STATUS})"
  if [[ -s "$BODY_FILE" ]]; then
    cat "$BODY_FILE"
    echo
  fi
  exit 1
}

merge_labels_json() {
  local current_labels_json="$1"
  local add_label="$2"
  local remove_csv="$3"
  jq -c -n \
    --argjson current "$current_labels_json" \
    --arg add "$add_label" \
    --arg remove_csv "$remove_csv" '
      ($remove_csv | split(",") | map(select(length > 0))) as $remove
      | ($current // [])
      | map(select((. as $x | $remove | index($x)) | not))
      | . + [$add]
      | unique
    '
}

update_issue_summary_and_labels() {
  local issue_key="$1"
  local summary="$2"
  local add_label="$3"
  local remove_labels_csv="$4"

  api_call GET "/rest/api/3/issue/${issue_key}?fields=summary,labels"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to read issue ${issue_key}"

  local current_summary
  current_summary="$(jq -r '.fields.summary // empty' "$BODY_FILE")"
  local current_labels_json
  current_labels_json="$(jq -c '.fields.labels // []' "$BODY_FILE")"
  local merged_labels_json
  merged_labels_json="$(merge_labels_json "$current_labels_json" "$add_label" "$remove_labels_csv")"

  if [[ "$current_summary" == "$summary" && "$(jq -c <<<"$current_labels_json")" == "$(jq -c <<<"$merged_labels_json")" ]]; then
    return 0
  fi

  local payload
  payload="$(jq -n --arg summary "$summary" --argjson labels "$merged_labels_json" '{fields:{summary:$summary,labels:$labels}}')"
  api_call PUT "/rest/api/3/issue/${issue_key}" "$payload"
  [[ "$HTTP_STATUS" == "204" ]] || fail_with_body "failed to update issue ${issue_key}"
}

create_milestone_story() {
  local epic_key="$1"
  local summary="$2"

  local payload
  payload="$(jq -n \
    --arg projectKey "$JIRA_PROJECT_KEY" \
    --arg storyTypeId "$STORY_TYPE_ID" \
    --arg title "$summary" \
    --arg parentKey "$epic_key" \
    '{
      fields: {
        project: {key: $projectKey},
        issuetype: {id: $storyTypeId},
        summary: $title,
        parent: {key: $parentKey},
        labels: ["layer_milestone"]
      }
    }'
  )"

  api_call POST "/rest/api/3/issue" "$payload"
  [[ "$HTTP_STATUS" == "201" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to create milestone under ${epic_key}"
  jq -r '.key // empty' "$BODY_FILE"
}

require_cmd curl
require_cmd jq

require_env JIRA_BASE_URL
require_env JIRA_EMAIL
require_env JIRA_API_TOKEN
require_env JIRA_PROJECT_KEY

JIRA_BASE_URL="${JIRA_BASE_URL%/}"
JIRA_MILESTONES="${JIRA_MILESTONES:-M1 方案确认,M2 MVP 可用,M3 发布上线}"
JIRA_PLAN_KEYWORDS="${JIRA_PLAN_KEYWORDS:-规划,排期,拆解,计划}"

echo "Step 1/6: verifying Jira authentication..."
api_call GET "/rest/api/3/myself"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "unable to authenticate to Jira"

echo "Step 2/6: detecting issue types..."
api_call GET "/rest/api/3/issue/createmeta/${JIRA_PROJECT_KEY}/issuetypes"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to query issue types"

EPIC_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName == "Epic" or .hierarchyLevel == 1)) | .id' "$BODY_FILE" | head -n1)"
STORY_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName == "Story" or .name == "Story" or .name == "故事")) | .id' "$BODY_FILE" | head -n1)"
TASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName == "Task" or .name == "Task" or .name == "任务")) | .id' "$BODY_FILE" | head -n1)"
SUBTASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select(.subtask == true) | .id' "$BODY_FILE" | head -n1)"

if [[ -z "$EPIC_TYPE_ID" || -z "$STORY_TYPE_ID" || -z "$TASK_TYPE_ID" || -z "$SUBTASK_TYPE_ID" ]]; then
  fail_with_body "missing Epic/Story/Task/Sub-task type id"
fi

echo "Step 3/6: loading epics..."
EPIC_JQL="project = \"${JIRA_PROJECT_KEY}\" AND issuetype = ${EPIC_TYPE_ID} ORDER BY created ASC"
ENC_EPIC_JQL="$(uri_encode "$EPIC_JQL")"
api_call GET "/rest/api/3/search/jql?jql=${ENC_EPIC_JQL}&fields=summary,labels&maxResults=200"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to search epics"

mapfile -t EPIC_ROWS < <(jq -r '.issues[] | [.key, .fields.summary] | @tsv' "$BODY_FILE")
if [[ "${#EPIC_ROWS[@]}" -eq 0 ]]; then
  echo "No epics found in ${JIRA_PROJECT_KEY}. Nothing to classify."
  exit 0
fi

IFS=',' read -r -a MILESTONE_LIST <<< "$JIRA_MILESTONES"
IFS=',' read -r -a PLAN_KEYWORDS <<< "$JIRA_PLAN_KEYWORDS"

ROADMAP_UPDATED=0
MILESTONE_CREATED=0
MILESTONE_UPDATED=0
PLAN_UPDATED=0
TASK_UPDATED=0
EXEC_UPDATED=0

echo "Step 4/6: classifying roadmap(epic) and milestone(story)..."
for row in "${EPIC_ROWS[@]}"; do
  epic_key="$(printf "%s" "$row" | cut -f1)"
  epic_summary_raw="$(printf "%s" "$row" | cut -f2-)"
  base_epic_summary="$(strip_layer_prefix "$epic_summary_raw")"
  target_epic_summary="[路线图] ${base_epic_summary}"
  update_issue_summary_and_labels "$epic_key" "$target_epic_summary" "layer_roadmap" "layer_milestone,layer_plan,layer_task,layer_execution"
  ROADMAP_UPDATED=$((ROADMAP_UPDATED + 1))

  STORY_JQL="project = \"${JIRA_PROJECT_KEY}\" AND parent = ${epic_key} AND issuetype = ${STORY_TYPE_ID} ORDER BY created ASC"
  ENC_STORY_JQL="$(uri_encode "$STORY_JQL")"
  api_call GET "/rest/api/3/search/jql?jql=${ENC_STORY_JQL}&fields=summary,labels&maxResults=200"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load stories under ${epic_key}"

  declare -A STORY_BY_SUMMARY=()
  while IFS=$'\t' read -r s_key s_summary; do
    [[ -z "$s_key" || -z "$s_summary" ]] && continue
    STORY_BY_SUMMARY["$s_summary"]="$s_key"
  done < <(jq -r '.issues[] | [.key, .fields.summary] | @tsv' "$BODY_FILE")

  for raw_ms in "${MILESTONE_LIST[@]}"; do
    ms="$(trim "$raw_ms")"
    [[ -z "$ms" ]] && continue
    ms_summary="[里程碑] ${ms}"

    if [[ -n "${STORY_BY_SUMMARY[$ms_summary]:-}" ]]; then
      s_key="${STORY_BY_SUMMARY[$ms_summary]}"
      update_issue_summary_and_labels "$s_key" "$ms_summary" "layer_milestone" "layer_roadmap,layer_plan,layer_task,layer_execution"
      MILESTONE_UPDATED=$((MILESTONE_UPDATED + 1))
      continue
    fi

    created_key="$(create_milestone_story "$epic_key" "$ms_summary")"
    echo "Milestone created: ${created_key} (parent=${epic_key})"
    MILESTONE_CREATED=$((MILESTONE_CREATED + 1))
  done
done

echo "Step 5/6: classifying plan(task) and execution(sub-task)..."
TASK_JQL="project = \"${JIRA_PROJECT_KEY}\" AND issuetype = ${TASK_TYPE_ID} ORDER BY created ASC"
ENC_TASK_JQL="$(uri_encode "$TASK_JQL")"
api_call GET "/rest/api/3/search/jql?jql=${ENC_TASK_JQL}&fields=summary,labels,parent&maxResults=500"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load tasks"

mapfile -t TASK_ROWS < <(jq -r '.issues[] | [.key, .fields.summary] | @tsv' "$BODY_FILE")
for row in "${TASK_ROWS[@]}"; do
  task_key="$(printf "%s" "$row" | cut -f1)"
  task_summary_raw="$(printf "%s" "$row" | cut -f2-)"
  task_base="$(strip_layer_prefix "$task_summary_raw")"

  is_plan=0
  for kw_raw in "${PLAN_KEYWORDS[@]}"; do
    kw="$(trim "$kw_raw")"
    [[ -z "$kw" ]] && continue
    if [[ "$task_base" == *"$kw"* ]]; then
      is_plan=1
      break
    fi
  done

  if [[ "$is_plan" -eq 1 ]]; then
    target_summary="[计划] ${task_base}"
    update_issue_summary_and_labels "$task_key" "$target_summary" "layer_plan" "layer_roadmap,layer_milestone,layer_task,layer_execution"
    PLAN_UPDATED=$((PLAN_UPDATED + 1))
  else
    target_summary="[任务] ${task_base}"
    update_issue_summary_and_labels "$task_key" "$target_summary" "layer_task" "layer_roadmap,layer_milestone,layer_plan,layer_execution"
    TASK_UPDATED=$((TASK_UPDATED + 1))
  fi
done

SUBTASK_JQL="project = \"${JIRA_PROJECT_KEY}\" AND issuetype = ${SUBTASK_TYPE_ID} ORDER BY created ASC"
ENC_SUBTASK_JQL="$(uri_encode "$SUBTASK_JQL")"
api_call GET "/rest/api/3/search/jql?jql=${ENC_SUBTASK_JQL}&fields=summary,labels,parent&maxResults=1000"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load subtasks"

mapfile -t SUBTASK_ROWS < <(jq -r '.issues[] | [.key, .fields.summary] | @tsv' "$BODY_FILE")
for row in "${SUBTASK_ROWS[@]}"; do
  sub_key="$(printf "%s" "$row" | cut -f1)"
  sub_summary_raw="$(printf "%s" "$row" | cut -f2-)"
  sub_base="$(strip_layer_prefix "$sub_summary_raw")"
  target_summary="[执行] ${sub_base}"
  update_issue_summary_and_labels "$sub_key" "$target_summary" "layer_execution" "layer_roadmap,layer_milestone,layer_plan,layer_task"
  EXEC_UPDATED=$((EXEC_UPDATED + 1))
done

echo "Step 6/6: complete."
echo "- Roadmap(Epic) updated: ${ROADMAP_UPDATED}"
echo "- Milestones(Story) created: ${MILESTONE_CREATED}, updated: ${MILESTONE_UPDATED}"
echo "- Plan(Task) updated: ${PLAN_UPDATED}"
echo "- Task(Task) updated: ${TASK_UPDATED}"
echo "- Execution(Sub-task) updated: ${EXEC_UPDATED}"
