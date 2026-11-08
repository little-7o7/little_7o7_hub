// Окно Little Hub: состояние раз в 3 секунды, действия — POST с заголовком X-LH (так чужая страница не может).
"use strict";

const T = {
  ru: {
    pairTitle: "Подключить к хабу", pairText: "Хаб → Настройки → Компьютеры → «Добавить компьютер». Введите код оттуда.", pairBtn: "Подключить",
    hubAddr: "Адрес хаба", copiesTitle: "Копии", saveNow: "Сохранить сейчас", disksTitle: "Диски",
    disksText: "Копии пишутся в первый диск, где свободно больше 10% и 5 ГБ. Заполнился — во второй.", diskPh: "Папка или диск: D:\\ или /Volumes/HDD",
    add: "Добавить", ratesTitle: "Курсы валют", send: "Отправить", decryptTitle: "Расшифровать копию",
    decryptText: "Файл копии хаба (.l7copy) или Sarpo и пароль копий → файл SQL. Ничего не сохраняется.", decPh: "Пароль копий", decBtn: "Расшифровать",
    logTitle: "Журнал", hub: "Хаб", today: "сегодня", noCopy: "Ещё нет", up: "Выше", remove: "Убрать", active: "Сюда пишутся копии",
    spare: "Запасной", free: "свободно", of: "из", gb: "ГБ", unavailable: "Недоступен", waiting: "Хаб изменился в {t} — копия после 2 минут затишья",
    sentAt: "Отправлены", never: "ещё не отправлялись", ok: "принят", revoked: "Компьютер отключён в хабе. Подключите его заново кодом.",
    noPass: "В хабе не задан пароль копий: Настройки → Компьютеры → «Пароль копий».", saving: "Сохраняю…", version: "Версия", update: "Новая версия {v} — поставится сама",
    paired: "Подключён: {n} · {h}", notPaired: "Не подключён", decDone: "Готово: файл скачан", lastErr: "Ошибка", busy: "Сохраняю копию…",
    filesTitle: "Файлы проектов", filesText: "Excel, папки клиентов, поставщиков, мастеров и товаров с фото — в папке «Little Hub» на диске.",
    filesNone: "Ещё не раскладывались", filesBusy: "Раскладываю…", filesLine: "{t} · бизнесов {b} · записано файлов {f} · папок {i} · фото {p}", locked: "Открыты в Excel и не обновились: {n}",
    openFolder: "Открыть папку", folder: "Папка: {p}", open: "Открыть", progTitle: "Программа", updNow: "Обновить программу", updating: "Проверяю…", latestOk: "последняя",
  },
  en: {
    pairTitle: "Connect to the hub", pairText: "Hub → Settings → Computers → “Add computer”. Type the code from there.", pairBtn: "Connect",
    hubAddr: "Hub address", copiesTitle: "Copies", saveNow: "Save now", disksTitle: "Disks",
    disksText: "Copies go to the first disk with more than 10% and 5 GB free. When it fills up — the next one.", diskPh: "Folder or disk: D:\\ or /Volumes/HDD",
    add: "Add", ratesTitle: "Exchange rates", send: "Send", decryptTitle: "Decrypt a copy",
    decryptText: "A hub (.l7copy) or Sarpo copy and the backup password → an SQL file. Nothing is stored.", decPh: "Backup password", decBtn: "Decrypt",
    logTitle: "Log", hub: "Hub", today: "today", noCopy: "None yet", up: "Up", remove: "Remove", active: "Copies go here",
    spare: "Spare", free: "free", of: "of", gb: "GB", unavailable: "Unavailable", waiting: "The hub changed at {t} — copying after 2 quiet minutes",
    sentAt: "Sent", never: "not sent yet", ok: "accepted", revoked: "This computer was disconnected in the hub. Connect it again with a code.",
    noPass: "No backup password in the hub: Settings → Computers → “Backup password”.", saving: "Saving…", version: "Version", update: "New version {v} — installs by itself",
    paired: "Connected: {n} · {h}", notPaired: "Not connected", decDone: "Done: file downloaded", lastErr: "Error", busy: "Saving a copy…",
    filesTitle: "Project files", filesText: "Excel, folders of clients, suppliers, masters and products with photos — in the “Little Hub” folder on the disk.",
    filesNone: "Not laid out yet", filesBusy: "Laying out…", filesLine: "{t} · businesses {b} · files written {f} · folders {i} · photos {p}", locked: "Open in Excel and not updated: {n}",
    openFolder: "Open folder", folder: "Folder: {p}", open: "Open", progTitle: "Program", updNow: "Update the program", updating: "Checking…", latestOk: "latest",
  },
};

