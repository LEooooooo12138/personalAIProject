/*!
 * Chat Widget — embeddable AI chat for the Go Agent.
 * Zero dependencies. Drop into any HTML page.
 *
 * Usage:
 *   <script src="/chat/chat-widget.js"></script>
 *   <script>
 *     ChatWidget.init({ endpoint: "ws://localhost:8080/channels/webchat/ws" });
 *   </script>
 */
(function () {
  "use strict";

  var DEFAULTS = {
    endpoint: "ws://" + location.host + "/channels/webchat/ws",
    theme: "dark",
    position: "bottom-right",
    greeting: "你好，有什么可以帮你的？",
    placeholder: "输入消息..."
  };

  var cfg, ws, container, panel, messages, input, toggleBtn;
  var theme = "dark";
  var connected = false;
  var reconnectTimer = null;

  // ── Markdown ──────────────────────────────────────────

  function renderMd(text) {
    if (!text) return "";
    var out = escapeHtml(text);
    out = out.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
    out = out.replace(/\*(.+?)\*/g, "<em>$1</em>");
    out = out.replace(/`([^`]+)`/g, "<code>$1</code>");
    out = out.replace(/```(\w*)\n?([\s\S]*?)```/g, "<pre><code>$2</code></pre>");
    out = out.replace(/^### (.+)$/gm, "<h4>$1</h4>");
    out = out.replace(/^## (.+)$/gm, "<h3>$1</h3>");
    out = out.replace(/^# (.+)$/gm, "<h2>$1</h2>");
    out = out.replace(/^- (.+)$/gm, "<li>$1</li>");
    out = out.replace(/(<li>.*<\/li>)/s, "<ul>$1</ul>");
    out = out.replace(/\n/g, "<br>");
    return out;
  }

  function escapeHtml(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  }

  // ── DOM ───────────────────────────────────────────────

  function createStyles() {
    var s = document.createElement("style");
    s.id = "chat-widget-styles";
    s.textContent =
      ".cw-container{position:fixed;z-index:9999;font-family:system-ui,-apple-system,sans-serif}" +
      ".cw-container.br{bottom:20px;right:20px}" +
      ".cw-container.bl{bottom:20px;left:20px}" +
      ".cw-toggle{width:52px;height:52px;border-radius:50%;border:none;cursor:pointer;display:flex;align-items:center;justify-content:center;box-shadow:0 2px 12px rgba(0,0,0,.25);transition:transform .15s}" +
      ".cw-toggle:hover{transform:scale(1.08)}" +
      ".cw-toggle svg{width:24px;height:24px;fill:none;stroke:currentColor;stroke-width:2}" +
      ".cw-panel{position:absolute;bottom:64px;right:0;width:360px;height:520px;max-height:calc(100vh - 100px);border-radius:10px;overflow:hidden;display:none;flex-direction:column;box-shadow:0 4px 24px rgba(0,0,0,.3)}" +
      ".cw-panel.open{display:flex}" +
      ".cw-header{padding:12px 16px;display:flex;align-items:center;justify-content:space-between;font-size:14px;font-weight:600}" +
      ".cw-header-actions{display:flex;gap:6px;align-items:center}" +
      ".cw-header-btn{background:none;border:none;cursor:pointer;padding:4px;border-radius:4px;display:flex;align-items:center}" +
      ".cw-header-btn svg{width:16px;height:16px}" +
      ".cw-messages{flex:1;overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:8px}" +
      ".cw-msg{max-width:85%;padding:8px 12px;border-radius:12px;font-size:13px;line-height:1.5;word-break:break-word}" +
      ".cw-msg.user{align-self:flex-end;border-bottom-right-radius:4px}" +
      ".cw-msg.assistant{align-self:flex-start;border-bottom-left-radius:4px}" +
      ".cw-msg h2,.cw-msg h3,.cw-msg h4{margin:4px 0;font-size:14px}" +
      ".cw-msg ul{margin:4px 0;padding-left:18px}" +
      ".cw-msg li{margin:2px 0}" +
      ".cw-msg pre{margin:4px 0;padding:8px;border-radius:4px;font-size:12px;overflow-x:auto}" +
      ".cw-msg code{font-family:monospace;font-size:12px}" +
      ".cw-msg :not(pre)>code{padding:1px 4px;border-radius:3px}" +
      ".cw-typing{display:flex;gap:4px;padding:8px 12px;align-self:flex-start}" +
      ".cw-typing span{width:6px;height:6px;border-radius:50%;animation:cw-bounce 1.4s infinite ease-in-out}" +
      ".cw-typing span:nth-child(2){animation-delay:.16s}" +
      ".cw-typing span:nth-child(3){animation-delay:.32s}" +
      "@keyframes cw-bounce{0%,80%,100%{transform:scale(.6);opacity:.4}40%{transform:scale(1);opacity:1}}" +
      ".cw-input-wrap{display:flex;padding:8px 12px;gap:8px;border-top:1px solid}" +
      ".cw-input{flex:1;border:none;outline:none;font-size:13px;padding:8px 0;background:transparent;resize:none;font-family:inherit;line-height:1.4}" +
      ".cw-send{background:none;border:none;cursor:pointer;padding:4px;display:flex;align-items:center}" +
      ".cw-send svg{width:20px;height:20px}" +
      ".cw-send:disabled{opacity:.3;cursor:default}" +
      ".cw-dot{width:8px;height:8px;border-radius:50%;display:inline-block;margin-right:6px}" +
      // Dark theme
      ".cw-dark .cw-toggle{background:#1a1a2e;color:#e0e0e0}" +
      ".cw-dark .cw-panel{background:#16162a}" +
      ".cw-dark .cw-header{background:#1e1e38;color:#e0e0e0}" +
      ".cw-dark .cw-header-btn{color:#888}" +
      ".cw-dark .cw-header-btn:hover{background:rgba(255,255,255,.08);color:#ccc}" +
      ".cw-dark .cw-msg.user{background:#4f46e5;color:#fff}" +
      ".cw-dark .cw-msg.assistant{background:#1e1e38;color:#d0d0e0}" +
      ".cw-dark .cw-msg pre{background:#0f0f20}" +
      ".cw-dark .cw-msg :not(pre)>code{background:rgba(255,255,255,.1);color:#b0b0e0}" +
      ".cw-dark .cw-input-wrap{border-color:#2a2a4a}" +
      ".cw-dark .cw-input{color:#d0d0e0}" +
      ".cw-dark .cw-input::placeholder{color:#555}" +
      ".cw-dark .cw-send{color:#888}" +
      ".cw-dark .cw-send:hover{color:#ccc}" +
      ".cw-dark .cw-typing span{background:#6a6ae0}" +
      ".cw-dark .cw-dot.ok{background:#4ade80}" +
      // Light theme
      ".cw-light .cw-toggle{background:#fff;color:#333;box-shadow:0 2px 12px rgba(0,0,0,.1)}" +
      ".cw-light .cw-panel{background:#fff}" +
      ".cw-light .cw-header{background:#f8f8f8;color:#333}" +
      ".cw-light .cw-header-btn{color:#999}" +
      ".cw-light .cw-header-btn:hover{background:rgba(0,0,0,.05);color:#666}" +
      ".cw-light .cw-msg.user{background:#4f46e5;color:#fff}" +
      ".cw-light .cw-msg.assistant{background:#f1f1f5;color:#333}" +
      ".cw-light .cw-msg pre{background:#e8e8ee}" +
      ".cw-light .cw-msg :not(pre)>code{background:rgba(0,0,0,.06);color:#555}" +
      ".cw-light .cw-input-wrap{border-color:#e0e0e0}" +
      ".cw-light .cw-input{color:#333}" +
      ".cw-light .cw-input::placeholder{color:#aaa}" +
      ".cw-light .cw-send{color:#999}" +
      ".cw-light .cw-send:hover{color:#666}" +
      ".cw-light .cw-typing span{background:#4f46e5}" +
      ".cw-light .cw-dot.ok{background:#22c55e}";
    document.head.appendChild(s);
  }

  function buildDom() {
    container = document.createElement("div");
    container.className = "cw-container " + (cfg.position === "bottom-left" ? "bl" : "br");
    container.innerHTML =
      '<button class="cw-toggle" title="Chat">' +
      '<svg viewBox="0 0 24 24"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/></svg>' +
      "</button>" +
      '<div class="cw-panel">' +
      '<div class="cw-header">' +
      "<span>AI Assistant</span>" +
      '<div class="cw-header-actions">' +
      '<button class="cw-header-btn cw-theme-btn" title="Toggle theme">' +
      '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>' +
      "</button>" +
      '<button class="cw-header-btn cw-close-btn" title="Close">' +
      '<svg viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>' +
      "</button>" +
      "</div>" +
      "</div>" +
      '<div class="cw-messages"></div>' +
      '<div class="cw-input-wrap">' +
      '<textarea class="cw-input" placeholder="' + (cfg.placeholder || "输入消息...") + '" rows="1"></textarea>' +
      '<button class="cw-send" title="Send" disabled>' +
      '<svg viewBox="0 0 24 24"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>' +
      "</button>" +
      "</div>" +
      "</div>";

    document.body.appendChild(container);

    toggleBtn = container.querySelector(".cw-toggle");
    panel = container.querySelector(".cw-panel");
    messages = container.querySelector(".cw-messages");
    input = container.querySelector(".cw-input");
    var sendBtn = container.querySelector(".cw-send");

    toggleBtn.addEventListener("click", togglePanel);
    container.querySelector(".cw-close-btn").addEventListener("click", closePanel);
    container.querySelector(".cw-theme-btn").addEventListener("click", toggleTheme);
    sendBtn.addEventListener("click", sendMessage);
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
      }
    });
    input.addEventListener("input", function () {
      sendBtn.disabled = !input.value.trim();
    });
  }

  // ── Panel ─────────────────────────────────────────────

  function togglePanel() {
    var open = panel.classList.contains("open");
    if (open) {
      closePanel();
    } else {
      openPanel();
    }
  }

  function openPanel() {
    panel.classList.add("open");
    toggleBtn.style.display = "none";
    input.focus();
    if (!connected) connect();
  }

  function closePanel() {
    panel.classList.remove("open");
    toggleBtn.style.display = "";
  }

  // ── Theme ─────────────────────────────────────────────

  function applyTheme(t) {
    theme = t;
    container.classList.remove("cw-dark", "cw-light");
    container.classList.add("cw-" + theme);
  }

  function toggleTheme() {
    applyTheme(theme === "dark" ? "light" : "dark");
  }

  // ── WebSocket ─────────────────────────────────────────

  var sessionID = "";
  var historyLoaded = false;
  try { sessionID = localStorage.getItem("chat-widget-sid") || ""; } catch (e) {}

  function loadHistory(userId) {
    var apiBase = cfg.endpoint.replace("/channels/webchat/ws", "").replace("ws://", "http://").replace("wss://", "https://");
    var url = apiBase + "/sessions/webchat/" + encodeURIComponent(userId) + "/messages";
    fetch(url)
      .then(function(r) { return r.json(); })
      .then(function(data) {
        historyLoaded = true;
        var msgs = data.messages || [];
        if (msgs.length > 0) {
          // We have history: clear anything and render it.
          messages.innerHTML = "";
          msgs.forEach(function(m) {
            appendMessage(m.role, m.content);
          });
        } else if (cfg.greeting && messages.children.length === 0) {
          // No history: show greeting as fallback.
          appendMessage("assistant", cfg.greeting);
        }
      })
      .catch(function() {
        historyLoaded = true;
        // On error, show greeting if panel is still empty.
        if (cfg.greeting && messages.children.length === 0) {
          appendMessage("assistant", cfg.greeting);
        }
      });
  }

  // ── Session List ─────────────────────────────────────

  function toggleSessionsList() {
    var showing = sessionsList.classList.contains("open");
    if (showing) {
      sessionsList.classList.remove("open");
      messages.style.display = "";
      input.parentElement.style.display = "";
    } else {
      loadSessionList();
      sessionsList.classList.add("open");
      messages.style.display = "none";
      input.parentElement.style.display = "none";
    }
  }

  function loadSessionList() {
    var apiBase = cfg.endpoint.replace("/channels/webchat/ws", "").replace("ws://", "http://").replace("wss://", "https://");
    fetch(apiBase + "/sessions?channel=webchat")
      .then(function(r) { return r.json(); })
      .then(function(sessions) {
        renderSessionList(sessions || []);
      })
      .catch(function() { /* fail silently */ });
  }

  function renderSessionList(sessions) {
    sessionsList.innerHTML = "";

    // "New Chat" button
    var newBtn = document.createElement("div");
    newBtn.className = "cw-new-chat";
    newBtn.innerHTML = '<svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg> New Chat';
    newBtn.addEventListener("click", newSession);
    sessionsList.appendChild(newBtn);

    sessions.forEach(function(s) {
      var item = document.createElement("div");
      item.className = "cw-session-item";
      if (s.user_id === sessionID) item.classList.add("active");

      var title = document.createElement("div");
      title.className = "cw-session-title";
      title.textContent = formatDate(s.last_active_at);

      var meta = document.createElement("div");
      meta.className = "cw-session-meta";
      meta.textContent = s.round_count + " rounds / " + s.message_count + " msgs";

      item.appendChild(title);
      item.appendChild(meta);

      if (s.preview) {
        var preview = document.createElement("div");
        preview.className = "cw-session-preview";
        preview.textContent = s.preview;
        item.appendChild(preview);
      }

      item.addEventListener("click", function() { switchSession(s.user_id); });
      sessionsList.appendChild(item);
    });
  }

  function switchSession(userId) {
    if (userId === sessionID) {
      // Same session: just show messages
      toggleSessionsList();
      return;
    }
    // Switch: clear state, reconnect with new session ID
    sessionID = userId;
    try { localStorage.setItem("chat-widget-sid", sessionID); } catch (e) {}
    historyLoaded = false;
    messages.innerHTML = "";
    if (ws) { try { ws.close(); } catch (e) {} }
    connected = false;
    toggleSessionsList();
    connect();
  }

  function newSession() {
    sessionID = "";
    try { localStorage.removeItem("chat-widget-sid"); } catch (e) {}
    historyLoaded = false;
    messages.innerHTML = "";
    if (ws) { try { ws.close(); } catch (e) {} }
    connected = false;
    if (cfg.greeting) { appendMessage("assistant", cfg.greeting); }
    toggleSessionsList();
    connect();
  }

  function formatDate(iso) {
    if (!iso) return "Unknown";
    var d = new Date(iso);
    var now = new Date();
    var diff = now - d;
    if (diff < 86400000) {
      return d.toLocaleTimeString([], {hour:"2-digit", minute:"2-digit"});
    }
    return d.toLocaleDateString([], {month:"short", day:"numeric"}) + " " +
           d.toLocaleTimeString([], {hour:"2-digit", minute:"2-digit"});
  }

  function connect() {
    if (ws && ws.readyState === WebSocket.OPEN) return;

    try {
      ws = new WebSocket(cfg.endpoint);
    } catch (e) {
      scheduleReconnect();
      return;
    }

    ws.onopen = function () {
      connected = true;
      updateStatus(true);
      if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
      if (sessionID && !historyLoaded) { loadHistory(sessionID); }
    };

    ws.onmessage = function (e) {
      removeTyping();
      var data;
      try { data = JSON.parse(e.data); } catch (err) { return; }

      if (data.type === "session") {
        // Server echoed our session ID — persist it.
        sessionID = data.session_id;
        try { localStorage.setItem("chat-widget-sid", sessionID); } catch (ex) {}
      } else if (data.type === "context") {
        // Show retrieved vault sources as a subtle indicator.
        var el = document.createElement("div");
        el.className = "cw-msg assistant";
        el.style.opacity = "0.6";
        el.style.fontSize = "11px";
        el.style.fontStyle = "italic";
        el.textContent = data.content;
        messages.appendChild(el);
        messages.scrollTop = messages.scrollHeight;
      } else if (data.type === "stream") {
        // Incremental streaming token: build text character by character.
        removeTyping();
        var streamEl = document.getElementById("cw-stream-msg");
        if (!streamEl) {
            streamEl = document.createElement("div");
            streamEl.className = "cw-msg assistant";
            streamEl.id = "cw-stream-msg";
            messages.appendChild(streamEl);
        }
        // Accumulate raw text and re-render as Markdown each token.
        streamEl._rawText = (streamEl._rawText || "") + data.content;
        streamEl.innerHTML = renderMd(streamEl._rawText);
        messages.scrollTop = messages.scrollHeight;
    } else if (data.type === "response") {
        // Final response: clear any streaming state and display the complete message.
        var streamMsg = document.getElementById("cw-stream-msg");
        if (streamMsg) {
            streamMsg.removeAttribute("id");
            streamMsg._rawText = null;
        } else {
            appendMessage("assistant", data.content);
        }
      } else if (data.type === "error") {
        appendMessage("assistant", "[Error] " + (data.message || "unknown error"));
      }
    };

    ws.onclose = function () {
      connected = false;
      updateStatus(false);
      scheduleReconnect();
    };

    ws.onerror = function () {
      ws.close();
    };
  }

  function scheduleReconnect() {
    if (reconnectTimer) return;
    updateStatus(false);
    reconnectTimer = setTimeout(function () {
      reconnectTimer = null;
      connect();
    }, 3000);
  }

  function sendMessage() {
    var text = input.value.trim();
    if (!text) return;

    appendMessage("user", text);
    input.value = "";
    container.querySelector(".cw-send").disabled = true;

    if (!connected) {
      connect();
      // Queue retry: try again after connection
      var retry = setInterval(function () {
        if (connected) {
          clearInterval(retry);
          doSend(text);
        }
      }, 200);
      return;
    }

    doSend(text);
  }

  function doSend(text) {
    showTyping();
    try {
      ws.send(JSON.stringify({ session_id: sessionID, content: text }));
    } catch (e) {
      removeTyping();
      appendMessage("assistant", "[Error] failed to send message");
    }
  }

  // ── Messages ──────────────────────────────────────────

  function appendMessage(role, content) {
    var el = document.createElement("div");
    el.className = "cw-msg " + role;
    el.innerHTML = renderMd(content);
    messages.appendChild(el);
    messages.scrollTop = messages.scrollHeight;
  }

  function showTyping() {
    removeTyping();
    var el = document.createElement("div");
    el.className = "cw-typing";
    el.id = "cw-typing";
    el.innerHTML = "<span></span><span></span><span></span>";
    messages.appendChild(el);
    messages.scrollTop = messages.scrollHeight;
  }

  function removeTyping() {
    var el = document.getElementById("cw-typing");
    if (el) el.remove();
  }

  function updateStatus(ok) {
    var dot = container.querySelector(".cw-dot");
    if (ok) {
      if (!dot) {
        dot = document.createElement("span");
        dot.className = "cw-dot ok";
        var header = container.querySelector(".cw-header span");
        if (header) header.prepend(dot);
      }
    } else {
      if (dot) dot.remove();
    }
  }

  // ── Init ──────────────────────────────────────────────

  function init(opts) {
    cfg = {};
    var key;
    for (key in DEFAULTS) { cfg[key] = DEFAULTS[key]; }
    for (key in opts) { cfg[key] = opts[key]; }

    createStyles();
    buildDom();
    applyTheme(cfg.theme || "dark");

    // Add greeting message.
    if (cfg.greeting) {
      appendMessage("assistant", cfg.greeting);
    }
  }

  // Expose.
  window.ChatWidget = { init: init };
})();
