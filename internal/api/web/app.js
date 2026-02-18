const els = {
  baseUrl: document.getElementById("baseUrl"),
  bootstrapToken: document.getElementById("bootstrapToken"),
  sessionToken: document.getElementById("sessionToken"),
  workspacePath: document.getElementById("workspacePath"),
  backend: document.getElementById("backend"),
  prompt: document.getElementById("prompt"),
  runId: document.getElementById("runId"),
  log: document.getElementById("log"),
  pairStartOut: document.getElementById("pairStartOut"),
  runOut: document.getElementById("runOut"),
};

function logLine(msg, obj) {
  const line = `[${new Date().toLocaleTimeString()}] ${msg}`;
  const body = obj ? `${line}\n${JSON.stringify(obj, null, 2)}` : line;
  els.log.textContent = `${body}\n\n${els.log.textContent}`.slice(0, 12000);
}

function baseUrl() {
  return els.baseUrl.value.trim().replace(/\/+$/, "");
}

function authHeader(token) {
  if (!token) return {};
  return { Authorization: `Bearer ${token}` };
}

async function request(path, opts = {}) {
  const res = await fetch(`${baseUrl()}${path}`, opts);
  let data = {};
  try {
    data = await res.json();
  } catch (_) {
    data = {};
  }
  if (!res.ok) {
    throw new Error(`${res.status} ${JSON.stringify(data)}`);
  }
  return data;
}

document.getElementById("healthBtn").addEventListener("click", async () => {
  try {
    const data = await request("/healthz");
    logLine("health ok", data);
  } catch (e) {
    logLine(`health failed: ${e.message}`);
  }
});

document.getElementById("backendsBtn").addEventListener("click", async () => {
  try {
    const token = els.sessionToken.value.trim() || els.bootstrapToken.value.trim();
    const data = await request("/api/v3/backends", { headers: { ...authHeader(token) } });
    logLine("backends", data);
  } catch (e) {
    logLine(`backends failed: ${e.message}`);
  }
});

document.getElementById("pairStartBtn").addEventListener("click", async () => {
  try {
    const token = els.bootstrapToken.value.trim();
    const data = await request("/api/v3/pair/start", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...authHeader(token),
      },
      body: JSON.stringify({
        permissions: [
          "runs:submit",
          "runs:read",
          "runs:cancel",
          "backends:read",
          "devices:read",
          "devices:write",
        ],
      }),
    });
    els.pairStartOut.textContent = JSON.stringify(data, null, 2);
    logLine("pair/start ok", data);
  } catch (e) {
    logLine(`pair/start failed: ${e.message}`);
  }
});

document.getElementById("submitRunBtn").addEventListener("click", async () => {
  try {
    const token = els.sessionToken.value.trim();
    const data = await request("/api/v3/runs", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...authHeader(token),
      },
      body: JSON.stringify({
        workspace_id: "ui",
        workspace_path: els.workspacePath.value.trim(),
        backend: els.backend.value,
        prompt: els.prompt.value,
        options: { schema_version: "v2" },
      }),
    });
    els.runId.value = data.run_id || "";
    els.runOut.textContent = JSON.stringify(data, null, 2);
    logLine("run submitted", data);
  } catch (e) {
    logLine(`submit failed: ${e.message}`);
  }
});

document.getElementById("pollRunBtn").addEventListener("click", async () => {
  try {
    const runId = els.runId.value.trim();
    if (!runId) throw new Error("run_id is empty");
    const token = els.sessionToken.value.trim();
    const data = await request(`/api/v3/runs/${runId}`, { headers: { ...authHeader(token) } });
    els.runOut.textContent = JSON.stringify(data, null, 2);
    logLine("run state", { run_id: runId, status: data.status, terminal: data.terminal });
  } catch (e) {
    logLine(`poll failed: ${e.message}`);
  }
});

logLine("ui ready", { ui: "/ui/", api: baseUrl() });