let lang = "ru";
let state = null;
const $ = (id) => document.getElementById(id);
const t = (k, vars) => (T[lang][k] || k).replace(/\{(\w+)\}/g, (_, x) => (vars && vars[x] !== undefined ? vars[x] : ""));
const el = (tag, attrs, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else e.setAttribute(k, v);
  }
  for (const c of kids) if (c !== null && c !== undefined) e.append(c);
  return e;
};
const time = (iso) => (iso ? new Date(iso).toLocaleString(lang === "en" ? "en-GB" : "ru-RU", { dateStyle: "short", timeStyle: "short" }) : "");
const gb = (n) => (n / 1e9).toLocaleString(lang === "en" ? "en-US" : "ru-RU", { maximumFractionDigits: n < 1e10 ? 1 : 0 });
const size = (n) => (n < 1e6 ? `${Math.max(1, Math.round(n / 1e3))} KB` : `${(n / 1e6).toFixed(1)} MB`);

async function post(path, body) {
  const r = await fetch(path, { method: "POST", headers: { "X-LH": "1", "Content-Type": "application/json" }, body: JSON.stringify(body || {}) });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j;
}

function texts() {
  document.documentElement.lang = lang;
  for (const e of document.querySelectorAll("[data-t]")) e.textContent = t(e.dataset.t);
  for (const e of document.querySelectorAll("[data-ph]")) e.placeholder = t(e.dataset.ph);
  for (const b of document.querySelectorAll(".lang button")) b.setAttribute("aria-pressed", String(b.dataset.lang === lang));
}

function showErr(id, msg) {
  const e = $(id);
  e.textContent = msg || "";
  e.hidden = !msg;
}

