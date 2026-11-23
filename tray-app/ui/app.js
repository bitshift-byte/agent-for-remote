const API = "http://localhost:17890";
const el = (id) => document.getElementById(id);

let initialized = false;
let lastServerURL = "";

const stateLabels = {
  running: "运行中（双向同步 + 本地执行就绪）",
  starting: "正在连接云端...",
  stopped: "已停止",
  conflict: "另一台设备正在同步",
  lock_lost: "已被其他设备接管（已自动暂停）",
  error: "连接异常",
};

async function callAPI(path, opts = {}) {
  const res = await fetch(API + path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

function render(st) {
  lastServerURL = st.server || "";
  if (!initialized) {
    el("server").value = st.server || "http://localhost:8080";
    el("username").value = st.username || "admin";
    el("dir").value = st.dir || "";
    el("device-name").value = st.device_name || "";
    initialized = true;
  }

  const state = st.state || "stopped";
  el("status-dot").className = "dot " + state;
  el("status-badge").className = "badge " + state;
  el("status-badge").textContent = stateLabels[state] || state;

  el("meta-device").textContent = `${st.device_name || "-"} (${st.os || "-"})`;
  if (state === "running") {
    el("meta-lock").textContent = `本机已持有（Epoch #${st.epoch || 1}）`;
  } else if (st.locked_by) {
    el("meta-lock").textContent = `由 ${st.locked_by.device_name || st.locked_by.device_id} (${st.locked_by.os}) 持有`;
  } else {
    el("meta-lock").textContent = "未持有";
  }

  const alertBox = el("alert-box");
  if (st.last_error) {
    alertBox.textContent = st.last_error;
    alertBox.classList.remove("hidden");
  } else {
    alertBox.classList.add("hidden");
  }

  el("btn-takeover").style.display = (state === "conflict" || state === "lock_lost") ? "inline-block" : "none";
  el("btn-stop").disabled = !st.running;

  const logsEl = el("logs");
  const atBottom = logsEl.scrollHeight - logsEl.scrollTop - logsEl.clientHeight < 30;
  logsEl.textContent = (st.logs && st.logs.length) ? st.logs.join("\n") : "暂无日志";
  if (atBottom) logsEl.scrollTop = logsEl.scrollHeight;
}

async function poll() {
  try {
    const st = await callAPI("/local/status");
    render(st);
  } catch {
    el("status-badge").textContent = "正在启动本地引擎...";
  }
}

el("btn-pick").addEventListener("click", async () => {
  try {
    if (window.__TAURI__ && window.__TAURI__.core) {
      const picked = await window.__TAURI__.core.invoke("pick_folder");
      if (picked) el("dir").value = picked;
    } else {
      alert("请在桌面托盘应用中点击选择文件夹，或直接输入本地目录路径。");
    }
  } catch (err) {
    alert("选择目录失败: " + err);
  }
});

async function saveAndStart(force) {
  try {
    const cfg = {
      server: el("server").value.trim(),
      username: el("username").value.trim(),
      dir: el("dir").value.trim(),
      device_name: el("device-name").value.trim(),
    };
    const secretVal = el("access-code").value;
    if (secretVal) cfg.token = secretVal;

    await callAPI("/local/config", {
      method: "POST",
      body: JSON.stringify(cfg),
    });
    el("access-code").value = "";
    const st = await callAPI("/local/start", {
      method: "POST",
      body: JSON.stringify({ force: !!force }),
    });
    render(st);
  } catch (err) {
    el("alert-box").textContent = err.message;
    el("alert-box").classList.remove("hidden");
  }
}

el("btn-save-start").addEventListener("click", () => saveAndStart(false));
el("btn-takeover").addEventListener("click", () => saveAndStart(true));
el("btn-stop").addEventListener("click", async () => {
  try {
    const st = await callAPI("/local/stop", { method: "POST" });
    render(st);
  } catch (err) {
    alert(err.message);
  }
});
el("btn-open-web").addEventListener("click", async () => {
  const target = el("server").value.trim() || lastServerURL || "http://localhost:8080";
  if (window.__TAURI__ && window.__TAURI__.core) {
    await window.__TAURI__.core.invoke("open_url", { url: target });
  } else {
    window.open(target, "_blank");
  }
});

poll();
setInterval(poll, 1200);
