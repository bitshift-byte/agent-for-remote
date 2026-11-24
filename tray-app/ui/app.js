const API = "http://localhost:17890";
const el = (id) => document.getElementById(id);

let initialized = false;
let lastServerURL = "";
let lastMountsKey = "";
let lastApprovalsKey = "";

const stateLabels = {
  running: "运行中",
  starting: "连接中...",
  stopped: "已停止",
  conflict: "挂载锁冲突",
  lock_lost: "已被其他设备接管",
  error: "连接异常",
};

function escapeHTML(str) {
  return String(str).replace(/[&<>"']/g, (m) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[m]));
}

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

function renderApprovals(list) {
  const box = el("approval-box");
  const container = el("approval-list");
  const items = list || [];
  const key = JSON.stringify(items.map((x) => x.job_id));
  if (items.length === 0) {
    box.classList.add("hidden");
    if (lastApprovalsKey !== "") {
      container.innerHTML = "";
      lastApprovalsKey = "";
    }
    return;
  }
  box.classList.remove("hidden");
  if (key === lastApprovalsKey) return;
  lastApprovalsKey = key;

  container.innerHTML = items.map((item) => `
    <div class="approval-item">
      <div class="approval-reason">${escapeHTML(item.reason)}</div>
      <div class="approval-cmd">${escapeHTML(item.command)}</div>
      <div style="display:flex;gap:6px;">
        <button data-approve="${escapeHTML(item.job_id)}" class="btn-primary" style="height:26px;padding:0 10px;font-size:11.5px;">允许执行</button>
        <button data-reject="${escapeHTML(item.job_id)}" class="btn-danger" style="height:26px;padding:0 10px;font-size:11.5px;">拒绝</button>
      </div>
    </div>
  `).join("");

  container.querySelectorAll("[data-approve]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      try {
        render(await callAPI("/local/approvals/decide", {
          method: "POST",
          body: JSON.stringify({ job_id: btn.getAttribute("data-approve"), approve: true }),
        }));
      } catch (err) {
        showAlert(err.message);
      }
    });
  });
  container.querySelectorAll("[data-reject]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      try {
        render(await callAPI("/local/approvals/decide", {
          method: "POST",
          body: JSON.stringify({ job_id: btn.getAttribute("data-reject"), approve: false }),
        }));
      } catch (err) {
        showAlert(err.message);
      }
    });
  });
}

function renderMounts(mounts, legacyDir) {
  const box = el("mounts-list");
  if (!box) return;
  const list = mounts || [];
  const key = JSON.stringify(list) + "|" + (legacyDir || "");
  if (key === lastMountsKey) return;
  lastMountsKey = key;

  if (list.length === 0) {
    const legacyHint = legacyDir
      ? `<div style="font-size:11px;color:var(--text-secondary);padding:6px 8px;border:1px dashed var(--border-strong);border-radius:7px;">当前正以旧版单目录模式同步：<code style="font-family:var(--mono);">${escapeHTML(legacyDir)}</code><br/>在下方添加仓库后将自动切换为多仓库独立挂载模式。</div>`
      : `<div style="font-size:11px;color:var(--text-muted);padding:4px 0;">暂未挂载仓库，请在下方选择本地项目目录并点击「添加挂载」。</div>`;
    box.innerHTML = legacyHint;
    return;
  }

  box.innerHTML = list.map((m) => `
    <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;padding:7px 9px;border-radius:7px;background:var(--surface-2);border:1px solid var(--border);">
      <div style="min-width:0;flex:1;">
        <div style="display:flex;align-items:center;gap:6px;">
          <span class="dot ${m.enabled ? "running" : ""}" style="width:6px;height:6px;"></span>
          <strong style="font-size:12px;color:var(--text);">${escapeHTML(m.name)}</strong>
          <span style="font-family:var(--mono);font-size:10.5px;color:#4ade80;">/workspace/${escapeHTML(m.mount_path || m.name)}</span>
        </div>
        <div style="font-family:var(--mono);font-size:10.5px;color:var(--text-muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin-top:2px;">${escapeHTML(m.dir)}</div>
      </div>
      <div style="display:flex;gap:4px;flex-shrink:0;">
        <button data-toggle-dir="${escapeHTML(m.dir)}" data-enabled="${m.enabled ? "1" : "0"}" class="btn-secondary" style="height:24px;padding:0 8px;font-size:11px;">${m.enabled ? "暂停" : "恢复"}</button>
        <button data-remove-dir="${escapeHTML(m.dir)}" class="btn-danger" style="height:24px;padding:0 8px;font-size:11px;" title="仅停止本地同步，云端目录保留">移除</button>
      </div>
    </div>
  `).join("");

  box.querySelectorAll("[data-toggle-dir]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const dir = btn.getAttribute("data-toggle-dir");
      const currentlyEnabled = btn.getAttribute("data-enabled") === "1";
      try {
        render(await callAPI("/local/mounts", {
          method: "POST",
          body: JSON.stringify({ action: "toggle", dir, enabled: !currentlyEnabled }),
        }));
      } catch (err) {
        showAlert(err.message);
      }
    });
  });

  box.querySelectorAll("[data-remove-dir]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const dir = btn.getAttribute("data-remove-dir");
      try {
        render(await callAPI("/local/mounts", {
          method: "POST",
          body: JSON.stringify({ action: "remove", dir }),
        }));
      } catch (err) {
        showAlert(err.message);
      }
    });
  });
}

