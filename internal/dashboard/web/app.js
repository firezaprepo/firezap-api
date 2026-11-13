// FireZAP API dashboard. Plain JavaScript, no dependencies. It calls the same REST API with the
// admin key typed by the user. Every value that comes from the API is written with textContent
// (never parsed as HTML), and the server sends a strict Content-Security-Policy.
"use strict";

const KEY_STORE = "fz.adminKey"; // sessionStorage: forgotten when the tab closes
const LANG_STORE = "fz.lang"; // localStorage: only the language preference
const REFRESH_MS = 5000;

let adminKey = "";
let lang = "en";
let refreshTimer = null;
let dialogTimer = null;

const $ = (id) => document.getElementById(id);

function store(kind, op, key, value) {
  try {
    const s = kind === "session" ? window.sessionStorage : window.localStorage;
    if (op === "get") return s.getItem(key) || "";
    if (op === "set") s.setItem(key, value);
    if (op === "del") s.removeItem(key);
  } catch (_) { /* private mode or blocked storage: the dashboard still works */ }
  return "";
}

// ── Texts ────────────────────────────────────────────────────────────────────
function t(key, vars) {
  const dict = window.I18N[lang] || window.I18N.en;
  let s = dict[key] ?? window.I18N.en[key] ?? key;
  for (const [k, v] of Object.entries(vars || {})) s = s.split("{" + k + "}").join(String(v));
  return s;
}

function applyTexts() {
  document.documentElement.lang = lang;
  for (const node of document.querySelectorAll("[data-i18n]")) node.textContent = t(node.dataset.i18n);
  $("lang").value = lang;
}

// el("button", {class: "primary", onclick: fn}, "text", childNode)
function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else if (k === "class") node.className = v;
    else node.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

function toast(msg) {
  const box = $("toast");
  box.textContent = msg;
  box.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => { box.hidden = true; }, 3500);
}

// ── API ──────────────────────────────────────────────────────────────────────
async function api(method, path, body) {
  let res;
  try {
    res = await fetch(path, {
      method,
      headers: Object.assign({ Authorization: "Bearer " + adminKey }, body ? { "Content-Type": "application/json" } : {}),
      body: body ? JSON.stringify(body) : undefined,
      cache: "no-store",
      credentials: "omit",
    });
  } catch (_) {
    throw new Error(t("netError"));
  }
  let data = null;
  if (res.status !== 204) {
    try { data = await res.json(); } catch (_) { data = null; }
  }
  if (res.status === 401 && path === "/instances" && method === "GET") throw Object.assign(new Error(t("badKey")), { status: 401 });
  if (res.status === 401) { signOut(t("badKey")); throw new Error(t("badKey")); }
  if (res.status === 429) throw new Error(t("retryIn", { s: (data && data.retryAfter) || res.headers.get("Retry-After") || "?" }));
  if (!res.ok) throw new Error(t("error", { msg: (data && data.error) || res.status }));
  return data;
}

const inst = (id) => "/instances/" + encodeURIComponent(id);

// ── Sign in / out ────────────────────────────────────────────────────────────
async function signIn(key) {
  adminKey = key;
  await api("GET", "/instances"); // throws on a bad key
  store("session", "set", KEY_STORE, key);
  $("login").hidden = true;
  $("app").hidden = false;
  $("logout").hidden = false;
  await refresh();
  clearInterval(refreshTimer);
  refreshTimer = setInterval(() => { if (!document.hidden && !$("dialog").open) refresh(); }, REFRESH_MS);
}

function signOut(message) {
  adminKey = "";
  store("session", "del", KEY_STORE);
  clearInterval(refreshTimer);
  closeDialog();
  $("app").hidden = true;
  $("logout").hidden = true;
  $("login").hidden = false;
  $("admin-key").value = "";
  const err = $("login-error");
  err.textContent = message || "";
  err.hidden = !message;
}

// ── List ─────────────────────────────────────────────────────────────────────
async function refresh() {
  let data;
  try { data = await api("GET", "/instances"); } catch (e) { if (adminKey) toast(e.message); return; }
  const list = (data && data.instances) || [];
  list.sort((a, b) => a.id.localeCompare(b.id));
  $("empty").hidden = list.length > 0;
  $("list").replaceChildren(...list.map(card));
}

function card(i) {
  const s = i.sending || { lastMinute: 0, lastHour: 0, queued: 0 };
  const linked = i.status === "connected" || !!i.phone;
  return el("article", { class: "card" },
    el("div", { class: "inst-head" },
      el("div", {}, el("div", { class: "inst-name" }, i.name || i.id), el("div", { class: "inst-id" }, i.id)),
      el("span", { class: "badge " + i.status }, t("status_" + i.status))),
    el("dl", { class: "facts" },
      el("dt", {}, t("phone")), el("dd", {}, i.phone ? "+" + i.phone : t("none")),
      el("dt", {}, t("webhook")), el("dd", {}, i.webhookUrl || t("none")),
      el("dt", {}, t("sending")), el("dd", {}, t("sendingValue", { m: s.lastMinute, h: s.lastHour, q: s.queued }))),
    el("div", { class: "actions" },
      i.status !== "connected" ? el("button", { class: "primary", onclick: () => connectDialog(i) }, t("connect")) : null,
      i.status === "connected" ? el("button", { onclick: () => testDialog(i) }, t("sendTest")) : null,
      el("button", { onclick: () => webhookDialog(i) }, t("setWebhook")),
      linked ? el("button", { onclick: () => unlink(i) }, t("unlink")) : null,
      el("button", { class: "danger", onclick: () => removeDialog(i) }, t("remove"))));
}

