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

function showAlert(msg) {
  const box = el("alert-box");
  if (!msg) {
    box.classList.add("hidden");
    box.textContent = "";
  } else {
    box.textContent = msg;
    box.classList.remove("hidden");
  }
}

function render(st) {
  lastServerURL = st.server || "";
  if (!initialized) {
    el("pair-server").value = st.server || "http://localhost:8080";
    el("pair-dir").value = st.dir || "";
    el("pair-device").value = st.device_name || "";
    el("bound-dir").value = st.dir || "";
    initialized = true;
  }

  const isBound = !!st.bound;
  el("view-pair").classList.toggle("hidden", isBound);
  el("view-bound").classList.toggle("hidden", !isBound);

  const state = st.state || "stopped";
  el("status-dot").className = "dot " + state;
  el("status-badge").className = "badge " + state;
  el("status-badge").textContent = isBound ? (stateLabels[state] || state) : "未绑定设备";

  el("meta-account").textContent = `${st.username || "-"} @ ${st.server || "-"}`;
  el("meta-device").textContent = `${st.device_name || "-"} (${st.os || "-"})${st.encrypted ? " · 🔒 AES-256-GCM" : ""}`;
  if (state === "running") {
    el("meta-lock").textContent = `本机已持有（Epoch #${st.epoch || 1}）`;
  } else if (st.locked_by) {
    el("meta-lock").textContent = `由 ${st.locked_by.device_name || st.locked_by.device_id} (${st.locked_by.os}) 持有`;
  } else {
    el("meta-lock").textContent = "未持有";
  }

  showAlert(st.last_error || "");
  el("btn-takeover").classList.toggle("hidden", !(state === "conflict" || state === "lock_lost"));
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

async function pickFolderInto(inputId, autoSave) {
  try {
    if (window.__TAURI__ && window.__TAURI__.core) {
      const picked = await window.__TAURI__.core.invoke("pick_folder");
      if (picked) {
        el(inputId).value = picked;
        if (autoSave) {
          const st = await callAPI("/local/config", {
            method: "POST",
            body: JSON.stringify({ dir: picked }),
          });
          render(st);
        }
      }
    } else {
      alert("请在桌面托盘应用中点击选择文件夹，或直接输入本地目录路径。");
    }
  } catch (err) {
    showAlert("选择目录失败: " + err);
  }
}

el("btn-pick-pair").addEventListener("click", () => pickFolderInto("pair-dir", false));
el("btn-pick-bound").addEventListener("click", () => pickFolderInto("bound-dir", true));

el("btn-bind").addEventListener("click", async () => {
  showAlert("");
  try {
    const st = await callAPI("/local/pair", {
      method: "POST",
      body: JSON.stringify({
        input: el("pair-input").value.trim(),
        server: el("pair-server").value.trim(),
        dir: el("pair-dir").value.trim(),
        device_name: el("pair-device").value.trim(),
      }),
    });
    el("pair-input").value = "";
    el("bound-dir").value = st.dir || "";
    render(st);
  } catch (err) {
    showAlert(err.message);
  }
});

el("btn-start").addEventListener("click", async () => {
  showAlert("");
  try {
    await callAPI("/local/config", {
      method: "POST",
      body: JSON.stringify({ dir: el("bound-dir").value.trim() }),
    });
    const st = await callAPI("/local/start", {
      method: "POST",
      body: JSON.stringify({ force: false }),
    });
    render(st);
  } catch (err) {
    showAlert(err.message);
  }
});

el("btn-takeover").addEventListener("click", async () => {
  showAlert("");
  try {
    const st = await callAPI("/local/start", {
      method: "POST",
      body: JSON.stringify({ force: true }),
    });
    render(st);
  } catch (err) {
    showAlert(err.message);
  }
});

el("btn-stop").addEventListener("click", async () => {
  try {
    render(await callAPI("/local/stop", { method: "POST" }));
  } catch (err) {
    showAlert(err.message);
  }
});

el("btn-unbind").addEventListener("click", async () => {
  try {
    render(await callAPI("/local/unbind", { method: "POST" }));
  } catch (err) {
    showAlert(err.message);
  }
});

el("btn-open-web").addEventListener("click", async () => {
  const target = lastServerURL || "http://localhost:8080";
  if (window.__TAURI__ && window.__TAURI__.core) {
    await window.__TAURI__.core.invoke("open_url", { url: target });
  } else {
    window.open(target, "_blank");
  }
});

poll();
setInterval(poll, 1200);
