#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  local cmd="$1"
  command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: missing command: $cmd"; exit 1; }
}

require_env() {
  local key="$1"
  [[ -n "${!key:-}" ]] || { echo "ERROR: missing env: $key"; exit 1; }
}

uri_encode() {
  jq -nr --arg v "$1" '$v|@uri'
}

adf_doc() {
  local text="$1"
  jq -c -n --arg t "$text" '{type:"doc",version:1,content:[{type:"paragraph",content:[{type:"text",text:$t}]}]}'
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
  echo "ERROR: $msg (HTTP $HTTP_STATUS)"
  if [[ -s "$BODY_FILE" ]]; then
    cat "$BODY_FILE"
    echo
  fi
  exit 1
}

find_issue_key_by_summary() {
  local project_key="$1"
  local issue_type_id="$2"
  local summary="$3"
  local parent_key="${4:-}"
  local jql
  if [[ -n "$parent_key" ]]; then
    jql="project = \"${project_key}\" AND parent = ${parent_key} AND issuetype = ${issue_type_id} ORDER BY created ASC"
  else
    jql="project = \"${project_key}\" AND issuetype = ${issue_type_id} ORDER BY created ASC"
  fi
  local enc
  enc="$(uri_encode "$jql")"
  api_call GET "/rest/api/3/search/jql?jql=${enc}&fields=summary&maxResults=200"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to query issues in ${project_key}"
  jq -r --arg s "$summary" '.issues[] | select(.fields.summary == $s) | .key' "$BODY_FILE" | head -n1
}