// ── Dialog ───────────────────────────────────────────────────────────────────
function openDialog(title, ...body) {
  stopDialogTimer();
  $("dialog-title").textContent = title;
  $("dialog-body").replaceChildren(...body);
  const d = $("dialog");
  if (!d.open) d.showModal();
  const first = d.querySelector("#dialog-body input, #dialog-body textarea");
  if (first) first.focus();
}

function closeDialog() {
  stopDialogTimer();
  const d = $("dialog");
  if (d.open) d.close();
}

function stopDialogTimer() { clearInterval(dialogTimer); clearTimeout(dialogTimer); dialogTimer = null; }

function field(id, label, input, help) {
  return el("div", {}, el("label", { for: id }, label), input, help ? el("p", { class: "muted small" }, help) : null);
}

function secretBox(value) {
  const copyBtn = el("button", { type: "button", onclick: async () => {
    try { await navigator.clipboard.writeText(value); copyBtn.textContent = t("copied"); } catch (_) { /* select by hand */ }
  } }, t("copy"));
  return el("div", { class: "secret" }, el("code", {}, value), copyBtn);
}

// A form whose submit runs fn(values); errors appear inside the form.
function form(fields, submitLabel, fn) {
  const err = el("p", { class: "error", hidden: true });
  const btn = el("button", { type: "submit", class: "primary" }, submitLabel);
  const f = el("form", {}, ...fields, err, btn);
  f.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    err.hidden = true;
    btn.disabled = true;
    try { await fn(); } catch (e) { err.textContent = e.message; err.hidden = false; } finally { btn.disabled = false; }
  });
  return f;
}

// ── Actions ──────────────────────────────────────────────────────────────────
function createDialog() {
  const id = el("input", { id: "f-id", required: true, pattern: "[a-z0-9\\-]{3,40}", maxlength: "40", autocomplete: "off", spellcheck: "false" });
  const name = el("input", { id: "f-name", maxlength: "80", autocomplete: "off" });
  openDialog(t("createTitle"), form([
    field("f-id", t("instanceId"), id, t("instanceIdHelp")),
    field("f-name", t("instanceName"), name),
  ], t("create"), async () => {
    const res = await api("POST", "/instances", { id: id.value.trim().toLowerCase(), name: name.value.trim() });
    await refresh();
    const created = res.instance;
    openDialog(t("createdTitle"),
      el("p", { class: "notice" }, t("apiKeyOnce")), secretBox(res.apiKey),
      el("div", { class: "row" },
        el("button", { class: "primary", onclick: () => connectDialog(created) }, t("connect")),
        el("button", { onclick: closeDialog }, t("done"))));
  }));
}

function connectDialog(i) {
  const tabQR = el("button", { type: "button", "aria-selected": "true" }, t("tabQR"));
  const tabPair = el("button", { type: "button", "aria-selected": "false" }, t("tabPair"));
  const area = el("div");
  const select = (which) => {
    tabQR.setAttribute("aria-selected", String(which === "qr"));
    tabPair.setAttribute("aria-selected", String(which === "pair"));
    stopDialogTimer();
    if (which === "qr") showQR(i, area); else showPair(i, area);
  };
  tabQR.addEventListener("click", () => select("qr"));
  tabPair.addEventListener("click", () => select("pair"));
  openDialog(t("connectTitle", { id: i.id }), el("div", { class: "tabs", role: "tablist" }, tabQR, tabPair), area);
  select("qr");
}

function connected(area) {
  stopDialogTimer();
  area.replaceChildren(el("p", { class: "code" }, "✓"), el("p", {}, t("connectedOk")),
    el("button", { class: "primary", onclick: closeDialog }, t("done")));
  refresh();
}

async function showQR(i, area) {
  area.replaceChildren(el("p", { class: "muted" }, t("qrWaiting")));
  try { await api("POST", inst(i.id) + "/connect"); } catch (e) { area.replaceChildren(el("p", { class: "error" }, e.message)); return; }
  let hadCode = false;
  const tick = async () => {
    let q;
    try { q = await api("GET", inst(i.id) + "/qr"); } catch (e) { area.replaceChildren(el("p", { class: "error" }, e.message)); stopDialogTimer(); return; }
    if (q.status === "connected") return connected(area);
    if (q.code && typeof q.image === "string" && q.image.startsWith("data:image/png;base64,")) {
      hadCode = true;
      area.replaceChildren(el("p", { class: "muted" }, t("qrHelp")), el("div", { class: "qr" }, el("img", { src: q.image, alt: "QR code" })));
    } else if (hadCode && q.status === "disconnected") {
      stopDialogTimer();
      area.replaceChildren(el("p", {}, t("qrExpired")), el("button", { class: "primary", onclick: () => showQR(i, area) }, t("newCode")));
    }
  };
  await tick();
  if ($("dialog").open && !dialogTimer) dialogTimer = setInterval(tick, 2000);
}