function render(s) {
  state = s;
  if (s.lang && s.lang !== lang) lang = s.lang;
  texts();
  $("sub").textContent = s.paired ? t("paired", { n: s.name, h: s.hub.replace(/^https:\/\//, "") }) : t("notPaired");
  $("pairCard").hidden = s.paired;
  if (!$("hubUrl").value && s.hub) $("hubUrl").value = s.hub;

  const n = $("notices");
  n.replaceChildren();
  if (s.revoked) n.append(el("div", { class: "notice bad" }, t("revoked")));
  else if (s.noPassword) n.append(el("div", { class: "notice" }, t("noPass")));
  else if (s.error) n.append(el("div", { class: "notice bad" }, `${t("lastErr")}: ${s.error}`));
  if (s.diskError) n.append(el("div", { class: "notice bad" }, s.diskError));

  const c = $("copies");
  c.replaceChildren();
  const NAMES = { hub: t("hub"), sarpo: "Sarpo Business", strassio: "Strassio" };
  const line = (k, x) => el("li", {}, el("strong", {}, NAMES[k] || k), x ? el("span", { class: "muted" }, `${x.day === s.today ? t("today") : x.day} · ${time(x.at)} · ${size(x.bytes)}`) : el("span", { class: "muted" }, t("noCopy")));
  c.append(line("hub", s.copies && s.copies.hub));
  Object.keys(s.copies || {}).filter((k) => k !== "hub").sort().forEach((k) => c.append(line(k, s.copies[k])));

  const fl = $("files");
  fl.replaceChildren();
  $("filesStep").textContent = s.filesBusy ? `${t("filesBusy")} ${s.filesStep || ""}` : "";
  const fs = s.files && s.files.projects ? Object.entries(s.files.projects) : [];
  if (!fs.length) fl.append(el("li", {}, el("span", { class: "muted" }, t("filesNone"))));
  fs.forEach(([k, p]) => {
    fl.append(
      el(
        "li",
        {},
        el(
          "div",
          { class: "disk" },
          el("strong", {}, NAMES[k] || k),
          el("p", { class: "muted small" }, t("filesLine", { t: time(s.files.at), b: p.businesses, f: p.files, i: p.items, p: p.photos })),
          p.locked ? el("p", { class: "small" }, t("locked", { n: p.locked })) : null,
          ...(p.errors || []).map((e) => el("p", { class: "err small" }, e)),
        ),
      ),
    );
  });
  $("saveNow").disabled = !s.paired || s.busy;
  $("saveNow").textContent = s.busy ? t("saving") : t("saveNow");
  $("waiting").hidden = !s.waiting;
  if (s.waiting) $("waiting").textContent = t("waiting", { t: time(s.waiting) });

  const d = $("disks");
  d.replaceChildren();
  (s.disks || []).forEach((k, i) => {
    const used = k.total ? Math.min(100, Math.round(((k.total - k.free) / k.total) * 100)) : 0;
    const bar = el("span", { class: !k.ok ? "bad" : used > 90 ? "warn" : "" });
    bar.style.width = `${used}%`;
    const badge = !k.ok ? el("span", { class: "badge bad" }, k.error || t("unavailable")) : k.active ? el("span", { class: "badge ok" }, t("active")) : el("span", { class: "badge" }, t("spare"));
    const list = s.disks.map((x) => x.path);
    d.append(
      el(
        "li",
        {},
        el(
          "div",
          { class: "disk" },
          el("div", { class: "top-line" }, el("span", { class: "path" }, k.path), badge),
          el("div", { class: "bar" }, bar),
          el("p", { class: "muted small" }, k.total ? `${t("free")} ${gb(k.free)} ${t("gb")} ${t("of")} ${gb(k.total)} ${t("gb")}` : ""),
          el(
            "div",
            { class: "acts" },
            k.ok ? el("button", { class: "btn btn-small", type: "button", onclick: () => openFolder(k.path, "diskErr") }, t("open")) : null,
            i > 0 ? el("button", { class: "btn btn-small", type: "button", onclick: () => setDisks([...list.slice(0, i - 1), list[i], list[i - 1], ...list.slice(i + 1)]) }, t("up")) : null,
            list.length > 1 ? el("button", { class: "btn btn-small btn-danger", type: "button", onclick: () => setDisks(list.filter((_, j) => j !== i)) }, t("remove")) : null,
          ),
        ),
      ),
    );
  });
  const sg = $("suggest");
  sg.replaceChildren(...(s.suggest || []).map((p) => el("option", { value: p })));

  $("ratesAt").textContent = s.ratesAt ? `${t("sentAt")}: ${time(s.ratesAt)}` : t("never");
  $("ratesNow").disabled = !s.paired;
  const r = $("rates");
  r.replaceChildren();
  for (const x of s.rates || []) r.append(el("li", {}, el("strong", {}, x.source), x.ok ? el("span", { class: "badge ok" }, `${t("ok")} · ${x.day || ""}`) : el("span", { class: "badge bad" }, x.error || "—")));
  for (const e of s.ratesErrors || []) r.append(el("li", {}, el("span", { class: "muted small" }, e)));

  const l = $("log");
  l.replaceChildren(...(s.log || []).slice().reverse().map((x) => el("li", {}, el("time", {}, time(x.at)), el("span", { class: x.bad ? "bad" : "" }, x.text))));
  $("ver").textContent = `${t("version")} ${s.version}${s.latest && s.latest !== s.version ? " · " + t("update", { v: s.latest }) : s.latest === s.version ? " · " + t("latestOk") : ""}${s.updateError ? " · " + s.updateError : ""}`;
  $("updNow").disabled = !s.paired || s.updating;
  $("updNow").textContent = s.updating ? t("updating") : t("updNow");
  $("folder").textContent = s.folder ? t("folder", { p: s.folder }) : "";
  $("openFolder").disabled = !s.folder;
}

async function refresh() {
  try {
    const r = await fetch("/api/state");
    if (r.ok) render(await r.json());
  } catch {}
}

async function openFolder(disk, errId) {
  try {
    await post("/api/open", { disk });
    showErr(errId, "");
  } catch (e) {
    showErr(errId, e.message);
  }
}

async function setDisks(list) {
  try {
    await post("/api/disks", { disks: list });
    showErr("diskErr", "");
  } catch (e) {
    showErr("diskErr", e.message);
  }
  refresh();
}

$("pairForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    await post("/api/pair", { code: $("code").value, hub: $("hubUrl").value });
    showErr("pairErr", "");
    $("code").value = "";
  } catch (x) {
    showErr("pairErr", x.message);
  }
  refresh();
});
$("diskForm").addEventListener("submit", (e) => {
  e.preventDefault();
  const p = $("diskPath").value.trim();
  if (!p || !state) return;
  $("diskPath").value = "";
  setDisks([...state.disks.map((x) => x.path), p]);
});
$("openFolder").addEventListener("click", () => openFolder("", "openErr"));
$("updNow").addEventListener("click", async () => {
  await post("/api/update").catch(() => {});
  refresh();
});
$("saveNow").addEventListener("click", async () => {
  await post("/api/save").catch(() => {});
  setTimeout(refresh, 300);
});
$("ratesNow").addEventListener("click", async () => {
  await post("/api/rates").catch(() => {});
  setTimeout(refresh, 3000);
});
$("decForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = new FormData();
  f.set("file", $("decFile").files[0]);
  f.set("password", $("decPass").value);
  const r = await fetch("/api/decrypt", { method: "POST", headers: { "X-LH": "1" }, body: f });
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    showErr("decErr", j.error || r.statusText);
    return;
  }
  const blob = await r.blob();
  const name = (/filename="?([^"]+)"?/.exec(r.headers.get("Content-Disposition") || "") || [])[1] || "copy.sql";
  const a = el("a", { href: URL.createObjectURL(blob), download: name });
  document.body.append(a);
  a.click();
  a.remove();
  $("decPass").value = "";
  showErr("decErr", "");
});
for (const b of document.querySelectorAll(".lang button"))
  b.addEventListener("click", async () => {
    lang = b.dataset.lang;
    texts();
    await post("/api/lang", { lang }).catch(() => {});
    refresh();
  });

texts();
refresh();
setInterval(refresh, 3000);
