// IPTV 直播源管理器 — 前端交互
(function () {
  "use strict";

  const CSRF = window.__csrf_token || "";
  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));

  // ── API helper ────────────────────────────────────────────────
  async function api(method, path, body) {
    const opts = { method, headers: {} };
    if (CSRF) opts.headers["X-CSRF-Token"] = CSRF;
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const res = await fetch(path, opts);
    let data = {};
    try { data = await res.json(); } catch (e) {}
    return { ok: res.ok, status: res.status, data };
  }

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])
    );
  }
  const badge = (s) =>
    s === "success"
      ? '<span class="badge ok">成功</span>'
      : s === "failed" || s === "timeout" || s === "connection_failed"
      ? '<span class="badge fail">' + esc(s) + "</span>"
      : '<span class="badge warn">' + esc(s || "未测") + "</span>";

  function escapeHtml(s) { return esc(s); }
  function escapeHtmlAttr(s) { return esc(s).replace(/'/g, "&#39;"); }

  function toast(msg, kind) {
    let box = document.getElementById("toast-box");
    if (!box) {
      box = document.createElement("div");
      box.id = "toast-box";
      box.style.cssText = "position:fixed;top:18px;right:18px;z-index:2000;display:flex;flex-direction:column;gap:8px;";
      document.body.appendChild(box);
    }
    const color = kind === "error" ? "var(--danger)" : kind === "success" ? "var(--success)" : "var(--primary)";
    const t = document.createElement("div");
    t.style.cssText = "background:var(--bg-card);border:1px solid " + color + ";color:var(--text);padding:10px 14px;border-radius:10px;font-size:13px;box-shadow:var(--shadow);max-width:340px;";
    t.textContent = msg;
    box.appendChild(t);
    setTimeout(() => { t.remove(); }, 3200);
  }

  // ── theme ────────────────────────────────────────────────────
  function applyTheme(t) {
    document.documentElement.setAttribute("data-theme", t);
    try { localStorage.setItem("lsm-theme", t); } catch (e) {}
  }
  function initTheme() {
    let t = "dark";
    try { t = localStorage.getItem("lsm-theme") || "dark"; } catch (e) {}
    applyTheme(t);
    const btn = $("#theme-toggle");
    if (btn) btn.onclick = () => applyTheme(document.documentElement.getAttribute("data-theme") === "dark" ? "light" : "dark");
    const lo = $("#logout-btn");
    if (lo) lo.onclick = async () => { await api("POST", "/api/auth/logout"); location.href = "/login"; };
  }

  // ── active nav ───────────────────────────────────────────────
  function markNav() {
    const p = location.pathname;
    $$(".nav a").forEach((a) => {
      const href = a.getAttribute("href");
      a.classList.toggle("active", href === p || (href !== "/" && p.startsWith(href)));
    });
  }

  // ── login ────────────────────────────────────────────────────
  function initLogin() {
    const form = $("#login-form");
    if (!form) return;
    form.onsubmit = async (e) => {
      e.preventDefault();
      const msg = $("#login-msg");
      msg.textContent = "登录中…"; msg.className = "msg";
      const r = await api("POST", "/api/auth/login", {
        username: $("#login-user").value,
        password: $("#login-pass").value,
      });
      if (r.ok && r.data.ok) { location.href = "/"; }
      else { msg.textContent = r.data.error || "登录失败"; msg.className = "msg err"; }
    };
  }

  // ── dashboard ────────────────────────────────────────────────
  let _dashTimer = null;
  async function initDashboard() {
    await loadDashboard();
  }
  async function loadDashboard() {
    const r = await api("GET", "/api/dashboard/stats");
    const s = r.data || {};
    const cs = $("#collect-status");
    if (s.collected === false) {
      // 冷缓存：显示提示并每 3s 轮询，直到后台采集完成（不再阻塞登录）
      if (cs) { cs.style.display = ""; cs.textContent = s.message || "正在采集源数据，请稍候自动刷新…"; }
      setText("#st-total", "…"); setText("#st-valid", "…"); setText("#st-invalid", "…"); setText("#st-rate", "…");
      if (!_dashTimer) _dashTimer = setTimeout(() => { _dashTimer = null; loadDashboard(); }, 3000);
      return;
    }
    if (cs) cs.style.display = "none";
    if (_dashTimer) { clearTimeout(_dashTimer); _dashTimer = null; }
    setText("#st-total", s.total_sources);
    setText("#st-valid", s.valid);
    setText("#st-invalid", s.invalid);
    setText("#st-rate", s.rate);
    const g = await api("GET", "/api/dashboard/channel-stats");
    const gl = $("#group-list");
    if (gl && g.data) {
      if (g.data.collected === false) {
        gl.innerHTML = '<div class="muted">正在采集…</div>';
      } else if (g.data.channels) {
        gl.innerHTML = g.data.channels.map((c) =>
          `<div class="row" style="justify-content:space-between;"><span>${esc(c.name)}</span><b>${c.count}</b></div>`
        ).join("") || '<div class="muted">暂无数据</div>';
      }
    }
    const si = await api("GET", "/api/dashboard/system");
    const box = $("#sys-info");
    if (box && si.data) {
      const o = si.data.system || {};
      box.innerHTML = Object.keys(o).map((k) =>
        `<div class="row" style="justify-content:space-between;"><span class="muted">${esc(k)}</span><b>${esc(o[k])}</b></div>`
      ).join("") || '<div class="muted">—</div>';
    }
    const gt = $("#goto-test");
    if (gt) gt.onclick = () => (location.href = "/test");
  }
  function setText(sel, v) { const e = $(sel); if (e) e.textContent = v == null ? "–" : v; }

  // ── sources (仿照 Python: 源文件列表 → 展开频道) ──────────────
  async function initSources() {
    const IS_ADMIN = !!$("#btn-add-file");
    let sourceFilesData = [];
    let categoryDict = {};
    let activePopover = null;
    let uaFilePopover = null;
    let uaChannelPopover = null;
    let currentExpandedId = null;
    let channelsPageData = [];
    let channelsTotal = 0;
    let channelsPage = 1;
    const channelsPageSize = 100;
    let channelsSearchTimer = null;
    let editingChannelIdx = -1;
    let editingChannelName = "";

    async function loadCategoryDictionary() {
      const r = await api("GET", "/api/category-dictionary");
      if (r.ok && r.data) categoryDict = r.data.raw || {};
    }

    function truncateText(t, n) {
      t = t || "";
      return t.length > n ? t.substring(0, n - 3) + "..." : t;
    }

    function loadSourceFiles() {
      api("GET", "/api/source-files").then((r) => {
        if (!r.ok) { renderFilesError("加载失败"); return; }
        const d = r.data || {};
        renderSourceFiles(d);
        if (d.warming) {
          // 首屏已即时渲染配置列表；缓存预热后自动刷新以回填频道数（不显示空白 spinner）
          setTimeout(loadSourceFiles, 3000);
        }
      }).catch((e) => renderFilesError(String(e)));
    }

    function renderFilesError(e) {
      $("#source-files-tbody").innerHTML = '<tr><td colspan="7" class="muted" style="text-align:center;padding:18px">加载失败: ' + esc(e) + "</td></tr>";
    }

    function renderSourceFiles(data) {
      const files = data.files || [];
      sourceFilesData = files;
      let total = 0;
      files.forEach((f) => { total += (f.channel_count || 0); });
      $("#source-stats-info").textContent = "源文件: " + files.length + " 个 | 频道: " + total + " 个";

      if (!files.length) {
        $("#source-files-tbody").innerHTML = '<tr><td colspan="7" class="muted" style="text-align:center;padding:18px">暂无源文件，点击"添加源文件"开始</td></tr>';
        return;
      }
      let html = "";
      files.forEach((f) => {
        let typeLabel, typeClass;
        if (f.type === "online") { typeLabel = "在线URL"; typeClass = "badge ok"; }
        else if (f.type === "github") {
          const dm = f.download_method || "raw";
          const dmLabels = { raw: "直连", mirror: "镜像" };
          typeLabel = "GitHub ";
          if (IS_ADMIN) {
            typeLabel += '<span class="badge" style="font-size:10px;cursor:pointer" title="点击切换下载方式" onclick="changeDownloadMethod(\'' + esc(f.id) + "','" + esc(dm) + "')\">#" + (dmLabels[dm] || dm) + "</span>";
          } else {
            typeLabel += "#" + (dmLabels[dm] || dm);
          }
          typeClass = "badge warn";
        } else { typeLabel = "本地"; typeClass = "badge ok"; }

        const stClass = f.file_status_class === "ok" ? "badge ok" : "badge warn";
        const chCount = f.channel_count > 0 ? f.channel_count : "-";
        const addr = truncateText(f.url_or_path || "", 60);

        const ua = f.ua_settings || {};
        let uaBadge;
        if (ua.enabled && ua.ua_value) {
          const uaShort = truncateText(ua.ua_value, 22);
          const uaPos = ua.ua_position === "url" ? "URL参数" : "属性";
          uaBadge = '<span class="badge" style="cursor:pointer;font-size:11px" title="' + escapeHtmlAttr(ua.ua_value) + " (" + uaPos + ')" onclick="showFileUaEditor(\'' + esc(f.id) + "')\">UA: " + esc(uaShort) + "</span>";
        } else {
          uaBadge = '<span class="badge" style="cursor:pointer;font-size:11px;opacity:.8" onclick="showFileUaEditor(\'' + esc(f.id) + "')\">未设置</span>";
        }

        html += '<tr id="file-row-' + esc(f.id) + '">';
        html += "<td>" + esc(f.name || "") + "</td>";
        html += '<td><span class="' + typeClass + '">' + typeLabel + "</span></td>";
        html += '<td class="url-cell" title="' + escapeHtmlAttr(f.url_or_path || "") + '">' + esc(addr) + "</td>";
        html += '<td><span class="' + stClass + '">' + esc(f.file_status || "未知") + "</span></td>";
        html += '<td style="text-align:center">' + chCount + "</td>";
        html += "<td>" + uaBadge + "</td>";
        html += '<td style="white-space:nowrap">';
        html += '<button class="btn" style="padding:5px 10px;font-size:12px" onclick="expandChannels(\'' + esc(f.id) + "')\">展开</button> ";
        if (IS_ADMIN) html += '<button class="btn danger" style="padding:5px 10px;font-size:12px" onclick="deleteSourceFile(\'' + esc(f.id) + "')\">删除</button>";
        html += "</td></tr>";
      });
      $("#source-files-tbody").innerHTML = html;
    }

    function expandChannels(fileId) {
      if (currentExpandedId === fileId) { closeChannels(); return; }
      currentExpandedId = fileId;
      channelsPage = 1;
      $("#channels-search").value = "";
      $$("#source-files-tbody tr").forEach((r) => { r.style.background = ""; });
      const row = $("#file-row-" + fileId);
      if (row) row.style.background = "rgba(91,140,255,.10)";
      $("#channels-card").style.display = "";
      $("#channels-tbody").innerHTML = '<tr><td colspan="6" class="muted" style="text-align:center;padding:16px">解析中…</td></tr>';
      $("#channels-info").textContent = "";
      $("#channels-pagination").innerHTML = "";
      let fileObj = null;
      sourceFilesData.forEach((f) => { if (f.id === fileId) fileObj = f; });
      $("#channels-title").textContent = "频道列表 - " + (fileObj ? fileObj.name : "");
      fetchChannelsPage();
    }

    function fetchChannelsPage() {
      if (!currentExpandedId) return;
      const search = $("#channels-search").value.trim();
      let url = "/api/source-files/" + currentExpandedId + "/channels?page=" + channelsPage + "&size=" + channelsPageSize;
      if (search) url += "&search=" + encodeURIComponent(search);
      $("#channels-tbody").innerHTML = '<tr><td colspan="6" class="muted" style="text-align:center;padding:16px">加载中…</td></tr>';
      api("GET", url).then((r) => {
        if (!r.ok) { renderChannelsError("加载失败"); return; }
        const d = r.data || {};
        if (d.warming) {
          $("#channels-tbody").innerHTML = '<tr><td colspan="6" class="muted" style="text-align:center;padding:16px">正在采集源数据，请稍候…</td></tr>';
          setTimeout(fetchChannelsPage, 3000);
          return;
        }
        channelsPageData = d.channels || [];
        channelsTotal = d.total || 0;
        $("#channels-info").textContent = "共 " + channelsTotal + " 个频道，第 " + (d.page || 1) + "/" + (Math.ceil(channelsTotal / channelsPageSize) || 1) + " 页";
        renderChannelsPage();
      }).catch((e) => renderChannelsError(String(e)));
    }

    function renderChannelsError(e) {
      $("#channels-tbody").innerHTML = '<tr><td colspan="6" class="muted" style="text-align:center;padding:16px">加载失败: ' + esc(e) + "</td></tr>";
    }

    function channelsSearchDebounced() {
      if (channelsSearchTimer) clearTimeout(channelsSearchTimer);
      channelsSearchTimer = setTimeout(() => { channelsPage = 1; fetchChannelsPage(); }, 400);
    }

    function closeChannels() {
      $("#channels-card").style.display = "none";
      currentExpandedId = null;
      $$("#source-files-tbody tr").forEach((r) => { r.style.background = ""; });
    }

    function renderChannelsPage() {
      const pageData = channelsPageData;
      const total = channelsTotal;
      const totalPages = Math.ceil(total / channelsPageSize) || 1;
      const tb = $("#channels-tbody");
      if (!pageData.length) {
        tb.innerHTML = '<tr><td colspan="6" class="muted" style="text-align:center;padding:16px">无频道数据</td></tr>';
      } else {
        let html = "";
        pageData.forEach((ch, i) => {
          const chName = ch.name || "";
          const mapping = ch.existing_mapping;
          const isManual = mapping && mapping.is_manual == 1;
          let catDisplay, catClass;
          if (isManual) {
            const parts = [];
            if (mapping.content && mapping.content !== "其他频道") parts.push(mapping.content);
            if (mapping.region && mapping.region !== "未知") parts.push(mapping.region);
            if (mapping.quality && mapping.quality !== "未知") parts.push(mapping.quality);
            catDisplay = parts.length ? parts.join(" · ") : "已设置";
            catClass = "manual";
          } else if (mapping) {
            catDisplay = mapping.content || ch.category || "-";
            catClass = "auto";
          } else {
            catDisplay = ch.category || "-";
            catClass = "auto";
          }
          let uaDisplay, uaClass;
          if (ch.ua_override) { uaDisplay = truncateText(ch.user_agent || "", 20); uaClass = "manual"; }
          else if (ch.user_agent) { uaDisplay = truncateText(ch.user_agent, 20); uaClass = "auto"; }
          else { uaDisplay = "-"; uaClass = "auto"; }

          html += '<tr id="ch-row-' + i + '" style="position:relative">';
          html += "<td>" + esc(chName) + "</td>";
          html += '<td class="url-cell" title="' + escapeHtmlAttr(ch.url || "") + '">' + esc(truncateText(ch.url || "", 60)) + "</td>";
          html += "<td>" + esc(ch.group || "-") + "</td>";
          html += '<td><span class="cat-badge ' + uaClass + '" onclick="showCategoryEditor(' + i + ",'" + escapeHtmlAttr(chName) + "')\" title=\"点击修改分类\">" + esc(catDisplay) + "</span></td>";
          html += '<td><span class="cat-badge ' + uaClass + '" onclick="showChannelUaEditor(' + i + ')" title="点击配置 UA">' + esc(uaDisplay) + "</span></td>";
          html += '<td style="white-space:nowrap"><button class="btn" style="padding:5px 10px;font-size:12px" onclick="showCategoryEditor(' + i + ",'" + escapeHtmlAttr(chName) + "')\">分类</button></td>";
          html += "</tr>";
        });
        tb.innerHTML = html;
      }
      const container = $("#channels-pagination");
      if (totalPages <= 1) { container.innerHTML = ""; return; }
      let ph = "";
      ph += '<button onclick="channelsGoPage(' + (channelsPage - 1) + ')"' + (channelsPage <= 1 ? " disabled" : "") + ">上一页</button>";
      const s0 = Math.max(1, channelsPage - 2), e0 = Math.min(totalPages, channelsPage + 2);
      if (s0 > 1) { ph += '<button onclick="channelsGoPage(1)">1</button>'; if (s0 > 2) ph += "<span>…</span>"; }
      for (let p = s0; p <= e0; p++) {
        ph += '<button onclick="channelsGoPage(' + p + ')"' + (p === channelsPage ? ' class="active"' : "") + ">" + p + "</button>";
      }
      if (e0 < totalPages) { if (e0 < totalPages - 1) ph += "<span>…</span>"; ph += '<button onclick="channelsGoPage(' + totalPages + ')">' + totalPages + "</button>"; }
      ph += '<button onclick="channelsGoPage(' + (channelsPage + 1) + ')"' + (channelsPage >= totalPages ? " disabled" : "") + ">下一页</button>";
      container.innerHTML = ph;
    }

    window.channelsGoPage = function (p) { channelsPage = p; fetchChannelsPage(); };

    // ── 分类编辑 popover ──
    function showCategoryEditor(chIdx, chName) {
      if (activePopover) { activePopover.remove(); activePopover = null; }
      editingChannelIdx = chIdx; editingChannelName = chName;
      const ch = channelsPageData[chIdx];
      if (!ch) return;
      const currentMapping = ch.existing_mapping || {};
      const rowEl = $("#ch-row-" + chIdx);
      if (!rowEl) return;
      const dimOrder = ["content", "region", "language", "quality", "media_type", "genre"];
      const dimLabels = { content: "内容分类", region: "地域", language: "语言", quality: "画质", media_type: "媒体类型", genre: "节目类型" };
      let h = '<div class="popover" id="cat-popover">';
      h += '<div class="ptitle">分类编辑 — ' + esc(chName) + "</div>";
      dimOrder.forEach((dim) => {
        const opts = categoryDict[dim] || [];
        const curVal = currentMapping[dim] || "";
        const curVals = curVal ? curVal.split(",") : [];
        const isMulti = dim === "content";
        h += '<div class="dim-row">';
        if (isMulti) {
          h += '<label>' + (dimLabels[dim] || dim) + ' <span style="font-size:11px;opacity:.6">(可多选)</span></label>';
          h += '<select id="cat-dim-' + dim + '" multiple size="5" style="min-height:90px">';
        } else {
          h += "<label>" + (dimLabels[dim] || dim) + "</label>";
          h += '<select id="cat-dim-' + dim + '"><option value="">— 自动 —</option>';
        }
        opts.forEach((o) => {
          const sel = isMulti
            ? (curVals.indexOf(o.value) >= 0 ? " selected" : "")
            : (o.value === curVal ? " selected" : "");
          h += '<option value="' + escapeHtmlAttr(o.value) + '"' + sel + ">" + esc(o.label || o.value) + "</option>";
        });
        h += "</select>";
        if (isMulti) h += '<div style="font-size:11px;opacity:.6;margin-top:2px">多选时输出会复制频道到各分组</div>';
        h += "</div>";
      });
      h += '<div class="pop-actions">';
      h += '<button class="btn" onclick="hideCategoryEditor()">取消</button>';
      h += '<button class="btn danger" onclick="clearChannelCategory()">清除手动分类</button>';
      h += '<button class="btn primary" onclick="saveChannelCategory()">保存</button>';
      h += "</div></div>";
      const wrap = document.createElement("div"); wrap.innerHTML = h;
      const pop = wrap.firstChild;
      rowEl.parentNode.insertBefore(pop, rowEl.nextSibling);
      activePopover = pop;
    }
    window.showCategoryEditor = showCategoryEditor;
    window.hideCategoryEditor = function () {
      if (activePopover) { activePopover.remove(); activePopover = null; }
      editingChannelIdx = -1; editingChannelName = "";
    };
    window.saveChannelCategory = async function () {
      if (!editingChannelName) return;
      const dims = ["content", "region", "language", "quality", "media_type", "genre"];
      const cats = {};
      for (const d of dims) {
        const el = $("#cat-dim-" + d);
        if (!el) continue;
        if (d === "content" && el.multiple) {
          const sel = [];
          for (const o of el.options) if (o.selected && o.value) sel.push(o.value);
          if (sel.length) cats[d] = sel.join(",");
        } else if (el.value) {
          cats[d] = el.value;
        }
      }
      const r = await api("PUT", "/api/channel-mapping/" + encodeURIComponent(editingChannelName), cats);
      if (r.ok) {
        toast("分类已保存: " + editingChannelName, "success");
        if (channelsPageData[editingChannelIdx]) channelsPageData[editingChannelIdx].existing_mapping = Object.assign({ is_manual: 1 }, cats);
        window.hideCategoryEditor(); renderChannelsPage();
      } else {
        toast(r.data.error || "保存失败", "error");
      }
    };
    window.clearChannelCategory = async function () {
      if (!editingChannelName) return;
      if (!confirm("确认清除 \"" + editingChannelName + "\" 的手动分类？")) return;
      const r = await api("DELETE", "/api/channel-mapping/" + encodeURIComponent(editingChannelName));
      if (r.ok) {
        toast("手动分类已清除", "success");
        if (channelsPageData[editingChannelIdx]) channelsPageData[editingChannelIdx].existing_mapping = null;
        window.hideCategoryEditor(); renderChannelsPage();
      } else {
        toast(r.data.error || "清除失败", "error");
      }
    };

    // ── 文件级 UA ──
    function showFileUaEditor(fileId) {
      if (uaFilePopover) { uaFilePopover.remove(); uaFilePopover = null; }
      if (activePopover) { activePopover.remove(); activePopover = null; }
      let fileObj = null;
      sourceFilesData.forEach((f) => { if (f.id === fileId) fileObj = f; });
      if (!fileObj) return;
      const ua = fileObj.ua_settings || {};
      const enabled = ua.enabled || false;
      const uaValue = ua.ua_value || "";
      const uaPosition = ua.ua_position || "extinf";
      let h = '<div class="popover" id="ua-file-popover" style="min-width:360px">';
      h += '<div class="ptitle">UA 配置 — ' + esc(fileObj.name) + "</div>";
      h += '<div class="dim-row" style="margin-bottom:12px"><label style="min-width:auto"><input type="checkbox" id="ua-enabled" ' + (enabled ? "checked" : "") + ' onchange="onFileUaToggle()"> 启用 User-Agent</label></div>';
      h += '<div class="dim-row"><label>输出位置</label><select id="ua-position"><option value="extinf"' + (uaPosition === "extinf" ? " selected" : "") + '>EXTINF 属性</option><option value="url"' + (uaPosition === "url" ? " selected" : "") + ">URL 参数</option></select></div>";
      h += '<div class="dim-row" style="align-items:flex-start"><label style="margin-top:4px">UA 值</label><textarea id="ua-value" style="min-height:60px;font-size:12px;resize:vertical" placeholder="Mozilla/5.0 ...">' + escapeHtml(uaValue) + "</textarea></div>";
      h += '<div style="font-size:11px;opacity:.6;margin:4px 0 0 54px">快捷: <a href="javascript:void(0)" onclick="setQuickUa(\'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36\')">Chrome</a> | <a href="javascript:void(0)" onclick="setQuickUa(\'VLC/3.0.18 LibVLC/3.0.18\')">VLC</a> | <a href="javascript:void(0)" onclick="setQuickUa(\'okhttp/4.9.3\')">okhttp</a></div>';
      h += '<div class="pop-actions"><button class="btn" onclick="hideFileUaEditor()">取消</button>';
      if (enabled) h += '<button class="btn danger" onclick="clearFileUa(\'' + esc(fileId) + "')\">清除</button>";
      h += '<button class="btn primary" onclick="saveFileUa(\'' + esc(fileId) + "')\">保存</button></div></div>";
      const wrap = document.createElement("div"); wrap.innerHTML = h;
      const pop = wrap.firstChild;
      const row = $("#file-row-" + fileId);
      if (row) row.parentNode.insertBefore(pop, row.nextSibling); else document.body.appendChild(pop);
      uaFilePopover = pop;
      window.onFileUaToggle();
    }
    window.showFileUaEditor = showFileUaEditor;
    window.onFileUaToggle = function () {
      const en = $("#ua-enabled").checked;
      $("#ua-position").disabled = !en; $("#ua-value").disabled = !en;
    };
    window.setQuickUa = function (v) { $("#ua-value").value = v; };
    window.hideFileUaEditor = function () { if (uaFilePopover) { uaFilePopover.remove(); uaFilePopover = null; } };
    window.saveFileUa = async function (fileId) {
      const enabled = $("#ua-enabled").checked;
      const uaValue = $("#ua-value").value.trim();
      const uaPosition = $("#ua-position").value;
      if (enabled && !uaValue) { toast("启用 UA 时必须填写 UA 值", "error"); return; }
      const r = await api("PUT", "/api/source-files/" + fileId + "/ua", { enabled: enabled, ua_value: uaValue, ua_position: uaPosition });
      if (r.ok) { toast("UA 设置已保存", "success"); window.hideFileUaEditor(); loadSourceFiles(); }
      else toast(r.data.error || "保存失败", "error");
    };
    window.clearFileUa = async function (fileId) {
      if (!confirm("确认清除该源文件的 UA 配置？")) return;
      const r = await api("DELETE", "/api/source-files/" + fileId + "/ua");
      if (r.ok) { toast("UA 设置已清除", "success"); window.hideFileUaEditor(); loadSourceFiles(); }
      else toast(r.data.error || "清除失败", "error");
    };

    // ── 频道级 UA ──
    function showChannelUaEditor(chIdx) {
      if (uaChannelPopover) { uaChannelPopover.remove(); uaChannelPopover = null; }
      if (activePopover) { activePopover.remove(); activePopover = null; }
      const ch = channelsPageData[chIdx];
      if (!ch) return;
      const rowEl = $("#ch-row-" + chIdx);
      if (!rowEl) return;
      const isOverride = ch.ua_override || false;
      const uaValue = ch.user_agent || "";
      const uaPosition = ch.ua_position || "extinf";
      let h = '<div class="popover" id="ua-ch-popover" style="min-width:360px">';
      h += '<div class="ptitle">频道 UA — ' + esc(ch.name || "") + "</div>";
      h += isOverride ? '<div style="font-size:11px;color:var(--warn);margin-bottom:8px">⚠ 当前为频道级覆盖</div>'
        : (ch.user_agent ? '<div style="font-size:11px;opacity:.6;margin-bottom:8px">当前 UA 来自文件级/内置，设置后将覆盖</div>'
          : '<div style="font-size:11px;opacity:.6;margin-bottom:8px">当前无 UA，设置后单独配置</div>');
      h += '<div class="dim-row"><label>输出位置</label><select id="ch-ua-position"><option value="extinf"' + (uaPosition === "extinf" ? " selected" : "") + '>EXTINF 属性</option><option value="url"' + (uaPosition === "url" ? " selected" : "") + ">URL 参数</option></select></div>";
      h += '<div class="dim-row" style="align-items:flex-start"><label style="margin-top:4px">UA 值</label><textarea id="ch-ua-value" style="min-height:60px;font-size:12px;resize:vertical" placeholder="留空清除覆盖">' + escapeHtml(uaValue) + "</textarea></div>";
      h += '<div style="font-size:11px;opacity:.6;margin:4px 0 0 54px">快捷: <a href="javascript:void(0)" onclick="setQuickChUa(\'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36\')">Chrome</a> | <a href="javascript:void(0)" onclick="setQuickChUa(\'VLC/3.0.18 LibVLC/3.0.18\')">VLC</a> | <a href="javascript:void(0)" onclick="setQuickChUa(\'okhttp/4.9.3\')">okhttp</a></div>';
      h += '<div class="pop-actions"><button class="btn" onclick="hideChannelUaEditor()">取消</button>';
      if (isOverride) h += '<button class="btn danger" onclick="clearChannelUa(' + chIdx + ')">清除覆盖</button>';
      h += '<button class="btn primary" onclick="saveChannelUa(' + chIdx + ')">保存</button></div></div>';
      const wrap = document.createElement("div"); wrap.innerHTML = h;
      const pop = wrap.firstChild;
      rowEl.parentNode.insertBefore(pop, rowEl.nextSibling);
      uaChannelPopover = pop;
    }
    window.showChannelUaEditor = showChannelUaEditor;
    window.setQuickChUa = function (v) { $("#ch-ua-value").value = v; };
    window.hideChannelUaEditor = function () { if (uaChannelPopover) { uaChannelPopover.remove(); uaChannelPopover = null; } };
    window.saveChannelUa = async function (chIdx) {
      const ch = channelsPageData[chIdx];
      if (!ch) return;
      const uaValue = $("#ch-ua-value").value.trim();
      const uaPosition = $("#ch-ua-position").value;
      if (!uaValue) {
        if (ch.ua_override) { await window.clearChannelUa(chIdx); }
        else toast("请输入 UA 值", "error");
        return;
      }
      const r = await api("PUT", "/api/source-files/" + currentExpandedId + "/channel-ua", { url: ch.url, ua_value: uaValue, ua_position: uaPosition });
      if (r.ok) {
        toast("频道 UA 已保存", "success");
        channelsPageData[chIdx].user_agent = uaValue;
        channelsPageData[chIdx].ua_position = uaPosition;
        channelsPageData[chIdx].ua_override = true;
        window.hideChannelUaEditor(); renderChannelsPage();
      } else toast(r.data.error || "保存失败", "error");
    };
    window.clearChannelUa = async function (chIdx) {
      const ch = channelsPageData[chIdx];
      if (!ch) return;
      const r = await api("DELETE", "/api/source-files/" + currentExpandedId + "/channel-ua?url=" + encodeURIComponent(ch.url));
      if (r.ok) { toast("频道 UA 覆盖已删除", "success"); window.hideChannelUaEditor(); expandChannels(currentExpandedId); }
      else toast(r.data.error || "清除失败", "error");
    };

    // ── 添加 / 删除 / 采集 ──
    window.showAddFileForm = function () {
      $("#add-file-modal").style.display = "flex";
      $("#file-type").value = "online"; $("#file-value").value = ""; $("#add-file-result").innerHTML = "";
      onTypeChange();
    };
    window.hideAddFileForm = function () { $("#add-file-modal").style.display = "none"; };
    function onTypeChange() {
      const type = $("#file-type").value;
      const label = $("#value-label"), input = $("#file-value"), help = $("#value-help"), mg = $("#method-group");
      if (type === "online") { label.textContent = "文件 URL"; input.placeholder = "https://example.com/iptv.m3u"; help.textContent = "包含多个视频源的 m3u/txt 文件 URL，添加后自动下载"; mg.style.display = "none"; }
      else if (type === "github") { label.textContent = "GitHub 仓库"; input.placeholder = "owner/repo 或 owner/repo/branch"; help.textContent = "格式: owner/repo、owner/repo/branch、owner/repo/branch/path。添加后需点击\"采集所有源\""; mg.style.display = ""; }
      else { label.textContent = "本地路径"; input.placeholder = "./config/sources/myfile.m3u"; help.textContent = "项目根目录下的文件或目录，目录下所有 m3u/txt 都会被解析"; mg.style.display = "none"; }
    }
    window.onTypeChange = onTypeChange;
    window.submitAddFile = async function () {
      const type = $("#file-type").value;
      const value = $("#file-value").value.trim();
      const resultDiv = $("#add-file-result");
      if (!value) { resultDiv.innerHTML = '<div class="msg err">地址不能为空</div>'; return; }
      resultDiv.innerHTML = '<div class="msg">处理中…</div>';
      const body = { type: type, value: value };
      if (type === "github") body.download_method = $("#download-method").value;
      const r = await api("POST", "/api/source-files", body);
      if (r.ok || (r.data && (r.data.status === "created" || r.data.status === "exists"))) {
        window.hideAddFileForm();
        toast(r.data.message || "添加成功", r.data.status === "exists" ? "info" : "success");
        loadSourceFiles();
      } else {
        resultDiv.innerHTML = '<div class="msg err">' + esc(r.data.error || "操作失败") + "</div>";
      }
    };
    window.deleteSourceFile = async function (fileId) {
      let fileName = fileId;
      sourceFilesData.forEach((f) => { if (f.id === fileId) fileName = f.name; });
      if (!confirm("确认删除源文件 \"" + fileName + "\"？")) return;
      const r = await api("DELETE", "/api/source-files/" + fileId);
      if (r.ok) { toast("删除成功", "success"); if (currentExpandedId === fileId) closeChannels(); loadSourceFiles(); }
      else toast(r.data.error || "删除失败", "error");
    };
    window.changeDownloadMethod = async function (fileId, currentMethod) {
      const methods = ["raw", "mirror"];
      const labels = { raw: "raw.githubusercontent.com 直连", mirror: "镜像站（需先配置 github_mirror）" };
      const dm = prompt("选择下载通道:\n1. raw（直连）\n2. mirror（镜像，需先配置 github_mirror）\n\n当前: " + (labels[currentMethod] || currentMethod), currentMethod);
      if (!dm || !methods.includes(dm)) return;
      const r = await api("PUT", "/api/source-files/" + fileId, { download_method: dm });
      if (r.ok) { toast("下载方式已更新", "success"); loadSourceFiles(); }
      else toast(r.data.error || "更新失败", "error");
    };

    async function collectAllSources() {
      const btn = $("#btn-collect");
      btn.disabled = true; btn.textContent = "采集中…";
      const r = await api("POST", "/api/sources/collect");
      toast(r.data.message || "源采集已启动", "info");
      setTimeout(() => { btn.disabled = false; btn.textContent = "采集所有源"; loadSourceFiles(); }, 5000);
    }

    // ── 绑定工具栏 ──
    $("#btn-add-file").onclick = window.showAddFileForm;
    $("#btn-add-close").onclick = window.hideAddFileForm;
    $("#add-file-backdrop").onclick = window.hideAddFileForm;
    $("#btn-add-cancel").onclick = window.hideAddFileForm;
    $("#btn-add-submit").onclick = window.submitAddFile;
    $("#btn-refresh").onclick = loadSourceFiles;
    $("#btn-close-channels").onclick = closeChannels;
    $("#channels-search").oninput = channelsSearchDebounced;
    if ($("#btn-collect")) $("#btn-collect").onclick = collectAllSources;

    await loadCategoryDictionary();
    loadSourceFiles();
  }

  // ── rules ────────────────────────────────────────────────────
  async function initRules() {
    if (!$("#rules-body")) return;
    async function load() {
      const [rl, di, ex] = await Promise.all([
        api("GET", "/api/rules"), api("GET", "/api/rules/dimensions"), api("GET", "/api/rules/exclusions"),
      ]);
      $("#rules-body").innerHTML = (rl.data.rules || rl.data || []).map((r) =>
        `<tr><td>${esc(r.rule_type)}</td><td>${esc(r.name)}</td><td>${esc((r.keywords || []).join(", "))}</td>
        <td>${r.priority}</td><td>${r.is_active ? "启用" : "停用"}</td>
        <td><button class="btn" data-edit="${r.id}">编辑</button> <button class="btn danger" data-del="${r.id}">删</button></td></tr>`
      ).join("") || '<tr><td colspan="6" class="muted">无规则</td></tr>';
      $("#dim-list").innerHTML = (di.data.dimensions || di.data || []).map((d) =>
        `<span class="badge ok">${esc(d.dim_name)} <small class="muted">${esc(d.dim_key)}</small></span>`
      ).join(" ");
      $("#excl-body").innerHTML = (ex.data.exclusions || ex.data || []).map((e) =>
        `<tr><td>${esc(e.province_keyword)}</td><td>${esc(e.excluded_keyword)}</td><td>${esc(e.note)}</td>
        <td><button class="btn danger" data-exdel="${e.id}">删</button></td></tr>`
      ).join("") || '<tr><td colspan="4" class="muted">无排除</td></tr>';
    }
    let editing = null;
    $("#rule-add").onclick = () => { editing = null; $("#rule-edit-box").classList.remove("hidden"); };
    $("#rule-cancel").onclick = () => { $("#rule-edit-box").classList.add("hidden"); };
    $("#rule-save").onclick = async () => {
      const body = {
        rule_type: $("#rule-type").value, name: $("#rule-name").value,
        keywords: $("#rule-keywords").value.split(",").map((s) => s.trim()).filter(Boolean),
        priority: parseInt($("#rule-priority").value || "100"), is_active: true,
      };
      const r = editing ? await api("PUT", "/api/rules/" + editing, body) : await api("POST", "/api/rules", body);
      $("#rule-msg").textContent = r.ok ? "已保存" : (r.data.error || "失败");
      $("#rule-msg").className = "msg " + (r.ok ? "ok" : "err");
      if (r.ok) { $("#rule-edit-box").classList.add("hidden"); load(); }
    };
    $("#rules-body").onclick = async (e) => {
      const id = e.target.getAttribute("data-edit") || e.target.getAttribute("data-del");
      if (!id) return;
      if (e.target.getAttribute("data-del") && !confirm("确认删除规则？")) return;
      if (e.target.getAttribute("data-del")) { await api("DELETE", "/api/rules/" + id); load(); return; }
      const r = await api("GET", "/api/rules"); const rule = (r.data.rules || []).find((x) => String(x.id) === id);
      if (rule) { editing = id; $("#rule-type").value = rule.rule_type; $("#rule-name").value = rule.name;
        $("#rule-keywords").value = (rule.keywords || []).join(", "); $("#rule-priority").value = rule.priority;
        $("#rule-edit-box").classList.remove("hidden"); }
    };
    $("#excl-add").onclick = async () => {
      const a = prompt("省份关键词"); const b = prompt("排除关键词"); if (!a || !b) return;
      await api("POST", "/api/rules/exclusions", { province_keyword: a, excluded_keyword: b }); load();
    };
    $("#excl-body").onclick = async (e) => {
      const id = e.target.getAttribute("data-exdel"); if (!id) return;
      await api("DELETE", "/api/rules/exclusions/" + id); load();
    };
    $("#rule-reimport").onclick = async () => { const r = await api("POST", "/api/rules/reimport"); alert(r.data.error || "已重新导入"); load(); };
    $("#rule-reset").onclick = async () => { if (confirm("重置分类字典为默认？")) { await api("POST", "/api/category-dictionary/reset-defaults"); } };
    load();
  }

  // ── config ───────────────────────────────────────────────────
  async function initConfig() {
    if (!$("#cfg-form")) return;
    let fields = {}, values = {};
    function renderCfgField(sec, k, field, curVal) {
      const typ = field && field.type ? field.type : "string";
      const opts = field && field.options ? field.options : null;
      const optLabels = (field && field.option_labels) ? field.option_labels : null;
      const secret = !!(field && field.secret);
      const label = (field && field.label) ? field.label : k;
      const desc = (field && field.description) ? field.description : "";
      const fv = (field && field.value != null && field.value !== "") ? field.value : "";
      const val = (curVal != null && curVal !== "") ? curVal : ((field && field.default != null) ? field.default : "");
      const id = "cfg-" + sec + "-" + k;
      const labelHtml = `<span class="cfg-label" style="min-width:220px;display:flex;flex-direction:column;gap:2px;"><span style="font-weight:600;">${esc(label)}</span>${desc ? `<span class="muted" style="font-size:12px;line-height:1.4;">${esc(desc)}</span>` : ""}</span>`;
      if (secret) {
        const set = (curVal != null && curVal !== "") || fv !== "" || (field && field.set);
        const ph = set ? "（已保存，留空或 ******** 表示不修改）" : "（未设置）";
        const initVal = set ? "********" : "";
        return `<label class="row" style="margin:8px 0;align-items:flex-start;">${labelHtml}<input class="input" type="password" id="${id}" data-sec="${esc(sec)}" data-key="${esc(k)}" data-secret="1" value="${esc(initVal)}" placeholder="${esc(ph)}"></label>`;
      }
      if (typ === "bool") {
        const checked = ("" + val) === "True" || ("" + val) === "true" || ("" + val) === "1" ? "checked" : "";
        return `<label class="row" style="margin:8px 0;align-items:flex-start;">${labelHtml}<input type="checkbox" id="${id}" data-sec="${esc(sec)}" data-key="${esc(k)}" ${checked}></label>`;
      }
      if (opts && opts.length) {
        const cur = "" + val;
        const optsHtml = opts.map((o) => {
          const ol = (optLabels && optLabels[o]) ? optLabels[o] : o;
          return `<option value="${esc(o)}"${o === cur ? " selected" : ""}>${esc(ol)}</option>`;
        }).join("");
        return `<label class="row" style="margin:8px 0;align-items:flex-start;">${labelHtml}<select class="input" id="${id}" data-sec="${esc(sec)}" data-key="${esc(k)}">${optsHtml}</select></label>`;
      }
      const inputType = (typ === "int" || typ === "float") ? "number" : "text";
      return `<label class="row" style="margin:8px 0;align-items:flex-start;">${labelHtml}<input class="input" type="${inputType}" id="${id}" data-sec="${esc(sec)}" data-key="${esc(k)}" value="${esc(val)}"></label>`;
    }

    function bindGithubTest() {
      const btn = $("#cfg-github-test");
      if (!btn) return;
      btn.onclick = async () => {
        const tEl = $("#cfg-GitHub-api_token");
        const t = tEl ? tEl.value.trim() : "";
        // 输入为掩码(已保存)或空时，不传 token，让后端用已保存的 GitHub.api_token 校验
        const body = (t && t !== "********") ? { token: t } : {};
        const msg = $("#cfg-github-msg");
        msg.textContent = "测试中…"; msg.className = "msg";
        const r = await api("POST", "/api/github/test-token", body);
        if (!r.ok) { msg.textContent = (r.data && r.data.error) || "请求失败"; msg.className = "msg err"; return; }
        if (r.data.valid) {
          msg.textContent = "Token 有效（额度 " + (r.data.remaining == null ? "?" : r.data.remaining) + "/" + (r.data.limit == null ? "?" : r.data.limit) + "）";
          msg.className = "msg ok";
        } else {
          msg.textContent = "Token 无效：" + (r.data.error || ("HTTP " + (r.data.status || "")));
          msg.className = "msg err";
        }
      };
    }

    async function load() {
      const [f, v] = await Promise.all([api("GET", "/api/config/fields"), api("GET", "/api/config")]);
      fields = f.data.fields || f.data || {}; values = v.data || {};
      const secs = (f.data.sections && f.data.sections.length) ? f.data.sections : Object.keys(fields);
      const titles = f.data.section_titles || {};
      $("#cfg-form").innerHTML = secs.map((sec) => {
        const fv = fields[sec] || {}; const vv = values[sec] || {};
        const items = Object.keys(fv).length ? fv : vv;
        const secTitle = titles[sec] || sec;
        // GitHub 段追加「测试 Token」按钮，让用户填完即可就地验证
        const extra = sec === "GitHub"
          ? `<div style="margin-top:8px;display:flex;align-items:center;gap:8px"><button type="button" class="btn primary" id="cfg-github-test">测试 GitHub Token</button><span id="cfg-github-msg" class="msg"></span></div>`
          : "";
        return `<div class="card"><h3>${esc(secTitle)}</h3>${Object.keys(items).map((k) => renderCfgField(sec, k, items[k], vv[k])).join("")}${extra}</div>`;
      }).join("") || '<div class="muted">无配置</div>';
      bindGithubTest();
    }
    $("#cfg-save").onclick = async () => {
      const cfg = {};
      $$("#cfg-form [data-sec]").forEach((el) => {
        const s = el.getAttribute("data-sec"), k = el.getAttribute("data-key");
        let v;
        if (el.type === "checkbox") v = el.checked ? "True" : "False";
        else v = el.value;
        if (el.getAttribute("data-secret") && (v === "" || v === "********")) return; // 密钥留空或掩码不改
        cfg[s] = cfg[s] || {}; cfg[s][k] = v;
      });
      const r = await api("PUT", "/api/config", cfg);
      $("#cfg-msg").textContent = r.ok ? "已保存" : (r.data.error || "失败");
      $("#cfg-msg").className = "msg " + (r.ok ? "ok" : "err");
      if (r.ok) location.reload();
    };
    $("#cfg-reload").onclick = load;
    $("#cfg-validate").onclick = async () => {
      let firstErr = ""; let checked = 0;
      const els = $$("#cfg-form [data-sec]");
      for (const el of els) {
        const s = el.getAttribute("data-sec"), k = el.getAttribute("data-key");
        let v;
        if (el.type === "checkbox") v = el.checked ? "True" : "False";
        else v = el.value;
        if (el.getAttribute("data-secret") && (v === "" || v === "********")) continue; // 密钥掩码不校验
        const r = await api("POST", "/api/config/validate", { section: s, key: k, value: v });
        checked++;
        if (r.data && r.data.valid === false) { firstErr = "[" + s + "." + k + "] " + (r.data.error || "校验失败"); break; }
      }
      const msg = firstErr ? firstErr : (checked ? "校验通过（共 " + checked + " 项）" : "无可校验项");
      $("#cfg-msg").textContent = msg;
      $("#cfg-msg").className = "msg " + (firstErr ? "err" : "ok");
    };
    load();
  }

  // ── system ───────────────────────────────────────────────────
  async function initSystem() {
    if (!$("#sys-stats")) return;
    const [info, net] = await Promise.all([api("GET", "/api/system/info"), api("GET", "/api/system/network")]);
    // 注意：api 返回的 data 是整段响应对象，需再取 .info / .network 一层
    const s = (info.data && info.data.info) || {};
    $("#sys-stats").innerHTML = [
      ["频道总数", s.total_sources], ["有效", s.valid], ["排除", s.invalid], ["CPU", s.cpu], ["内存", s.memory],
    ].map(([l, n]) => `<div class="card stat"><div class="n">${esc(n == null ? "–" : n)}</div><div class="l">${esc(l)}</div></div>`).join("");
    const detailBox = $("#sys-detail");
    if (detailBox) {
      const ff = s.ffprobe_available ? ("可用" + (s.ffprobe_path ? " (" + s.ffprobe_path + ")" : "")) : "不可用";
      const rows = [
        ["主机名", s.hostname], ["平台", s.platform], ["Go 版本", s.go_version],
        ["CPU 核心", s.num_cpu], ["协程数", s.goroutines], ["堆内存", (s.mem_alloc_mb == null ? "–" : s.mem_alloc_mb + " MB")],
        ["ffprobe", ff], ["GitHub Token", s.github_token_set ? "已设置" : "未设置"], ["更新时间", s.timestamp],
      ];
      detailBox.innerHTML = rows.map(([l, v]) =>
        `<label class="row" style="margin:6px 0;"><span class="muted" style="min-width:160px;">${esc(l)}</span><b>${esc(v == null ? "–" : v)}</b></label>`
      ).join("");
    }
    const n = (net.data && net.data.network) || {};
    const isSecret = (k) => k === "proxy_password" || k === "proxy_username" || k === "github_token";
    const tokenSet = !!(s && s.github_token_set);
    let netHtml = Object.keys(n).map((k) => {
      const secret = isSecret(k);
      const val = secret ? "" : n[k];
      let ph = "";
      if (secret) {
        if (k === "github_token") ph = tokenSet ? "（已设置，留空不改）" : "（未设置）";
        else ph = n[k] ? "（已设置，留空不改）" : "（未设置）";
      }
      return `<label class="row" style="margin:6px 0;"><span class="muted" style="min-width:160px;">${esc(k)}</span>
      <input class="input" type="${secret ? "password" : "text"}" data-net="${esc(k)}" value="${esc(val)}" placeholder="${esc(ph)}"></label>`;
    }).join("");
    $("#net-info").innerHTML = netHtml;
    const saveBtn = $("#net-save");
    if (saveBtn) {
      saveBtn.onclick = async () => {
        const payload = {};
        $$("#net-info [data-net]").forEach((el) => {
          const k = el.dataset.net, v = el.value;
          if (isSecret(k) && v === "") return; // 密码留空表示不修改
          payload[k] = v;
        });
        const r = await api("POST", "/api/system/network", payload);
        const msg = $("#net-msg");
        msg.textContent = r.ok ? "网络配置已保存" : (r.data.error || "保存失败");
        msg.className = "msg " + (r.ok ? "ok" : "err");
      };
    }
    $("#net-test").onclick = async () => {
      const tEl = document.querySelector('[data-net="github_token"]');
      const t = tEl ? tEl.value.trim() : "";
      const r = await api("POST", "/api/github/test-token", t ? { token: t } : {});
      const msg = $("#net-msg");
      if (!r.ok) { msg.textContent = r.data.error || "请求失败"; msg.className = "msg err"; return; }
      if (r.data.valid) { msg.textContent = "Token 有效（剩余额度 " + (r.data.remaining == null ? "?" : r.data.remaining) + "）"; msg.className = "msg ok"; }
      else { msg.textContent = "Token 无效：" + (r.data.error || ("HTTP " + (r.data.status || ""))); msg.className = "msg err"; }
    };
  }

  // ── logs + audit (合并：Tab 切换) ────────────────────────────
  async function initLogs() {
    const logsBox = $("#logs-box");
    const auditBody = $("#audit-body");
    const auditTabBtn = $('.tab-btn[data-tab="audit-logs"]');
    let logTimer = null;
    let auditPage = 1;
    const auditPageSize = 50;

    // ── Tab 切换 ──
    $$(".tab-btn").forEach((btn) => {
      btn.onclick = () => {
        const name = btn.dataset.tab;
        $$(".tab-btn").forEach((b) => { b.classList.toggle("active", b === btn); b.setAttribute("aria-selected", b === btn); });
        $$(".tab-content").forEach((c) => c.classList.toggle("active", c.id === "tab-" + name));
        if (name === "audit-logs") { loadAuditActions(); loadAuditLogs(1); stopLogAuto(); }
        else { startLogAuto(); }
      };
    });

    // ── 应用日志 ──
    function filterByLevel(lines, level) {
      if (!level || level === "ALL") return lines;
      const re = new RegExp("\\b" + level + "\\b");
      return lines.filter((l) => re.test(l));
    }
    async function loadLogs() {
      if (!logsBox) return;
      const tail = $("#log-tail").value || 500;
      const level = $("#log-level").value;
      const r = await api("GET", "/api/logs?lines=" + encodeURIComponent(tail));
      const lines = (r.data && r.data.lines) || [];
      const shown = filterByLevel(lines, level);
      logsBox.textContent = shown.join("\n") || "无日志";
      const cnt = $("#log-count");
      if (cnt) cnt.textContent = "共 " + shown.length + " 行" + (shown.length !== lines.length ? "（已按级别过滤）" : "");
    }
    function startLogAuto() {
      if (logTimer) return;
      if ($("#log-autorefresh") && $("#log-autorefresh").checked) logTimer = setInterval(loadLogs, 5000);
    }
    function stopLogAuto() { if (logTimer) { clearInterval(logTimer); logTimer = null; } }
    $("#logs-refresh").onclick = loadLogs;
    $("#log-level").onchange = loadLogs;
    $("#log-tail").onchange = loadLogs;
    $("#log-autorefresh").onchange = () => { if ($("#log-autorefresh").checked) startLogAuto(); else stopLogAuto(); };

    // ── 审计日志 ──
    async function loadAuditActions() {
      if (!auditTabBtn) return;
      const ra = await api("GET", "/api/audit/actions");
      const sel = $("#audit-action");
      (ra.data.actions || []).forEach((a) => { const o = document.createElement("option"); o.value = a; o.textContent = a; sel.appendChild(o); });
    }
    async function loadAuditLogs(page) {
      if (!auditBody) return;
      auditPage = page || 1;
      const action = $("#audit-action").value;
      const user = $("#audit-user").value.trim();
      const q = new URLSearchParams({ limit: auditPageSize, offset: (auditPage - 1) * auditPageSize });
      if (action) q.set("action", action);
      if (user) q.set("username", user);
      const r = await api("GET", "/api/audit?" + q.toString());
      const entries = (r.data && r.data.entries) || [];
      const total = r.data.total || 0;
      auditBody.innerHTML = entries.map((e) =>
        `<tr><td>${esc(e.created_at)}</td><td>${esc(e.username)}</td><td>${esc(e.action)}</td>
        <td>${esc(e.target)}</td><td>${esc(e.detail)}</td><td>${esc(e.ip_address)}</td></tr>`).join("") || '<tr><td colspan="6" class="muted">无记录</td></tr>';
      const tot = $("#audit-total");
      if (tot) tot.textContent = "共 " + total + " 条";
      renderAuditPagination(total);
    }
    function renderAuditPagination(total) {
      const box = $("#audit-pagination");
      if (!box) return;
      const totalPages = Math.ceil(total / auditPageSize) || 1;
      if (totalPages <= 1) { box.innerHTML = ""; return; }
      let h = "";
      h += '<button ' + (auditPage <= 1 ? "disabled" : "") + ' onclick="auditGoPage(' + (auditPage - 1) + ')">上一页</button>';
      for (let p = 1; p <= totalPages; p++) {
        h += '<button class="' + (p === auditPage ? "active" : "") + '" onclick="auditGoPage(' + p + ')">' + p + "</button>";
      }
      h += '<button ' + (auditPage >= totalPages ? "disabled" : "") + ' onclick="auditGoPage(' + (auditPage + 1) + ')">下一页</button>';
      box.innerHTML = h;
    }
    window.auditGoPage = function (p) { loadAuditLogs(p); };
    if ($("#audit-refresh")) $("#audit-refresh").onclick = () => loadAuditLogs(auditPage);

    // ── init ──
    loadLogs();
    startLogAuto();
    if (auditTabBtn) loadAuditActions();
  }

  // ── users ────────────────────────────────────────────────────
  async function initUsers() {
    if (!$("#users-body")) return;
    async function load() {
      const r = await api("GET", "/api/users");
      $("#users-body").innerHTML = (r.data.users || r.data || []).map((u) =>
        `<tr><td>${u.id}</td><td>${esc(u.username)}</td><td>${esc(u.role)}</td><td>${esc(u.display_name)}</td>
        <td>${u.is_active ? "启用" : "停用"}</td>
        <td><button class="btn" data-edit="${u.id}">编辑</button> <button class="btn danger" data-del="${u.id}">删</button></td></tr>`).join("");
    }
    let editing = null;
    $("#user-add").onclick = () => { editing = null; $("#user-edit-box").classList.remove("hidden"); };
    $("#user-cancel").onclick = () => $("#user-edit-box").classList.add("hidden");
    $("#user-save").onclick = async () => {
      const body = { username: $("#user-name").value, role: $("#user-role").value,
        password: $("#user-pass").value, is_active: $("#user-active").checked };
      const r = editing ? await api("PUT", "/api/users/" + editing, body) : await api("POST", "/api/users", body);
      $("#user-msg").textContent = r.ok ? "已保存" : (r.data.error || "失败");
      $("#user-msg").className = "msg " + (r.ok ? "ok" : "err");
      if (r.ok) { $("#user-edit-box").classList.add("hidden"); load(); }
    };
    $("#users-body").onclick = async (e) => {
      const id = e.target.getAttribute("data-edit") || e.target.getAttribute("data-del"); if (!id) return;
      if (e.target.getAttribute("data-del") && !confirm("确认删除用户？")) return;
      if (e.target.getAttribute("data-del")) { await api("DELETE", "/api/users/" + id); load(); return; }
      const r = await api("GET", "/api/users"); const u = (r.data.users || []).find((x) => String(x.id) === id);
      if (u) { editing = id; $("#user-name").value = u.username; $("#user-role").value = u.role; $("#user-active").checked = u.is_active; $("#user-edit-box").classList.remove("hidden"); }
    };
    load();
  }

  // ── test (realtime, SSE) ─────────────────────────────────────
  let es = null, sessionId = null;
  const CATEGORY_LABELS = {
    'connection_failed': '连接失败',
    'dns_failed': 'DNS解析失败',
    'auth_blocked': '鉴权/防盗链',
    'not_found': '资源不存在',
    'timeout': '超时',
    'network_incompatible': '网络不兼容',
    'ad_playlist': '广告/占位源',
    'no_valid_streams': '无有效流',
    'no_probe_tool_available': '无探测工具',
    'json_parse_error': '解析错误',
    'global_blacklist': '全局黑名单',
    'frozen': '已冻结冷却',
    'aborted': '已中断',
    'unknown': '未知',
  };
  let currentCategoryFilter = '';

  function renderErrorBreakdown(bd) {
    const box = $("#error-breakdown");
    if (!box) return;
    bd = bd || {};
    const keys = Object.keys(bd).sort((a, b) => bd[b] - bd[a]);
    if (!keys.length) { box.innerHTML = ''; return; }
    let html = '<div class="bd-title">失败原因分布（点击类别可筛选结果）</div><div class="bd-chips">';
    keys.forEach((k) => {
      const label = CATEGORY_LABELS[k] || k;
      const active = (currentCategoryFilter === k) ? ' active' : '';
      html += '<span class="bd-chip cat-' + k + active + '" onclick="filterByCategory(\'' + k + '\')">'
        + esc(label) + ' <b>' + bd[k] + '</b></span>';
    });
    if (currentCategoryFilter) {
      html += '<span class="bd-clear" onclick="clearCategoryFilter()">清除筛选</span>';
    }
    html += '</div>';
    box.innerHTML = html;
  }
  window.filterByCategory = function (k) {
    currentCategoryFilter = (currentCategoryFilter === k) ? '' : k;
    renderErrorBreakdown(lastBreakdown);
    renderResults(lastResults);
  };
  window.clearCategoryFilter = function () {
    currentCategoryFilter = '';
    renderErrorBreakdown(lastBreakdown);
    renderResults(lastResults);
  };

  let lastResults = {}, lastBreakdown = {};

  function renderResults(resultsMap) {
    const body = $("#test-body");
    const empty = $("#test-empty");
    if (!body) return;
    let list = Object.keys(resultsMap || {}).map((k) => resultsMap[k]);
    $("#result-count").textContent = '(' + list.length + ')';
    if (!list.length) {
      body.innerHTML = '';
      if (empty) empty.style.display = '';
      return;
    }
    if (empty) empty.style.display = 'none';

    if (currentCategoryFilter) {
      list = list.filter((r) => r.status !== 'success' && r.category === currentCategoryFilter);
    }
    // 排序：失败优先展示，其次通过
    const order = { 'failed': 0, 'success': 1 };
    list.sort((a, b) => (order[a.status] || 99) - (order[b.status] || 99));

    body.innerHTML = list.map((rr) => {
      const isOK = rr.status === 'success';
      const statusBadge = isOK
        ? '<span class="badge ok">通过</span>'
        : '<span class="badge fail">失败</span>';
      // 详情
      let detail = '';
      if (rr.retry_info) detail += '<div class="retry-info">重试: ' + esc(rr.retry_info) + '</div>';
      const reason = rr.message || rr.error;
      if (reason) detail += '<div class="failure-reason">原因: ' + esc(reason) + '</div>';
      if (rr.response_time) detail += '<span class="test-meta">响应: ' + esc(rr.response_time) + 's</span>';
      if (rr.resolution) detail += '<span class="test-meta"> | 分辨率: ' + esc(rr.resolution) + '</span>';
      // 所在源
      let src = '';
      if (rr.source_type) {
        const isRemote = rr.source_type === 'online' || rr.source_type === 'github';
        const tagCls = isRemote ? 'tag-online' : 'tag-local';
        const tagTxt = rr.source_type === 'online' ? '在线' : (rr.source_type === 'github' ? 'GitHub' : '本地');
        src = '<span class="src-tag ' + tagCls + '">' + tagTxt + '</span>'
          + '<div class="src-path">' + esc(rr.source || '') + '</div>';
      } else {
        src = '<div class="src-path">' + esc(rr.source || '') + '</div>';
      }
      // 错误类别
      let cat = '';
      if (!isOK && rr.category) {
        const catLabel = CATEGORY_LABELS[rr.category] || rr.category;
        cat = '<span class="cat-badge cat-' + rr.category + '">' + esc(catLabel) + '</span>';
      } else {
        cat = '<span class="cat-none">—</span>';
      }
      return '<tr>'
        + '<td><strong>' + esc(rr.name || '未知') + '</strong></td>'
        + '<td>' + statusBadge + '</td>'
        + '<td><div class="url-main">' + esc(rr.url || '') + '</div></td>'
        + '<td>' + src + '</td>'
        + '<td>' + cat + '</td>'
        + '<td class="detail-cell">' + detail + '</td>'
        + '</tr>';
    }).join('');
  }

  async function initTest() {
    if (!$("#test-start")) return;
    const bar = $("#test-bar"), status = $("#test-status");

    function loadTestFileOptions() {
      const sel = $("#test-file-select");
      if (!sel) return;
      api("GET", "/api/source-files").then((r) => {
        const files = (r.data && r.data.files) || [];
        sel.innerHTML = '<option value="">— 请选择在线源文件 —</option>';
        files.filter((f) => f.type === "online").forEach((f) => {
          const opt = document.createElement("option");
          opt.value = f.id;
          opt.textContent = f.name + (f.channel_count ? " · " + f.channel_count + " 频道" : "");
          sel.appendChild(opt);
        });
      }).catch(() => { sel.innerHTML = '<option value="">加载文件列表失败</option>'; });
    }
    window.onTestScopeChange = function () {
      const sel = $("#test-file-select");
      const scope = $("#test-scope").value;
      if (scope === "online") {
        sel.style.display = "";
        if (sel.options.length <= 1) loadTestFileOptions();
      } else {
        sel.style.display = "none"; sel.value = "";
      }
    };

    async function trigger() {
      const scope = $("#test-scope") ? $("#test-scope").value : "all";
      const fileId = (scope === "online" && $("#test-file-select")) ? $("#test-file-select").value : "";
      if (scope === "online" && !fileId) {
        status.textContent = "请先选择要测试的在线源文件";
        toast("请先选择在线源文件", "error");
        return;
      }
      const payload = {
        concurrent: parseInt($("#test-concurrent").value || "40"),
        timeout: parseInt($("#test-timeout").value || "10"),
        speed_test: $("#test-speed").checked,
      };
      if (scope === "online") payload.file_id = fileId;
      else payload.scope = "all";
      const r = await api("POST", "/api/test/trigger", payload);
      if (!r.ok || !r.data.session_id) { status.textContent = (r.data && r.data.error) || "启动失败"; return; }
      sessionId = r.data.session_id;
      if (r.data.ffprobe_available === false) {
        toast("未找到 ffprobe/ffmpeg，测试将无法正常探测，请检查 tools/ffmpeg 或配置", "error");
      }
      currentCategoryFilter = '';
      const total = r.data.total || 0;
      const dedupNote = (r.data.dedup_removed && r.data.dedup_removed > 0)
        ? "（已自动去重 " + r.data.dedup_removed + " 个重复地址，原始 " + (r.data.total_before_dedup || "?") + " 个）"
        : "";
      status.textContent = "测试中… 共 " + total + " 个频道" + dedupNote;
      if (es) es.close();
      es = new EventSource("/api/test/stream?session_id=" + sessionId);
      es.onmessage = (ev) => {
        const d = JSON.parse(ev.data); const p = d.progress || {};
        bar.style.width = (p.percent || 0) + "%";
        $("#stat-total").textContent = p.total || 0;
        $("#stat-completed").textContent = p.completed || 0;
        $("#stat-passed").textContent = p.success || 0;
        $("#stat-failed").textContent = p.failed || 0;
        const dedupNote2 = (p.dedup_removed && p.dedup_removed > 0)
          ? "（去重 " + p.dedup_removed + "，原始 " + (p.total_before_dedup || "?") + "）" : "";
        status.textContent = `进度 ${p.completed || 0}/${p.total || 0} (${p.percent || 0}%)${dedupNote2} — ${p.status}`;
        lastResults = d.results || {};
        lastBreakdown = p.error_breakdown || {};
        renderErrorBreakdown(lastBreakdown);
        renderResults(lastResults);
        if (p.status === "done" || p.status === "canceling") { es.close(); }
      };
    }
    $("#test-start").onclick = trigger;
    $("#test-pause").onclick = () => api("POST", "/api/test/pause", { session_id: sessionId });
    $("#test-resume").onclick = () => api("POST", "/api/test/resume", { session_id: sessionId });
    $("#test-cancel").onclick = () => api("POST", "/api/test/cancel", { session_id: sessionId });
  }

  // ── boot ─────────────────────────────────────────────────────
  // ── EPG 节目单网格 ────────────────────────────────────────────
  let _epgRows = [], _epgDay = 0, _epgKeyword = "", _epgTimer = null, _epgLastUrl = "", _epgRunTimer = null;
  function utcDate(s) {
    const m = (s || "").match(/^(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})/);
    if (!m) return null;
    return new Date(Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6]));
  }
  function fmtClock(utc) {
    const d = utcDate(utc);
    if (!d) return "";
    return String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
  }
  async function initEpg() {
    const IS_ADMIN = !!$("#btn-refresh-all");
    async function loadStatus() {
      const r = await api("GET", "/api/epg/status");
      const d = r.data || {};
      const cfg = d.config || {};
      const st = d.stats || {};
      setText("#st-sources", (st.enabled_sources || 0) + " / " + (st.source_count || 0));
      setText("#st-channels", st.channel_count || 0);
      setText("#st-programmes", st.programme_count || 0);
      setText("#st-matched", st.matched_channels || 0);
      setText("#st-last", d.last_refresh ? d.last_refresh.replace("T", " ").slice(0, 16) : "—");
      _epgLastUrl = d.url || "";
      const info = $("#epg-status-info");
      if (info) {
        const en = cfg.enabled ? '<span style="color:var(--success)">已启用</span>' : '<span style="color:var(--danger)">已停用</span>';
        const inj = cfg.inject_into_m3u ? "是" : "否";
        let link = _epgLastUrl ? '<a href="' + esc(_epgLastUrl) + '" target="_blank" style="color:var(--primary)">' + esc(_epgLastUrl) + "</a>" : "（未生成）";
        info.innerHTML = "EPG " + en + " · 注入 M3U：" + inj + " · 链接：" + link;
      }
      const run = $("#epg-running");
      if (d.running) {
        if (run) { run.style.display = ""; run.textContent = "刷新中：" + (d.message || ""); }
        if (!_epgRunTimer) _epgRunTimer = setInterval(pollStatus, 3000);
      } else if (run) {
        run.style.display = "none";
        if (_epgRunTimer) { clearInterval(_epgRunTimer); _epgRunTimer = null; }
      }
    }
    async function pollStatus() {
      await loadStatus();
      if (!_epgRunTimer) await loadGrid(); // 刷新结束，重载网格
    }
    function renderRow(row) {
      const now = new Date();
      const progs = (row.programmes || []).map((p) => {
        const st = utcDate(p.start_utc), sp = utcDate(p.stop_utc);
        const isNow = st && sp && st <= now && now < sp;
        return '<div class="prog' + (isNow ? " now" : "") + '"><span class="pt">' + fmtClock(p.start_utc) +
          "–" + fmtClock(p.stop_utc) + '</span><span class="ptitle">' + esc(p.title || "（无标题）") + "</span></div>";
      }).join("");
      const icon = row.icon ? '<img class="ch-icon" src="' + esc(row.icon) + '" onerror="this.style.display=\'none\'">' : "";
      const match = row.matched_channel
        ? '对齐：<b>' + esc(row.matched_channel) + "</b>"
        : (IS_ADMIN ? '<span class="match-link" data-id="' + row.tvg_id + '">点击对齐</span>' : "未对齐");
      return '<div class="epg-row"><div class="ch"><div class="ch-name">' + icon + esc(row.display_name || row.tvg_id) +
        '</div><div class="ch-match">' + match + '</div></div><div class="progs">' +
        (progs || '<div class="muted">无节目数据</div>') + "</div></div>";
    }
    async function loadGrid() {
      const r = await api("GET", "/api/epg/grid?day=" + _epgDay + "&keyword=" + encodeURIComponent(_epgKeyword) + "&limit=80");
      const box = $("#epg-grid");
      if (!box) return;
      const rows = (r.data && r.data.rows) || [];
      _epgRows = rows;
      if (!rows.length) { box.innerHTML = '<div class="muted" style="text-align:center;padding:24px">暂无节目单数据，请先到「源管理」刷新节目单源。</div>'; return; }
      box.innerHTML = rows.map(renderRow).join("");
      $$(".match-link", box).forEach((el) => {
        el.onclick = () => openMatch(el.getAttribute("data-id"));
      });
    }
    // 频道对齐 Modal
    let _matchTvg = "";
    function openMatch(tvgId) {
      _matchTvg = tvgId;
      const row = _epgRows.find((x) => x.tvg_id === tvgId);
      $("#match-channel-name").textContent = row ? (row.display_name || tvgId) : tvgId;
      $("#match-input").value = "";
      $("#match-result").innerHTML = "";
      $("#match-modal").style.display = "flex";
    }
    $("#match-close").onclick = $("#match-cancel").onclick = () => ($("#match-modal").style.display = "none");
    $("#match-backdrop").onclick = () => ($("#match-modal").style.display = "none");
    $("#match-save").onclick = async () => {
      const v = $("#match-input").value.trim();
      const r = await api("POST", "/api/epg/channels/" + encodeURIComponent(_matchTvg) + "/match", { matched_channel: v, tvg_id: _matchTvg });
      if (r.ok && r.data.ok) { $("#match-modal").style.display = "none"; toast("已保存对齐", "success"); loadGrid(); }
      else $("#match-result").innerHTML = '<div class="msg err">' + esc(r.data.error || "保存失败") + "</div>";
    };
    $("#match-clear").onclick = async () => {
      const r = await api("POST", "/api/epg/channels/" + encodeURIComponent(_matchTvg) + "/match", { matched_channel: "", tvg_id: _matchTvg });
      if (r.ok && r.data.ok) { $("#match-modal").style.display = "none"; toast("已清除对齐"); loadGrid(); }
    };
    // 工具栏
    $$(".day-tab").forEach((b) => b.onclick = () => {
      $$(".day-tab").forEach((x) => x.classList.remove("active"));
      b.classList.add("active");
      _epgDay = parseInt(b.getAttribute("data-day"), 10) || 0;
      loadGrid();
    });
    let _t = null;
    $("#epg-search").oninput = () => {
      clearTimeout(_t);
      _t = setTimeout(() => { _epgKeyword = $("#epg-search").value.trim(); loadGrid(); }, 400);
    };
    if (IS_ADMIN) {
      $("#btn-refresh-all").onclick = async () => {
        const r = await api("POST", "/api/epg/refresh-all");
        toast(r.data.message || "已触发刷新", r.ok ? "success" : "error");
        loadStatus();
      };
      $("#btn-generate").onclick = async () => {
        const r = await api("POST", "/api/epg/generate");
        if (r.ok && r.data.ok) toast("已生成：" + r.data.path, "success");
        else toast(r.data.error || "生成失败", "error");
        loadStatus();
      };
    }
    $("#btn-copy-url").onclick = async () => {
      if (!_epgLastUrl) { toast("暂无节目单链接", "error"); return; }
      try { await navigator.clipboard.writeText(_epgLastUrl); toast("已复制链接", "success"); }
      catch (e) { toast(_epgLastUrl, "success"); }
    };
    await loadStatus();
    await loadGrid();
  }

  // ── EPG 源管理 ───────────────────────────────────────────────
  let _srcAll = [], _srcTimer = null;
  async function initEpgSources() {
    const IS_ADMIN = !!$("#btn-add-source");
    async function loadSources() {
      const r = await api("GET", "/api/epg/sources");
      _srcAll = (r.data && r.data.sources) || [];
      renderSources();
    }
    function renderSources() {
      const onlyEn = $("#only-enabled") && $("#only-enabled").checked;
      const kw = ($("#src-search").value || "").trim().toLowerCase();
      const rows = _srcAll.filter((s) => {
        if (onlyEn && !s.enabled) return false;
        if (kw && !(String(s.name).toLowerCase().includes(kw) || String(s.url).toLowerCase().includes(kw))) return false;
        return true;
      });
      const tb = $("#epg-sources-tbody");
      if (!tb) return;
      if (!rows.length) { tb.innerHTML = '<tr><td colspan="8" class="muted" style="text-align:center;padding:18px">没有匹配的节目单源</td></tr>'; return; }
      tb.innerHTML = rows.map((s) => {
        const stt = s.last_status === "success"
          ? '<span class="badge ok">成功</span>'
          : s.last_status === "failed"
          ? '<span class="badge fail" title="' + esc(s.last_error || "") + '">失败</span>'
          : '<span class="badge warn">未抓取</span>';
        const refresh = [s.refresh_mode || "全局", s.refresh_mode === "interval" ? (s.refresh_minutes || 0) + "分" : (s.refresh_at || "")].filter(Boolean).join(" / ");
        const actions = [
          IS_ADMIN ? '<button class="btn sm" data-act="refresh" data-id="' + s.id + '">刷新</button>' : "",
          IS_ADMIN ? '<button class="btn sm" data-act="edit" data-id="' + s.id + '">编辑</button>' : "",
          IS_ADMIN ? '<button class="btn sm danger" data-act="del" data-id="' + s.id + '">删除</button>' : "",
        ].join(" ");
        return '<tr>' +
          "<td>" + esc(s.name) + "</td>" +
          '<td class="url-cell" title="' + esc(s.url) + '">' + esc(s.url) + "</td>" +
          "<td>" + (IS_ADMIN
            ? '<input type="checkbox" class="switch" data-act="toggle" data-id="' + s.id + '"' + (s.enabled ? " checked" : "") + ">"
            : (s.enabled ? "是" : "否")) + "</td>" +
          "<td>" + (s.priority || 0) + "</td>" +
          "<td>" + esc(refresh) + "</td>" +
          "<td>" + stt + (s.last_fetch_at ? '<br><span class="muted" style="font-size:11px">' + esc(s.last_fetch_at) + "</span>" : "") + "</td>" +
          "<td>" + (s.last_channel_count || 0) + " / " + (s.last_programme_count || 0) + "</td>" +
          "<td>" + actions + "</td>" +
          "</tr>";
      }).join("");
      $$("button[data-act]", tb).forEach((b) => {
        b.onclick = () => {
          const id = b.getAttribute("data-id");
          const act = b.getAttribute("data-act");
          if (act === "refresh") doRefresh(id);
          else if (act === "edit") openModal(id);
          else if (act === "del") doDelete(id);
          else if (act === "toggle") doToggle(id, b.checked);
        };
      });
    }
    async function doRefresh(id) {
      const r = await api("POST", "/api/epg/sources/" + id + "/refresh");
      toast(r.data.message || "已触发刷新", r.ok ? "success" : "error");
      startPoll();
    }
    async function doToggle(id, on) {
      const src = _srcAll.find((x) => x.id == id);
      if (!src) return;
      const upd = Object.assign({}, src, { enabled: on });
      const r = await api("PUT", "/api/epg/sources/" + id, upd);
      if (!(r.ok && r.data.ok)) { toast(r.data.error || "更新失败", "error"); loadSources(); }
    }
    async function doDelete(id) {
      if (!confirm("确认删除该节目单源及其全部频道/节目数据？")) return;
      const r = await api("DELETE", "/api/epg/sources/" + id);
      if (r.ok && r.data.ok) { toast("已删除", "success"); loadSources(); }
      else toast(r.data.error || "删除失败", "error");
    }
    function startPoll() {
      if (_srcTimer) return;
      _srcTimer = setInterval(async () => {
        const r = await api("GET", "/api/epg/status");
        if (!(r.data && r.data.running)) { clearInterval(_srcTimer); _srcTimer = null; loadSources(); }
      }, 3000);
    }
    // Modal
    function openModal(id) {
      const s = id ? _srcAll.find((x) => x.id == id) : null;
      $("#src-modal-title").textContent = s ? "编辑节目单源" : "新增节目单源";
      $("#src-id").value = s ? s.id : "";
      $("#src-name").value = s ? s.name : "";
      $("#src-url").value = s ? s.url : "";
      $("#src-enabled").checked = s ? !!s.enabled : true;
      $("#src-priority").value = s ? (s.priority || 100) : 100;
      $("#src-mode").value = s ? (s.refresh_mode || "") : "";
      $("#src-at").value = s ? (s.refresh_at || "") : "";
      $("#src-minutes").value = s ? (s.refresh_minutes || 360) : 360;
      $("#src-remark").value = s ? (s.remark || "") : "";
      $("#src-result").innerHTML = "";
      $("#src-modal").style.display = "flex";
    }
    $("#src-close").onclick = $("#src-cancel").onclick = () => ($("#src-modal").style.display = "none");
    $("#src-backdrop").onclick = () => ($("#src-modal").style.display = "none");
    if (IS_ADMIN) {
      $("#btn-add-source").onclick = () => openModal(null);
      $("#src-save").onclick = async () => {
        const id = $("#src-id").value;
        const body = {
          name: $("#src-name").value.trim(),
          url: $("#src-url").value.trim(),
          enabled: $("#src-enabled").checked,
          priority: parseInt($("#src-priority").value, 10) || 100,
          refresh_mode: $("#src-mode").value,
          refresh_at: $("#src-at").value.trim(),
          refresh_minutes: parseInt($("#src-minutes").value, 10) || 0,
          remark: $("#src-remark").value.trim(),
        };
        if (!body.url) { $("#src-result").innerHTML = '<div class="msg err">地址不能为空</div>'; return; }
        const r = id
          ? await api("PUT", "/api/epg/sources/" + id, body)
          : await api("POST", "/api/epg/sources", body);
        if (r.ok && r.data.ok) { $("#src-modal").style.display = "none"; toast("已保存", "success"); loadSources(); }
        else $("#src-result").innerHTML = '<div class="msg err">' + esc(r.data.error || "保存失败") + "</div>";
      };
      $("#btn-refresh-all").onclick = async () => {
        const r = await api("POST", "/api/epg/refresh-all");
        toast(r.data.message || "已触发刷新", r.ok ? "success" : "error");
        startPoll();
      };
      $("#btn-generate").onclick = async () => {
        const r = await api("POST", "/api/epg/generate");
        if (r.ok && r.data.ok) toast("已生成：" + r.data.path, "success");
        else toast(r.data.error || "生成失败", "error");
      };
    }
    if ($("#only-enabled")) $("#only-enabled").onchange = renderSources;
    if ($("#src-search")) $("#src-search").oninput = renderSources;
    await loadSources();
  }

  document.addEventListener("DOMContentLoaded", () => {
    initTheme(); markNav();
    const p = location.pathname;
    if (p === "/login") return initLogin();
    initDashboard._ = 1;
    const map = { "/": initDashboard, "/sources": initSources, "/rules": initRules, "/config": initConfig,
      "/system": initSystem, "/logs": initLogs, "/audit": initLogs, "/users": initUsers, "/test": initTest,
      "/epg": initEpg, "/epg/sources": initEpgSources };
    (map[p] || initDashboard)();
  });
})();
