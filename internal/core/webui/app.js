(() => {
  "use strict";

  const state = {
    mode: "text",
    file: null,
    downloadURL: "",
    downloadName: "output.cleaned",
  };

  const $ = (id) => document.getElementById(id);
  const connectionBadge = $("connectionBadge");
  const connectionStatus = $("connectionStatus");
  const resultBadge = $("resultBadge");
  const summary = $("summary");
  const reportOutput = $("reportOutput");
  const cleanedPanel = $("cleanedPanel");
  const cleanedText = $("cleanedText");
  const downloadButton = $("downloadButton");
  const actionButtons = [$("inspectButton"), $("detectButton"), $("cleanButton")];

  function setBadge(element, label, kind) {
    element.textContent = label;
    element.className = `status-badge ${kind}`;
  }

  function setConnectionStatus(message, kind = "muted") {
    connectionStatus.textContent = message;
    connectionStatus.className = `connection-status ${kind}`;
    setBadge(connectionBadge, kind === "success" ? "● 本机服务" : "● 服务离线", kind);
  }

  function setBusy(busy) {
    actionButtons.forEach((button) => { button.disabled = busy; });
  }

  function setFlowStep(step) {
    document.querySelectorAll(".flow-step").forEach((item) => {
      const itemStep = Number(item.dataset.step);
      item.classList.toggle("active", itemStep === step);
      item.classList.toggle("done", itemStep < step);
    });
  }

  async function apiFetch(path, init = {}) {
    const headers = new Headers(init.headers || {});
    if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), 5 * 60 * 1000);
    let response;
    try {
      response = await fetch(path, { ...init, headers, signal: controller.signal });
    } catch (error) {
      if (error.name === "AbortError") throw new Error("请求超时");
      throw new Error("无法连接本机服务");
    } finally {
      window.clearTimeout(timer);
    }
    const bodyText = await response.text();
    let body = null;
    try { body = bodyText ? JSON.parse(bodyText) : null; } catch (_) { /* handled below */ }
    if (!response.ok || (body && body.ok === false)) {
      throw new Error(body && body.error ? body.error : `HTTP ${response.status}`);
    }
    if (!body) throw new Error("服务返回了无效响应");
    return body;
  }

  function bytesToBase64(bytes) {
    let binary = "";
    const chunkSize = 0x8000;
    for (let offset = 0; offset < bytes.length; offset += chunkSize) {
      binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize));
    }
    return window.btoa(binary);
  }

  function base64ToBytes(encoded) {
    const binary = window.atob(encoded);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
    return bytes;
  }

  function selectedOptions() {
    return {
      layer_a_only: $("layerAOnly").checked,
      detect_before: $("detectBefore").checked,
      detect_after: $("detectAfter").checked,
      keep_non_ai_metadata: $("keepMetadata").checked,
    };
  }

  async function requestPayload() {
    if (state.mode === "file") {
      if (!state.file) throw new Error("请先选择文件");
      return {
        file: bytesToBase64(new Uint8Array(await state.file.arrayBuffer())),
        name: state.file.name,
        options: selectedOptions(),
      };
    }
    const text = $("textInput").value;
    if (!text) throw new Error("请输入文本");
    return {
      file: bytesToBase64(new TextEncoder().encode(text)),
      name: $("textName").value.trim() || "input.txt",
      options: selectedOptions(),
    };
  }

  function clearDownload() {
    if (state.downloadURL) window.URL.revokeObjectURL(state.downloadURL);
    state.downloadURL = "";
    downloadButton.hidden = true;
  }

  function appendSummary(label, value) {
    const item = document.createElement("div");
    item.className = "summary-item";
    const labelNode = document.createElement("span");
    labelNode.textContent = label;
    const valueNode = document.createElement("strong");
    valueNode.textContent = value || "—";
    item.append(labelNode, valueNode);
    summary.append(item);
  }

  function renderResult(result, operation) {
    summary.replaceChildren();
    summary.className = "summary";
    appendSummary("操作", operation);
    appendSummary("类型", result.kind || "unknown");
    appendSummary("格式", result.format || "—");
    if (typeof result.suspicious === "boolean") appendSummary("可疑证据", result.suspicious ? "发现" : "未发现");
    if (result.report && typeof result.report.changed === "boolean") appendSummary("文件变化", result.report.changed ? "已变化" : "未变化");
    reportOutput.textContent = JSON.stringify(result.report || result, null, 2);
    reportOutput.hidden = false;
    cleanedPanel.hidden = true;
    clearDownload();
    setBadge(resultBadge, operation, operation === "清理" ? "success" : "info");
  }

  function cleanedFileName(name) {
    const dot = name.lastIndexOf(".");
    if (dot > 0) return `${name.slice(0, dot)}.cleaned${name.slice(dot)}`;
    return `${name}.cleaned`;
  }

  function renderCleaned(result, payload) {
    if (!result.cleaned) throw new Error("清理响应缺少输出");
    const bytes = base64ToBytes(result.cleaned);
    const name = cleanedFileName(payload.name || "input");
    const blob = new Blob([bytes], { type: state.mode === "text" ? "text/plain;charset=utf-8" : "application/octet-stream" });
    state.downloadURL = window.URL.createObjectURL(blob);
    state.downloadName = name;
    downloadButton.hidden = false;
    cleanedPanel.hidden = false;
    if (state.mode === "text") cleanedText.value = new TextDecoder().decode(bytes);
    else cleanedText.value = `${name} · ${bytes.byteLength.toLocaleString()} bytes`;
  }

  async function run(operation, path) {
    setBusy(true);
    setFlowStep(path === "/clean" ? 3 : 2);
    setBadge(resultBadge, "处理中", "muted");
    try {
      const payload = await requestPayload();
      const result = await apiFetch(path, { method: "POST", body: JSON.stringify(payload) });
      renderResult(result, operation);
      if (path === "/clean") renderCleaned(result, payload);
    } catch (error) {
      summary.className = "summary error-state";
      summary.replaceChildren();
      const title = document.createElement("strong");
      title.textContent = "失败";
      const detail = document.createElement("p");
      detail.textContent = error.message;
      summary.append(title, detail);
      reportOutput.hidden = true;
      cleanedPanel.hidden = true;
      clearDownload();
      setBadge(resultBadge, "失败", "error");
    } finally {
      setBusy(false);
    }
  }

  function renderCapabilities(capabilities) {
    const container = $("capabilities");
    container.replaceChildren();
    const groups = [
      ["tools", "工具"],
      ["scorers", "检测器"],
      ["text_detectors", "文本检测"],
      ["pixel_backends", "像素后端"],
      ["harnesses", "Harness"],
      ["rewrite_backends", "重写后端"],
      ["text_generators", "文本生成"],
    ];
    let count = 0;
    groups.forEach(([key, label]) => {
      const values = capabilities[key];
      if (!values || typeof values !== "object") return;
      Object.entries(values).filter(([, enabled]) => typeof enabled === "boolean").forEach(([name, enabled]) => {
        const chip = document.createElement("span");
        chip.className = `capability-chip ${enabled ? "available" : "missing"}`;
        chip.textContent = `${label} · ${name} ${enabled ? "✓" : "× 未配置"}`;
        chip.title = enabled ? "已启用" : "尚未配置或不可用；不影响基础使用";
        container.append(chip);
        count += 1;
      });
    });
    if (!count) {
      const placeholder = document.createElement("span");
      placeholder.className = "capability-placeholder";
      placeholder.textContent = "没有可报告的能力";
      container.append(placeholder);
    }
  }

  async function connect() {
    try {
      const [health, capabilities] = await Promise.all([apiFetch("/health"), apiFetch("/capabilities")]);
      setConnectionStatus(`本机服务 · ${health.version || "unknown"}`, "success");
      renderCapabilities(capabilities);
    } catch (error) {
      setConnectionStatus(error.message, "error");
      $("capabilities").replaceChildren();
      const placeholder = document.createElement("span");
      placeholder.className = "capability-placeholder";
      placeholder.textContent = "服务未连接";
      $("capabilities").append(placeholder);
    }
  }

  function setMode(mode) {
    state.mode = mode;
    setFlowStep(1);
    document.querySelectorAll("[data-mode]").forEach((tab) => {
      const active = tab.dataset.mode === mode;
      tab.classList.toggle("active", active);
      tab.setAttribute("aria-selected", String(active));
    });
    $("textSource").classList.toggle("hidden", mode !== "text");
    $("fileSource").classList.toggle("hidden", mode !== "file");
    clearDownload();
  }

  function showFile(file) {
    state.file = file;
    $("fileMeta").textContent = file ? `${file.name} · ${file.size.toLocaleString()} bytes` : "尚未选择";
  }

  $("inspectButton").addEventListener("click", () => run("检查", "/inspect"));
  $("detectButton").addEventListener("click", () => run("检测", "/detect"));
  $("cleanButton").addEventListener("click", () => run("清理", "/clean"));
  downloadButton.addEventListener("click", () => {
    if (!state.downloadURL) return;
    const anchor = document.createElement("a");
    anchor.href = state.downloadURL;
    anchor.download = state.downloadName;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
  });
  document.querySelectorAll("[data-mode]").forEach((tab) => tab.addEventListener("click", () => setMode(tab.dataset.mode)));
  $("fileInput").addEventListener("change", (event) => showFile(event.target.files[0] || null));
  $("chooseFileButton").addEventListener("click", (event) => {
    event.stopPropagation();
    $("fileInput").click();
  });
  $("dropZone").addEventListener("click", () => $("fileInput").click());
  $("dropZone").addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") { event.preventDefault(); $("fileInput").click(); }
  });
  $("dropZone").addEventListener("dragover", (event) => { event.preventDefault(); $("dropZone").classList.add("dragging"); });
  $("dropZone").addEventListener("dragleave", () => $("dropZone").classList.remove("dragging"));
  $("dropZone").addEventListener("drop", (event) => {
    event.preventDefault();
    $("dropZone").classList.remove("dragging");
    showFile(event.dataTransfer.files[0] || null);
  });
  window.addEventListener("beforeunload", clearDownload);
  connect();
})();
