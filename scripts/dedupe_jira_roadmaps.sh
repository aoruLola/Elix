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

add_archived_prefix() {
  local s="$1"
  if [[ "$s" == "[归档]"* ]]; then
    printf "%s" "$s"
  else
    printf "[归档] %s" "$s"
  fi
}

merge_archived_label() {
  local labels_json="$1"
  jq -c -n --argjson arr "$labels_json" '($arr // []) + ["archived"] | unique'
}

transition_to_done() {
  local issue_key="$1"
  api_call GET "/rest/api/3/issue/${issue_key}/transitions"
  [[ "$HTTP_STATUS" == "200" ]] || return 0
  local tid
  tid="$(jq -r '.transitions[] | select((.to.statusCategory.key=="done") or (.to.untranslatedName=="Done") or (.to.name=="已完成")) | .id' "$BODY_FILE" | head -n1)"
  [[ -n "$tid" ]] || return 0
  local payload
  payload="$(jq -n --arg id "$tid" '{transition:{id:$id}}')"
  api_call POST "/rest/api/3/issue/${issue_key}/transitions" "$payload"
  [[ "$HTTP_STATUS" == "204" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to transition ${issue_key} to done"
}

archive_issue() {
  local issue_key="$1"
  api_call GET "/rest/api/3/issue/${issue_key}?fields=summary,labels"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load issue ${issue_key}"
  local summary labels new_summary new_labels payload
  summary="$(jq -r '.fields.summary // empty' "$BODY_FILE")"
  labels="$(jq -c '.fields.labels // []' "$BODY_FILE")"
  new_summary="$(add_archived_prefix "$summary")"
  new_labels="$(merge_archived_label "$labels")"
  payload="$(jq -n --arg s "$new_summary" --argjson l "$new_labels" '{fields:{summary:$s,labels:$l}}')"
  api_call PUT "/rest/api/3/issue/${issue_key}" "$payload"
  [[ "$HTTP_STATUS" == "204" ]] || fail_with_body "failed to archive-update ${issue_key}"
  transition_to_done "$issue_key"
}

collect_descendants() {
  local project_key="$1"
  local root="$2"
  local -a queue=("$root")
  declare -A seen=()
  local -a out=()

  while [[ "${#queue[@]}" -gt 0 ]]; do
    local current="${queue[0]}"
    queue=("${queue[@]:1}")
    local jql="project = ${project_key} AND parent = ${current} ORDER BY created ASC"
    local enc
    enc="$(uri_encode "$jql")"
    api_call GET "/rest/api/3/search/jql?jql=${enc}&fields=none&maxResults=500"
    [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to list children of ${current}"
    while IFS= read -r child; do
      [[ -z "$child" ]] && continue
      if [[ -z "${seen[$child]:-}" ]]; then
        seen["$child"]=1
        out+=("$child")
        queue+=("$child")
      fi
    done < <(jq -r '.issues[].key' "$BODY_FILE")
  done
  printf "%s\n" "${out[@]}"
}

update_board_filter() {
  local project_key="$1"
  api_call GET "/rest/agile/1.0/board?projectKeyOrId=${project_key}&maxResults=50"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to query board for ${project_key}"
  local board_id
  board_id="$(jq -r '.values[0].id // empty' "$BODY_FILE")"
  [[ -n "$board_id" ]] || return 0

  api_call GET "/rest/agile/1.0/board/${board_id}/configuration"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load board config for ${project_key}"
  local filter_id
  filter_id="$(jq -r '.filter.id // empty' "$BODY_FILE")"
  [[ -n "$filter_id" ]] || return 0

  api_call GET "/rest/api/3/filter/${filter_id}"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load filter ${filter_id}"
  local name desc new_jql payload
  name="$(jq -r '.name' "$BODY_FILE")"
  desc="$(jq -r '.description // ""' "$BODY_FILE")"
  new_jql="project = ${project_key} AND issuetype != Sub-task AND (labels is EMPTY OR labels not in (archived)) ORDER BY created DESC"
  payload="$(jq -n --arg n "$name" --arg d "$desc" --arg q "$new_jql" '{name:$n,description:$d,jql:$q}')"
  api_call PUT "/rest/api/3/filter/${filter_id}" "$payload"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to update filter ${filter_id}"
}

dedupe_project() {
  local project_key="$1"
  echo "==> dedupe ${project_key}"

  local jql enc
  jql="project = ${project_key} AND issuetype = Epic ORDER BY key ASC"
  enc="$(uri_encode "$jql")"
  api_call GET "/rest/api/3/search/jql?jql=${enc}&fields=summary&maxResults=200"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to list epics for ${project_key}"

  declare -a EPICS=()
  while IFS= read -r k; do
    [[ -z "$k" ]] && continue
    EPICS+=("$k")
  done < <(jq -r '.issues[].key' "$BODY_FILE")

  if [[ "${#EPICS[@]}" -le 1 ]]; then
    update_board_filter "$project_key"
    return 0
  fi

  local keep=""
  local keep_count="-1"
  for e in "${EPICS[@]}"; do
    local c
    c="$(collect_descendants "$project_key" "$e" | sed '/^$/d' | wc -l | tr -d ' ')"
    if [[ "$c" -gt "$keep_count" ]]; then
      keep="$e"
      keep_count="$c"
    elif [[ "$c" -eq "$keep_count" && "$e" < "$keep" ]]; then
      keep="$e"
    fi
  done
  echo "keep epic: ${keep}"

  for e in "${EPICS[@]}"; do
    if [[ "$e" == "$keep" ]]; then
      continue
    fi
    while IFS= read -r child; do
      [[ -z "$child" ]] && continue
      archive_issue "$child"
    done < <(collect_descendants "$project_key" "$e")
    archive_issue "$e"
    echo "archived duplicate epic: ${e}"
  done

  update_board_filter "$project_key"
}

require_cmd jq
require_cmd curl
require_env JIRA_BASE_URL
require_env JIRA_EMAIL
require_env JIRA_API_TOKEN

JIRA_BASE_URL="${JIRA_BASE_URL%/}"
PROJECT_KEYS="${PROJECT_KEYS:-EHC,OMC,NRP,POC,CMC}"
IFS=',' read -r -a KEYS <<< "$PROJECT_KEYS"
for key in "${KEYS[@]}"; do
  [[ -z "$key" ]] && continue
  dedupe_project "$key"
done
echo "Done."
