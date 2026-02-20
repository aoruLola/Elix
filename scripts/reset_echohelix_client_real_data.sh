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

trim() {
  local s="$1"
  s="${s#"${s%%[![:space:]]*}"}"
  s="${s%"${s##*[![:space:]]}"}"
  printf "%s" "$s"
}

strip_archive_prefix() {
  local s="$1"
  printf "%s" "$s" | sed -E 's/^\[归档\][[:space:]]*//'
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

find_transition_id() {
  local issue_key="$1"
  local mode="$2" # done | in_progress | selected
  api_call GET "/rest/api/3/issue/${issue_key}/transitions"
  [[ "$HTTP_STATUS" == "200" ]] || return 0
  case "$mode" in
    done)
      jq -r '.transitions[] | select((.to.statusCategory.key=="done") or (.to.untranslatedName=="Done") or (.to.name=="已完成")) | .id' "$BODY_FILE" | head -n1
      ;;
    in_progress)
      jq -r '.transitions[] | select((.to.statusCategory.key=="indeterminate") or (.to.untranslatedName=="In Progress") or (.to.name=="正在进行")) | .id' "$BODY_FILE" | head -n1
      ;;
    selected)
      jq -r '.transitions[] | select((.to.untranslatedName=="Selected for Development") or (.to.name=="Selected for Development")) | .id' "$BODY_FILE" | head -n1
      ;;
    *)
      ;;
  esac
}