function showPair(i, area) {
  const phone = el("input", { id: "f-phone", inputmode: "numeric", required: true, autocomplete: "off" });
  area.replaceChildren(form([field("f-phone", t("pairPhone"), phone, t("pairHelp"))], t("getCode"), async () => {
    const res = await api("POST", inst(i.id) + "/pair", { phone: phone.value });
    const code = String(res.code || "");
    area.replaceChildren(el("p", { class: "muted" }, t("pairCodeHelp")),
      el("p", { class: "code" }, code.length === 8 ? code.slice(0, 4) + "-" + code.slice(4) : code));
    dialogTimer = setInterval(async () => {
      try { const s = await api("GET", inst(i.id)); if (s.status === "connected") connected(area); } catch (_) { /* keep waiting */ }
    }, 2000);
  }));
  phone.focus();
}

function webhookDialog(i) {
  const url = el("input", { id: "f-url", type: "url", placeholder: "https://", autocomplete: "off", spellcheck: "false" });
  url.value = i.webhookUrl || "";
  const secret = el("input", { id: "f-secret", type: "password", autocomplete: "new-password", spellcheck: "false" });
  openDialog(t("webhookTitle", { id: i.id }), form([
    field("f-url", t("webhookUrl"), url),
    field("f-secret", t("webhookSecret"), secret, t("webhookSecretHelp")),
  ], t("save"), async () => {
    const res = await api("PUT", inst(i.id) + "/webhook", { url: url.value.trim(), secret: secret.value });
    await refresh();
    if (res.secret) {
      openDialog(t("webhookTitle", { id: i.id }), el("p", { class: "notice" }, t("secretOnce")), secretBox(res.secret),
        el("button", { class: "primary", onclick: closeDialog }, t("done")));
    } else {
      closeDialog();
      toast(t("saved"));
    }
  }));
}

function testDialog(i) {
  const to = el("input", { id: "f-to", inputmode: "numeric", required: true, autocomplete: "off" });
  const text = el("textarea", { id: "f-text", required: true, maxlength: "4096" });
  text.value = t("testDefault");
  openDialog(t("testTitle", { id: i.id }), form([
    field("f-to", t("testTo"), to),
    field("f-text", t("testText"), text),
  ], t("send"), async () => {
    const res = await api("POST", inst(i.id) + "/messages/text", { to: to.value, text: text.value });
    closeDialog();
    toast(t("sentOk", { id: res.id }));
    refresh();
  }));
}

async function unlink(i) {
  if (!window.confirm(t("confirmUnlink", { id: i.id }))) return;
  try { await api("POST", inst(i.id) + "/logout"); toast(t("unlinked")); } catch (e) { toast(e.message); }
  refresh();
}

function removeDialog(i) {
  const typed = el("input", { id: "f-confirm", autocomplete: "off", spellcheck: "false" });
  openDialog(t("remove") + " " + i.id, el("p", {}, t("confirmDelete", { id: i.id })), form([
    field("f-confirm", t("typeToConfirm", { id: i.id }), typed),
  ], t("remove"), async () => {
    if (typed.value.trim() !== i.id) throw new Error(t("typeToConfirm", { id: i.id }));
    await api("DELETE", inst(i.id));
    closeDialog();
    toast(t("deleted"));
    refresh();
  }));
}

// ── Start ────────────────────────────────────────────────────────────────────
async function start() {
  let cfg = { defaultLanguage: "en", version: "" };
  try { cfg = await (await fetch("config.json", { cache: "no-store" })).json(); } catch (_) { /* defaults */ }
  const saved = store("local", "get", LANG_STORE);
  const browser = (navigator.language || "").toLowerCase().startsWith("pt") ? "pt-BR" : "";
  lang = window.I18N[saved] ? saved : window.I18N[cfg.defaultLanguage] ? cfg.defaultLanguage : browser || "en";
  $("version").textContent = cfg.version ? "v" + cfg.version : "";
  applyTexts();

  $("lang").addEventListener("change", (e) => {
    lang = e.target.value;
    store("local", "set", LANG_STORE, lang);
    applyTexts();
    if (adminKey) refresh();
  });
  $("logout").addEventListener("click", () => signOut(""));
  $("new-instance").addEventListener("click", createDialog);
  $("dialog").addEventListener("close", stopDialogTimer);
  $("login-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const err = $("login-error");
    err.hidden = true;
    try { await signIn($("admin-key").value.trim()); } catch (e) { adminKey = ""; err.textContent = e.message; err.hidden = false; }
  });

  const key = store("session", "get", KEY_STORE);
  if (key) {
    try { await signIn(key); return; } catch (_) { store("session", "del", KEY_STORE); }
  }
  signOut("");
}

document.addEventListener("DOMContentLoaded", start);