ensure_issue() {
  local project_key="$1"
  local issue_type_id="$2"
  local summary="$3"
  local labels_csv="$4"
  local description="$5"
  local parent_key="${6:-}"

  local exists
  exists="$(find_issue_key_by_summary "$project_key" "$issue_type_id" "$summary" "$parent_key")"
  if [[ -n "$exists" ]]; then
    echo "$exists"
    return 0
  fi

  local labels_json
  if [[ -n "$labels_csv" ]]; then
    labels_json="$(jq -c -n --arg v "$labels_csv" '$v | split(",") | map(select(length > 0))')"
  else
    labels_json="[]"
  fi
  local desc_doc
  desc_doc="$(adf_doc "$description")"

  local payload
  if [[ -n "$parent_key" ]]; then
    payload="$(jq -n \
      --arg projectKey "$project_key" \
      --arg issueTypeId "$issue_type_id" \
      --arg summary "$summary" \
      --arg parentKey "$parent_key" \
      --argjson labels "$labels_json" \
      --argjson desc "$desc_doc" \
      '{
        fields:{
          project:{key:$projectKey},
          issuetype:{id:$issueTypeId},
          summary:$summary,
          parent:{key:$parentKey},
          labels:$labels,
          description:$desc
        }
      }'
    )"
  else
    payload="$(jq -n \
      --arg projectKey "$project_key" \
      --arg issueTypeId "$issue_type_id" \
      --arg summary "$summary" \
      --argjson labels "$labels_json" \
      --argjson desc "$desc_doc" \
      '{
        fields:{
          project:{key:$projectKey},
          issuetype:{id:$issueTypeId},
          summary:$summary,
          labels:$labels,
          description:$desc
        }
      }'
    )"
  fi

  api_call POST "/rest/api/3/issue" "$payload"
  [[ "$HTTP_STATUS" == "201" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to create issue ${summary} in ${project_key}"
  jq -r '.key // empty' "$BODY_FILE"
}

transition_to_selected() {
  local issue_key="$1"
  api_call GET "/rest/api/3/issue/${issue_key}/transitions"
  [[ "$HTTP_STATUS" == "200" ]] || return 0
  local tid
  tid="$(jq -r '.transitions[] | select((.to.untranslatedName=="Selected for Development") or (.to.name=="Selected for Development")) | .id' "$BODY_FILE" | head -n1)"
  [[ -n "$tid" ]] || return 0
  local payload
  payload="$(jq -n --arg id "$tid" '{transition:{id:$id}}')"
  api_call POST "/rest/api/3/issue/${issue_key}/transitions" "$payload"
  [[ "$HTTP_STATUS" == "204" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to transition ${issue_key} to Selected for Development"
}

transition_to_in_progress() {
  local issue_key="$1"
  api_call GET "/rest/api/3/issue/${issue_key}/transitions"
  [[ "$HTTP_STATUS" == "200" ]] || return 0
  local tid
  tid="$(jq -r '.transitions[] | select((.to.statusCategory.key=="indeterminate") or (.to.untranslatedName=="In Progress") or (.to.name=="正在进行")) | .id' "$BODY_FILE" | head -n1)"
  [[ -n "$tid" ]] || return 0
  local payload
  payload="$(jq -n --arg id "$tid" '{transition:{id:$id}}')"
  api_call POST "/rest/api/3/issue/${issue_key}/transitions" "$payload"
  [[ "$HTTP_STATUS" == "204" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to transition ${issue_key} to In Progress"
}

setup_one_project() {
  local key="$1"
  local name="$2"
  local active_flag="$3"

  echo "==> setup project: ${key} (${name})"
  JIRA_PROJECT_KEY="$key" \
  JIRA_PROJECT_NAME="$name" \
  JIRA_FILTER_JQL="project = ${key} AND issuetype not in subTaskIssueTypes() ORDER BY created DESC" \
  JIRA_EPICS="[路线图] ${name}" \
  ./scripts/setup_jira_minimal.sh >/tmp/jira_setup_"$key".log 2>&1 || {
    cat /tmp/jira_setup_"$key".log
    echo "ERROR: setup_jira_minimal.sh failed for ${key}"
    exit 1
  }

  api_call GET "/rest/api/3/issue/createmeta/${key}/issuetypes"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load issue types for ${key}"
  local epic_type_id story_type_id task_type_id subtask_type_id
  epic_type_id="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName=="Epic" or .hierarchyLevel==1)) | .id' "$BODY_FILE" | head -n1)"
  story_type_id="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName=="Story" or .name=="Story" or .name=="故事")) | .id' "$BODY_FILE" | head -n1)"
  task_type_id="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName=="Task" or .name=="Task" or .name=="任务")) | .id' "$BODY_FILE" | head -n1)"
  subtask_type_id="$(jq -r '(.values // .issueTypes // [])[] | select(.subtask==true) | .id' "$BODY_FILE" | head -n1)"
  [[ -n "$epic_type_id" && -n "$story_type_id" && -n "$task_type_id" && -n "$subtask_type_id" ]] || fail_with_body "missing issue type ids in ${key}"

  local epic_key
  epic_key="$(ensure_issue "$key" "$epic_type_id" "[路线图] ${name}" "layer_roadmap" "项目主路线图：定义范围、节奏与交付目标。")"

  local m1 m2 m3
  m1="$(ensure_issue "$key" "$story_type_id" "[里程碑] M1 范围冻结" "layer_milestone" "完成需求基线、接口清单、验收标准。" "$epic_key")"
  m2="$(ensure_issue "$key" "$story_type_id" "[里程碑] M2 MVP 可用" "layer_milestone" "核心能力可运行、关键流程打通。" "$epic_key")"
  m3="$(ensure_issue "$key" "$story_type_id" "[里程碑] M3 上线验收" "layer_milestone" "完成发布检查、监控告警与回滚预案。" "$epic_key")"
  : "$m1" "$m2" "$m3"

  local plan_key task_key
  plan_key="$(ensure_issue "$key" "$task_type_id" "[计划] 范围与排期" "layer_plan" "拆分本项目周期计划并定义优先级。" "$epic_key")"
  task_key="$(ensure_issue "$key" "$task_type_id" "[任务] 核心功能开发" "layer_task" "完成主流程实现、联调与质量校验。" "$epic_key")"

  ensure_issue "$key" "$subtask_type_id" "[执行] 输出需求清单" "layer_execution" "明确本周交付与验收口径。" "$plan_key" >/dev/null
  ensure_issue "$key" "$subtask_type_id" "[执行] 排期与风险评估" "layer_execution" "识别阻塞项并制定缓解方案。" "$plan_key" >/dev/null
  ensure_issue "$key" "$subtask_type_id" "[执行] 本周开发" "layer_execution" "按优先级完成核心功能编码。" "$task_key" >/dev/null
  ensure_issue "$key" "$subtask_type_id" "[执行] 联调与验收" "layer_execution" "完成联调、回归测试与验收记录。" "$task_key" >/dev/null

  transition_to_selected "$epic_key"
  transition_to_selected "$plan_key"
  transition_to_selected "$task_key"

  if [[ "$active_flag" == "1" ]]; then
    transition_to_in_progress "$epic_key"
    transition_to_in_progress "$plan_key"
  fi
}

require_cmd jq
require_cmd curl

require_env JIRA_BASE_URL
require_env JIRA_EMAIL
require_env JIRA_API_TOKEN

JIRA_BASE_URL="${JIRA_BASE_URL%/}"
SPACE_SPECS="${SPACE_SPECS:-EHC|EchoHelix Client;OMC|OmniCoral Workspace;NRP|NeonRipple Workspace;POC|ProjectOcean Workspace;CMC|ChamiClaw Workspace}"
ACTIVE_PROJECT_KEY="${ACTIVE_PROJECT_KEY:-EHC}"

echo "Verifying Jira auth..."
api_call GET "/rest/api/3/myself"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "unable to authenticate"

IFS=';' read -r -a ITEMS <<< "$SPACE_SPECS"
for item in "${ITEMS[@]}"; do
  [[ -z "$item" ]] && continue
  key="${item%%|*}"
  name="${item#*|}"
  [[ -z "$key" || -z "$name" ]] && continue
  active="0"
  if [[ "$key" == "$ACTIVE_PROJECT_KEY" ]]; then
    active="1"
  fi
  setup_one_project "$key" "$name" "$active"
done

echo "Done. Created/updated spaces:"
for item in "${ITEMS[@]}"; do
  key="${item%%|*}"
  name="${item#*|}"
  [[ -z "$key" || -z "$name" ]] && continue
  echo "- ${key}: ${JIRA_BASE_URL}/jira/software/projects/${key}/boards"
done