function render(st) {
  lastServerURL = st.server || "";
  if (!initialized) {
    el("pair-server").value = st.server || "http://localhost:8080";
    el("pair-dir").value = st.dir || "";
    el("pair-device").value = st.device_name || "";
    initialized = true;
  }

  const isBound = !!st.bound;
  el("view-pair").classList.toggle("hidden", isBound);
  el("view-bound").classList.toggle("hidden", !isBound);

  const state = st.state || "stopped";
  el("status-dot").className = "dot " + state;
  el("status-badge").className = "status-pill badge " + state;
  el("status-text").textContent = isBound ? (stateLabels[state] || state) : "未绑定";

  el("meta-account").textContent = `${st.username || "-"} @ ${st.server || "-"}`;
  el("meta-device").textContent = `${st.device_name || "-"} · ${st.os || "-"}${st.encrypted ? " · AES-256" : ""}`;
  const enabledCount = (st.mounts || []).filter((m) => m.enabled).length;
  if (state === "running") {
    el("meta-lock").textContent = enabledCount > 0
      ? `并行挂载 ${enabledCount} 个仓库`
      : `已持有 · Epoch #${st.epoch || 1}`;
  } else if (st.locked_by) {
    el("meta-lock").textContent = `${st.locked_by.device_name || st.locked_by.device_id} (${st.locked_by.os})`;
  } else {
    el("meta-lock").textContent = "未持有";
  }

  renderApprovals(st.pending_approvals);
  renderMounts(st.mounts, st.dir);

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
    el("status-text").textContent = "启动引擎中...";
  }
}

async function pickFolderInto(inputId) {
  try {
    if (window.__TAURI__ && window.__TAURI__.core) {
      const picked = await window.__TAURI__.core.invoke("pick_folder");
      if (picked) {
        el(inputId).value = picked;
      }
    } else {
      alert("请在桌面托盘应用中点击选择文件夹，或直接输入本地目录路径。");
    }
  } catch (err) {
    showAlert("选择目录失败: " + err);
  }
}

el("btn-pick-pair").addEventListener("click", () => pickFolderInto("pair-dir"));
el("btn-pick-mount").addEventListener("click", () => pickFolderInto("mount-dir"));

el("btn-add-mount").addEventListener("click", async () => {
  const dir = el("mount-dir").value.trim();
  const name = el("mount-name").value.trim();
  if (!dir) {
    showAlert("请先选择或填写本地仓库目录路径");
    return;
  }
  showAlert("");
  try {
    const st = await callAPI("/local/mounts", {
      method: "POST",
      body: JSON.stringify({ action: "add", dir, name }),
    });
    el("mount-dir").value = "";
    el("mount-name").value = "";
    render(st);
  } catch (err) {
    showAlert(err.message);
  }
});

el("btn-bind").addEventListener("click", async () => {
  showAlert("");
  try {
    const dir = el("pair-dir").value.trim();
    const st = await callAPI("/local/pair", {
      method: "POST",
      body: JSON.stringify({
        input: el("pair-input").value.trim(),
        server: el("pair-server").value.trim(),
        dir,
        device_name: el("pair-device").value.trim(),
      }),
    });
    el("pair-input").value = "";
    render(st);
  } catch (err) {
    showAlert(err.message);
  }
});

el("btn-start").addEventListener("click", async () => {
  showAlert("");
  try {
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