transition_issue() {
  local issue_key="$1"
  local mode="$2"
  local tid
  tid="$(find_transition_id "$issue_key" "$mode")"
  [[ -n "$tid" ]] || return 0
  local payload
  payload="$(jq -n --arg id "$tid" '{transition:{id:$id}}')"
  api_call POST "/rest/api/3/issue/${issue_key}/transitions" "$payload"
  [[ "$HTTP_STATUS" == "204" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to transition ${issue_key} to ${mode}"
}

merge_labels() {
  local labels_json="$1"
  local add_label="$2"
  jq -c -n --argjson arr "$labels_json" --arg add "$add_label" '($arr // []) + [$add] | unique'
}

archive_issue() {
  local issue_key="$1"
  api_call GET "/rest/api/3/issue/${issue_key}?fields=summary,labels"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to read issue ${issue_key}"

  local summary
  summary="$(jq -r '.fields.summary // empty' "$BODY_FILE")"
  local labels
  labels="$(jq -c '.fields.labels // []' "$BODY_FILE")"
  local clean
  clean="$(strip_archive_prefix "$summary")"
  local new_summary="[归档] ${clean}"
  local new_labels
  new_labels="$(merge_labels "$labels" "archived")"

  local payload
  payload="$(jq -n --arg s "$new_summary" --argjson l "$new_labels" '{fields:{summary:$s,labels:$l}}')"
  api_call PUT "/rest/api/3/issue/${issue_key}" "$payload"
  [[ "$HTTP_STATUS" == "204" ]] || fail_with_body "failed to archive-update ${issue_key}"

  transition_issue "$issue_key" "done"
}

collect_descendants() {
  local root="$1"
  local -a queue=("$root")
  declare -A seen=()
  local -a out=()

  while [[ "${#queue[@]}" -gt 0 ]]; do
    local current="${queue[0]}"
    queue=("${queue[@]:1}")
    local jql="project = \"${JIRA_PROJECT_KEY}\" AND parent = ${current} ORDER BY created ASC"
    local enc
    enc="$(uri_encode "$jql")"
    api_call GET "/rest/api/3/search/jql?jql=${enc}&fields=none&maxResults=200"
    [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to query children of ${current}"
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

update_issue_core() {
  local issue_key="$1"
  local summary="$2"
  local label="$3"

  api_call GET "/rest/api/3/issue/${issue_key}?fields=labels"
  [[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to read issue ${issue_key}"
  local old_labels
  old_labels="$(jq -c '.fields.labels // []' "$BODY_FILE")"
  local cleaned_labels
  cleaned_labels="$(jq -c -n --argjson arr "$old_labels" '($arr // []) | map(select(. != "archived"))')"
  local merged
  merged="$(merge_labels "$cleaned_labels" "$label")"

  local payload
  payload="$(jq -n --arg s "$summary" --argjson l "$merged" '{fields:{summary:$s,labels:$l}}')"
  api_call PUT "/rest/api/3/issue/${issue_key}" "$payload"
  [[ "$HTTP_STATUS" == "204" ]] || fail_with_body "failed to update issue ${issue_key}"
}

adf_doc() {
  local text="$1"
  jq -c -n --arg t "$text" '{type:"doc",version:1,content:[{type:"paragraph",content:[{type:"text",text:$t}]}]}'
}

create_child_issue() {
  local issue_type_id="$1"
  local parent_key="$2"
  local summary="$3"
  local label="$4"
  local desc="$5"
  local desc_doc
  desc_doc="$(adf_doc "$desc")"

  local payload
  payload="$(jq -n \
    --arg projectKey "$JIRA_PROJECT_KEY" \
    --arg issueTypeId "$issue_type_id" \
    --arg parentKey "$parent_key" \
    --arg summary "$summary" \
    --arg label "$label" \
    --argjson desc "$desc_doc" \
    '{
      fields: {
        project: {key: $projectKey},
        issuetype: {id: $issueTypeId},
        parent: {key: $parentKey},
        summary: $summary,
        labels: [$label],
        description: $desc
      }
    }'
  )"
  api_call POST "/rest/api/3/issue" "$payload"
  [[ "$HTTP_STATUS" == "201" || "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to create issue ${summary}"
  jq -r '.key // empty' "$BODY_FILE"
}

require_cmd jq
require_cmd curl

require_env JIRA_BASE_URL
require_env JIRA_EMAIL
require_env JIRA_API_TOKEN
require_env JIRA_PROJECT_KEY

JIRA_BASE_URL="${JIRA_BASE_URL%/}"
TARGET_NAME="${TARGET_NAME:-EchoHelix}"
TARGET_EPIC_SUMMARY="${TARGET_EPIC_SUMMARY:-[路线图] EchoHelix Client}"

echo "Step 1/8: verifying auth and issue types..."
api_call GET "/rest/api/3/myself"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "unable to authenticate"

api_call GET "/rest/api/3/issue/createmeta/${JIRA_PROJECT_KEY}/issuetypes"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load issue types"
EPIC_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName=="Epic" or .hierarchyLevel==1)) | .id' "$BODY_FILE" | head -n1)"
STORY_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName=="Story" or .name=="故事" or .name=="Story")) | .id' "$BODY_FILE" | head -n1)"
TASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select((.subtask | not) and (.untranslatedName=="Task" or .name=="任务" or .name=="Task")) | .id' "$BODY_FILE" | head -n1)"
SUBTASK_TYPE_ID="$(jq -r '(.values // .issueTypes // [])[] | select(.subtask==true) | .id' "$BODY_FILE" | head -n1)"
[[ -n "$EPIC_TYPE_ID" && -n "$STORY_TYPE_ID" && -n "$TASK_TYPE_ID" && -n "$SUBTASK_TYPE_ID" ]] || fail_with_body "failed to detect type ids"

echo "Step 2/8: locating target epic..."
TARGET_JQL="project = \"${JIRA_PROJECT_KEY}\" AND issuetype = ${EPIC_TYPE_ID} AND summary ~ \"${TARGET_NAME}\" ORDER BY created ASC"
TARGET_ENC="$(uri_encode "$TARGET_JQL")"
api_call GET "/rest/api/3/search/jql?jql=${TARGET_ENC}&fields=summary,labels&maxResults=20"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to find target epic"
TARGET_EPIC_KEY="$(jq -r '.issues[0].key // empty' "$BODY_FILE")"
if [[ -z "$TARGET_EPIC_KEY" ]]; then
  echo "ERROR: target epic matching '${TARGET_NAME}' not found."
  exit 1
fi
echo "Target epic: ${TARGET_EPIC_KEY}"

echo "Step 3/8: archiving non-target projects..."
OTHER_JQL="project = \"${JIRA_PROJECT_KEY}\" AND issuetype = ${EPIC_TYPE_ID} AND key != ${TARGET_EPIC_KEY} ORDER BY created ASC"
OTHER_ENC="$(uri_encode "$OTHER_JQL")"
api_call GET "/rest/api/3/search/jql?jql=${OTHER_ENC}&fields=summary&maxResults=200"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load other epics"
mapfile -t OTHER_EPICS < <(jq -r '.issues[].key' "$BODY_FILE")

ARCHIVED_COUNT=0
for epic in "${OTHER_EPICS[@]}"; do
  [[ -z "$epic" ]] && continue
  while IFS= read -r child; do
    [[ -z "$child" ]] && continue
    archive_issue "$child"
    ARCHIVED_COUNT=$((ARCHIVED_COUNT + 1))
  done < <(collect_descendants "$epic")
  archive_issue "$epic"
  ARCHIVED_COUNT=$((ARCHIVED_COUNT + 1))
done

echo "Step 4/8: archiving old templates under target epic..."
while IFS= read -r child; do
  [[ -z "$child" ]] && continue
  archive_issue "$child"
  ARCHIVED_COUNT=$((ARCHIVED_COUNT + 1))
done < <(collect_descendants "$TARGET_EPIC_KEY")
echo "Archived issues: ${ARCHIVED_COUNT}"

echo "Step 5/8: updating board filter to hide archived issues..."
api_call GET "/rest/agile/1.0/board?projectKeyOrId=${JIRA_PROJECT_KEY}&maxResults=50"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load boards"
BOARD_ID="$(jq -r '.values[0].id // empty' "$BODY_FILE")"
[[ -n "$BOARD_ID" ]] || fail_with_body "no board found"
api_call GET "/rest/agile/1.0/board/${BOARD_ID}/configuration"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load board config"
FILTER_ID="$(jq -r '.filter.id // empty' "$BODY_FILE")"
[[ -n "$FILTER_ID" ]] || fail_with_body "board filter missing"
api_call GET "/rest/api/3/filter/${FILTER_ID}"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load filter"
FILTER_NAME="$(jq -r '.name' "$BODY_FILE")"
FILTER_DESC="$(jq -r '.description // ""' "$BODY_FILE")"
NEW_JQL="project = ${JIRA_PROJECT_KEY} AND issuetype != Sub-task AND (labels is EMPTY OR labels not in (archived)) ORDER BY Rank ASC, created DESC"
FILTER_PAYLOAD="$(jq -n --arg name "$FILTER_NAME" --arg desc "$FILTER_DESC" --arg jql "$NEW_JQL" '{name:$name,description:$desc,jql:$jql}')"
api_call PUT "/rest/api/3/filter/${FILTER_ID}" "$FILTER_PAYLOAD"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to update filter"

echo "Step 6/8: setting target roadmap epic..."
update_issue_core "$TARGET_EPIC_KEY" "$TARGET_EPIC_SUMMARY" "layer_roadmap"
transition_issue "$TARGET_EPIC_KEY" "in_progress"

echo "Step 7/8: creating real milestones, plans, tasks, and executions..."
declare -a MILESTONES=(
  "[里程碑] M1 认证与设备配对闭环|||完成登录、/pair/start、/pair/complete 与 token 刷新，客户端可稳定接入。"
  "[里程碑] M2 会话与运行链路可用|||打通 /sessions、/runs、事件流订阅，支持核心交互闭环。"
  "[里程碑] M3 文件与审批能力完备|||完成文件上传下载、审批流与 backend call 控制能力。"
  "[里程碑] M4 上线发布与运维保障|||完成用量看板、应急开关、发布验收与回滚预案。"
)

declare -a PLAN_TASKS=(
  "[计划] 客户端需求基线与范围冻结|||梳理 API 覆盖矩阵，锁定 v1 范围与验收清单。"
  "[计划] API 契约映射与错误码规范|||建立接口字段映射、错误分级与重试策略。"
  "[计划] 迭代排期与发布门禁定义|||明确里程碑节奏、测试门禁、发布与回滚策略。"
)

declare -a REAL_TASKS=(
  "[任务] 登录与设备配对流程实现|||实现设备配对入口、签名校验与会话保持。"
  "[任务] 会话列表与回合交互界面实现|||支持会话创建、切换、turn 发送与 interrupt。"
  "[任务] 运行事件流与终端输出渲染|||接入 websocket 事件流并进行增量渲染与异常处理。"
  "[任务] 文件上传下载与附件管理实现|||实现 /files 上传、下载、失败重试与校验提示。"
  "[任务] 审批中心与后端调用控制实现|||实现审批队列、处理动作与调用权限提示。"
  "[任务] 用量统计与配额告警视图实现|||展示 token/quota 指标并提供阈值提醒。"
  "[任务] 紧急停机与恢复操作入口实现|||实现 emergency stop/resume 与二次确认机制。"
)

declare -A CREATED_TASKS=()

for row in "${MILESTONES[@]}"; do
  summary="${row%%|||*}"
  desc="${row#*|||}"
  key="$(create_child_issue "$STORY_TYPE_ID" "$TARGET_EPIC_KEY" "$summary" "layer_milestone" "$desc")"
  transition_issue "$key" "selected"
done

for row in "${PLAN_TASKS[@]}"; do
  summary="${row%%|||*}"
  desc="${row#*|||}"
  key="$(create_child_issue "$TASK_TYPE_ID" "$TARGET_EPIC_KEY" "$summary" "layer_plan" "$desc")"
  CREATED_TASKS["$summary"]="$key"
  transition_issue "$key" "selected"
done

for row in "${REAL_TASKS[@]}"; do
  summary="${row%%|||*}"
  desc="${row#*|||}"
  key="$(create_child_issue "$TASK_TYPE_ID" "$TARGET_EPIC_KEY" "$summary" "layer_task" "$desc")"
  CREATED_TASKS["$summary"]="$key"
  transition_issue "$key" "selected"
done

create_exec() {
  local parent_key="$1"
  local summary="$2"
  local desc="$3"
  local key
  key="$(create_child_issue "$SUBTASK_TYPE_ID" "$parent_key" "$summary" "layer_execution" "$desc")"
  transition_issue "$key" "selected"
}

create_exec "${CREATED_TASKS["[计划] 客户端需求基线与范围冻结"]}" "[执行] 输出接口能力矩阵与缺口清单" "汇总 /api/v3 关键能力，标记客户端必做与可延后项。"
create_exec "${CREATED_TASKS["[计划] 客户端需求基线与范围冻结"]}" "[执行] 组织评审并冻结范围版本" "完成评审记录并固化范围文档，作为后续开发基线。"

create_exec "${CREATED_TASKS["[计划] API 契约映射与错误码规范"]}" "[执行] 建立接口字段映射表" "对齐请求参数、响应结构与前端模型。"
create_exec "${CREATED_TASKS["[计划] API 契约映射与错误码规范"]}" "[执行] 定义错误处理与重试策略" "区分可重试与不可重试错误并落地统一提示。"

create_exec "${CREATED_TASKS["[计划] 迭代排期与发布门禁定义"]}" "[执行] 制定迭代节奏与验收节点" "为每个里程碑定义截止日期与检查项。"
create_exec "${CREATED_TASKS["[计划] 迭代排期与发布门禁定义"]}" "[执行] 建立发布与回滚清单" "定义上线前检查、灰度策略和回滚触发条件。"

create_exec "${CREATED_TASKS["[任务] 登录与设备配对流程实现"]}" "[执行] 接入 /pair/start 与 /pair/complete" "实现配对码流程与签名提交流程。"
create_exec "${CREATED_TASKS["[任务] 登录与设备配对流程实现"]}" "[执行] 实现 token 刷新与失效处理" "接入 /session/refresh 并统一处理过期态。"

create_exec "${CREATED_TASKS["[任务] 会话列表与回合交互界面实现"]}" "[执行] 实现 /sessions 列表与详情页" "支持分页、状态展示与会话切换。"
create_exec "${CREATED_TASKS["[任务] 会话列表与回合交互界面实现"]}" "[执行] 实现 turns 与 interrupt 交互" "支持发送回合请求和中断进行中任务。"

create_exec "${CREATED_TASKS["[任务] 运行事件流与终端输出渲染"]}" "[执行] 接入 runs/events websocket 订阅" "构建稳定连接、重连与心跳策略。"
create_exec "${CREATED_TASKS["[任务] 运行事件流与终端输出渲染"]}" "[执行] 终端流式输出与错误高亮" "按事件类型增量渲染并标注异常。"

create_exec "${CREATED_TASKS["[任务] 文件上传下载与附件管理实现"]}" "[执行] 实现 /files 上传与进度反馈" "支持大文件进度、失败重试与大小校验。"
create_exec "${CREATED_TASKS["[任务] 文件上传下载与附件管理实现"]}" "[执行] 文件下载与权限提示" "实现下载入口并对无权限场景给出提示。"

create_exec "${CREATED_TASKS["[任务] 审批中心与后端调用控制实现"]}" "[执行] 实现审批列表与处理动作" "支持审批通过/拒绝并同步状态。"
create_exec "${CREATED_TASKS["[任务] 审批中心与后端调用控制实现"]}" "[执行] backend/call 限制与提示文案" "对受限调用展示可操作反馈。"

create_exec "${CREATED_TASKS["[任务] 用量统计与配额告警视图实现"]}" "[执行] 接入 /usage/tokens 与 /usage/quota" "展示 token 使用量与配额余额趋势。"
create_exec "${CREATED_TASKS["[任务] 用量统计与配额告警视图实现"]}" "[执行] 实现阈值告警与周报导出" "支持阈值设置、预警通知和周报导出。"

create_exec "${CREATED_TASKS["[任务] 紧急停机与恢复操作入口实现"]}" "[执行] 接入 emergency stop/resume 操作" "提供安全确认、执行结果反馈与失败重试。"
create_exec "${CREATED_TASKS["[任务] 紧急停机与恢复操作入口实现"]}" "[执行] 增加操作审计记录展示" "展示执行人、时间、原因，满足审计需求。"

echo "Step 8/8: setting initial in-progress chain..."
transition_issue "${CREATED_TASKS["[计划] 客户端需求基线与范围冻结"]}" "in_progress"

JQL_EXEC_IN_PLAN="project = \"${JIRA_PROJECT_KEY}\" AND parent = ${CREATED_TASKS["[计划] 客户端需求基线与范围冻结"]} ORDER BY created ASC"
ENC_EXEC_IN_PLAN="$(uri_encode "$JQL_EXEC_IN_PLAN")"
api_call GET "/rest/api/3/search/jql?jql=${ENC_EXEC_IN_PLAN}&fields=none&maxResults=10"
[[ "$HTTP_STATUS" == "200" ]] || fail_with_body "failed to load plan subtasks"
FIRST_EXEC_KEY="$(jq -r '.issues[0].key // empty' "$BODY_FILE")"
[[ -n "$FIRST_EXEC_KEY" ]] && transition_issue "$FIRST_EXEC_KEY" "in_progress"

echo "Completed."
echo "- target epic: ${TARGET_EPIC_KEY}"
echo "- board url: ${JIRA_BASE_URL}/jira/software/projects/${JIRA_PROJECT_KEY}/boards"
