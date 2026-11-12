<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import BDrop from "./BDrop.vue";
import CloudPicker from "./CloudPicker.vue";
import TPick from "./TPick.vue";
import Icon from "./Icon.vue";
import { cloudLogo, cloudLabel, cloudIcon, brandOf, brandFromText, BRANDS, BRAND_OPTIONS } from "./cloudLogo.js";

// ---------------------------------------------------------------------------
// 基础
// ---------------------------------------------------------------------------
// API 前缀：网关下页面路径为 /app/encbackup/，直连/开发时为 /，从地址栏自动推导
const API = ref(location.pathname.replace(/\/index\.html$/, "").replace(/\/+$/, ""));
const nav = ref("tasks"); // tasks | snap | hist
const toast = reactive({ show: false, msg: "", type: "ok" });

function showToast(msg, type = "ok") {
  toast.msg = msg;
  toast.type = type;
  toast.show = true;
  setTimeout(() => (toast.show = false), 3200);
}

async function api(path, opts = {}) {
  const r = await fetch(API.value + path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
  return data;
}

function fmtBytes(n) {
  if (n == null || isNaN(n)) return "-";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = Number(n);
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return v.toFixed(i > 1 ? 1 : 0) + " " + u[i];
}

function fmtDuration(sec) {
  if (!sec) return "-";
  if (sec < 60) return sec + " 秒";
  const m = Math.floor(sec / 60);
  if (m < 60) return m + " 分 " + (sec % 60) + " 秒";
  return Math.floor(m / 60) + " 时 " + (m % 60) + " 分";
}

// RFC3339 → "16:02"（当天）或 "10-26 16:02"
function fmtClock(iso) {
  if (!iso) return "-";
  const d = new Date(iso);
  if (isNaN(d)) return "-";
  const hm = String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
  const now = new Date();
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  return sameDay ? hm : `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${hm}`;
}

// "2006-01-02 15:04:05" → "明天 03:00" / "10-28 03:00"
function fmtNextRun(s) {
  if (!s) return "";
  const d = new Date(s.replace(" ", "T"));
  if (isNaN(d)) return "";
  const hm = String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const startOfTarget = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const diffDays = Math.round((startOfTarget - startOfToday) / 86400000);
  if (diffDays === 0) return "今天 " + hm;
  if (diffDays === 1) return "明天 " + hm;
  return `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${hm}`;
}

function schedText(t) {
  if (t.sched_mode === "manual") return "手动";
  if (t.sched_mode === "daily") {
    const n = Number(t.daily_days) || 1;
    return (n === 1 ? "每天 " : "每 " + n + " 天 ") + (t.daily_time || "03:00");
  }
  if (t.sched_mode === "weekly") {
    const ds = (t.weekly_days || []).slice().sort((a, b) => a - b);
    return "每周 " + (ds.length ? ds.map((d) => WD_SHORT[d]).join("、") : "—") + " " + (t.daily_time || "03:00");
  }
  if (t.sched_mode === "monthly") {
    const ds = (t.monthly_days || []).slice().sort((a, b) => a - b);
    return "每月 " + (ds.length ? ds.map((d) => (d === -1 ? "末天" : d + "日")).join("、") : "—") + " " + (t.daily_time || "03:00");
  }
  if (t.sched_mode === "interval") return "每 " + (t.interval_hrs || 0) + " 小时";
  return "-";
}
const WD_SHORT = ["日", "一", "二", "三", "四", "五", "六"];
const schedModes = [
  { v: "manual", l: "手动" }, { v: "daily", l: "每天" }, { v: "weekly", l: "每周" },
  { v: "monthly", l: "每月" }, { v: "interval", l: "间隔" },
];
const monthDayList = Array.from({ length: 31 }, (_, i) => i + 1).concat([-1]);
// 周/月多选：至少保留一项
function toggleWDay(v) {
  const l = wiz.form.weekly_days;
  const i = l.indexOf(v);
  if (i >= 0) { if (l.length > 1) l.splice(i, 1); } else l.push(v);
}
function toggleMDay(v) {
  const l = wiz.form.monthly_days;
  const i = l.indexOf(v);
  if (i >= 0) { if (l.length > 1) l.splice(i, 1); } else l.push(v);
}

// "2026-10-25 09:41:33" → "10-25 09:41"
function fmtShort(s) {
  if (!s) return "";
  return s.slice(5, 16);
}

// 源目录摘要："相册、资料 等 4 项"
function pathSummary(t) {
  const names = (t.paths || []).map((p) => p.split("/").filter(Boolean).pop() || p);
  if (names.length <= 2) return names.join("、");
  return names.slice(0, 2).join("、") + " 等 " + names.length + " 项";
}

// 目的地显示路径：rclone:ol:天翼云盘/飞牛加密备份 → 天翼云盘/飞牛加密备份；rclone:wd-xx:备份 → 备份
function destPath(r) {
  if (!r) return "";
  return (r.uri || "").split(":").pop() || r.uri || "";
}

// 任务状态展示信息
function statusInfo(t) {
  if (t.enabled === false) return { cls: "disabled", badge: "minus", text: "已禁用" };
  if (t.last_status === "running") return { cls: "running", badge: "run", text: "备份中…" };
  if (t.auth_warn) return { cls: "failed", badge: "warn", text: "云盘授权失效", detail: t.auth_warn };
  // 兼容历史数据：迁移自 Backrest 的任务 last_status 为空但 last_done_at 有值（已备份成功过）
  if (t.last_status === "success" || (t.last_status === "" && t.last_done_at))
    return {
      cls: "success", badge: "ok",
      text: "上次备份 " + fmtShort(t.last_done_at),
      detail: (t.last_files ? "传输完成 " + (t.last_files || 0).toLocaleString() + " 项" +
        (t.last_bytes ? " / " + fmtBytes(t.last_bytes) : "") +
        (t.last_duration_sec ? " · 用时 " + fmtDuration(t.last_duration_sec) : "") : ""),
    };
  if (t.last_status === "failed")
    return { cls: "failed", badge: "warn", text: "备份失败 " + fmtShort(t.last_run_at), detail: t.last_message || "" };
  if (t.last_status === "stopped")
    return { cls: "idle", badge: "minus", text: "已停止", detail: "上次运行 " + fmtShort(t.last_run_at) + " 已手动停止" };
  return { cls: "idle", badge: "none", text: "从未备份" };
}

// ---------------------------------------------------------------------------
// 状态
// ---------------------------------------------------------------------------
const status = ref(null);
const cfg = reactive({ repos: [], tasks: [] });
const progress = reactive({
  task_id: "", started_at: "", percent: 0, files_done: 0, bytes_done: 0,
  total_bytes: 0, current_files: [], error_count: 0,
});
let pollTimer = null;

// ---------------------------------------------------------------------------
// 预计完成时间
// ---------------------------------------------------------------------------
// restic 的 percent_done 不是匀速的：开头扫描目录时会长时间停在 0，之后才快速
// 推进。所以只看整段平均会把「刚开始」估得极长、把「快结束」估得过短。
// 这里用最近 90 秒的采样算瞬时速率，样本不足时再退回整段平均。
const etaText = ref("");
let etaSamples = [];

function resetEta() {
  etaSamples = [];
  etaText.value = "";
}

function updateEta(p) {
  const pct = Number(p.percent) || 0;
  if (!p || !p.task_id || pct <= 0.2 || pct >= 99.9) {
    resetEta();
    return;
  }
  const now = Date.now();
  etaSamples.push({ t: now, pct });
  while (etaSamples.length > 2 && now - etaSamples[0].t > 90000) etaSamples.shift();

  const a = etaSamples[0];
  const b = etaSamples[etaSamples.length - 1];
  let rate = 0; // 每秒推进的百分点
  if (b.pct > a.pct && b.t > a.t) rate = (b.pct - a.pct) / ((b.t - a.t) / 1000);

  if (!(rate > 0)) {
    const startedAt = Date.parse(p.started_at || "");
    if (!isNaN(startedAt) && now > startedAt && pct > 0) {
      rate = pct / ((now - startedAt) / 1000);
    }
  }
  if (!(rate > 0)) return; // 算不出来就保持上一次的估计，别让数字忽隐忽现

  const remainSec = (100 - pct) / rate;
  if (!isFinite(remainSec) || remainSec <= 0 || remainSec > 86400) return;

  const done = new Date(now + remainSec * 1000);
  const hm = String(done.getHours()).padStart(2, "0") + ":" + String(done.getMinutes()).padStart(2, "0");
  const sameDay = done.getDate() === new Date().getDate() && done.getMonth() === new Date().getMonth();
  const clock = sameDay
    ? hm
    : `${String(done.getMonth() + 1).padStart(2, "0")}-${String(done.getDate()).padStart(2, "0")} ${hm}`;
  etaText.value = `${clock} · 约 ${fmtDuration(Math.max(1, Math.round(remainSec)))}`;
}

// 统一的进度落地入口：赋值 + 重算 ETA，保证首屏与轮询走同一条路径
function applyProgress(p) {
  if (!p) return;
  Object.assign(progress, p);
  updateEta(p);
}

async function loadAll() {
  try {
    // 进度与状态并行拉取：备份页首屏就能拿到正在运行的任务，
    // 不再等第一次轮询（原来要空等 2 秒进度条才出现）
    const [st, cf, pg] = await Promise.all([
      api("/api/status"),
      api("/api/config"),
      api("/api/progress").catch(() => null),
    ]);
    status.value = st;
    if (Array.isArray(st.roots) && st.roots.length) USER_ROOTS.value = st.roots.slice();
    if (st.user_root) USER_ROOT.value = st.user_root;
    if (st.home) HOME_ROOT.value = st.home;
    cfg.repos = cf.repos || [];
    cfg.tasks = cf.tasks || [];
    if (pg && pg.task_id) applyProgress(pg);
    // progress 接口偶发拿不到时，用 status 的 running_task 兜底，先让进度区渲染出来
    if (!progress.task_id && st.running_task) progress.task_id = st.running_task;
    if (progress.task_id) startPolling();
  } catch (e) {
    showToast("无法连接后端：" + e.message, "bad");
  }
}

function startPolling() {
  if (pollTimer) return;
  const tick = async () => {
    try {
      const p = await api("/api/progress");
      applyProgress(p);
      if (!p.task_id) {
        clearInterval(pollTimer);
        pollTimer = null;
        resetEta();
        loadAll();
      }
    } catch (e) { /* 忽略 */ }
  };
  tick(); // 立即拉一次，不等第一个 2 秒间隔
  pollTimer = setInterval(tick, 2000);
}

// 页面从后台切回时立刻补一次进度，避免停留在切走前的旧数值
function onPageVisible() {
  if (document.visibilityState !== "visible" || !progress.task_id) return;
  api("/api/progress").then(applyProgress).catch(() => {});
}

function repoName(id) {
  const r = cfg.repos.find((x) => x.id === id);
  return r ? r.name : id;
}
function repoObj(id) {
  return cfg.repos.find((x) => x.id === id) || null;
}

// ---------------------------------------------------------------------------
// 任务操作
// ---------------------------------------------------------------------------
async function runTask(t) {
  try {
    await api(`/api/tasks/${t.id}/run`, { method: "POST" });
    progress.task_id = t.id;
    progress.percent = 0;
    resetEta();
    showToast(`「${t.name}」已开始备份`);
    startPolling();
    const x = cfg.tasks.find((y) => y.id === t.id);
    if (x) x.last_status = "running";
    menuOpen.value = "";
    sheet.show = false;
  } catch (e) {
    showToast(e.message, "bad");
  }
}
async function stopTask(t) {
  try {
    await api(`/api/tasks/${t.id}/stop`, { method: "POST" });
    showToast("已发送停止指令");
    menuOpen.value = "";
    sheet.show = false;
  } catch (e) {
    showToast(e.message, "bad");
  }
}
function delTask(t) {
  delDlg.task = t;
  delDlg.show = true;
  menuOpen.value = "";
  sheet.show = false;
}
async function doDelTask() {
  const t = delDlg.task;
  if (!t || delDlg.busy) return;
  delDlg.busy = true;
  try {
    await api(`/api/tasks/${t.id}/delete`, { method: "POST" });
    showToast("任务已删除");
    delDlg.show = false;
    loadAll();
  } catch (e) {
    showToast(e.message, "bad");
  }
  delDlg.busy = false;
}

// ⋯ 菜单 / 手机操作面板
const menuOpen = ref("");
function toggleMenu(id) {
  menuOpen.value = menuOpen.value === id ? "" : id;
}
const sheet = reactive({ show: false, task: null });
function openSheet(t) {
  sheet.task = t;
  sheet.show = true;
}

// ---------------------------------------------------------------------------
// 新建/编辑任务向导
// ---------------------------------------------------------------------------
const wiz = reactive({
  show: false, editing: null, copy: false, saving: false, openRule: false, openPlan: false, openKeep: false, openAdv: false,
  form: {
    id: "", name: "", paths: [], excludes: "", repo_id: "",
    sched_mode: "manual", daily_time: "03:00", interval_hrs: 24, keep_last: 3, enabled: true,
    daily_days: 1, weekly_days: [1], monthly_days: [1],
    password: "", showPass: false,
  },
});
function openWizard(task) {
  wiz.editing = !!task;
  wiz.copy = false;
  wiz.show = true;
  wiz.openRule = false; wiz.openPlan = false; wiz.openKeep = false; wiz.openAdv = false;
  menuOpen.value = "";
  sheet.show = false;
  if (task) {
    const rp = cfg.repos.find((r) => r.id === task.repo_id);
    const parts = rp ? uriToParts(rp.uri) : null;
    Object.assign(wiz.form, {
      id: task.id, name: task.name, paths: [...(task.paths || [])],
      excludes: (task.excludes || []).join("\n"), repo_id: task.repo_id,
      dest_cloud: rp && rp.cloud === "webdav" ? rp.id : parts ? parts.cloud : (task.dest_cloud || ""),
      dest_subdir: parts ? parts.subdir : (task.dest_subdir || ""),
      sched_mode: task.sched_mode || "manual", daily_time: task.daily_time || "03:00",
      interval_hrs: task.interval_hrs || 24, keep_last: task.keep_last || 0,
      daily_days: task.daily_days || 1,
      weekly_days: (task.weekly_days || []).length ? [...task.weekly_days] : [1],
      monthly_days: (task.monthly_days || []).length ? [...task.monthly_days] : [1],
      enabled: task.enabled !== false,
      password: "", showPass: false,
    });
  } else {
    Object.assign(wiz.form, {
      id: "", name: "", paths: [], excludes: SYS_TMP_DEFAULT.join("\n"), repo_id: "",
      dest_cloud: "", dest_subdir: "",
      sched_mode: "manual", daily_time: "03:00", interval_hrs: 24, keep_last: 3, enabled: true,
      daily_days: 1, weekly_days: [1], monthly_days: [1],
      password: "", showPass: false,
    });
  }
}
const destReady = computed(() => !!(wiz.form.dest_cloud && wiz.form.dest_subdir));
const wizReady = computed(() => !!(wiz.form.name.trim() && wiz.form.paths.length && (destReady.value || wiz.form.repo_id)));
function closeWizard() { wiz.show = false; }

// 生成加密密码（新建任务时自动生成强随机密码，用户可手动改）
function genWizPw() {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%&*";
  const b = new Uint8Array(20);
  crypto.getRandomValues(b);
  let s = "";
  for (let i = 0; i < b.length; i++) s += chars[b[i] % chars.length];
  wiz.form.password = s;
}

// 复制仓库密码到剪贴板（编辑态专用：向后端取明文，不落前端内存回显）
async function copyRepoPw() {
  const rid = wiz.form.repo_id;
  if (!rid) { showToast("未关联仓库", "bad"); return; }
  try {
    const res = await api("/api/repos/" + rid + "/password");
    const pw = res && res.password;
    if (!pw) { showToast("该仓库无密码（纯连接）", "bad"); return; }
    let ok = false;
    try {
      await navigator.clipboard.writeText(pw);
      ok = true;
    } catch (e) {
      // 剪贴板 API 不可用（非 https/无权限）时降级
      const ta = document.createElement("textarea");
      ta.value = pw; ta.style.position = "fixed"; ta.style.opacity = "0";
      document.body.appendChild(ta); ta.select();
      try { ok = document.execCommand("copy"); } catch (e2) { ok = false; }
      document.body.removeChild(ta);
    }
    showToast(ok ? "密码已复制到剪贴板" : "复制失败，请手动记录");
  } catch (e) {
    showToast("获取密码失败：" + e.message, "bad");
  }
}

function addManualPath() {
  const p = prompt("输入源目录绝对路径，例如 /vol1/1000/相册");
  if (p && p.trim()) {
    const v = p.trim().replace(/\/+$/, "");
    if (v && !wiz.form.paths.includes(v)) wiz.form.paths.push(v);
  }
}
function removePath(i) { wiz.form.paths.splice(i, 1); }

// 编辑备份来源地（仿飞牛：草稿列表 + 点选即加）
const srcEdit = reactive({ show: false, paths: [] });
function openSrcEdit() {
  srcEdit.paths = [...wiz.form.paths];
  srcEdit.show = true;
}
function confirmSrc() {
  wiz.form.paths = [...srcEdit.paths];
  srcEdit.show = false;
}
function dispPath(p) {
  const s = shortPath(p);
  return s.startsWith("/") ? s : "存储空间1/我的文件/" + s;
}

// 目录选择器（仿飞牛：大图标列表，点行=选中，右侧 › 进入子级）
// 可见根目录由后端按登录账号给出（status.roots），可能不止一个（管理员授权了多个目录）
const USER_ROOT = ref("/vol1/1000");
const USER_ROOTS = ref(["/vol1/1000"]);
const HOME_ROOT = ref("/vol1/1000"); // 本人家目录，只用于把绝对路径显示成相对路径
const browser = reactive({ show: false, path: "", dirs: [], loading: false, showAll: false });

function baseName(p) {
  const segs = String(p || "").split("/").filter(Boolean);
  return segs.length ? segs[segs.length - 1] : p;
}
function isScopeRoot(p) { return USER_ROOTS.value.includes(p); }
// 路径所属的根（多个授权目录时用来切面包屑）
function activeRoot(p) { return USER_ROOTS.value.find((r) => p === r || p.startsWith(r + "/")) || ""; }
// 根目录的展示名：就是家目录时叫「我的文件」，否则用目录名（如「资源」）
function scopeRootName(p) { return p === HOME_ROOT.value ? "我的文件" : baseName(p); }

async function openBrowser() {
  browser.show = true;
  browser.showAll = false;
  // 一律先列「已授权的根目录」，不管授权了 1 个还是多个。
  // 只有一个根时也要把根本身显示出来（可整行选中），否则一进来就是它的子目录，
  // 看起来像「只看到某个子目录」，也选不到根本身。
  browser.path = "";
  browser.dirs = [];
}
async function browseTo(p) {
  // 空路径 = 根目录列表（纯前端状态，不请求后端）
  if (!p) { browser.path = ""; browser.dirs = []; return; }
  browser.loading = true;
  try {
    const r = await api("/api/browse", { method: "POST", body: JSON.stringify({ path: p, show_all: browser.showAll }) });
    browser.dirs = r.dirs || [];
    browser.path = r.path;
    if (r.roots && r.roots.length) USER_ROOTS.value = r.roots.slice();
    if (r.home) HOME_ROOT.value = r.home;
  } catch (e) { showToast(e.message, "bad"); }
  browser.loading = false;
}
function pathFull(d) { return browser.path.replace(/\/$/, "") + "/" + d.name; }
function pickSrc(d) { pickPath(pathFull(d)); }
// 选中某个绝对路径（根目录本身、当前所在目录都走这里）
function pickPath(p) {
  const np = String(p || "").replace(/\/$/, "");
  if (!np) return;
  if (!srcEdit.paths.includes(np)) srcEdit.paths.push(np);
  browser.show = false;
}

// 授权目录现在由后端实时读取（后台每几秒同步一次平台授权表），
// 管理员在应用中心改完勾选，这里不用重启应用，点一下立刻重新读取。
const reloading = ref(false);
async function reloadScope() {
  if (reloading.value) return;
  reloading.value = true;
  try {
    const r = await api("/api/reload-scope", { method: "POST" });
    if (r && r.live) {
      applyScopeRoots(r.roots, r.home);
      await refreshScopeView();
      showToast(r.changed ? "授权目录已更新" : "授权目录已是最新");
    } else {
      showToast((r && r.msg) || "无法直接读取授权目录，请到应用中心重启应用", "bad");
    }
  } catch (e) {
    showToast(e.message, "bad");
  } finally {
    reloading.value = false;
  }
}

// applyScopeRoots 把后端算出的可访问根目录同步到界面
function applyScopeRoots(roots, home) {
  if (Array.isArray(roots) && roots.length) {
    USER_ROOTS.value = roots.slice();
    // 当前浏览位置已经不在授权范围里：退回根列表，避免停在非法目录
    const still = roots.some((r) => !browser.path || browser.path === r || browser.path.startsWith(r + "/"));
    if (!still) browser.path = "";
  }
  if (home) HOME_ROOT.value = home;
}
async function refreshScopeView() {
  // 停在根列表就继续停在根列表；授权目录数量变化时不要把用户「推」进某个目录
  if (browser.path) {
    await browseTo(browser.path).catch(() => {});
  } else {
    browser.dirs = [];
  }
}
// 后台静默跟随：授权目录被改动后，界面在十几秒内自己更新
let scopeTimer = null;
async function syncScope() {
  if (document.visibilityState !== "visible") return;
  try {
    const st = await api("/api/scope");
    const roots = Array.isArray(st.roots) ? st.roots.slice() : [];
    if (!roots.length) return;
    const same = JSON.stringify(roots) === JSON.stringify(USER_ROOTS.value) && (st.home || "") === HOME_ROOT.value;
    if (same) return;
    applyScopeRoots(roots, st.home);
    await refreshScopeView();
    showToast("授权目录已更新");
  } catch (e) { /* 忽略：下次轮询再试 */ }
}
function fmtMtime(ts) {
  if (!ts) return "";
  const d = new Date(ts * 1000);
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function atScopeRoot() { return isScopeRoot(browser.path); }
function parentPath() {
  if (!browser.path) return "";
  // 已在某个根上：返回根目录列表（无论授权了几个根）
  if (atScopeRoot()) return "";
  const up = browser.path.split("/").slice(0, -1).join("/");
  return up || "/";
}
function crumbSegs() {
  const r = activeRoot(browser.path);
  return browser.path.replace(r, "").split("/").filter(Boolean);
}
// 云端目录面包屑分段（方案C）
const cloudSegs = computed(() => cloudBrowser.path.split("/").filter(Boolean));

  // 备份规则预设
const excludePresets = {
  sys:   { label: "系统文件",  pats: ["**/@eaDir/**", "**/#recycle/**", "**/desktop.ini", "**/@Recycle/**", "**/@Recently-Snapshot/**"] },
  tmp:   { label: "临时文件",  pats: ["**/*.tmp", "**/*~", "**/Thumbs.db", "**/*.swp"] },
  cache: { label: "缓存目录",  pats: ["**/cache/**", "**/.cache/**"] },
  mac:   { label: "macOS 垃圾", pats: ["**/.DS_Store", "**/._*", "**/.Spotlight-V100", "**/.Trashes"] },
};
const SYS_TMP_DEFAULT = [...excludePresets.sys.pats, ...excludePresets.tmp.pats];
function excludeLines() { return wiz.form.excludes.split("\n").map((s) => s.trim()).filter(Boolean); }
function presetOn(key) {
  return excludePresets[key].pats.every((p) => excludeLines().includes(p));
}
function togglePreset(key) {
  const lines = excludeLines();
  const pats = excludePresets[key].pats;
  const on = pats.every((p) => lines.includes(p));
  const next = on ? lines.filter((l) => !pats.includes(l)) : [...lines, ...pats.filter((p) => !lines.includes(p))];
  wiz.form.excludes = next.join("\n");
}
const wizPresetOnList = computed(() => Object.keys(excludePresets).filter(presetOn));
const wizPresetDetail = computed(() => wizPresetOnList.value.flatMap((k) => excludePresets[k].pats));
const wizExcludeCount = computed(() => excludeLines().length);
function repoSubdir(r) {
  const parts = uriToParts(r.uri);
  return parts ? parts.subdir : "";
}
function shortPath(p) {
  // 优先相对「本人家目录」显示（能看到 资源/相册 这样的一级目录名），
  // 不在家目录里再退回相对授权根，都不匹配就显示绝对路径
  const bases = [HOME_ROOT.value, USER_ROOT.value].filter(Boolean);
  for (const b of bases) if (p.startsWith(b + "/")) return p.slice(b.length + 1);
  return p;
}

async function wizSave(runAfter) {
  if (wiz.saving) return; // 防重复提交（init 走云盘网络可能耗时十几秒）
  if (!wizReady.value) {
    showToast("请完善任务名称、源目录和目标仓库", "bad");
    return;
  }
  // 加密密码：仅新建任务时生成/透传；编辑态不可改，置空由后端复用仓库既有密码
  if (!wiz.editing && !wiz.form.password) genWizPw();
  wiz.saving = true;
  const body = {
    ...wiz.form,
    dest_cloud: destValueToRemote(wiz.form.dest_cloud), // 后端 findOrCreateRepoLocked 只认云类型 / wd-xxx 远端名
    paths: wiz.form.paths,
    excludes: wiz.form.excludes.split("\n").map((s) => s.trim()).filter(Boolean),
    interval_hrs: Number(wiz.form.interval_hrs) || 24,
    keep_last: Number(wiz.form.keep_last) || 0,
    daily_days: Number(wiz.form.daily_days) || 1,
    weekly_days: [...wiz.form.weekly_days],
    monthly_days: [...wiz.form.monthly_days],
  };
  try {
    const res = await api("/api/tasks", { method: "POST", body: JSON.stringify(body) });
    if (res && res.init_error) {
      showToast("仓库自动初始化失败，首次备份可能报错：" + res.init_error, "bad");
    } else {
      showToast(wiz.editing ? "任务已保存" : "任务已创建");
    }
    wiz.show = false;
    await loadAll();
    if (runAfter === true && res && res.id) {
      const t = cfg.tasks.find((x) => x.id === res.id);
      if (t) await runTask(t);
    }
  } catch (e) {
    showToast(e.message, "bad");
  } finally {
    wiz.saving = false;
  }
}

// ---------------------------------------------------------------------------
// 仓库管理（云端目录属于仓库，URI 自动拼装）
// ---------------------------------------------------------------------------
const cloudLabels = { tianyi: "天翼云盘", y115: "115" };
// 目的地两步式：云端下拉选项（不做预置，只列已有仓库用到的云类型 + WebDAV 仓库）
// 选择云端改为卡片式弹层（CloudPicker）：直接列仓库，卡片右侧「⋯」承载密码/移除
const cloudPick = reactive({ show: false, reopenAfterAdd: false });
// 当前任务目的地对应的仓库（dest_cloud 可能是云类型 key，也可能是 WebDAV 仓库 id/远端名）
const destRepo = computed(() => {
  const v = wiz.form.dest_cloud;
  if (!v) return null;
  const cands = cfg.repos.filter((r) => r.id === v || (r.remote || r.id) === v || r.cloud === v);
  if (!cands.length) return null;
  // 同一云盘多个仓库时，用目录反查用户当初选的是哪一个（dest_cloud 只存云类型 key）
  const sub = (wiz.form.dest_subdir || "").replace(/^\/+/, "");
  if (sub) {
    const hit = cands.find((r) => dirOfRepo(r) === sub);
    if (hit) return hit;
  }
  return cands[0];
});
function openCloudPick() {
  cloudPick.show = true;
}
// 选中仓库：云盘类仓库 → dest_cloud 用云类型 key；WebDAV → 用仓库 id（保存时换算成远端名）
// 同时把该仓库的目录预填到 dest_subdir，省掉第二步手动浏览
function onCloudPick(r) {
  wiz.form.dest_cloud = r.cloud === "tianyi" || r.cloud === "y115" ? r.cloud : r.id;
  wiz.form.dest_subdir = dirOfRepo(r);
}
function dirOfRepo(r) {
  const uri = (r.uri || "").replace(/^rclone:/, "");
  if (r.cloud === "webdav") {
    const i = uri.indexOf(":");
    return i < 0 ? "" : uri.slice(i + 1).replace(/^\/+/, "");
  }
  const m = /^ol:[^/]+\/(.*)$/.exec(uri);
  return m ? m[1] : "";
}
function onCloudPickAdd() {
  cloudPick.reopenAfterAdd = true;
  openRepo(null);
}
async function fetchRepoPw(id) {
  const res = await api(`/api/repos/${id}/password`);
  return res.password || "";
}
// b-3：仓库健康状态 → 第二行换成对用户更有用的信息（最近备份时间），不展示地址
function repoHealth(repoId) {
  const ts = cfg.tasks.filter((t) => t.repo_id === repoId);
  if (!ts.length) return { sub: "未被任务使用", dot: "idle" };
  if (ts.some((t) => t.last_status === "running")) return { sub: "备份中…", dot: "run" };
  const done = ts.filter((t) => t.last_done_at).sort((a, b) => (a.last_done_at < b.last_done_at ? 1 : -1))[0];
  if (done) return { sub: "上一次备份 " + fmtShort(done.last_done_at), dot: "ok" };
  const failed = ts.find((t) => t.last_status === "failed");
  if (failed) return { sub: "备份失败 " + fmtShort(failed.last_run_at), dot: "bad" };
  return { sub: "从未备份", dot: "idle" };
}
// 目的地云端显示名：ol 云盘用 label，WebDAV 用仓库名
// 下拉 value：WebDAV 仓库用唯一 repo id（同一 WebDAV 连接的多个仓库共享同一个 wd-xxx 远端名，
// 用 remote 作 value 会在 find() 时永远命中第一个 → 表现为「选不中」）；保存/浏览时再换算回远端名
function destValueToRemote(v) {
  const r = cfg.repos.find((x) => x.id === v);
  if (r) return r.remote || v;
  return v; // tianyi/y115 云类型，或旧数据里的 wd-xxx 字符串
}
function destCloudLabel(v) {
  if (v === "tianyi" || v === "y115") return cloudLabels[v];
  const r = cfg.repos.find((x) => x.id === v) || cfg.repos.find((x) => (x.remote || x.id) === v);
  return r ? r.name : v;
}
const cloudOptions = [
  { value: "webdav", title: "WebDAV", sub: "直接填写地址/账号/密码" },
  { value: "other", title: "自定义（rclone URI）", sub: "手动填写 rclone 远端地址" },
];
const repoModal = reactive({
  show: false,
  saving: false,
  savingText: "",
  form: { id: "", name: "", cloud: "webdav", subdir: "", uri: "", password: "", remote: "", dav_url: "", dav_user: "", dav_pass: "", dav_vendor: "other", brand: "auto", showPass: false },
});
// WebDAV 连接测试
const davTest = reactive({ busy: false, ok: null, msg: "" });
// 「服务商」选自动时，实时回显识别结果，让用户能看见并纠正
const autoBrandHint = computed(() => {
  const f = repoModal.form;
  // 新建 WebDAV 时子目录存在 subdir 里，保存时才清空，所以这里要一起看
  return cloudLabel({ cloud: f.cloud, dav_url: f.dav_url, name: f.name, uri: f.subdir || f.uri, brand: "auto" });
});

// 仓库的品牌标识。仓库自身信息不足时，用其关联任务的云端目录补一次信号：
// WebDAV 走 OpenList 时，上游网盘名常常只出现在任务的 dest_subdir 里（如 115/相册）
function repoBrand(r) {
  if (!r) return "other";
  if (r.brand && r.brand !== "auto" && BRANDS[r.brand]) return r.brand;
  const own = brandOf(r);
  if (own !== "webdav" && own !== "other" && own !== "openlist") return own;
  for (const t of cfg.tasks || []) {
    if (t.repo_id !== r.id) continue;
    const k = brandFromText((t.dest_subdir || "").split("/")[0]);
    if (k) return k;
  }
  return own;
}
// 传给 CloudPicker 的仓库列表：把品牌算好，弹层里就不用再关心任务
const pickerRepos = computed(() =>
  (cfg.repos || []).map((r) => ({ ...r, brand: repoBrand(r) }))
);
async function testDav() {
  if (davTest.busy) return;
  davTest.busy = true;
  davTest.msg = "";
  try {
    const f = repoModal.form;
    const r = await api("/api/test-dav", { method: "POST", body: JSON.stringify({ remote: f.remote || "", dav_url: f.dav_url, dav_user: f.dav_user, dav_pass: f.dav_pass, dav_vendor: f.dav_vendor }) });
    davTest.ok = !!r.ok;
    davTest.msg = r.ok ? "连接成功，识别到 " + (r.dirs || 0) + " 个目录" : r.error || "连接失败";
  } catch (e) { davTest.ok = false; davTest.msg = e.message; }
  davTest.busy = false;
}
function uriToParts(uri) {
  const s = (uri || "").replace(/^rclone:/, "");
  // WebDAV 直连：rclone:wd-xxxxxxxx:<子目录> → {cloud:"webdav", remote, subdir}
  if (s.startsWith("wd-")) {
    const i = s.indexOf(":");
    return { cloud: "webdav", remote: i < 0 ? s : s.slice(0, i), subdir: i < 0 ? "" : s.slice(i + 1) };
  }
  // ol 中转：rclone:ol:<云盘名>/<子目录> → {cloud, subdir}
  const m = /^ol:([^/]+)\/(.*)$/.exec(s);
  if (!m) return null;
  const cloud = Object.keys(cloudLabels).find((k) => cloudLabels[k] === m[1]);
  return cloud ? { cloud, subdir: m[2] } : null;
}
function wdid() {
  const b = new Uint8Array(6);
  crypto.getRandomValues(b);
  return "wd-" + Array.from(b).map((x) => x.toString(16).padStart(2, "0")).join("");
}
function buildURI() {
  const f = repoModal.form;
  if (f.cloud === "other") return f.uri;
  if (f.cloud === "webdav") {
    if (!f.remote) f.remote = wdid();
    return "rclone:" + f.remote + "/" + (f.subdir || "").replace(/^\/+|\/+$/g, "");
  }
  return "rclone:ol:" + (cloudLabels[f.cloud] || f.cloud) + "/" + (f.subdir || "").replace(/^\/+|\/+$/g, "");
}
function openRepo(repo) {
  repoModal.show = true;
  menuOpen.value = "";
  if (repo) {
    const parts = uriToParts(repo.uri);
    Object.assign(repoModal.form, {
      id: repo.id, name: repo.name, password: "",
      cloud: repo.cloud === "webdav" ? "webdav" : parts ? parts.cloud : "other",
      subdir: parts ? parts.subdir : "",
      uri: repo.uri,
      remote: repo.remote || "",
      dav_url: repo.dav_url || "", dav_user: repo.dav_user || "", dav_pass: "", dav_vendor: repo.dav_vendor || "other", brand: repo.brand || "auto",
    });
  } else {
    Object.assign(repoModal.form, { id: "", name: "", cloud: "webdav", subdir: "", uri: "", password: "", remote: "", dav_url: "", dav_user: "", dav_pass: "", dav_vendor: "other", brand: "auto", showPass: false });
    davTest.ok = null; davTest.msg = "";
  }
}
function onCloudChange() {
  if (repoModal.form.cloud !== "other" && repoModal.form.cloud !== "webdav") repoModal.form.subdir = "飞牛加密备份";
}
// 云盘目录浏览（选云端目录：mode = repo 仓库设置 / dest 任务目的地）；remote 为 rclone 远端名（ol:天翼云盘 / wd-xxx）
// base：浏览起点根（WebDAV 仓库 = 其 URI 限定的子目录，用户只能在仓库范围内选）
const cloudBrowser = reactive({ show: false, path: "", dirs: [], loading: false, cloud: "", remote: "", mode: "repo", creating: false, base: "", dav_url: "", dav_user: "", dav_pass: "", dav_vendor: "other" });
function remoteOf(cloudKey) {
  const r = cfg.repos.find((x) => x.id === cloudKey); // 下拉新 value：仓库 id
  if (r) return r.cloud === "webdav" ? (r.remote || "") : remoteOf(r.cloud);
  if (cloudKey === "tianyi" || cloudKey === "y115") return "ol:" + cloudLabels[cloudKey];
  return cloudKey; // webdav：即 wd-xxx 远端名
}
async function openCloudBrowser() {
  cloudBrowser.show = true;
  cloudBrowser.mode = "repo";
  // WebDAV 新建：显示友好标签；已保存的 WebDAV 仓库显示其名字；ol 云盘显示中文名
  if (repoModal.form.cloud === "webdav") {
    cloudBrowser.cloud = repoModal.form.name || "WebDAV";
  } else {
    cloudBrowser.cloud = cloudLabels[repoModal.form.cloud] || repoModal.form.cloud;
  }
  // WebDAV 仓库用真实远端名（wd-xxx），ol 云盘用 ol:云盘名；否则 rclone 找不到 config 段
  cloudBrowser.remote = repoModal.form.cloud === "webdav" ? (repoModal.form.remote || "") : remoteOf(repoModal.form.cloud);
  // 新建 WebDAV（remote 未生成）：把地址/账号传给后端，用临时 :webdav: 直接浏览
  cloudBrowser.dav_url = repoModal.form.cloud === "webdav" && !repoModal.form.remote ? repoModal.form.dav_url : "";
  cloudBrowser.dav_user = repoModal.form.cloud === "webdav" && !repoModal.form.remote ? repoModal.form.dav_user : "";
  cloudBrowser.dav_pass = repoModal.form.cloud === "webdav" && !repoModal.form.remote ? repoModal.form.dav_pass : "";
  cloudBrowser.dav_vendor = repoModal.form.dav_vendor || "other";
  cloudBrowser.base = ""; // 仓库模式下不限定根（在服务器范围内选仓库目录）
  cloudBrowser.path = "";
  await browseCloud("");
}
function openDestBrowser() {
  cloudBrowser.show = true;
  cloudBrowser.mode = "dest";
  cloudBrowser.cloud = cloudLabels[wiz.form.dest_cloud] || destCloudLabel(wiz.form.dest_cloud);
  cloudBrowser.remote = remoteOf(wiz.form.dest_cloud);
  cloudBrowser.dav_url = cloudBrowser.dav_user = cloudBrowser.dav_pass = "";
  // WebDAV 仓库：以仓库 URI 限定的子目录为浏览根（选天翼仓库就只看得到天翼内）
  const r = cfg.repos.find((x) => x.id === wiz.form.dest_cloud) || cfg.repos.find((x) => (x.remote || x.id) === wiz.form.dest_cloud);
  cloudBrowser.base = r && r.cloud === "webdav" ? ((uriToParts(r.uri) || {}).subdir || "") : "";
  cloudBrowser.path = cloudBrowser.base;
  browseCloud(cloudBrowser.base);
}
async function browseCloud(p) {
  if (cloudBrowser.base && !p.startsWith(cloudBrowser.base)) p = cloudBrowser.base; // 不允许越过仓库根
  cloudBrowser.loading = true;
  try {
    const r = await api("/api/browse-cloud", {
      method: "POST",
      body: JSON.stringify({
        cloud: cloudBrowser.cloud, remote: cloudBrowser.remote, path: p,
        dav_url: cloudBrowser.dav_url || "", dav_user: cloudBrowser.dav_user || "",
        dav_pass: cloudBrowser.dav_pass || "", dav_vendor: cloudBrowser.dav_vendor || "other",
      }),
    });
    if (r.error) showToast("云盘读取失败（可能风控限流）：" + r.error, "bad");
    cloudBrowser.dirs = r.dirs || [];
    cloudBrowser.path = p;
  } catch (e) { showToast(e.message, "bad"); }
  cloudBrowser.loading = false;
}
function pickCloudDir(d) {
  // 点行 = 直接选该目录；右侧 › 才进入子级
  const full = cloudBrowser.path ? cloudBrowser.path + "/" + d : d;
  if (cloudBrowser.mode === "dest") wiz.form.dest_subdir = full;
  else repoModal.form.subdir = full;
  cloudBrowser.show = false;
}
function cloudParent() {
  const p = cloudBrowser.path.split("/").slice(0, -1).join("/");
  if (cloudBrowser.base && !p.startsWith(cloudBrowser.base)) return cloudBrowser.base;
  return p;
}
function chooseCloudDir() {
  if (cloudBrowser.mode === "dest") wiz.form.dest_subdir = cloudBrowser.path;
  else repoModal.form.subdir = cloudBrowser.path;
  cloudBrowser.show = false;
}
// 新建空白目录（目的地两步式）：按钮 → 弹窗输入名称确认
const mkdirDlg = reactive({ show: false, name: "", creating: false });
function openMkdirDlg() {
  mkdirDlg.name = "";
  mkdirDlg.show = true;
  setTimeout(() => document.getElementById("mkdirName")?.focus(), 60);
}
async function confirmMkdir() {
  const name = mkdirDlg.name.trim();
  if (!name || mkdirDlg.creating) return;
  if (name.includes("/")) { showToast("目录名不能包含 /", "bad"); return; }
  mkdirDlg.creating = true;
  try {
    const full = (cloudBrowser.path ? cloudBrowser.path + "/" : "") + name;
    const r = await api("/api/mkdir-cloud", { method: "POST", body: JSON.stringify({
      cloud: cloudBrowser.cloud, remote: cloudBrowser.remote, path: full,
      dav_url: cloudBrowser.dav_url || "", dav_user: cloudBrowser.dav_user || "",
      dav_pass: cloudBrowser.dav_pass || "", dav_vendor: cloudBrowser.dav_vendor || "other",
    }) });
    if (r.error) {
      showToast("创建失败：" + r.error, "bad");
    } else {
      showToast("目录已创建：" + full);
      mkdirDlg.show = false;
      if (cloudBrowser.mode === "dest") {
        wiz.form.dest_subdir = full;
        cloudBrowser.show = false;
      } else {
        await browseCloud(cloudBrowser.path);
      }
    }
  } catch (e) { showToast(e.message, "bad"); }
  mkdirDlg.creating = false;
}
async function saveRepo() {
  if (repoModal.saving) return;
  const f = repoModal.form;
  // 纯连接：subdir 置空，URI = rclone:<remote>:（无目录）；密码也留空（任务创建时再生成）
  if (f.cloud === "webdav") {
    f.subdir = "";
    f.uri = "";
    f.password = "";
  }
  if (!f.name) {
    showToast("保存失败：连接名称必填", "bad");
    return;
  }
  if (f.cloud === "webdav" && (!f.dav_url.trim() || !f.dav_user.trim())) {
    showToast("保存失败：WebDAV 地址与账号必填", "bad");
    return;
  }
  repoModal.saving = true;
  repoModal.savingText = f.id ? "正在保存…" : "正在保存连接…";
  try {
    const r = await api("/api/repos", { method: "POST", body: JSON.stringify(f) });
    if (r.error) { showToast("保存失败：" + r.error, "bad"); return; }
    showToast("连接已保存");
    repoModal.show = false;
    loadAll();
    if (cloudPick.reopenAfterAdd) {
      cloudPick.reopenAfterAdd = false;
      setTimeout(() => { cloudPick.show = true; }, 260);
    }
  } catch (e) {
    showToast("保存失败：" + e.message, "bad");
  } finally {
    repoModal.saving = false;
    repoModal.savingText = "";
  }
}
async function verifyRepo(r) {
  showToast(`正在验证仓库「${r.name}」…`);
  try {
    const res = await api(`/api/repos/${r.id}/verify`, { method: "POST" });
    if (res.ok) showToast(`验证通过：发现 ${res.snapshots} 个快照`);
    else showToast("验证失败：" + res.error, "bad");
  } catch (e) {
    showToast(e.message, "bad");
  }
}
async function delRepo(r) {
  const used = cfg.tasks.filter((t) => t.repo_id === r.id);
  if (used.length) {
    showToast(`「${r.name}」正被 ${used.length} 个任务使用（${used.map((t) => t.name).join("、")}），请先删除或改移这些任务`, "bad");
    return;
  }
  rmDlg.opt = r;
  rmDlg.show = true;
}
const rmDlg = reactive({ show: false, opt: null, busy: false });
async function confirmRmRepo() {
  if (!rmDlg.opt || rmDlg.busy) return;
  rmDlg.busy = true;
  try {
    await api(`/api/repos/${rmDlg.opt.id}/delete`, { method: "POST" });
    showToast("仓库已移除");
    if (wiz.form.dest_cloud && rmDlg.opt && (rmDlg.opt.id === wiz.form.dest_cloud || (rmDlg.opt.remote || rmDlg.opt.id) === wiz.form.dest_cloud)) {
      wiz.form.dest_cloud = "";
      wiz.form.dest_subdir = "";
    }
    rmDlg.show = false;
    loadAll();
  } catch (e) {
    showToast(e.message, "bad");
  } finally {
    rmDlg.busy = false;
  }
}

// ---------------------------------------------------------------------------
// 日志
// ---------------------------------------------------------------------------
const logModal = reactive({
  show: false, task: null, runs: [], file: "", content: "", loading: false, auto: true,
  summary: null,       // 精简解析结果（logSummary）
  raw: false,          // 是否显示原始日志
  historyOpen: false,  // 是否展开历史运行列表
});
let logTimer = null;
async function openLogs(t) {
  logModal.show = true;
  logModal.task = t;
  logModal.historyOpen = false;  // 默认折叠历史记录，只看最新一份
  menuOpen.value = "";
  sheet.show = false;
  await loadRuns();
  if (logModal.runs.length) {
    logModal.file = logModal.runs[0].file;
    await loadLog();
  }
  logTimer = setInterval(async () => {
    if (logModal.auto && logModal.show) await loadLog(true);
  }, 3000);
}
async function loadRuns() {
  try {
    logModal.runs = await api(`/api/tasks/${logModal.task.id}/runs`);
  } catch (e) { logModal.runs = []; }
}
async function loadLog(silent) {
  if (!logModal.task) return;
  if (!silent) logModal.loading = true;
  try {
    const q = logModal.file ? `?file=${encodeURIComponent(logModal.file)}` : "";
    // 精简结果（时间轴 + 一行结果）
    const s = await api(`/api/tasks/${logModal.task.id}/log/summary${q}`);
    logModal.summary = s;
    // 原始日志按需拉取（默认不拉，展开时才取）
    if (logModal.raw) {
      const r = await api(`/api/tasks/${logModal.task.id}/log${q}`);
      logModal.content = r.content || "";
    } else {
      logModal.content = "";
    }
  } catch (e) { /* 忽略 */ }
  logModal.loading = false;
}
async function toggleRaw() {
  logModal.raw = !logModal.raw;
  if (logModal.raw && !logModal.content) {
    await loadLog(true);
  }
}
function closeLogs() {
  logModal.show = false;
  clearInterval(logTimer);
  logTimer = null;
}
function sumChipCls(s) {
  return s === "success" ? "ok" : s === "failed" ? "bad" : "run";
}
function sumChipTxt(s) {
  return s === "success" ? "✓ 成功" : s === "failed" ? "✕ 失败" : "⟳ 备份中";
}

// ---------------------------------------------------------------------------
// 快照 Tab：仓库列表（可展开）→ 快照详情 → 还原
// ---------------------------------------------------------------------------
const snapView = ref("list");   // list | detail | restore
const snapCur = ref(null);      // 当前查看的快照
const snapCurRepo = ref(null);  // 当前快照所属仓库
const snapDiff = ref(null);     // 与上一条快照的差异
const snapDiffLoading = ref(false);
const snapGap = ref("—");       // 与上一条快照的时间间隔

const rp = reactive({
  list: [],            // 仓库摘要（/api/repos/summary，只读本地缓存）
  loading: false,
  open: {},            // repoId -> 是否展开
  byRepo: {},          // repoId -> { snaps, tasks, cachedAt, totalBytes, latestTime, loading, loaded }
});

const TASK_COLORS = ["#3370ff", "#22c55e", "#f59e0b", "#a855f7", "#ef4444", "#06b6d4"];
// 任务色按 id 哈希取，不依赖列表顺序，避免展开不同仓库时颜色跳变
function taskColor(id) {
  if (!id || id === "__orphan") return "#9aa1ab";
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return TASK_COLORS[h % TASK_COLORS.length];
}

// 取（必要时初始化）某仓库的快照缓存
function repoBox(id) {
  if (!rp.byRepo[id]) {
    rp.byRepo[id] = { snaps: [], tasks: [], cachedAt: "", totalBytes: 0, latestTime: "", loading: false, loaded: false };
  }
  return rp.byRepo[id];
}

async function loadRepoSummary() {
  rp.loading = true;
  try {
    const r = await api("/api/repos/summary");
    rp.list = r.repos || [];
  } catch (e) { showToast(e.message, "bad"); }
  rp.loading = false;
}

// 展开 / 收起仓库；首次展开时才拉快照
async function toggleRepo(repo) {
  const id = repo.id;
  const willOpen = rp.open[id] !== true;
  rp.open[id] = willOpen;
  if (willOpen && !repoBox(id).loaded) await loadRepoSnaps(repo);
}

async function loadRepoSnaps(repo, refresh) {
  const box = repoBox(repo.id);
  box.loading = true;
  try {
    const r = await api(`/api/repos/${repo.id}/snapshots${refresh ? "?refresh=1" : ""}`);
    box.snaps = r.snapshots || [];
    box.tasks = r.tasks || [];
    box.cachedAt = r.cached_at || "";
    box.totalBytes = r.total_bytes || 0;
    box.latestTime = r.latest_time || "";
    box.loaded = true;
  } catch (e) { showToast(e.message, "bad"); }
  box.loading = false;
}

// "2026-10-31T02:00:00+08:00" → "10-31 02:00"（跨年补上年份）
function fmtSnapTime(t) {
  if (!t) return "-";
  const y = t.slice(0, 4);
  const nowY = String(new Date().getFullYear());
  return y === nowY ? t.slice(5, 16).replace("T", " ") : t.slice(0, 16).replace("T", " ");
}
function snapFiles(s) {
  const n = s.summary ? s.summary.total_files : null;
  return n == null || n === 0 ? "—" : Number(n).toLocaleString();
}
function snapBytes(s) {
  const b = s.summary ? s.summary.total_bytes : null;
  return b == null || b === 0 ? "—" : fmtBytes(b);
}
function snapFs(s, withUnit) {
  const f = snapFiles(s);
  const b = snapBytes(s);
  if (f === "—" && b === "—") return "—";
  return withUnit ? f + " 文件 · " + b : f + " · " + b;
}
function snapTagLabel(s) {
  const t = (s.tags || []).filter((x) => !x.startsWith("task:"));
  if (!t.length) return "自动";
  return t.length > 1 ? t[0] + " +" + (t.length - 1) : t[0];
}

const WEEK_CN = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
function weekdayOf(t) {
  if (!t) return "";
  const d = new Date(t);
  return isNaN(d.getTime()) ? "" : WEEK_CN[d.getDay()];
}
// 两条快照的时间差 → "11 小时 44 分" / "3 天 2 小时"
function gapText(cur, prev) {
  if (!cur || !prev) return "—";
  const a = new Date(cur).getTime();
  const b = new Date(prev).getTime();
  if (isNaN(a) || isNaN(b) || a <= b) return "—";
  let m = Math.floor((a - b) / 60000);
  const d = Math.floor(m / 1440); m -= d * 1440;
  const h = Math.floor(m / 60); m -= h * 60;
  if (d) return `${d} 天 ${h} 小时`;
  if (h) return `${h} 小时 ${m} 分`;
  return `${m} 分`;
}

// 某仓库下按任务分组（未关联任务的单独一组）
function repoGroupsOf(repoId) {
  const box = rp.byRepo[repoId];
  if (!box) return [];
  const groups = box.tasks.map((t) => ({
    id: t.id,
    name: t.name,
    paths: t.paths || [],
    snaps: box.snaps.filter((s) => s.task_id === t.id),
  }));
  const orphan = box.snaps.filter((s) => !s.task_id);
  if (orphan.length) groups.push({ id: "__orphan", name: "未关联任务", paths: [], snaps: orphan });
  return groups.filter((g) => g.snaps.length);
}

// 打开快照详情
async function openSnapDetail(s, repo) {
  snapCur.value = s;
  snapCurRepo.value = repo;
  snapView.value = "detail";
  snapDiff.value = null;
  sdStopPoll();
  sdReset();
  sdLoadRoot();
  const box = rp.byRepo[repo.id];
  const list = box ? box.snaps : [];
  const i = list.findIndex((x) => x.id === s.id);
  const prev = i >= 0 ? list[i + 1] : null;
  snapGap.value = prev ? gapText(s.time, prev.time) : "最早一份";
  if (!s.task_id || !prev) return;
  snapDiffLoading.value = true;
  try {
    const d = await api(`/api/tasks/${s.task_id}/snapdiff?cur=${s.short_id || s.id}&prev=${prev.short_id || prev.id}`);
    snapDiff.value = d && !d.error ? d : null;
  } catch { snapDiff.value = null; }
  snapDiffLoading.value = false;
}

function backToSnapList() {
  snapView.value = "list";
  snapCur.value = null;
  snapCurRepo.value = null;
  snapDiff.value = null;
  snapGap.value = "—";
  sdStopPoll();
  sdReset();
}

// ---------------------------------------------------------------------------
// 快照详情 · 目录树（桌面：内联展开树 / 手机：逐层下钻）
// ---------------------------------------------------------------------------
const isNarrow = ref(typeof window !== "undefined" && window.matchMedia("(max-width: 768px)").matches);
if (typeof window !== "undefined") {
  const mq = window.matchMedia("(max-width: 768px)");
  const onMq = (e) => { isNarrow.value = e.matches; };
  if (mq.addEventListener) mq.addEventListener("change", onMq);
  else if (mq.addListener) mq.addListener(onMq);
}

const sd = reactive({
  roots: [],        // 根目录路径
  nodes: {},        // path -> { name, path, is_dir, children:[], loaded, loading }
  open: {},         // path -> 是否展开
  cur: "",          // 手机端当前目录（"" = 快照根）
  sel: [],          // 选中的最上层路径
  building: false,  // 后台还在读整份清单
  parsed: 0,
  elapsed: 0,
  err: "",
});
let sdTimer = null;
function sdStopPoll() {
  if (sdTimer) { clearTimeout(sdTimer); sdTimer = null; }
}
function sdReset() {
  sd.roots = []; sd.nodes = {}; sd.open = {}; sd.cur = ""; sd.sel = [];
  sd.building = false; sd.parsed = 0; sd.elapsed = 0; sd.err = "";
}
function sdEnsure(path, it) {
  if (!sd.nodes[path]) {
    sd.nodes[path] = {
      name: (it && it.name) || path.split("/").pop() || path,
      path,
      is_dir: it ? it.is_dir !== false : true,
      size: (it && it.size) || 0,
      children: [],
      loaded: false,
      loading: false,
    };
  }
  return sd.nodes[path];
}
async function sdFetch(dir, limit) {
  const s = snapCur.value;
  const sid = s.short_id || s.id;
  const lim = limit ? `&limit=${limit}` : "";
  return await api(`/api/tasks/${s.task_id}/files?snapshot=${encodeURIComponent(sid)}&dir=${encodeURIComponent(dir)}${lim}`);
}
// 进入详情页：先用快照元数据把根目录画出来，同时触发后台读清单
async function sdLoadRoot() {
  sdStopPoll();
  const s = snapCur.value;
  if (!s) return;
  if (!s.task_id) { sd.err = "这条快照没有关联备份任务，无法浏览目录"; return; }
  sd.err = "";
  sd.roots = (s.paths || []).slice();
  sd.roots.forEach((p) => sdEnsure(p));
  if (isNarrow.value && !sd.cur) sd.cur = "";
  try {
    const r = await sdFetch("");
    sd.parsed = r.parsed || 0;
    sd.elapsed = r.elapsed || 0;
    if (r.loading) {
      sd.building = true;
      sdTimer = setTimeout(sdLoadRoot, 2000);
      return;
    }
    sd.building = false;
    if (r.error) { sd.err = r.error; return; }
    for (const it of r.items || []) if (it.is_dir) sdEnsure(it.path, it);
    // 快照元数据里没带 paths 时，用接口返回的根目录兜底
    if (!sd.roots.length) {
      const fromItems = (r.items || []).filter((it) => it.is_dir).map((it) => it.path);
      const list = fromItems.length ? fromItems : (r.roots || []);
      if (list.length) { sd.roots = list.slice(); list.forEach((p) => sdEnsure(p)); }
    }
  } catch (e) {
    sd.building = false;
    sd.err = e.message;
  }
}
// 拉某个目录的内容（目录 + 文件，文件最多 300 个，避免一次铺太多）
async function sdLoadDir(path) {
  const n = sdEnsure(path);
  if (n.loading) return;
  n.loading = true;
  try {
    const r = await sdFetch(path, 300);
    if (r.loading) {
      n.loading = false;
      sd.building = true;
      sd.parsed = r.parsed || 0;
      sd.elapsed = r.elapsed || 0;
      sdStopPoll();
      sdTimer = setTimeout(() => sdLoadDir(path), 2000);
      return;
    }
    n.loading = false;
    sd.building = false;
    if (r.error) { sd.err = r.error; return; }
    const kids = [];
    for (const it of r.items || []) {
      sdEnsure(it.path, it);
      kids.push(it.path);
    }
    n.children = kids;
    n.total = r.dir_total || kids.length;
    n.truncated = !!r.truncated;
    n.loaded = true;
  } catch (e) {
    n.loading = false;
    sd.err = e.message;
  }
}
// 选中语义：只记最上层路径，勾父目录自动覆盖子路径
function sdPicked(p) {
  if (sd.sel.includes(p)) return true;
  return sd.sel.some((a) => p.startsWith(a + "/"));
}
function sdHalf(p) {
  if (sdPicked(p)) return false;
  return sd.sel.some((x) => x.startsWith(p + "/"));
}
function sdToggle(p) {
  const at = sd.sel.indexOf(p);
  if (at >= 0) { sd.sel.splice(at, 1); return; }
  for (let i = sd.sel.length - 1; i >= 0; i--) {
    if (p.startsWith(sd.sel[i] + "/")) sd.sel.splice(i, 1);
  }
  sd.sel.push(p);
  for (let i = sd.sel.length - 1; i >= 0; i--) {
    if (sd.sel[i] !== p && sd.sel[i].startsWith(p + "/")) sd.sel.splice(i, 1);
  }
}
function sdSelectAll() {
  sd.sel = (snapCur.value && snapCur.value.paths ? snapCur.value.paths : []).slice();
}
// 手机：面包屑行尾的勾选框 = 选中当前整个目录（在快照根时 = 整份快照）
const sdCurAll = computed(() => {
  if (sd.cur) return sdPicked(sd.cur);
  return sd.roots.length > 0 && sd.roots.every((p) => sdPicked(p));
});
const sdCurPart = computed(() => {
  if (sd.cur) return sdHalf(sd.cur);
  return !sdCurAll.value && sd.roots.some((p) => sdPicked(p) || sdHalf(p));
});
function sdToggleCur() {
  if (sd.cur) { sdToggle(sd.cur); return; }
  if (sdCurAll.value) { sd.sel = []; return; }
  sdSelectAll();
}
// 当前目录下的条目（桌面表格与手机下钻共用）
const sdItems = computed(() => {
  if (!sd.cur) return sd.roots.map((p) => sd.nodes[p]).filter(Boolean);
  const n = sd.nodes[sd.cur];
  if (!n || !n.children) return [];
  return n.children.map((p) => sd.nodes[p]).filter(Boolean);
});
const sdCurNode = computed(() => (sd.cur ? sd.nodes[sd.cur] : null));
const sdCrumb = computed(() => {
  const dir = sd.cur;
  if (!dir) return [];
  const root = sd.roots.find((r) => dir === r || dir.startsWith(r + "/")) || "";
  const out = [];
  if (root) {
    out.push({ label: root.split("/").pop() || root, path: root });
    let acc = root;
    for (const seg of dir.slice(root.length).split("/").filter(Boolean)) {
      acc += "/" + seg;
      out.push({ label: seg, path: acc });
    }
  }
  return out;
});
function sdEnter(path) {
  sd.cur = path;
  const n = sdEnsure(path);
  if (!n.loaded) sdLoadDir(path);
}
function sdUp() {
  if (!sd.cur) return;
  if (sd.roots.includes(sd.cur)) { sd.cur = ""; return; }
  const up = sd.cur.split("/").slice(0, -1).join("/");
  sd.cur = up && sd.roots.some((r) => up.startsWith(r + "/")) ? up : "";
}
function sdRestoreLabel() {
  return sd.sel.length ? `还原（${sd.sel.length} 项）` : "还原此快照";
}
// 带着已选目录进还原向导，直接从「目的地」开始
async function sdRestore() {
  const s = snapCur.value;
  if (!s) return;
  if (!s.task_id) { showToast("这条快照没有关联任务，无法还原", "bad"); return; }
  const t = cfg.tasks.find((x) => x.id === s.task_id);
  if (!t) { showToast("关联的备份任务已被删除", "bad"); return; }
  const inc = (sd.sel.length ? sd.sel : (s.paths || [])).slice();
  openRestoreWizard();
  rst.fromSnap = true;
  rst.task = t;
  await loadRestoreSnaps(t);
  rst.cur = rst.snaps.find((x) => x.id === s.id) || s;
  rst.includes = inc.map((p) => ({ path: p, name: p.split("/").pop() || p, is_dir: true }));
  rstStep.value = 3;
  snapView.value = "restore";
}

// 详情页「浏览文件」：直接打开快照文件浏览器（只读浏览）
function browseSnapFiles() {
  const s = snapCur.value;
  if (!s) return;
  if (!s.task_id) { showToast("这条快照没有关联任务，无法浏览文件", "bad"); return; }
  const t = cfg.tasks.find((x) => x.id === s.task_id);
  if (!t) { showToast("关联的备份任务已被删除", "bad"); return; }
  rst.task = t;
  rst.cur = s;
  rst.includes = [];
  openRstPick();
}

// 从快照详情进入还原：跳过「选任务」「选快照」，直接从选文件开始
async function restoreFromSnap() {
  const s = snapCur.value;
  if (!s) return;
  if (!s.task_id) { showToast("这条快照没有关联任务，无法还原", "bad"); return; }
  const t = cfg.tasks.find((x) => x.id === s.task_id);
  if (!t) { showToast("关联的备份任务已被删除", "bad"); return; }
  openRestoreWizard();
  rst.fromSnap = true;
  rst.task = t;
  await loadRestoreSnaps(t);
  rst.cur = rst.snaps.find((x) => x.id === s.id) || s;
  rstStep.value = 2;
  snapView.value = "restore";
}

// ---------------------------------------------------------------------------
// 还原功能：快照时间线 + 文件选择 + 目的地/冲突策略 + 还原历史
// ---------------------------------------------------------------------------

const rst = reactive({
  task: null, busy: false,
  fromSnap: false,           // true = 从快照详情进入，跳过前两步
  snaps: [], snapsLoading: false,
  cur: null,                 // 选中的快照
  diffs: {},                 // sid -> {added, changed, removed} 或 {none:true}
  dest: "original",          // original | custom
  target: "",
  conflict: "if-changed",    // always | if-changed | never | rename
  includes: [],              // [{path, name, is_dir}]
  cachedAt: "",              // 快照列表缓存生成时间
  repoSnapCount: 0,          // 仓库全量快照数
});
const rstPick = reactive({
  show: false, dir: "", q: "", items: [], loading: false,
  sel: [],                   // 选中路径数组
  crumb: [],
  building: false,           // 正在后台读取整份快照清单
  parsed: 0,                 // 已解析条目数
  elapsed: 0,                // 已耗时（秒）
  err: "",
  meta: {},                  // path -> item，跨目录记住名称与类型
});
let pickTimer = null;
function stopPickPoll() {
  if (pickTimer) { clearTimeout(pickTimer); pickTimer = null; }
}
// 还原向导步骤游标：0=选任务 1=快照 2=文件 3=目的地 4=冲突
const rstStep = ref(0);
const rstSteps = [
  { id: "task", label: "选任务", title: "选择备份任务", hint: "选择要还原哪个备份任务的数据" },
  { id: "snap", label: "选快照", title: "选择快照", hint: "选择要还原到的时间点" },
  { id: "files", label: "选文件", title: "选择要还原的文件", hint: "可进入子目录勾选" },
  { id: "dest", label: "目的地", title: "还原到哪里", hint: "写回原位置或指定新路径" },
  { id: "conf", label: "冲突", title: "遇到同名文件", hint: "已有同名文件时的处理方式" },
];
// 从快照详情进入时，前两步已在浏览中完成，步骤条只展示后三步
const rstOffset = computed(() => (rst.fromSnap ? 2 : 0));
const rstVisibleSteps = computed(() => rstSteps.slice(rstOffset.value));
const rstCanPrev = computed(() => rstStep.value > rstOffset.value);
const tb = reactive({ show: false, path: "", dirs: [], user_root: "", loading: false });
const restores = ref([]);
let rstTimer = null;

// 「新建还原」入口：直接打开还原向导（第 0 步 = 选任务）
function openRpick() { openRestoreWizard(); }

// 打开还原向导（从第 0 步「选任务」开始）
function openRestoreWizard() {
  rst.task = null;
  rst.fromSnap = false;
  rst.cur = null;
  rst.snaps = [];
  rst.diffs = {};
  rst.cachedAt = "";
  rst.dest = "original";
  rst.target = "";
  rst.conflict = "if-changed";
  rst.includes = [];
  rstStep.value = 0;
}
// 第 0 步：选中任务后进入第 1 步，并加载该任务的快照
function selectTask(t) {
  rst.task = t;
  rstStep.value = 1;
  loadRestoreSnaps(t);
}
// 步骤游标导航
function rstNext() {
  if (rstStep.value < 4 && rstStepReady(rstStep.value)) rstStep.value++;
}
function rstPrev() {
  if (rstStep.value > rstOffset.value) rstStep.value--;
}
// 当前步骤是否满足「下一步」条件
function rstStepReady(i) {
  if (i === 0) return !!rst.task;
  if (i === 1) return !!rst.cur;
  if (i === 2) return rst.includes.length > 0;
  if (i === 3) return rst.dest === "original" || (rst.target || "").startsWith("/");
  return true; // 冲突策略总有默认值
}
// 任务卡片「还原」按钮：切到快照页，定位到该任务所在仓库，打开最新快照详情
async function openRestore(t) {
  nav.value = "snap";
  snapView.value = "list";
  if (!rp.list.length) await loadRepoSummary();
  const repo = rp.list.find((r) => r.id === t.repo_id);
  if (!repo) { showToast("找不到这个任务对应的仓库", "bad"); return; }
  rp.open[repo.id] = true;
  const box = repoBox(repo.id);
  if (!box.loaded) await loadRepoSnaps(repo);
  const s = box.snaps.find((x) => x.task_id === t.id);
  if (!s) { showToast("这个任务还没有快照，先跑一次备份", "bad"); return; }
  await openSnapDetail(s, repo);
}
async function loadRestoreSnaps(t, refresh) {
  rst.snapsLoading = true;
  try {
    const r = await api(`/api/tasks/${t.id}/snapshots${refresh ? "?refresh=1" : ""}`);
    rst.snaps = r.snapshots || [];
    rst.cachedAt = r.cached_at || "";
    rst.repoSnapCount = r.repo_snap_count || 0;
    // 懒加载相邻快照 diff 徽章（串行避免打爆云端）
    let chain = Promise.resolve();
    for (let i = 0; i < rst.snaps.length; i++) {
      const s = rst.snaps[i];
      const prev = rst.snaps[i + 1];
      if (!prev) { rst.diffs[s.id] = { none: true }; continue; }
      chain = chain.then(async () => {
        try {
          rst.diffs[s.id] = await api(`/api/tasks/${t.id}/snapdiff?cur=${s.short_id || s.id}&prev=${prev.short_id || prev.id}`);
        } catch { rst.diffs[s.id] = { err: true }; }
      });
    }
  } catch (e) { showToast(e.message, "bad"); }
  rst.snapsLoading = false;
}
function snapBadge(sid) {
  const d = rst.diffs[sid];
  if (!d) return { text: "…", cls: "idle" };
  if (d.none) return { text: "首份", cls: "idle" };
  if (d.err) return { text: "—", cls: "idle" };
  const total = (d.added || 0) + (d.changed || 0) + (d.removed || 0);
  if (!total) return { text: "无变化", cls: "idle" };
  return { text: `+${d.added || 0}/~${d.changed || 0}/-${d.removed || 0}`, cls: total > 0 ? "chg" : "idle" };
}
function snapTime(s) {
  // restic RFC3339 → "10-25 21:59"
  if (!s || !s.time) return "";
  return s.time.slice(5, 16).replace("T", " ");
}
function pickSnap(s) { rst.cur = s; }

// ---- 文件选择器 ----
function openRstPick() {
  rstPick.show = true;
  rstPick.dir = "";
  rstPick.q = "";
  rstPick.crumb = [];
  rstPick.sel = rst.includes.map((x) => x.path);
  rstPick.meta = {};
  for (const x of rst.includes) rstPick.meta[x.path] = x;
  loadRstDir("");
}
async function loadRstDir(dir) {
  if (!rst.cur) return;
  stopPickPoll();
  rstPick.loading = true;
  rstPick.err = "";
  try {
    const sid = rst.cur.short_id || rst.cur.id;
    const r = await api(`/api/tasks/${rst.task.id}/files?snapshot=${encodeURIComponent(sid)}&dir=${encodeURIComponent(dir)}`);
    rstPick.dir = dir;
    rstPick.crumb = buildPickCrumb(dir);
    rstPick.q = "";
    rstPick.parsed = r.parsed || 0;
    rstPick.elapsed = r.elapsed || 0;
    rstPick.building = !!r.loading;
    rstPick.items = r.items || [];
    for (const it of rstPick.items) rstPick.meta[it.path] = it;
    if (r.error) {
      rstPick.err = r.error;
      rstPick.building = false;
      // 目录在快照里不存在（比如从某个源目录往上走到了 /vol1/1000）→ 回到快照根
      if (dir && /不存在/.test(r.error)) { loadRstDir(pickUpDir()); return; }
    }
    rstPick.loading = false;
    // 首次读取大快照清单要跑一会儿，轮询直到就绪
    if (rstPick.building) {
      pickTimer = setTimeout(() => loadRstDir(dir), 2000);
    }
  } catch (e) {
    rstPick.loading = false;
    rstPick.building = false;
    showToast(e.message, "bad");
  }
}
// 面包屑按「快照源目录」切分，避免出现 /vol1/1000 这种不属于快照的层级
function buildPickCrumb(dir) {
  const roots = (rst.cur && rst.cur.paths) || [];
  const root = roots.find((r) => dir === r || dir.startsWith(r + "/")) || "";
  const out = [];
  if (root) {
    out.push({ label: root.split("/").pop() || root, path: root });
    let acc = root;
    for (const seg of dir.slice(root.length).split("/").filter(Boolean)) {
      acc += "/" + seg;
      out.push({ label: seg, path: acc });
    }
  } else {
    let acc = "";
    for (const seg of dir.split("/").filter(Boolean)) {
      acc += "/" + seg;
      out.push({ label: seg, path: acc });
    }
  }
  return out;
}
// 上一级：已经在某个源目录上时回到「快照根」
function pickUpDir() {
  const d = rstPick.dir;
  if (!d) return "";
  const roots = (rst.cur && rst.cur.paths) || [];
  if (roots.includes(d)) return "";
  const up = d.split("/").slice(0, -1).join("/");
  return up && !roots.some((r) => up.startsWith(r + "/")) && !roots.includes(up) ? "" : up;
}
function closeRstPick() {
  stopPickPoll();
  rstPick.show = false;
  rstPick.building = false;
}
// 把「当前所在目录」整体加入选择
function pickCurDir() {
  const d = rstPick.dir;
  if (!d) return;
  if (!rstPick.sel.includes(d)) {
    for (let i = rstPick.sel.length - 1; i >= 0; i--) {
      if (rstPick.sel[i].startsWith(d + "/")) rstPick.sel.splice(i, 1);
    }
    rstPick.sel.push(d);
  }
}
// 选中整份快照（即快照的所有源目录）
function pickWholeSnap() {
  const roots = rst.cur && rst.cur.paths ? rst.cur.paths : [];
  rstPick.sel = roots.slice();
}
function isPicked(p) { return rstPick.sel.includes(p); }
function isHalf(p) { return !isPicked(p) && rstPick.sel.some((x) => p.startsWith(x + "/")); }
function togglePick(it) {
  const arr = rstPick.sel;
  const at = arr.indexOf(it.path);
  if (at >= 0) { arr.splice(at, 1); return; }
  // 父目录已被选中：展开为「除当前项外的同级全选」
  const anc = arr.find((p) => it.path.startsWith(p + "/"));
  if (anc) {
    const siblings = rstPick.items.filter((x) => x.path !== it.path).map((x) => x.path);
    arr.splice(arr.indexOf(anc), 1, ...siblings);
  } else {
    arr.push(it.path);
  }
  // 选目录时去掉冗余子路径
  for (let i = arr.length - 1; i >= 0; i--) {
    if (arr[i] !== it.path && arr[i].startsWith(it.path + "/")) arr.splice(i, 1);
  }
}
function confirmRstPick() {
  rst.includes = rstPick.sel.map((p) => {
    const m = rstPick.meta[p];
    return m
      ? { path: p, name: m.name, is_dir: !!m.is_dir }
      : { path: p, name: p.split("/").pop() || p, is_dir: true };
  });
  closeRstPick();
}
function removeInclude(x) {
  const at = rst.includes.indexOf(x);
  if (at >= 0) rst.includes.splice(at, 1);
}

// ---- 本地目标目录浏览 ----
async function openTargetBrowser() {
  tb.show = true;
  // 一律先列授权根目录；进入根之后，「使用当前目录」就能选中根本身
  tb.path = "";
  tb.dirs = [];
}
async function browseTarget(p) {
  // 空路径 = 根目录列表（纯前端状态）
  if (!p) { tb.path = ""; tb.dirs = []; return; }
  tb.loading = true;
  try {
    const r = await api("/api/browse", { method: "POST", body: JSON.stringify({ path: p }) });
    tb.dirs = r.dirs || [];
    tb.path = r.path;
    tb.user_root = r.user_root || "";
    if (r.roots && r.roots.length) USER_ROOTS.value = r.roots.slice();
    if (r.home) HOME_ROOT.value = r.home;
  } catch (e) { showToast(e.message, "bad"); }
  tb.loading = false;
}
function tbUp() {
  if (!tb.path) return;
  if (isScopeRoot(tb.path)) { tb.path = ""; tb.dirs = []; return; }
  browseTarget(tb.path.split("/").slice(0, -1).join("/"));
}
function tbSegs() {
  const r = activeRoot(tb.path);
  return tb.path.replace(r, "").split("/").filter(Boolean);
}
function tbCrumbRoot() { browseTarget(activeRoot(tb.path) || USER_ROOTS.value[0]); }
function tbCrumbTo(i) { browseTarget(activeRoot(tb.path) + "/" + tbSegs().slice(0, i + 1).join("/")); }
function pickTarget() { if (!tb.path) return; rst.target = tb.path; tb.show = false; }

// ---- 执行还原 ----
const rstReady = computed(() => rst.cur && rst.includes.length && (rst.dest === "original" || (rst.target || "").startsWith("/")));
async function doRestore() {
  if (!rstReady.value || rst.busy) return;
  rst.busy = true;
  try {
    await api(`/api/tasks/${rst.task.id}/restore`, {
      method: "POST",
      body: JSON.stringify({
        snapshot: rst.cur.short_id || rst.cur.id,
        snapshot_time: rst.cur.time || "",
        includes: rst.includes.map((x) => x.path),
        dest: rst.dest,
        target: rst.dest === "custom" ? rst.target : "",
        conflict: rst.conflict,
      }),
    });
    showToast("还原已开始，可在「还原记录」里看进度");
    snapView.value = "detail";
    loadRestores();
  } catch (e) { showToast(e.message, "bad"); }
  rst.busy = false;
}

// ---- 还原历史 ----
let restoresLoaded = false; // 是否已成功加载过（缓存标志）
async function loadRestores() {
  try {
    const r = await api("/api/restores");
    restores.value = r.restores || [];
    restoresLoaded = true;
  } catch { /* 忽略 */ }
}
// 是否有运行中的还原（决定是否需要高频轮询）
const hasRunningRestore = () => restores.value.some((j) => j.status === "running");
// 快照页顶部进度条用
const runningRestores = computed(() => restores.value.filter((j) => j.status === "running"));
async function stopRestore(j) {
  try {
    await api(`/api/restores/${j.id}/stop`, { method: "POST" });
    showToast("已发送停止指令");
  } catch (e) { showToast(e.message, "bad"); }
}
function rstStatusText(j) {
  return { running: "还原中", success: "已还原", failed: "失败", stopped: "已停止" }[j.status] || j.status;
}
function rstStatusCls(j) {
  return { running: "run", success: "ok", failed: "bad", stopped: "idle" }[j.status] || "idle";
}
// 结果条文本：成功时精简结论 + 统计（新增/覆盖/保留/跳过），失败/停止直接用 message
function rstResultText(j) {
  if (j.status === "failed" || j.status === "stopped") {
    return j.message || (j.status === "stopped" ? "已手动停止" : "还原失败");
  }
  // success：优先用统计字段拼精简结论
  const restored = j.restored || 0, replaced = j.replaced || 0, renamed = j.renamed || 0, skipped = j.skipped || 0;
  if (restored + replaced + renamed + skipped > 0) {
    const parts = [`新增 ${restored}`, `覆盖 ${replaced}`];
    if (renamed > 0) parts.push(`保留两者 ${renamed}`);
    if (skipped > 0) parts.push(`跳过 ${skipped}`);
    return `已还原 ${restored + replaced + renamed} 项（${parts.join(" · ")}）`;
  }
  // 无统计字段（旧记录 / 原位置还原）：清洗旧长句「已还原 N 项到 <路径>（新增 X / 覆盖 Y / ...）」
  let msg = j.message || "还原完成";
  const m = msg.match(/^(已还原 \d+ 项)(?:到 [^（]*)?（(.*)）$/);
  if (m) {
    // 解析括号内的统计项，只保留非 0
    const keep = m[2].split(" / ").filter((s) => !/ 0$/.test(s));
    return keep.length ? `${m[1]}（${keep.join(" · ")}）` : m[1];
  }
  return msg;
}

// ---------------------------------------------------------------------------
// 全局滚动锁：任一模态弹窗打开时锁住底层页面
// 否则在弹层空白处上下滑动 / 滚轮，会带着背后的任务列表一起滚（滚动穿透）
// ---------------------------------------------------------------------------
const anyModal = computed(() =>
  !!(
    sheet.show || wiz.show || srcEdit.show || browser.show || cloudPick.show ||
    repoModal.show || cloudBrowser.show || mkdirDlg.show || rmDlg.show ||
    logModal.show || rstPick.show || tb.show
  )
);
let lockedY = 0;
// iOS 上 html/body overflow:hidden 压不住橡皮筋，需 fixed 定位；桌面/安卓用 overflow 即可（不影响输入框聚焦）
const isIOS =
  /iP(hone|ad|od)/.test(navigator.userAgent) ||
  (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);

function lockBody() {
  const b = document.body.style;
  if (isIOS) {
    lockedY = window.scrollY || window.pageYOffset || 0;
    b.position = "fixed";
    b.top = -lockedY + "px";
    b.left = "0";
    b.right = "0";
    b.width = "100%";
  } else {
    // 补偿消失的滚动条宽度，避免打开弹窗时页面横向跳动
    const sbw = window.innerWidth - document.documentElement.clientWidth;
    if (sbw > 0) b.paddingRight = sbw + "px";
    document.documentElement.classList.add("scroll-locked");
  }
}
function unlockBody() {
  const b = document.body.style;
  b.position = "";
  b.top = "";
  b.left = "";
  b.right = "";
  b.width = "";
  b.paddingRight = "";
  document.documentElement.classList.remove("scroll-locked");
  if (isIOS) window.scrollTo(0, lockedY);
}

watch(anyModal, (on) => (on ? lockBody() : unlockBody()));

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------
// 切到快照页时：按需拉仓库摘要（只读本地缓存，很快）+ 还原记录
watch(nav, (n) => {
  if (n === "hist") {
    if (!hist.loaded) loadHistory();
    return;
  }
  if (n !== "snap") return;
  if (!rp.list.length) loadRepoSummary();
  if (!restoresLoaded || hasRunningRestore()) loadRestores();
});

// 还原进度轮询：仅当存在「运行中」还原时才高频刷新，避免无意义请求
function ensureRstTimer() {
  if (hasRunningRestore()) {
    if (!rstTimer) {
      rstTimer = setInterval(() => {
        if (!hasRunningRestore()) { clearInterval(rstTimer); rstTimer = null; return; }
        loadRestores();
      }, 2500);
    }
  } else if (rstTimer) {
    clearInterval(rstTimer);
    rstTimer = null;
  }
}
watch(restores, ensureRstTimer);

// ---------------------------------------------------------------------------
// 历史：备份（任务日志）+ 还原（config.restores）
// ---------------------------------------------------------------------------
const hist = reactive({
  loading: false,
  loaded: false,
  items: [],     // 后端按时间倒序返回
  tab: "all",    // all | bak | rst
  onlyFail: false,
  cur: null,     // 当前选中（桌面右侧详情 / 手机就地展开）
});

let histPending = null;   // 正在进行的请求：下拉刷新时复用它，避免重复请求也避免空转
function loadHistory(force) {
  if (histPending) return histPending;
  hist.loading = true;
  histPending = (async () => {
    try {
      const r = await api("/api/history?limit=80");
      hist.items = r.items || [];
      hist.loaded = true;
      if (!isNarrow.value && (force || !hist.cur)) {
        hist.cur = histList.value[0] || null;
      } else if (hist.cur) {
        // 刷新后用新数据替换选中项，详情里的进度/状态跟着更新
        hist.cur = hist.items.find((x) => x.kind === hist.cur.kind && x.id === hist.cur.id) || hist.cur;
      }
    } catch (e) {
      showToast(e.message || "读取历史失败", "bad");
    } finally {
      hist.loading = false;
      histPending = null;
    }
  })();
  return histPending;
}

const histBackups = computed(() => hist.items.filter((x) => x.kind === "backup"));
const histRestores = computed(() => hist.items.filter((x) => x.kind === "restore"));
const histList = computed(() => {
  let l = hist.tab === "bak" ? histBackups.value : hist.tab === "rst" ? histRestores.value : hist.items;
  if (hist.onlyFail) l = l.filter((x) => x.status === "failed");
  return l;
});

// 切换分段时，右侧详情跟着换到该分段的第一条
watch(
  () => hist.tab + "|" + hist.onlyFail,
  () => {
    hist.cur = isNarrow.value ? null : histList.value[0] || null;
  }
);

const histGroups = computed(() => {
  const out = [];
  const map = {};
  for (const it of histList.value) {
    const key = (it.time || "").slice(0, 10) || "unknown";
    if (!map[key]) {
      map[key] = { key, label: histDayLabel(key), items: [] };
      out.push(map[key]);
    }
    map[key].items.push(it);
  }
  return out;
});

function histDayLabel(key) {
  if (key === "unknown") return "时间未知";
  const pad = (n) => String(n).padStart(2, "0");
  const now = new Date();
  const fmt = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  if (key === fmt(now)) return "今天 · " + key.slice(5);
  if (key === fmt(new Date(now.getTime() - 86400000))) return "昨天 · " + key.slice(5);
  const d = new Date(key + "T00:00:00");
  const wk = isNaN(d.getTime()) ? "" : " " + WEEK_CN[d.getDay()];
  return key.slice(5) + wk;
}
function hhmm(t) {
  return t && t.length >= 16 ? t.slice(11, 16) : "—";
}
function histSel(it) {
  return !!(hist.cur && hist.cur.kind === it.kind && hist.cur.id === it.id);
}
function pickHist(it) {
  if (isNarrow.value && histSel(it)) { hist.cur = null; return; }
  hist.cur = it;
}
function histStatusCls(s) {
  if (s === "success") return "ok";
  if (s === "failed") return "bad";
  if (s === "running") return "run";
  return "gray";
}
function histStatusTxt(it) {
  const s = it.status;
  if (s === "success") return it.kind === "restore" ? "完成" : "成功";
  if (s === "failed") return "失败";
  if (s === "running") return it.kind === "restore" ? "进行中" : "备份中";
  if (s === "stopped") return "已停止";
  return "—";
}
function histTitle(it) {
  if (it.kind === "backup") return it.task_name || "备份";
  const inc = it.includes || [];
  const seg = (inc[0] || "").split("/").filter(Boolean).pop();
  if (seg) return `还原「${seg}」` + (inc.length > 1 ? ` 等 ${inc.length} 项` : "");
  return it.task_name ? `还原 · ${it.task_name}` : "还原";
}
// compact = 手机就地展开时，标题和摘要行已经在上面，不再重复
function histKVs(it, compact) {
  const out = [];
  if (it.kind === "backup") {
    if (it.task_name && !compact) out.push({ k: "任务", v: it.task_name });
    if (it.file) out.push({ k: "日志", v: it.file });
    if (it.one_line && !compact) out.push({ k: "结果", v: it.one_line });
  } else {
    if (it.task_name) out.push({ k: "任务", v: it.task_name });
    out.push({ k: "来源", v: it.snapshot ? "快照 " + String(it.snapshot).slice(0, 8) : "—" });
    out.push({ k: "写回", v: it.dest === "original" ? "原位置" : it.target || "新路径" });
    out.push({ k: "冲突", v: histConflictTxt(it.conflict) });
    if ((it.includes || []).length) {
      out.push({ k: "范围", v: it.includes.join("\n") });
    }
    if (it.one_line && !compact) out.push({ k: "结果", v: it.one_line });
    if (it.message && it.status !== "success" && it.message !== it.one_line) out.push({ k: "说明", v: it.message });
  }
  return out;
}
function histConflictTxt(c) {
  const m = { always: "覆盖全部", "if-changed": "仅覆盖有变化", never: "跳过已存在", rename: "保留两者" };
  return m[c] || c || "—";
}
function openHistLog(it) {
  const t = cfg.tasks.find((x) => x.id === it.task_id);
  if (!t) { showToast("这个任务已经不在了", "bad"); return; }
  openLogs(t).then(() => {
    if (it.file) { logModal.file = it.file; loadLog(); }
  });
}

// ---------------------------------------------------------------------------
// 手机下拉刷新（备份 / 快照 / 历史 三个页签通用）
// ---------------------------------------------------------------------------
const ptr = reactive({ dist: 0, refreshing: false, dragging: false });
let ptrStartY = 0;
let ptrTracking = false;
const PTR_TRIGGER = 52;

// 内容跟随位移（比下拉距离慢，克制一些）；松手用带回弹的缓动
const ptrStyle = computed(() => {
  if (!ptr.dist) return {};
  return {
    transform: "translateY(" + (ptr.dist * 0.62).toFixed(1) + "px)",
    transition: ptr.dragging ? "none" : "transform .34s cubic-bezier(.34,1.4,.64,1)",
  };
});
// 顶部细条填充比例
const ptrPct = computed(() => Math.min(100, (ptr.dist / PTR_TRIGGER) * 100));
// 文字提示淡入
const ptrTxOpacity = computed(() => Math.min(1, ptr.dist / 26));
function ptrTop() {
  return window.scrollY || document.documentElement.scrollTop || 0;
}
async function refreshCurrent() {
  if (nav.value === "hist") {
    await loadHistory(true);
    return;
  }
  if (nav.value === "snap") {
    await Promise.all([loadRepoSummary(), loadRestores()]);
    if (snapView.value === "detail" && snapCur.value && snapCurRepo.value) {
      await openSnapDetail(snapCur.value, snapCurRepo.value);
    }
    return;
  }
  await loadAll();
}
function ptrOnStart(e) {
  if (!isNarrow.value || ptr.refreshing) return;
  if (anyModal.value) return;
  // 还原配置页有自己的底部操作条，不做下拉刷新，避免操作条跟着位移
  if (nav.value === "snap" && snapView.value === "restore") return;
  if (e.touches.length !== 1) return;
  if (ptrTop() > 0) return;
  ptrStartY = e.touches[0].clientY;
  ptrTracking = true;
  ptr.dragging = true;
}
function ptrOnMove(e) {
  if (!ptrTracking || ptr.refreshing) return;
  const dy = e.touches[0].clientY - ptrStartY;
  if (dy <= 0) {
    ptr.dist = 0;
    return;
  }
  if (ptrTop() > 0) {
    ptrTracking = false;
    ptr.dist = 0;
    return;
  }
  // 阻尼：手指移动 1px，指示器走 0.45px，最多 92px
  ptr.dist = Math.min(92, dy * 0.45);
  if (ptr.dist > 4 && e.cancelable) e.preventDefault();
}
async function ptrOnEnd() {
  if (!ptrTracking) return;
  ptrTracking = false;
  ptr.dragging = false;
  if (ptr.dist >= PTR_TRIGGER) {
    ptr.refreshing = true;
    ptr.dist = PTR_TRIGGER;
    try {
      await refreshCurrent();
    } catch (e) { /* 静默，错误已在各自逻辑里提示 */ }
    ptr.refreshing = false;
  }
  ptr.dist = 0;
}

// ---------------------------------------------------------------------------
// 概览统计（方案 B：备份页横幅 / 快照页、历史页统计卡）
// 只用已有接口：任务配置 + /api/repos/summary + /api/history（都只读本地缓存）
// ---------------------------------------------------------------------------
const taskFilter = ref("all"); // all | run | warn

function taskNeedsCare(t) {
  return t.enabled !== false && (!!t.auth_warn || t.last_status === "failed");
}
const taskCounts = computed(() => ({
  all: cfg.tasks.length,
  run: cfg.tasks.filter((t) => t.last_status === "running").length,
  warn: cfg.tasks.filter(taskNeedsCare).length,
}));
const taskQuery = ref("");
const filteredTasks = computed(() => {
  let list = cfg.tasks;
  if (taskFilter.value === "run") list = list.filter((t) => t.last_status === "running");
  else if (taskFilter.value === "warn") list = list.filter(taskNeedsCare);
  const q = taskQuery.value.trim().toLowerCase();
  if (q) {
    list = list.filter((t) =>
      [t.name, repoName(t.repo_id), ...(t.paths || [])].some((x) => String(x || "").toLowerCase().includes(q))
    );
  }
  return list;
});

// 「⋯ → 查看快照」：切到快照页并展开该任务所在仓库
async function openTaskSnaps(t) {
  menuOpen.value = "";
  sheet.show = false;
  nav.value = "snap";
  backToSnapList();
  if (!rp.list.length) await loadRepoSummary();
  const repo = rp.list.find((r) => r.id === t.repo_id);
  if (!repo) { showToast("找不到这个任务对应的仓库", "bad"); return; }
  rp.open[repo.id] = true;
  if (!repoBox(repo.id).loaded) await loadRepoSnaps(repo);
}

// 「⋯ → 复制任务」：以原任务为模板新建（同目的地会沿用该仓库已有密码）
function copyTask(t) {
  openWizard(t);
  wiz.editing = false;
  wiz.copy = true;
  wiz.form.id = "";
  wiz.form.name = t.name + " 副本";
  // 目的地解析不到已有仓库时（会新建仓库），必须让用户看到并保存新密码
  if (!destRepo.value) wiz.copy = false;
}

// 删除确认弹窗（替代浏览器原生 confirm）
const delDlg = reactive({ show: false, task: null, busy: false });

// 快照表格：与同组上一份（更早）快照比较文件数 / 数据量
function snapDelta(snaps, i) {
  const cur = snaps[i], prev = snaps[i + 1];
  if (!prev) return { first: true };
  const fa = cur.summary ? cur.summary.total_files : null, fb = prev.summary ? prev.summary.total_files : null;
  const ba = cur.summary ? cur.summary.total_bytes : null, bb = prev.summary ? prev.summary.total_bytes : null;
  if (fa == null || fb == null) return { unknown: true };
  const df = fa - fb, db = (ba || 0) - (bb || 0);
  if (!df && !db) return { same: true };
  const sign = (n) => (n > 0 ? "+" : n < 0 ? "−" : "±");
  return {
    files: df ? sign(df) + Math.abs(df).toLocaleString() + " 文件" : "",
    bytes: db ? sign(db) + fmtBytes(Math.abs(db)) : "",
    up: df > 0 || (df === 0 && db > 0),
  };
}
function restoreSnapRow(s, r) {
  snapCur.value = s;
  snapCurRepo.value = r;
  restoreFromSnap();
}

// 近 30 天的备份记录（历史接口按时间倒序）
function within30d(time) {
  const d = new Date((time || "").replace(" ", "T"));
  return !isNaN(d) && Date.now() - d.getTime() <= 30 * 86400000;
}
const bak30 = computed(() => histBackups.value.filter((x) => within30d(x.time)));
const rst30 = computed(() => histRestores.value.filter((x) => within30d(x.time)));
const bak30Rate = computed(() => {
  const done = bak30.value.filter((x) => x.status === "success" || x.status === "failed");
  if (!done.length) return null;
  return (done.filter((x) => x.status === "success").length / done.length) * 100;
});

// 历史页横幅文案：近 30 天有失败则转 warn
const histHero = computed(() => {
  const fails = bak30.value.filter((x) => x.status === "failed").length;
  const done = bak30.value.filter((x) => x.status === "success" || x.status === "failed").length;
  const ratio = done ? (done - fails) / done : 0;
  const parts = [`共 ${bak30.value.length} 次备份`];
  if (rst30.value.length) parts.push(`${rst30.value.length} 次还原`);
  return {
    tone: fails ? "warn" : "ok",
    title: fails ? `${fails} 次备份未成功` : "近 30 天备份全部成功",
    sub: parts.join(" · "),
    ratio,
  };
});
function fmtRate(v) {
  if (v == null) return "—";
  return v >= 99.95 ? "100" : v.toFixed(1);
}

const overview = computed(() => {
  const tasks = cfg.tasks;
  const running = tasks.find((t) => t.last_status === "running");
  const care = tasks.filter(taskNeedsCare);
  const healthy = tasks.filter((t) => t.enabled !== false && !taskNeedsCare(t)).length;
  const lastOk = tasks.map((t) => t.last_done_at || "").filter(Boolean).sort().pop() || "";
  let tone = "ok", title = "数据保护正常";
  if (care.length) { tone = "warn"; title = `${care.length} 个任务需要关注`; }
  else if (running) { tone = "run"; title = "正在备份"; }
  const parts = [];
  if (running) parts.push(`「${running.name}」备份中 ${progress.task_id === running.id ? progress.percent.toFixed(0) + "%" : ""}`.trim());
  if (care.length) parts.push(care.map((t) => t.name).slice(0, 2).join("、") + (care.length > 2 ? " 等" : "") + " 上次备份未成功");
  else parts.push(`${tasks.length} 个任务均按计划运行`);
  if (lastOk) parts.push("最近成功备份 " + fmtNextRunOrShort(lastOk));
  return {
    tone, title, sub: parts.join(" · "),
    ratio: tasks.length ? healthy / tasks.length : 0,
    bytes: tasks.reduce((a, t) => a + (Number(t.last_bytes) || 0), 0),
    snaps: rp.list.some((r) => r.cached) ? rp.list.reduce((a, r) => a + (r.count || 0), 0) : null,
  };
});
// "2026-11-09 04:00:12" → "今天 04:00" / "11-02 07:40"
function fmtNextRunOrShort(s) {
  const d = new Date(s.replace(" ", "T"));
  if (isNaN(d)) return fmtShort(s);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return "今天 " + s.slice(11, 16);
  return fmtShort(s);
}

// 任务最近 10 次结果条（旧 → 新，不足 10 次用灰格补齐）
function taskSpark(t) {
  const runs = histBackups.value.filter((x) => x.task_id === t.id).slice(0, 10).reverse();
  const cls = (s) => (s === "success" ? "ok" : s === "failed" ? "f" : s === "running" ? "r" : "n");
  const out = runs.map((x) => ({ c: cls(x.status), tip: `${x.time} ${histStatusTxt(x)}` }));
  while (out.length < 10) out.unshift({ c: "e", tip: "" });
  return out;
}
function taskTagCls(t) {
  const c = statusInfo(t).cls;
  return c === "success" ? "ok" : c === "failed" ? "warn" : c === "running" ? "run" : "gray";
}

// 快照页统计
const snapOverview = computed(() => {
  const list = rp.list;
  return {
    repos: list.length,
    snaps: list.reduce((a, r) => a + (r.count || 0), 0),
    tasks: list.reduce((a, r) => a + (r.task_count || 0), 0),
    latest: list.map((r) => r.latest_time || "").filter(Boolean).sort().pop() || "",
  };
});

// 快照页横幅文案：有仓库没有任何快照时转 warn
const snapHero = computed(() => {
  const list = rp.list;
  const empty = list.filter((r) => !r.count);
  const ratio = list.length ? (list.length - empty.length) / list.length : 0;
  const latest = snapOverview.value.latest ? fmtSnapTime(snapOverview.value.latest) : "";
  const parts = [`${list.length} 个仓库`, `${snapOverview.value.snaps} 个快照`];
  if (latest) parts.push(`最近 ${latest}`);
  if (empty.length) parts.unshift(`${empty.map((r) => r.name).slice(0, 2).join("、")}${empty.length > 2 ? " 等" : ""}还没有快照`);
  return {
    tone: "ok",
    title: empty.length ? `${empty.length} 个仓库还没有快照` : "云端快照正常",
    sub: parts.join(" · "),
    ratio,
  };
});

// 复制任务时若把目的地改到新目录（不再命中已有仓库），恢复为新建密码输入
watch(destRepo, (r) => { if (wiz.copy && !r) wiz.copy = false; });

// 还原向导：步骤条下方的已选摘要；已完成的步骤可点回
const CONFLICT_TXT = { always: "全部覆盖", "if-changed": "仅覆盖有变化", never: "跳过已存在", rename: "保留两者" };
function rstStepSummary(id) {
  if (id === "task") return rst.task ? rst.task.name : "";
  if (id === "snap") return rst.cur ? snapTime(rst.cur) : "";
  if (id === "files") return rst.includes.length ? rst.includes.length + " 项" : "";
  if (id === "dest") return rst.dest === "original" ? "原位置" : (rst.target || "新路径");
  if (id === "conf") return CONFLICT_TXT[rst.conflict] || "";
  return "";
}
function rstGoto(i) {
  if (i < rstStep.value && i >= rstOffset.value) rstStep.value = i;
}
function rstCancel() {
  snapView.value = rst.fromSnap && snapCur.value ? "detail" : "list";
}

// 历史详情：失败的还原一键带回向导（沿用当时的快照、内容、目的地与冲突策略）
async function retryRestore(it) {
  const t = cfg.tasks.find((x) => x.id === it.task_id);
  if (!t) { showToast("关联的备份任务已被删除", "bad"); return; }
  openRestoreWizard();
  rst.task = t;
  nav.value = "snap";
  snapView.value = "restore";
  rstStep.value = 1;
  await loadRestoreSnaps(t);
  const sid = it.snapshot || "";
  const hit = sid && rst.snaps.find((x) => (x.short_id || "").startsWith(sid) || x.id.startsWith(sid) || sid.startsWith(x.short_id || "~"));
  if (!hit) { showToast("原快照已不存在，请重新选择", "bad"); return; }
  rst.cur = hit;
  rst.includes = (it.includes || []).map((p) => ({ path: p, name: p.split("/").pop() || p, is_dir: true }));
  if (it.dest === "custom" && it.target) { rst.dest = "custom"; rst.target = it.target; }
  if (it.conflict) rst.conflict = it.conflict;
  rstStep.value = rst.includes.length ? 4 : 2;
}
function histProgress(it) {
  if (it.status !== "running") return null;
  if (it.kind === "restore") return it.pct || 0;
  return it.task_id === progress.task_id ? progress.percent : null;
}

// 日志抽屉：把一行结果拆成几个数字块（"12,846 个文件 · 13.5 GB · 用时 18 分"）
const logStats = computed(() => {
  const sm = logModal.summary;
  if (!sm) return [];
  const out = [];
  if (sm.status === "running") out.push({ k: "进度", v: (sm.percent || 0).toFixed(1) + "%" });
  for (const part of String(sm.one_line || "").split("·").map((x) => x.trim()).filter(Boolean)) {
    let k = "";
    if (/文件|项/.test(part)) k = "文件";
    else if (/\d\s*(B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)\b/i.test(part)) k = "数据量";
    else if (/^用时/.test(part)) k = "用时";
    out.push({ k: k || "结果", v: k === "用时" ? part.replace(/^用时\s*/, "") : part });
  }
  if (sm.speed) out.push({ k: "速度", v: sm.speed });
  return out.slice(0, 4);
});

// 快照浏览（桌面）：全快照文件搜索
const sdq = reactive({ q: "", items: [], loading: false, done: false });
let sdqTimer = null;
watch(() => sdq.q, (q) => {
  clearTimeout(sdqTimer);
  if (!q.trim()) { sdq.items = []; sdq.done = false; return; }
  sdqTimer = setTimeout(sdSearch, 350);
});
async function sdSearch() {
  const s = snapCur.value;
  const q = sdq.q.trim();
  if (!s || !s.task_id || !q) return;
  sdq.loading = true;
  try {
    const sid = s.short_id || s.id;
    const r = await api(`/api/tasks/${s.task_id}/files?snapshot=${encodeURIComponent(sid)}&q=${encodeURIComponent(q)}`);
    if (q !== sdq.q.trim()) return;
    sdq.items = r.loading ? [] : r.items || [];
    sdq.done = !r.loading;
    if (r.loading) showToast("目录清单还在读取中，稍后再搜");
  } catch (e) { showToast(e.message, "bad"); }
  sdq.loading = false;
}
watch(snapCur, () => { sdq.q = ""; sdq.items = []; sdq.done = false; });
function sdBaseName(p) { return (p || "").split("/").pop() || p; }

// 任务状态变化（跑完 / 失败）后顺手刷新历史，结果条和成功率跟着更新
watch(
  () => cfg.tasks.map((t) => t.id + ":" + t.last_status + ":" + (t.last_done_at || "")).join("|"),
  (n, o) => { if (o && n !== o && hist.loaded) loadHistory(); }
);

// ---------------------------------------------------------------------------
// 在线更新（不依赖应用商店审核周期；后端见 updater.go）
// ---------------------------------------------------------------------------
const upd = ref(null);
const updDlg = reactive({ show: false, busy: false, resetAsk: false });
let updTimer = null;
const isAdmin = computed(() => !status.value?.auth?.identified || !!status.value?.auth?.admin);
// 横幅：有可用更新 / 需要去商店 / 上次自动回滚过，三种情况提示一次
const updBanner = computed(() => {
  const u = upd.value;
  if (!u || !u.enabled) return null;
  if (u.failed && !u.available) return { tone: "bad", text: `在线更新 v${u.failed} 启动失败，已自动回滚到 v${u.current}` };
  if (u.available) return { tone: "info", text: `发现新版本 ${u.latest}` };
  if (u.need_store) return { tone: "warn", text: `新版本 ${u.latest} 需要通过飞牛应用商店更新` };
  return null;
});

async function loadUpdate() {
  try {
    upd.value = await api("/api/update");
  } catch {
    upd.value = null; // 老后端没有这个接口时静默
  }
}

async function checkUpdate() {
  updDlg.busy = true;
  try {
    upd.value = await api("/api/update/check", { method: "POST" });
    if (!upd.value.latest) showToast("已是最新版本");
  } catch (e) {
    showToast(e.message, "bad");
  } finally {
    updDlg.busy = false;
  }
}

function openUpdate() {
  updDlg.show = true;
  updDlg.resetAsk = false;
  // 打开时没检查过就顺手检查一次
  if (upd.value?.enabled && !upd.value.checked_at && !updDlg.busy) checkUpdate();
}

// 安装/重启期间轮询：先看下载进度，重启后后端版本号变了就刷新页面加载新前端
function watchUpdate(target) {
  if (updTimer) clearInterval(updTimer);
  const started = Date.now();
  updTimer = setInterval(async () => {
    try {
      const st = await api("/api/status");
      if (target ? st.version === target : st.version !== upd.value?.current) {
        clearInterval(updTimer);
        updTimer = null;
        location.reload();
        return;
      }
      const u = await api("/api/update");
      if (u.phase === "error") {
        clearInterval(updTimer);
        updTimer = null;
        upd.value = u;
        showToast(u.error || "更新失败", "bad");
        return;
      }
      if (u.current === upd.value?.current) upd.value = u;
    } catch {
      // 重启中后端短暂不可达，继续等
    }
    if (Date.now() - started > 180000) {
      clearInterval(updTimer);
      updTimer = null;
      showToast("更新超时，请到应用中心查看应用状态", "bad");
      loadUpdate();
    }
  }, 1500);
}

async function applyUpdate() {
  updDlg.busy = true;
  try {
    const target = upd.value.latest;
    await api("/api/update/apply", { method: "POST" });
    upd.value = { ...upd.value, phase: "downloading", progress: 0, error: "" };
    watchUpdate(target);
  } catch (e) {
    showToast(e.message, "bad");
  } finally {
    updDlg.busy = false;
  }
}

async function resetUpdate() {
  updDlg.busy = true;
  try {
    await api("/api/update/reset", { method: "POST" });
    upd.value = { ...upd.value, phase: "restarting" };
    watchUpdate(upd.value.builtin);
  } catch (e) {
    showToast(e.message, "bad");
  } finally {
    updDlg.busy = false;
    updDlg.resetAsk = false;
  }
}

const updWorking = computed(() => ["downloading", "installing", "restarting"].includes(upd.value?.phase));
const updPhaseText = computed(() => {
  const u = upd.value;
  if (!u) return "";
  if (u.phase === "downloading") return `正在下载 ${Math.round((u.progress || 0) * 100)}%`;
  if (u.phase === "installing") return "正在校验并安装…";
  if (u.phase === "restarting") return "正在重启，完成后页面会自动刷新…";
  return "";
});

onMounted(() => {
  loadAll();
  loadUpdate();
  // 备份页横幅要用到快照数与近 30 天成功率；两个接口都只读本地缓存，开销很小
  loadRepoSummary();
  loadHistory();
  // 后端每 8 秒同步一次平台授权表，这里每 15 秒对一下，界面自动跟着变
  scopeTimer = setInterval(syncScope, 15000);
  document.addEventListener("visibilitychange", onPageVisible);
  window.addEventListener("touchstart", ptrOnStart, { passive: true });
  window.addEventListener("touchmove", ptrOnMove, { passive: false });
  window.addEventListener("touchend", ptrOnEnd, { passive: true });
  window.addEventListener("touchcancel", ptrOnEnd, { passive: true });
});
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer);
  if (logTimer) clearInterval(logTimer);
  if (rstTimer) clearInterval(rstTimer);
  if (scopeTimer) clearInterval(scopeTimer);
  if (updTimer) clearInterval(updTimer);
  window.removeEventListener("touchstart", ptrOnStart);
  window.removeEventListener("touchmove", ptrOnMove);
  window.removeEventListener("touchend", ptrOnEnd);
  window.removeEventListener("touchcancel", ptrOnEnd);
  document.removeEventListener("visibilitychange", onPageVisible);
  // 保险：组件销毁时解除滚动锁，避免 body 卡在 fixed
  unlockBody();
});
</script>

<template>
  <div class="page" @click="menuOpen = ''">
    <!-- ==================== 桌面左侧导航 ==================== -->
    <aside class="sidebar">
      <nav class="sb-nav">
        <button class="sb-item" :class="{ on: nav === 'tasks' }" @click="nav = 'tasks'">
          <Icon name="folder" :size="18" />
          备份任务<span class="sb-cnt" v-if="cfg.tasks.length">{{ cfg.tasks.length }}</span>
        </button>
        <button class="sb-item" :class="{ on: nav === 'snap' }" @click="nav = 'snap'">
          <Icon name="stack-2" :size="18" />
          快照
        </button>
        <button class="sb-item" :class="{ on: nav === 'hist' }" @click="nav = 'hist'">
          <Icon name="history" :size="18" />
          历史<span class="sb-cnt run" v-if="taskCounts.run || runningRestores.length">{{ taskCounts.run + runningRestores.length }}</span>
        </button>
      </nav>
      <div class="sb-foot" v-if="status">
        <div>量子密备 v{{ status.version || "-" }}</div>
        <button class="upd-link" v-if="upd && upd.enabled" :class="{ hot: upd.available }" @click="openUpdate">
          <span class="upd-dot" v-if="upd.available"></span>{{ upd.available ? "更新到 v" + upd.latest : "检查更新" }}
        </button>
        <div>restic {{ status.restic_version || "未找到" }}</div>
        <div>rclone {{ status.rclone_version || "未找到" }}</div>
      </div>
    </aside>

    <div class="main" :style="ptrStyle">

      <main class="content">
        <div class="upd-bar" v-if="updBanner" :class="'tone-' + updBanner.tone" @click="openUpdate">
          <Icon :name="updBanner.tone === 'info' ? 'refresh' : 'alert-circle'" :size="16" />
          <span>{{ updBanner.text }}</span>
          <span class="upd-bar-go">查看<Icon name="chevron-right" :size="14" /></span>
        </div>
        <!-- ==================== 备份任务（仿飞牛原生） ==================== -->
        <template v-if="nav === 'tasks'">
          <!-- 概览横幅（方案 B） -->
          <div class="ov-hero" v-if="cfg.tasks.length" :class="'tone-' + overview.tone">
            <div class="ov-ring">
              <svg viewBox="0 0 96 96" class="ov-ring-svg">
                <circle cx="48" cy="48" r="42" fill="none" class="ov-ring-bg" stroke-width="8"/>
                <circle cx="48" cy="48" r="42" fill="none" class="ov-ring-fg" stroke-width="8" stroke-linecap="round"
                  stroke-dasharray="264" :stroke-dashoffset="264 * (1 - overview.ratio)"/>
              </svg>
              <div class="ov-ring-c">
                <Icon v-if="overview.tone === 'warn'" name="shield-exclamation" :size="32" />
                <span v-else-if="overview.tone === 'run'" class="ov-spin"></span>
                <Icon v-else name="shield-check" :size="32" />
              </div>
            </div>
            <div class="ov-txt">
              <h2>{{ overview.title }}</h2>
              <p>{{ overview.sub }}</p>
              <div class="ov-acts">
                <button class="btn primary" @click="openWizard(null)"><Icon name="plus" :size="15" class="plus" /> 创建新任务</button>
              </div>
            </div>
            <div class="ov-kpis">
              <div class="ov-kpi"><span>已保护数据</span><b>{{ overview.bytes ? fmtBytes(overview.bytes) : "—" }}</b></div>
              <div class="ov-kpi"><span>云端快照</span><b>{{ overview.snaps == null ? "—" : overview.snaps }}<small v-if="overview.snaps != null">个</small></b></div>
              <div class="ov-kpi"><span>近 30 天成功率</span><b :class="{ ok: bak30Rate != null && bak30Rate >= 90 }">{{ fmtRate(bak30Rate) }}<small v-if="bak30Rate != null">%</small></b></div>
            </div>
          </div>

          <!-- 任务表格（桌面） -->
          <div class="panel task-panel" v-if="cfg.tasks.length">
            <div class="panel-h">
              <h3>备份任务</h3>
              <div class="pseg">
                <button :class="{ on: taskFilter === 'all' }" @click="taskFilter = 'all'">全部 {{ taskCounts.all }}</button>
                <button :class="{ on: taskFilter === 'run' }" @click="taskFilter = 'run'">运行中 {{ taskCounts.run }}</button>
                <button :class="{ on: taskFilter === 'warn' }" @click="taskFilter = 'warn'">需关注 {{ taskCounts.warn }}</button>
              </div>
              <label class="psearch">
                <Icon name="search" :size="14" />
                <input v-model="taskQuery" placeholder="搜索任务、目录或云盘" />
                <i v-if="taskQuery" @click="taskQuery = ''">✕</i>
              </label>
            </div>
            <table class="ttbl">
              <thead>
                <tr><th>任务</th><th>备份路线</th><th>状态</th><th>最近 10 次</th><th>下次运行</th><th class="r">操作</th></tr>
              </thead>
              <tbody>
                <tr v-for="t in filteredTasks" :key="t.id" @click="openLogs(t)">
                  <td>
                    <div class="tt-name">
                      <span class="tt-ic" :class="'c-' + taskTagCls(t)">
                        <Icon name="folder" :size="26" />
                      </span>
                      <div class="tt-nm">
                        <b :title="t.name">{{ t.name }}</b>
                        <small>{{ pathSummary(t) || "未选择目录" }}<template v-if="t.last_bytes"> · {{ fmtBytes(t.last_bytes) }}</template></small>
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="tt-route" :title="'fnos（本地） → ' + repoName(t.repo_id) + ' / ' + destPath(repoObj(t.repo_id))">
                      <span class="chip2">fnos</span>
                      <Icon name="arrow-narrow-right" :size="14" />
                      <span class="chip2">{{ repoName(t.repo_id) }}</span>
                    </div>
                  </td>
                  <td>
                    <div class="tt-prog" v-if="t.id === progress.task_id">
                      <div class="tt-prog-t"><span><span class="spin"></span> 备份中</span><b>{{ progress.percent.toFixed(0) }}%</b></div>
                      <div class="bar"><div class="bar-in" :style="{ width: progress.percent + '%' }"></div></div>
                      <div class="tt-prog-s">{{ fmtBytes(progress.bytes_done) }} / {{ fmtBytes(progress.total_bytes) }}<template v-if="etaText"> · {{ etaText }}</template></div>
                    </div>
                    <span v-else class="tag2" :class="taskTagCls(t)" :title="statusInfo(t).detail || ''">
                      <i class="d"></i>{{ statusInfo(t).text }}
                    </span>
                  </td>
                  <td>
                    <div class="spark" title="最近 10 次运行（旧 → 新）">
                      <i v-for="(s, i) in taskSpark(t)" :key="i" :class="s.c" :title="s.tip"></i>
                    </div>
                  </td>
                  <td class="num">{{ t.next_run_at ? fmtNextRun(t.next_run_at) : (t.enabled === false ? "已禁用" : "手动") }}</td>
                  <td class="r" @click.stop>
                    <div class="tc-acts">
                      <button class="btn ghost sm" v-if="t.last_status === 'running'" @click="stopTask(t)"><Icon name="player-stop" :size="13" /> 停止</button>
                      <template v-else>
                        <button class="icon-btn" v-if="t.enabled !== false" title="立即备份" @click="runTask(t)">
                          <Icon name="player-play" :size="16" />
                        </button>
                        <button class="icon-btn" title="还原" @click.stop="openRestore(t)">
                          <Icon name="arrow-back-up" :size="16" />
                        </button>
                      </template>
                      <button class="icon-btn" title="更多" @click.stop="toggleMenu(t.id)">
                        <Icon name="dots" :size="16" />
                      </button>
                      <div class="menu" v-if="menuOpen === t.id" @click.stop>
                        <button @click="openWizard(t)">编辑任务</button>
                        <button @click="openLogs(t)">运行日志</button>
                        <button @click="openTaskSnaps(t)">查看快照</button>
                        <button @click="openRestore(t)">还原</button>
                        <button @click="copyTask(t)">复制任务</button>
                        <hr />
                        <button class="danger" @click="delTask(t)">删除任务</button>
                      </div>
                    </div>
                  </td>
                </tr>
                <tr v-if="!filteredTasks.length" class="tt-empty">
                  <td colspan="6">{{ taskQuery.trim() ? "没有匹配「" + taskQuery.trim() + "」的任务" : taskFilter === "warn" ? "没有需要关注的任务，一切正常" : "当前没有正在运行的任务" }}</td>
                </tr>
              </tbody>
            </table>
            <div class="panel-foot">点击任务行查看运行日志</div>
          </div>

          <!-- 手机卡片 -->
          <div class="mcard-list" v-if="cfg.tasks.length">
            <div class="mcard" v-for="t in cfg.tasks" :key="'m' + t.id" @click="openSheet(t)">
              <div class="mc-top">
                <div class="tc-icon">
                  <Icon name="folder" :size="28" />
                  <span class="badge" :class="'b-' + statusInfo(t).badge">
                    <template v-if="statusInfo(t).badge === 'warn'">!</template>
                    <template v-else-if="statusInfo(t).badge === 'minus'">–</template>
                    <template v-else-if="statusInfo(t).badge === 'ok'">✓</template>
                    <template v-else-if="statusInfo(t).badge === 'run'"><i class="mini-spin"></i></template>
                  </span>
                </div>
                <div class="mc-title">
                  <b>{{ t.name }}</b>
                  <div class="tc-status" :class="statusInfo(t).cls">
                    <span class="st-main">{{ statusInfo(t).text }}</span>
                  </div>
                </div>
                <button class="mc-more" @click.stop="openSheet(t)"><Icon name="dots" :size="17" /></button>
              </div>
              <div class="mc-row"><span>来源地</span><span>fnos（本地） · {{ pathSummary(t) }}</span></div>
              <div class="mc-row"><span>目的地</span><span>{{ repoName(t.repo_id) }} · {{ destPath(repoObj(t.repo_id)) }}</span></div>
              <div class="mc-row"><span>上次备份</span><span>{{ t.last_done_at ? fmtShort(t.last_done_at) : "无记录" }}</span></div>
              <div class="mc-row"><span>下次备份</span><span>{{ t.next_run_at ? fmtNextRun(t.next_run_at) : "无计划" }}</span></div>

              <!-- 运行中进度（仅当前任务，手机卡片） -->
              <div class="tc-progress" v-if="t.id === progress.task_id">
                <div class="pg-head">
                  <span class="spin"></span> 正在备份
                  <span class="pg-pct">{{ progress.percent.toFixed(1) }}%</span>
                </div>
                <div class="bar"><div class="bar-in" :style="{ width: progress.percent + '%' }"></div></div>
                <div class="pg-meta">
                  <span><span class="pg-k">开始时间</span>{{ fmtClock(progress.started_at) }}</span>
                  <span><span class="pg-k">数据量</span>{{ fmtBytes(progress.bytes_done) }} / {{ fmtBytes(progress.total_bytes) }}</span>
                  <span><span class="pg-k">预计完成</span>{{ etaText || "计算中…" }}</span>
                  <span v-if="progress.error_count" class="pg-err"><Icon name="alert-triangle" :size="13" /> {{ progress.error_count }} 个错误</span>
                </div>
              </div>
            </div>
          </div>

          <!-- 空态 -->
          <div class="panel ov-empty" v-if="!cfg.tasks.length">
            <div class="ov-empty-ic">
              <Icon name="shield-check" :size="56" />
            </div>
            <b>还没有备份任务</b>
            <p>选择 NAS 上的文件夹，加密后定时备份到云盘，云端只存密文</p>
            <button class="btn primary hide-m" @click="openWizard(null)"><Icon name="plus" :size="15" class="plus" /> 创建第一个任务</button>
            <div class="ov-empty-steps"><span>① 选择要备份的目录</span><span>② 连接云盘并设置密码</span><span>③ 设定备份计划</span></div>
          </div>

          <!-- 手机操作面板 -->
          <div class="mask sheet-mask" v-if="sheet.show" @click.self="sheet.show = false">
            <div class="sheet">
              <div class="sheet-title">
                <span>{{ sheet.task ? sheet.task.name : "" }}</span>
                <button class="sheet-x" aria-label="关闭" @click="sheet.show = false">✕</button>
              </div>
              <template v-if="sheet.task && sheet.task.last_status === 'running'">
                <button class="sheet-btn" @click="sheet.task && stopTask(sheet.task)"><Icon name="player-stop" :size="16" />停止备份</button>
              </template>
              <template v-else-if="sheet.task && sheet.task.enabled !== false">
                <button class="sheet-btn primary" @click="sheet.task && runTask(sheet.task)"><Icon name="player-play" :size="16" />立即备份</button>
              </template>
              <button class="sheet-btn" @click="sheet.task && openLogs(sheet.task)">查看日志</button>
              <button class="sheet-btn" @click="sheet.task && openTaskSnaps(sheet.task)">查看快照</button>
              <button class="sheet-btn" @click="sheet.task && openWizard(sheet.task)">编辑任务</button>
              <button class="sheet-btn" @click="sheet.task && copyTask(sheet.task)">复制任务</button>
              <button class="sheet-btn danger" @click="sheet.task && delTask(sheet.task)">删除任务</button>
            </div>
          </div>
        </template>

        <!-- ==================== 云盘仓库管理已并入：创建任务向导「① 选择云端」下拉（👁 密码 / ✕ 移除） ==================== -->

        <!-- ==================== 快照：仓库列表 → 快照详情 → 还原 ==================== -->
        <template v-else-if="nav === 'snap'">

          <!-- ---------- 视图 1：仓库列表（点开展示快照） ---------- -->
          <template v-if="snapView === 'list'">
            <div class="dhead">
              <h1>快照</h1>
              <span class="dhead-sub">每次备份都会在云端留下一个可还原的时间点</span>
              <div class="dhead-acts">
                <button class="btn ghost" :disabled="rp.loading" @click="loadRepoSummary">
                  {{ rp.loading ? "读取中…" : "刷新" }}
                </button>
              </div>
            </div>

            <div class="ov-hero" v-if="rp.list.length" :class="'tone-' + snapHero.tone">
              <div class="ov-ring">
                <svg viewBox="0 0 96 96" class="ov-ring-svg">
                  <circle cx="48" cy="48" r="42" fill="none" class="ov-ring-bg" stroke-width="8"/>
                  <circle cx="48" cy="48" r="42" fill="none" class="ov-ring-fg" stroke-width="8" stroke-linecap="round"
                    stroke-dasharray="264" :stroke-dashoffset="264 * (1 - snapHero.ratio)"/>
                </svg>
                <div class="ov-ring-c">
                  <Icon :name="snapHero.tone === 'warn' ? 'alert-circle' : 'stack-2'" :size="32" />
                </div>
              </div>
              <div class="ov-txt">
                <h2>{{ snapHero.title }}</h2>
                <p>{{ snapHero.sub }}</p>
              </div>
              <div class="ov-kpis">
                <div class="ov-kpi"><span>云端仓库</span><b>{{ snapOverview.repos }}<small>个</small></b></div>
                <div class="ov-kpi"><span>快照总数</span><b>{{ snapOverview.snaps }}<small>个</small></b></div>
                <div class="ov-kpi"><span>关联任务</span><b>{{ snapOverview.tasks }}<small>个</small></b></div>
                <div class="ov-kpi"><span>最近快照</span><b class="sm">{{ snapOverview.latest ? fmtSnapTime(snapOverview.latest) : "—" }}</b></div>
              </div>
            </div>

            <!-- 正在进行的还原 -->
            <div class="sn-run" v-for="j in runningRestores" :key="'run' + j.id">
              <i class="mini-spin"></i>
              <span class="sn-run-t">正在还原 · {{ j.task_name }}</span>
              <div class="sn-run-bar"><i :style="{ width: (j.pct || 0) + '%' }"></i></div>
              <span class="sn-run-p">{{ Math.floor(j.pct || 0) }}%</span>
              <a class="sn-run-go" @click="nav = 'hist'; hist.tab = 'rst'">查看</a>
            </div>

            <div class="sn-list" v-if="rp.list.length">
              <div class="sn-repo" v-for="r in rp.list" :key="r.id" :class="{ open: rp.open[r.id] }">
                <div class="sn-head" @click="toggleRepo(r)">
                  <div class="sn-ic">
                    <img v-if="cloudLogo(repoBrand(r))" class="sn-logo" :src="cloudLogo(repoBrand(r))" :alt="cloudLabel(repoBrand(r))" :title="cloudLabel(repoBrand(r))" />
                    <Icon v-else :name="cloudIcon(repoBrand(r))" :size="28" color="var(--ic)" />
                  </div>
                  <div class="sn-body">
                    <div class="sn-name">
                      {{ r.name }}
                      <span class="sn-pill" :class="r.count ? 'ok' : 'gray'">{{ r.count ? r.count + " 个快照" : "无快照" }}</span>
                    </div>
                    <div class="sn-path mono">{{ r.uri || r.cloud || "" }}</div>
                    <div class="sn-meta">
                      <span><b>{{ r.task_count }}</b>个任务</span>
                      <span>最近快照 <b>{{ r.latest_time ? fmtSnapTime(r.latest_time) : "—" }}</b></span>
                    </div>
                  </div>
                  <div class="sn-stats">
                    <div><span>关联任务</span><b>{{ r.task_count }}</b></div>
                    <div><span>最近快照</span><b>{{ r.latest_time ? fmtSnapTime(r.latest_time) : "—" }}</b></div>
                  </div>
                  <div class="sn-chev">
                    <Icon name="chevron-right" :size="15" />
                  </div>
                </div>

                <div class="sn-drop" v-if="rp.open[r.id]">
                  <div class="sn-loading" v-if="repoBox(r.id).loading && !repoBox(r.id).snaps.length">
                    <i class="mini-spin"></i> 正在读取快照…
                  </div>

                  <template v-else-if="repoBox(r.id).snaps.length">
                    <div class="sn-grp" v-for="g in repoGroupsOf(r.id)" :key="g.id">
                      <div class="sn-grp-h">
                        <span class="sn-dot" :style="{ background: taskColor(g.id) }"></span>
                        <span class="sn-gn">{{ g.name }}</span>
                        <span class="sn-gp mono" v-if="g.paths.length">{{ g.paths[0] }}</span>
                        <span class="sn-gc">{{ g.snaps.length }} 个快照</span>
                      </div>
                      <table class="sn-tbl">
                        <thead>
                          <tr>
                            <th style="width:130px">时间</th>
                            <th style="width:120px">来源</th>
                            <th>较上一份</th>
                            <th style="width:190px">文件 / 大小</th>
                            <th style="width:110px"></th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr v-for="(s, si) in g.snaps" :key="s.id" @click="openSnapDetail(s, r)" :title="(s.paths || []).join('\n')">
                            <td class="t">{{ fmtSnapTime(s.time) }}</td>
                            <td>
                              <span class="tag2 pur" v-if="si === g.snaps.length - 1">首份</span>
                              <span class="tag2 gray" v-else :title="(s.tags || []).join('、')">{{ snapTagLabel(s) }}</span>
                            </td>
                            <td>
                              <template v-for="d in [snapDelta(g.snaps, si)]" :key="'d' + s.id">
                                <span class="dl dim" v-if="d.first">—</span>
                                <span class="dl z" v-else-if="d.same">无变化</span>
                                <span class="dl dim" v-else-if="d.unknown">—</span>
                                <template v-else>
                                  <span class="dl" :class="d.up ? 'a' : 'd'" v-if="d.files">{{ d.files }}</span>
                                  <span class="dl" :class="d.up ? 'a' : 'd'" v-if="d.bytes">{{ d.bytes }}</span>
                                </template>
                              </template>
                            </td>
                            <td class="mono dim nowrap">{{ snapFs(s) }}</td>
                            <td class="acts">
                              <span class="sn-link">浏览</span>
                              <template v-if="s.task_id"><i class="sn-sep">|</i><span class="sn-link" @click.stop="restoreSnapRow(s, r)">还原</span></template>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                    </div>

                    <div class="sn-mlist">
                      <div class="sn-mgrp" v-for="g in repoGroupsOf(r.id)" :key="'m' + g.id">
                        <div class="sn-mgrp-t">
                          <span class="sn-dot" :style="{ background: taskColor(g.id) }"></span>
                          <span class="sn-gn">{{ g.name }}</span>
                          <em>{{ g.snaps.length }}</em>
                        </div>
                        <div class="sn-mcard" v-for="s in g.snaps" :key="'ms' + s.id" @click="openSnapDetail(s, r)">
                          <div class="sn-mtop">
                            <b>{{ fmtSnapTime(s.time) }}</b>
                            <span class="sn-mgo">浏览 ›</span>
                          </div>
                          <div class="sn-msub" v-if="snapFs(s, true) !== '—'">{{ snapFs(s, true) }}</div>
                        </div>
                      </div>
                    </div>

                    <div class="sn-foot">
                      <span v-if="repoBox(r.id).cachedAt">缓存于 {{ repoBox(r.id).cachedAt.slice(5, 16) }}</span>
                      <button class="btn ghost sm" :disabled="repoBox(r.id).loading" @click.stop="loadRepoSnaps(r, true)">
                        {{ repoBox(r.id).loading ? "查询中…" : "↻ 刷新快照" }}
                      </button>
                    </div>
                  </template>

                  <div class="sn-empty" v-else>这个仓库还没有快照，跑一次备份就会产生第一个</div>
                </div>
              </div>
            </div>

            <div class="empty" v-else>
              <div class="empty-icon"><Icon name="database" :size="54" /></div>
              <p>还没有云端仓库</p>
              <p class="hint">在「备份」页创建任务时会同时建立加密仓库</p>
            </div>
          </template>

          <!-- ---------- 视图 2：快照详情 ---------- -->
          <template v-else-if="snapView === 'detail'">
            <div class="sd-bar">
              <button class="sd-back" @click="backToSnapList">‹ 快照</button>
              <span class="sd-sep">/</span>
              <span class="sd-crumb">{{ snapCurRepo ? snapCurRepo.name : "" }}</span>
              <span class="sd-sep">/</span>
              <b class="sd-crumb">{{ snapCur ? fmtSnapTime(snapCur.time) : "" }}</b>
            </div>

            <div class="sd-card">
              <div class="sd-top">
                <span class="sd-ic">
                  <Icon name="stack-2" :size="32" />
                </span>
                <div class="sd-l">
                  <div class="sd-time">{{ snapCur ? (snapCur.time || "").slice(0, 19).replace("T", " ") : "" }}</div>
                  <div class="sd-sub">
                    <span>{{ snapCur ? weekdayOf(snapCur.time) : "" }}</span>
                    <span class="sd-id mono">{{ snapCur ? (snapCur.short_id || snapCur.id || "").slice(0, 8) : "" }}</span>
                    <span class="sn-pill blue" v-if="snapCur && snapCur.task_name">{{ snapCur.task_name }}</span>
                  </div>
                </div>
                <div class="sd-acts">
                  <button class="btn primary" :disabled="!snapCur || !snapCur.task_id" @click="sdRestore">{{ sdRestoreLabel() }}</button>
                </div>
              </div>

              <div class="sd-kv">
                <div><span>主机</span><b>{{ (snapCur && snapCur.hostname) || "—" }}</b></div>
                <div><span>文件数</span><b>{{ snapCur ? snapFiles(snapCur) : "—" }}</b></div>
                <div><span>总大小</span><b>{{ snapCur ? snapBytes(snapCur) : "—" }}</b></div>
                <div><span>距上一快照</span><b>{{ snapGap }}</b></div>
              </div>
            </div>

            <div class="sd-panel">
              <div class="sd-ph">与上一快照的差异</div>
              <div class="sd-diff" v-if="snapDiff">
                <span class="add" :class="{ z: !snapDiff.added }">+ {{ snapDiff.added || 0 }} 新增</span>
                <span class="mod" :class="{ z: !snapDiff.changed }">~ {{ snapDiff.changed || 0 }} 修改</span>
                <span class="del" :class="{ z: !snapDiff.removed }">− {{ snapDiff.removed || 0 }} 删除</span>
                <span class="tot">共 {{ (snapDiff.added || 0) + (snapDiff.changed || 0) + (snapDiff.removed || 0) }} 个文件变动</span>
              </div>
              <div class="sd-diff" v-else>
                <span class="tot">{{ snapDiffLoading ? "正在对比…" : "这是最早的一条快照，没有可对比的上一条" }}</span>
              </div>
            </div>

            <!-- 备份内容：桌面端逐层进入的文件表格 + 搜索 + 底部操作栏 -->
            <div class="sd-panel sd-files" v-if="!isNarrow">
              <div class="sd-ph">
                <div class="fpath" v-if="!sdq.q.trim()">
                  <a :class="{ cur: !sd.cur }" @click="sd.cur = ''">快照根目录</a>
                  <template v-for="(c, i) in sdCrumb" :key="c.path">
                    <span class="sl">/</span>
                    <a :class="{ cur: i === sdCrumb.length - 1 }" @click="sdEnter(c.path)">{{ c.label }}</a>
                  </template>
                </div>
                <div class="fpath" v-else>
                  <b>搜索「{{ sdq.q.trim() }}」</b>
                  <span class="cnt">{{ sdq.loading ? "搜索中…" : sdq.done ? sdq.items.length + (sdq.items.length >= 300 ? "+" : "") + " 个文件" : "" }}</span>
                </div>
                <label class="psearch">
                  <Icon name="search" :size="14" />
                  <input v-model="sdq.q" placeholder="在快照中搜索文件名" />
                  <i v-if="sdq.q" @click="sdq.q = ''">✕</i>
                </label>
              </div>

              <div class="sd-build" v-if="sd.building">
                <i class="mini-spin"></i>
                <div>
                  <b>正在读取这份快照的完整目录清单</b>
                  <span>已解析 {{ sd.parsed.toLocaleString() }} 条 · {{ sd.elapsed }} 秒。大快照首次约需 1 分钟，读完会缓存，之后秒开。</span>
                </div>
              </div>
              <div class="sd-build err" v-if="sd.err"><b>读取失败</b><span>{{ sd.err }}</span></div>

              <!-- 目录浏览 -->
              <table class="ftbl" v-if="!sdq.q.trim()">
                <thead>
                  <tr>
                    <th class="c"><span class="cbx" :class="{ on: sdCurAll, half: sdCurPart }" title="选中当前目录全部" @click="sdToggleCur">✓</span></th>
                    <th>名称</th>
                    <th class="w">大小 / 项数</th>
                    <th class="g"></th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="sd.cur" @click="sdUp()">
                    <td></td>
                    <td colspan="3"><span class="fn back"><Icon name="arrow-back-up" :size="14" /> 返回上一级</span></td>
                  </tr>
                  <tr v-for="n in sdItems" :key="n.path" :class="{ on: sdPicked(n.path) }" @click="n.is_dir ? sdEnter(n.path) : sdToggle(n.path)">
                    <td class="c"><span class="cbx" :class="{ on: sdPicked(n.path), half: sdHalf(n.path) }" @click.stop="sdToggle(n.path)">✓</span></td>
                    <td>
                      <span class="fn">
                        <Icon v-if="n.is_dir" name="folder" :size="20" color="var(--ic)" />
                        <Icon v-else name="file" :size="20" color="var(--ic)" />
                        <span class="nm" :class="{ sel: sdPicked(n.path) }">{{ n.name }}</span>
                      </span>
                    </td>
                    <td class="w dim">
                      <template v-if="!n.is_dir">{{ fmtBytes(n.size) }}</template>
                      <template v-else-if="n.loading">读取中…</template>
                      <template v-else-if="n.loaded">{{ n.truncated ? "共 " + n.total : n.children.length }} 项</template>
                      <template v-else>—</template>
                    </td>
                    <td class="g"><span v-if="n.is_dir">›</span></td>
                  </tr>
                  <tr v-if="sdCurNode && sdCurNode.loading && !sdItems.length" class="emp"><td colspan="4"><i class="mini-spin"></i> 正在读取目录…</td></tr>
                  <tr v-else-if="!sdItems.length && !sd.building && !sd.err" class="emp"><td colspan="4">{{ sd.cur ? "这个目录是空的" : "这个快照里没有可浏览的内容" }}</td></tr>
                </tbody>
              </table>
              <div class="sdc-more" v-if="!sdq.q.trim() && sdCurNode && sdCurNode.truncated">
                该目录共 {{ sdCurNode.total }} 项，只显示前 300 项。需要找具体文件时，用右上角搜索。
              </div>

              <!-- 搜索结果 -->
              <table class="ftbl" v-if="sdq.q.trim()">
                <thead><tr><th class="c"></th><th>名称</th><th>所在目录</th><th class="w">大小</th></tr></thead>
                <tbody>
                  <tr v-for="it in sdq.items" :key="it.path" :class="{ on: sdPicked(it.path) }" @click="sdToggle(it.path)">
                    <td class="c"><span class="cbx" :class="{ on: sdPicked(it.path) }" @click.stop="sdToggle(it.path)">✓</span></td>
                    <td>
                      <span class="fn">
                        <Icon name="file" :size="17" color="var(--ic)" />
                        <span class="nm" :class="{ sel: sdPicked(it.path) }">{{ it.name }}</span>
                      </span>
                    </td>
                    <td class="mono dim ellip" :title="it.parent">{{ it.parent }}</td>
                    <td class="w dim">{{ fmtBytes(it.size) }}</td>
                  </tr>
                  <tr v-if="sdq.done && !sdq.items.length" class="emp"><td colspan="4">没有找到匹配的文件</td></tr>
                </tbody>
              </table>

              <div class="sd-selbar">
                <template v-if="sd.sel.length">
                  <span class="cnt-t">已选 <b>{{ sd.sel.length }}</b> 项</span>
                  <div class="sel-chips">
                    <span v-for="p in sd.sel.slice(0, 6)" :key="p" :title="p">{{ sdBaseName(p) }}<i @click="sdToggle(p)">✕</i></span>
                    <span v-if="sd.sel.length > 6" class="more">+{{ sd.sel.length - 6 }}</span>
                  </div>
                  <a class="clr" @click="sd.sel = []">清空</a>
                </template>
                <span v-else class="dim">勾选要还原的文件或目录；不勾选则还原整份快照</span>
                <span class="sp"></span>
                <button class="btn primary" :disabled="!snapCur || !snapCur.task_id" @click="sdRestore">{{ sd.sel.length ? "还原所选（" + sd.sel.length + " 项）" : "还原整份快照" }}</button>
              </div>
            </div>

            <!-- 备份内容：手机端逐层下钻（方案 C） -->
            <div class="sd-panel sd-drill" v-else>
              <div class="sd-ph">
                备份内容
                <span class="cnt" v-if="sd.sel.length">已选 {{ sd.sel.length }} 项</span>
              </div>

              <div class="sd-build" v-if="sd.building">
                <i class="mini-spin"></i>
                <div>
                  <b>正在读取目录清单</b>
                  <span>已解析 {{ sd.parsed.toLocaleString() }} 条 · {{ sd.elapsed }} 秒，读完会缓存。</span>
                </div>
              </div>
              <div class="sd-build err" v-if="sd.err"><b>读取失败</b><span>{{ sd.err }}</span></div>

              <div class="sdc-crumb">
                <span class="sdc-crumb-p">
                  <a @click="sd.cur = ''">快照根</a>
                  <template v-for="(c, i) in sdCrumb" :key="i">
                    <span class="sl">/</span>
                    <a :class="{ cur: i === sdCrumb.length - 1 }" @click="sd.cur = c.path">{{ c.label }}</a>
                  </template>
                </span>
                <span class="cbx" :class="{ on: sdCurAll, half: sdCurPart }" @click.stop="sdToggleCur">✓</span>
              </div>

              <div class="sdc-list">
                <div class="sdc-row" v-if="sd.cur" @click="sdUp()">
                  <span class="sdc-back"><Icon name="arrow-back-up" :size="16" /></span>
                  <span class="sdc-name" style="color:#6b7280">返回上一级</span>
                </div>
                <div class="sdc-row" v-for="n in sdItems" :key="n.path" :class="{ on: sdPicked(n.path) }" @click="n.is_dir ? sdEnter(n.path) : sdToggle(n.path)">
                  <Icon class="sdt-ic" v-if="n.is_dir" name="folder" :size="20" color="var(--ic)" />
                  <Icon class="sdt-ic" v-else name="file" :size="19" color="var(--ic)" />
                  <span class="sdc-name" :class="{ sel: sdPicked(n.path) }">{{ n.name }}</span>
                  <span class="sdc-size" v-if="!n.is_dir">{{ fmtBytes(n.size) }}</span>
                  <span class="sdc-go" v-if="n.is_dir">›</span>
                  <span class="cbx" :class="{ on: sdPicked(n.path), half: sdHalf(n.path) }" @click.stop="sdToggle(n.path)">✓</span>
                </div>
                <div class="sdc-more" v-if="sdCurNode && sdCurNode.truncated">
                  该目录共 {{ sdCurNode.total }} 项，只显示前 300 项。需要精确挑单个文件时，用还原向导里的文件选择器。
                </div>
                <div class="sn-empty" v-if="!sdItems.length && !sd.building && !sd.err">
                  {{ sd.cur ? "这个目录是空的" : "这个快照里没有可浏览的内容" }}
                </div>
              </div>
            </div>

            <div class="sd-panel" v-if="snapCur && (snapCur.tags || []).length">
              <div class="sd-ph">标签</div>
              <div class="sd-tags">
                <span class="sd-tag mono" v-for="t in snapCur.tags" :key="t">{{ t }}</span>
              </div>
            </div>
          </template>

          <!-- ---------- 视图 3：还原配置 ---------- -->
          <template v-else>
            <div class="sn-rst">
            <div class="sd-bar">
              <button class="sd-back" @click="snapView = 'detail'">‹ 返回快照</button>
              <span class="sd-sep">/</span>
              <b class="sd-crumb">还原 {{ rst.cur ? fmtSnapTime(rst.cur.time) : "" }}</b>
            </div>

            <div class="rst-stepper">
              <template v-for="(st, i) in rstVisibleSteps" :key="st.id">
                <div class="rst-step-line" v-if="i > 0" :class="{ done: i + rstOffset <= rstStep }"></div>
                <div class="rst-step-dot" :class="{ on: i + rstOffset === rstStep, done: i + rstOffset < rstStep }" @click="rstGoto(i + rstOffset)">
                  <span class="n">{{ i + rstOffset < rstStep ? "✓" : i + 1 }}</span>
                  <span class="lbw">
                    <span class="lb">{{ st.label }}</span>
                    <small class="sm" v-if="i + rstOffset < rstStep && rstStepSummary(st.id)" :title="rstStepSummary(st.id)">{{ rstStepSummary(st.id) }}</small>
                  </span>
                </div>
              </template>
            </div>

            <div class="rst-step-title" v-if="rstSteps[rstStep]">
              {{ rstSteps[rstStep].title }}<em>{{ rstSteps[rstStep].hint }}</em>
            </div>

            <div class="sd-panel pad">
              <!-- 第 0 步：选任务 -->
              <div v-if="rstStep === 0">
                <div class="rpick-list" v-if="cfg.tasks.length">
                  <button class="rpick-item" v-for="t in cfg.tasks" :key="t.id" :class="{ on: rst.task && rst.task.id === t.id }" @click="selectTask(t)">
                    <span class="rp-ic" :class="'c-' + taskTagCls(t)">
                      <Icon name="folder" :size="26" />
                    </span>
                    <span class="rp-txt">
                      <b>{{ t.name }}</b>
                      <small>{{ repoName(t.repo_id) }} · {{ t.last_done_at ? "上次备份 " + fmtShort(t.last_done_at) : "从未备份" }}</small>
                    </span>
                    <Icon name="chevron-right" :size="16" class="rp-arrow" />
                  </button>
                </div>
                <div class="empty" v-else>
                  <p>还没有备份任务</p>
                  <p class="hint">先在「备份」页创建备份任务并完成一次备份，产生快照后才能还原</p>
                </div>
              </div>

              <!-- 第 1 步：选快照 -->
              <div v-else-if="rstStep === 1">
                <div class="rstep-tools">
                  <em class="fld-hint">徽章 = 与上一份快照的差异</em>
                  <em class="snap-cache" v-if="rst.cachedAt" title="本地缓存快照列表，避免每次实时查云盘">缓存于 {{ rst.cachedAt.slice(5) }}</em>
                  <button type="button" class="snap-refresh" title="强制实时查询云盘并更新缓存" @click="loadRestoreSnaps(rst.task, true)" :disabled="rst.snapsLoading">
                    <Icon name="chevron-right" :size="13" />
                    刷新缓存
                  </button>
                </div>
                <div class="snap-timeline" v-if="rst.snaps.length">
                  <div class="snap-row" v-for="s in rst.snaps" :key="s.id" :class="{ on: rst.cur && rst.cur.id === s.id }" @click="pickSnap(s)">
                    <i class="snap-dot"></i>
                    <span class="snap-t">{{ snapTime(s) }}</span>
                    <span class="snap-id mono">{{ (s.short_id || s.id).slice(0, 8) }}</span>
                    <span class="snap-files" v-if="s.summary && (s.summary.total_files_processed || s.summary.total_files)">{{ s.summary.total_files_processed || s.summary.total_files }} 个文件 · {{ fmtBytes(s.summary.total_bytes_processed || s.summary.total_bytes) }}</span>
                    <span class="snap-badge" :class="snapBadge(s.id).cls">{{ snapBadge(s.id).text }}</span>
                  </div>
                </div>
                <div class="snap-loading" v-else-if="rst.snapsLoading"><i class="mini-spin"></i> 正在读取快照列表…</div>
                <div class="snap-loading" v-else>暂无快照，请先运行一次备份</div>
              </div>

              <!-- 第 2 步：选文件 -->
              <div v-else-if="rstStep === 2">
                <button type="button" class="pick-files-btn" @click="openRstPick">
                  <Icon name="folder" :size="17" />
                  {{ rst.includes.length ? "已选 " + rst.includes.length + " 项 · 点击修改" : "浏览快照文件…" }}
                </button>
                <div class="inc-tray" v-if="rst.includes.length">
                  <span class="t" v-for="x in rst.includes" :key="x.path"><b><Icon :name="x.is_dir ? 'folder' : 'file'" :size="13" /> {{ x.name }}</b><i @click="removeInclude(x)">×</i></span>
                </div>
                <div class="pick-hint" v-else>
                  不选则还原整份快照<template v-if="rst.cur && (rst.cur.paths || []).length">，即 {{ rst.cur.paths.join("、") }}</template>
                </div>
              </div>

              <!-- 第 3 步：目的地 -->
              <div v-else-if="rstStep === 3">
                <div class="dest-opts">
                  <button type="button" class="dest-opt" :class="{ on: rst.dest === 'original' }" @click="rst.dest = 'original'">
                    <span class="ic c-blue"><Icon name="map-pin" :size="24" /></span>
                    <span class="tx"><b>原位置</b><small>写回备份时的原始路径</small></span>
                    <span class="rd"></span>
                  </button>
                  <button type="button" class="dest-opt" :class="{ on: rst.dest === 'custom' }" @click="rst.dest = 'custom'">
                    <span class="ic c-green"><Icon name="folder" :size="24" /></span>
                    <span class="tx"><b>新路径</b><small>还原到指定目录，更安全，不影响现有文件</small></span>
                    <span class="rd"></span>
                  </button>
                </div>
                <div class="dest-note" v-if="rst.dest === 'original'">
                  将写回原路径：{{ rst.includes.map((x) => x.path).join("；") }}
                </div>
                <div class="target-row" v-else>
                  <input v-model="rst.target" placeholder="/vol1/1000/还原目录（将自动创建）" />
                  <button type="button" class="btn ghost" @click="openTargetBrowser">浏览</button>
                </div>
              </div>

              <!-- 第 4 步：冲突策略 -->
              <div v-else-if="rstStep === 4">
                <div class="conf-grid">
                  <button type="button" class="conf-card" :class="{ on: rst.conflict === 'always' }" @click="rst.conflict = 'always'">
                    <span class="conf-rd"></span>
                    <span class="conf-tx"><b>全部覆盖</b><small>直接覆盖本地同名文件</small></span>
                  </button>
                  <button type="button" class="conf-card" :class="{ on: rst.conflict === 'if-changed' }" @click="rst.conflict = 'if-changed'">
                    <span class="conf-rd"></span>
                    <span class="conf-tx"><b>仅覆盖有变化</b><small>内容没变的文件不重写</small></span>
                  </button>
                  <button type="button" class="conf-card" :class="{ on: rst.conflict === 'never' }" @click="rst.conflict = 'never'">
                    <span class="conf-rd"></span>
                    <span class="conf-tx"><b>跳过已存在</b><small>已存在的文件保持不动</small></span>
                  </button>
                  <button type="button" v-if="rst.dest === 'custom'" class="conf-card" :class="{ on: rst.conflict === 'rename' }" @click="rst.conflict = 'rename'">
                    <span class="conf-rd"></span>
                    <span class="conf-tx"><b>保留两者</b><small>还原版本加后缀另存</small></span>
                  </button>
                </div>
              </div>
            </div>

            <div class="rst-flow" v-if="rstStep === 4 && rst.cur && rst.includes.length">
              <span class="rst-flow-node"><Icon name="folder" :size="14" /> 快照 {{ (rst.cur.short_id || rst.cur.id).slice(0, 8) }}</span>
              <i class="rst-flow-lk">──▶</i>
              <span class="rst-flow-node hi">{{ rst.dest === "original" ? "⌖ 原位置" : "➝ " + (rst.target || "新路径") }}</span>
            </div>

            <div class="sd-foot">
              <button class="btn ghost hide-m" @click="rstCancel">取消</button>
              <div class="sp"></div>
              <button class="btn ghost" :disabled="!rstCanPrev" @click="rstPrev">上一步</button>
              <button v-if="rstStep < 4" class="btn primary" :disabled="!rstStepReady(rstStep)" @click="rstNext">下一步</button>
              <button v-else class="btn primary" :disabled="!rstReady || rst.busy" @click="doRestore">{{ rst.busy ? "启动中…" : "开始还原" }}</button>
            </div>
            </div>
          </template>
        </template>

        <!-- ==================== 历史：备份 + 还原 ==================== -->
        <template v-else>
          <div class="dhead">
            <h1>历史</h1>
            <div class="dhead-acts">
              <button class="btn ghost" :disabled="hist.loading" @click="loadHistory(true)">
                {{ hist.loading ? "读取中…" : "刷新" }}
              </button>
            </div>
          </div>

          <div class="ov-hero" v-if="hist.loaded" :class="'tone-' + histHero.tone">
            <div class="ov-ring">
              <svg viewBox="0 0 96 96" class="ov-ring-svg">
                <circle cx="48" cy="48" r="42" fill="none" class="ov-ring-bg" stroke-width="8"/>
                <circle cx="48" cy="48" r="42" fill="none" class="ov-ring-fg" stroke-width="8" stroke-linecap="round"
                  stroke-dasharray="264" :stroke-dashoffset="264 * (1 - histHero.ratio)"/>
              </svg>
              <div class="ov-ring-c">
                <Icon :name="histHero.tone === 'warn' ? 'alert-circle' : 'shield-check'" :size="32" />
              </div>
            </div>
            <div class="ov-txt">
              <h2>{{ histHero.title }}</h2>
              <p>{{ histHero.sub }}</p>
            </div>
            <div class="ov-kpis">
              <div class="ov-kpi"><span>近 30 天备份</span><b>{{ bak30.length }}<small>次</small></b></div>
              <div class="ov-kpi"><span>成功率</span><b :class="{ ok: bak30Rate != null && bak30Rate >= 90 }">{{ fmtRate(bak30Rate) }}<small v-if="bak30Rate != null">%</small></b></div>
              <div class="ov-kpi"><span>失败</span><b>{{ bak30.filter((x) => x.status === "failed").length }}<small>次</small></b></div>
              <div class="ov-kpi"><span>近 30 天还原</span><b>{{ rst30.length }}<small>次</small></b></div>
            </div>
          </div>
          <div class="hist-bar">
            <div class="hist-seg">
              <button :class="{ on: hist.tab === 'all' }" @click="hist.tab = 'all'">
                全部<span class="n" v-if="hist.items.length">{{ hist.items.length }}</span>
              </button>
              <button :class="{ on: hist.tab === 'bak' }" @click="hist.tab = 'bak'">
                备份<span class="n" v-if="histBackups.length">{{ histBackups.length }}</span>
              </button>
              <button :class="{ on: hist.tab === 'rst' }" @click="hist.tab = 'rst'">
                还原<span class="n" v-if="histRestores.length">{{ histRestores.length }}</span>
              </button>
            </div>
            <label class="hist-fail" :class="{ on: hist.onlyFail }">
              <input type="checkbox" v-model="hist.onlyFail" /> 仅看失败
            </label>
          </div>

          <div class="hist-wrap" :class="{ narrow: isNarrow }">
            <div class="hist-list">
              <div class="hist-loading" v-if="hist.loading && !hist.items.length"><i class="mini-spin"></i> 正在读取历史…</div>

              <template v-for="g in histGroups" :key="g.key">
                <div class="hist-day">{{ g.label }}<i></i></div>
                <div
                  class="hist-item"
                  v-for="it in g.items"
                  :key="it.kind + it.id"
                  :class="{ on: histSel(it) }"
                  @click="pickHist(it)"
                >
                  <div class="hi-row">
                    <span class="hi-ic" :class="'k-' + histStatusCls(it.status)">
                      <Icon v-if="it.kind === 'backup'" name="folder" :size="22" />
                      <Icon v-else name="arrow-back-up" :size="22" />
                    </span>
                    <div class="hi-main">
                      <div class="hi-top">
                        <span class="hi-st" :class="histStatusCls(it.status)">{{ histStatusTxt(it) }}</span>
                        <span class="hi-nm">{{ histTitle(it) }}</span>
                        <span class="hi-tm mono">{{ it.kind === "backup" ? "备份" : "还原" }} · {{ hhmm(it.time) }}</span>
                      </div>
                      <div class="hi-sub" v-if="it.one_line">{{ it.one_line }}</div>
                    </div>
                  </div>

                  <!-- 手机：点开就地展开 -->
                  <div class="hi-open" v-if="isNarrow && histSel(it)">
                    <div class="hi-kv" v-for="r in histKVs(it, true)" :key="r.k">
                      <span class="k">{{ r.k }}</span><span class="v mono">{{ r.v }}</span>
                    </div>
                    <div class="hi-tl" v-if="it.nodes && it.nodes.length">
                      <div class="n" v-for="(nd, i) in it.nodes" :key="i"><i class="mono">{{ nd.time }}</i>{{ nd.msg }}</div>
                    </div>
                    <div class="hi-ops">
                      <button class="btn ghost sm" v-if="it.kind === 'backup'" @click.stop="openHistLog(it)">查看完整日志</button>
                      <button class="btn ghost sm danger" v-else-if="it.status === 'running'" @click.stop="stopRestore(it)">停止还原</button>
                      <button class="btn primary sm" v-if="it.kind === 'restore' && it.status === 'failed'" @click.stop="retryRestore(it)">重新还原</button>
                    </div>
                  </div>
                </div>
              </template>

              <div class="sn-empty" v-if="!hist.loading && !histGroups.length">
                <p>{{ hist.onlyFail ? "没有失败记录" : hist.tab === "bak" ? "还没有备份记录" : hist.tab === "rst" ? "还没有还原记录" : "还没有任何记录" }}</p>
                <p class="hint" v-if="!hist.onlyFail">{{ hist.tab === "rst" ? "在「快照」里点开任意快照，就能发起还原" : "跑一次备份，这里就会出现记录" }}</p>
              </div>
            </div>

            <!-- 桌面：右侧详情 -->
            <div class="hist-det" v-if="!isNarrow">
              <template v-if="hist.cur">
                <div class="hd-tags">
                  <span class="hi-st" :class="histStatusCls(hist.cur.status)">{{ histStatusTxt(hist.cur) }}</span>
                  <span class="hi-st gray">{{ hist.cur.kind === "backup" ? "备份" : "还原" }}</span>
                </div>
                <div class="hd-top"><b>{{ histTitle(hist.cur) }}</b></div>
                <div class="hd-time">{{ hist.cur.time || "—" }}<template v-if="hist.cur.one_line"> · {{ hist.cur.one_line }}</template></div>
                <div class="hd-prog" v-if="histProgress(hist.cur) != null">
                  <div class="bar"><div class="bar-in" :style="{ width: histProgress(hist.cur) + '%' }"></div></div>
                  <b>{{ histProgress(hist.cur).toFixed(0) }}%</b>
                </div>
                <div class="hi-kv" v-for="r in histKVs(hist.cur)" :key="r.k">
                  <span class="k">{{ r.k }}</span><span class="v mono">{{ r.v }}</span>
                </div>
                <div class="hi-tl" v-if="hist.cur.nodes && hist.cur.nodes.length">
                  <div class="n" v-for="(nd, i) in hist.cur.nodes" :key="i"><i class="mono">{{ nd.time }}</i>{{ nd.msg }}</div>
                </div>
                <div class="hd-foot">
                  <button class="btn ghost" v-if="hist.cur.kind === 'backup'" @click="openHistLog(hist.cur)">查看完整日志</button>
                  <button class="btn ghost danger" v-else-if="hist.cur.status === 'running'" @click="stopRestore(hist.cur)">停止还原</button>
                  <button class="btn primary" v-if="hist.cur.kind === 'restore' && hist.cur.status === 'failed'" @click="retryRestore(hist.cur)">重新还原</button>
                </div>
              </template>
              <div class="sn-empty" v-else><p>选一条记录查看详情</p></div>
            </div>
          </div>
        </template>
      </main>
    </div>

    <!-- ======================== 任务弹窗（方案B：单页 + 内联展开） ======================== -->
    <div class="mask" v-if="wiz.show">
      <div class="modal tall">
        <div class="modal-head sheet-head">
          <h3>{{ wiz.editing ? "编辑任务" : wiz.copy ? "复制任务" : "新建备份任务" }}</h3>
          <button class="x" @click="closeWizard">✕</button>
        </div>
        <div class="modal-body single">

          <label class="fld">
            <span>任务名称</span>
            <input v-model="wiz.form.name" placeholder="例如：相册资料 → 天翼" />
          </label>

          <!-- 备份来源地 -->
          <div class="sec">
            <div class="sec-head">
              <b><i class="sec-n">1</i>备份来源地</b>
              <button class="link-btn" @click="openSrcEdit">设置</button>
            </div>
            <div class="sec-card">
              <div class="src-row">
                <div class="src-ico"><Icon name="device-desktop" :size="20" /></div>
                <div><b>fnos</b><span class="sec-sub">本地设备</span></div>
              </div>
              <div class="src-paths" v-if="wiz.form.paths.length">
                <div class="src-path" v-for="(p, i) in wiz.form.paths" :key="p">
                  <span><Icon name="folder" :size="14" color="var(--ic)" /> {{ shortPath(p) }}</span>
                  <b class="rm" @click="removePath(i)">✕</b>
                </div>
              </div>
              <div class="sec-sub pad" v-else>还未选择目录，点右上角「设置」开始选择</div>
            </div>
          </div>

          <!-- 备份目的地：两步式（选云端 → 选/新建目录） -->
          <div class="sec">
            <div class="sec-head"><b><i class="sec-n">2</i>备份目的地</b><button type="button" class="link-btn" @click="openRepo(null)">新增云端</button></div>
            <div class="sec-card dest-card">
              <div class="dest-grid">
              <div class="fld">
                <span>① 选择云端</span>
                <button type="button" class="cloud-pick-btn" @click="openCloudPick">
                  <template v-if="destRepo">
                    <img v-if="cloudLogo(repoBrand(destRepo))" class="cpb-logo" :src="cloudLogo(repoBrand(destRepo))" :alt="cloudLabel(repoBrand(destRepo))" />
                    <Icon v-else :name="cloudIcon(repoBrand(destRepo))" :size="20" />
                    <span class="cpb-txt">
                      <b>{{ destRepo.name }}</b>
                      <small>{{ destRepo.cloud === "webdav" ? (destRepo.dav_user ? destRepo.dav_user + "@" : "") + (destRepo.dav_url || "WebDAV").replace(/^https?:\/\//, "") : (destRepo.uri || "").replace(/^rclone:/, "") }}</small>
                    </span>
                  </template>
                  <span v-else class="ph">选择云端仓库</span>
                  <i class="chev-r">›</i>
                </button>
              </div>
              <div class="fld">
                <span>② 选择目标目录</span>
                <button type="button" class="dir-pick-btn" :disabled="!wiz.form.dest_cloud" @click="openDestBrowser">
                  <span :class="{ ph: !wiz.form.dest_subdir }">{{ wiz.form.dest_subdir || "点此浏览云盘，选择或新建目录" }}</span>
                  <i class="chev-r">›</i>
                </button>
              </div>
              </div>
              <div class="fld">
                <span>③ 加密密码</span>
                <div class="pwrow" v-if="!wiz.editing && !wiz.copy">
                  <input :type="wiz.form.showPass ? 'text' : 'password'" v-model="wiz.form.password" autocomplete="new-password" placeholder="自动生成，也可手动填写" />
                  <button type="button" class="eye" @click="wiz.form.showPass = !wiz.form.showPass">
                    <Icon v-if="wiz.form.showPass" name="eye-off" :size="16" />
                    <Icon v-else name="eye" :size="16" />
                  </button>
                  <button type="button" class="btn ghost sm" @click="genWizPw">生成</button>
                </div>
                <div class="pwrow" v-else>
                  <input type="password" value="••••••••" disabled readonly />
                  <button type="button" class="btn ghost sm" @click="copyRepoPw">
                    <Icon name="copy" :size="14" />
                    复制
                  </button>
                </div>
                <div class="hint-box" v-if="!wiz.editing && !wiz.copy">仓库密码只能设置一次，保存后不可修改，请妥善保管；忘记密码将无法恢复数据。</div>
                <div class="pw-note" v-else-if="wiz.copy">目的地与原任务相同，沿用该仓库已有的密码；改到新目录时可重新设置。</div>
              </div>
              <div class="flow-line" v-if="destReady && wiz.form.paths.length">
                <span class="node">fnos 本地</span><i class="lk">──▶</i>
                <span class="node hi">{{ wiz.form.dest_subdir }}</span>
              </div>
            </div>
          </div>

          <!-- 备份设置 -->
          <div class="sec">
            <div class="sec-head"><b><i class="sec-n">3</i>备份设置</b></div>
            <div class="sec-card">

              <!-- 备份规则（预设开关） -->
              <div class="opt-row" :class="{ open: wiz.openRule }" @click="wiz.openRule = !wiz.openRule">
                <span>备份规则</span>
                <em>{{ wizExcludeCount ? "排除 " + wizExcludeCount + " 条" : "不排除" }}</em>
                <i class="arr" :class="{ up: wiz.openRule }"></i>
              </div>
              <div class="opt-body" v-if="wiz.openRule">
                <div class="chips2">
                  <button v-for="(p, k) in excludePresets" :key="k" :class="{ on: presetOn(k) }" @click="togglePreset(k)">{{ p.label }}</button>
                </div>
                <div class="rule-detail" v-if="wizPresetDetail.length">
                  <div class="rt">已选预设包含：</div>
                  <code v-for="p in wizPresetDetail" :key="p">{{ p }}</code>
                </div>
                <div class="rule-prev">本次将排除 <b>{{ wizExcludeCount }}</b> 条规则{{ wizPresetOnList.length ? "（预设：" + wizPresetOnList.map((k) => excludePresets[k].label).join("＋") + "）" : "" }}</div>
                <div class="adv-row" @click="wiz.openAdv = !wiz.openAdv">
                  <span>高级：手动编辑规则（每行一条）</span>
                  <i class="arr" :class="{ up: wiz.openAdv }"></i>
                </div>
                <label class="fld slim" v-if="wiz.openAdv">
                  <textarea v-model="wiz.form.excludes" rows="4" placeholder="**/*.tmp&#10;缓存/"></textarea>
                </label>
              </div>

              <!-- 任务执行计划 -->
              <div class="opt-row" :class="{ open: wiz.openPlan }" @click="wiz.openPlan = !wiz.openPlan">
                <span>任务执行计划</span>
                <em>{{ schedText(wiz.form) }}</em>
                <i class="arr" :class="{ up: wiz.openPlan }"></i>
              </div>
              <div class="opt-body" v-if="wiz.openPlan">
                <div class="sched-seg">
                  <button v-for="m in schedModes" :key="m.v" :class="{ on: wiz.form.sched_mode === m.v }" @click="wiz.form.sched_mode = m.v">{{ m.l }}</button>
                </div>

                <!-- 按天 -->
                <div v-if="wiz.form.sched_mode === 'daily'">
                  <div class="sched-inline">每 <input type="number" v-model="wiz.form.daily_days" min="1" max="365" /> 天执行一次</div>
                  <div class="fld slim">
                    <span>执行时间</span>
                    <TPick v-model="wiz.form.daily_time" />
                  </div>
                </div>

                <!-- 按周 -->
                <div v-if="wiz.form.sched_mode === 'weekly'">
                  <div class="cfg-label">重复（可多选）</div>
                  <div class="chips2 sched-chips">
                    <button v-for="d in [1, 2, 3, 4, 5, 6, 0]" :key="d" :class="{ on: wiz.form.weekly_days.includes(d) }" @click="toggleWDay(d)">周{{ WD_SHORT[d] }}</button>
                  </div>
                  <div class="fld slim">
                    <span>执行时间</span>
                    <TPick v-model="wiz.form.daily_time" />
                  </div>
                </div>

                <!-- 按月 -->
                <div v-if="wiz.form.sched_mode === 'monthly'">
                  <div class="cfg-label">执行日期（可多选）</div>
                  <div class="chips2 sched-chips">
                    <button v-for="d in monthDayList" :key="d" :class="{ on: wiz.form.monthly_days.includes(d) }" @click="toggleMDay(d)">{{ d === -1 ? "末天" : d }}</button>
                  </div>
                  <div class="fld slim">
                    <span>执行时间</span>
                    <TPick v-model="wiz.form.daily_time" />
                  </div>
                </div>

                <!-- 固定间隔 -->
                <div v-if="wiz.form.sched_mode === 'interval'">
                  <label class="fld slim">
                    <span>间隔（小时）</span>
                    <input type="number" v-model="wiz.form.interval_hrs" min="1" />
                  </label>
                </div>

                <label class="fld row slim">
                  <input type="checkbox" v-model="wiz.form.enabled" />
                  <span>启用任务（停用后计划调度不执行）</span>
                </label>
              </div>

              <!-- 保留快照数 -->
              <div class="opt-row last" :class="{ open: wiz.openKeep }" @click="wiz.openKeep = !wiz.openKeep">
                <span>保留快照数</span>
                <em>{{ wiz.form.keep_last > 0 ? "最近 " + wiz.form.keep_last + " 份" : "永久保留" }}</em>
                <i class="arr" :class="{ up: wiz.openKeep }"></i>
              </div>
              <div class="opt-body" v-if="wiz.openKeep">
                <div class="keep-chips">
                  <button :class="{ on: wiz.form.keep_last === 0 }" @click="wiz.form.keep_last = 0">永久</button>
                  <button :class="{ on: wiz.form.keep_last === 2 }" @click="wiz.form.keep_last = 2">2 份</button>
                  <button :class="{ on: wiz.form.keep_last === 3 }" @click="wiz.form.keep_last = 3">3 份</button>
                  <button :class="{ on: wiz.form.keep_last === 5 }" @click="wiz.form.keep_last = 5">5 份</button>
                </div>
                <label class="fld slim">
                  <span>自定义数量（0 = 永久保留）</span>
                  <input type="number" v-model="wiz.form.keep_last" min="0" />
                </label>
              </div>

            </div>
          </div>

          <div class="hint-box">首次备份为全量上传，速度取决于云盘限速；后续增量备份只上传变化部分。</div>
        </div>

        <div class="modal-foot wiz-foot">
          <button class="btn ghost hide-m" @click="closeWizard" :disabled="wiz.saving">取消</button>
          <template v-if="wiz.editing">
            <button class="btn primary" @click="wizSave()" :disabled="!wizReady || wiz.saving">{{ wiz.saving ? "保存中…" : "保存" }}</button>
          </template>
          <template v-else>
            <button class="btn ghost" @click="wizSave()" :disabled="!wizReady || wiz.saving">{{ wiz.saving ? "保存中…" : "创建任务" }}</button>
            <button class="btn primary" @click="wizSave(true)" :disabled="!wizReady || wiz.saving">创建并立即备份</button>
          </template>
        </div>
      </div>
    </div>

    <!-- ======================== 仓库弹窗 ======================== -->
    <div class="mask" v-if="repoModal.show">
      <div class="modal">
        <div class="modal-head">
          <h3>云盘连接</h3>
          <button class="x" @click="repoModal.show = false">✕</button>
        </div>
        <div class="modal-body">
          <label class="fld">
            <span>连接名称</span>
            <input v-model="repoModal.form.name" placeholder="例如：我的 115 网盘" />
          </label>
          <template v-if="repoModal.form.cloud === 'webdav'">
            <label class="fld">
              <span>服务器地址</span>
              <input v-model="repoModal.form.dav_url" @input="davTest.msg = ''" placeholder="https://dav.example.com:5006/dav（需以 http:// 或 https:// 开头）" />
            </label>
            <label class="fld">
              <span>账号</span>
              <input v-model="repoModal.form.dav_user" @input="davTest.msg = ''" placeholder="WebDAV 用户名" />
            </label>
            <label class="fld">
              <span>密码{{ repoModal.form.id ? "（留空保持不变）" : "" }}</span>
              <div class="pwrow">
                <input :type="repoModal.form.showPass ? 'text' : 'password'" v-model="repoModal.form.dav_pass" @input="davTest.msg = ''" autocomplete="new-password" placeholder="WebDAV 密码" />
                <button type="button" class="eye" @click="repoModal.form.showPass = !repoModal.form.showPass">
                  <Icon v-if="repoModal.form.showPass" name="eye-off" :size="16" />
                  <Icon v-else name="eye" :size="16" />
                </button>
              </div>
            </label>
            <div class="fld">
              <span>连接测试</span>
              <div class="inrow">
                <button type="button" class="btn ghost" :disabled="davTest.busy || !repoModal.form.dav_url.trim()" @click="testDav">{{ davTest.busy ? "测试中…" : "测试连接" }}</button>
                <span v-if="davTest.msg" :class="davTest.ok ? 'tst-ok' : 'tst-bad'">{{ davTest.msg }}</span>
              </div>
            </div>
            <label class="fld">
              <span>服务商</span>
              <select v-model="repoModal.form.brand">
                <option v-for="o in BRAND_OPTIONS" :key="o[0]" :value="o[0]">{{ o[1] }}</option>
              </select>
              <div class="fld-hint">
                <template v-if="repoModal.form.brand === 'auto'">按地址与目录自动识别，当前识别为「{{ autoBrandHint }}」</template>
                <template v-else>已手动指定，列表里固定显示该标识</template>
              </div>
            </label>
          </template>
          <template v-else-if="repoModal.form.cloud !== 'other'">
            <div class="fld">
              <span>网盘类型</span>
              <div class="hint-box">该网盘通过 OpenList 中转连接，无需填写地址账号，直接保存即可。</div>
            </div>
          </template>
          <label class="fld" v-else>
            <span>rclone URI</span>
            <input v-model="repoModal.form.uri" class="mono" placeholder="rclone:远端名:路径" />
          </label>
        </div>
        <div v-if="repoModal.saving" class="saving-tip">
          <span class="spin"></span><span>{{ repoModal.savingText || "保存中…" }}</span>
        </div>
        <div class="modal-foot even">
          <span class="spacer"></span>
          <button class="btn ghost" @click="repoModal.show = false" :disabled="repoModal.saving">取消</button>
          <button class="btn primary" @click="saveRepo" :disabled="repoModal.saving">{{ repoModal.saving ? "保存中…" : "保存" }}</button>
        </div>
      </div>
    </div>

    <!-- ======================== 日志抽屉 ======================== -->
    <div class="mask drawer-mask" v-if="logModal.show" @click.self="closeLogs">
      <div class="modal wide log-drawer">
        <div class="modal-head">
          <h3>运行日志 — {{ logModal.task ? logModal.task.name : "" }}</h3>
          <label class="auto-lb"><input type="checkbox" v-model="logModal.auto" /> 自动刷新</label>
          <button class="x" @click="closeLogs">✕</button>
        </div>
        <div class="runs-list">
          <!-- 当前查看的日志（最新一条，默认选中） -->
          <button class="run-chip current" v-if="logModal.runs.length" @click="logModal.file = logModal.runs[0].file; loadLog()">
            <span class="run-chip-label">当前日志</span>
            <span class="run-chip-name">{{ logModal.runs[0].file.replace("_", " ") }}</span>
          </button>
          <!-- 历史运行（默认折叠，点开显示其余） -->
          <div class="runs-hist" v-if="logModal.runs.length > 1">
            <button class="hist-toggle" @click="logModal.historyOpen = !logModal.historyOpen">
              历史记录 ({{ logModal.runs.length - 1 }}) {{ logModal.historyOpen ? "▴" : "▾" }}
            </button>
            <template v-if="logModal.historyOpen">
              <button class="run-chip" :class="{ on: logModal.file === r.file }"
                v-for="r in logModal.runs.slice(1)" :key="r.file" @click="logModal.file = r.file; loadLog()">
                {{ r.file.replace("_", " ") }}
              </button>
            </template>
          </div>
          <span class="hint" v-if="!logModal.runs.length">暂无历史运行</span>
        </div>

        <div class="log-stats" v-if="logStats.length">
          <div v-for="(x, i) in logStats" :key="i"><span>{{ x.k }}</span><b>{{ x.v }}</b></div>
        </div>
        <!-- 精简结果视图 -->
        <div class="log-card" v-if="logModal.summary">
          <div class="log-head">
            <span class="lg-chip" :class="sumChipCls(logModal.summary.status)">{{ sumChipTxt(logModal.summary.status) }}</span>
            <b>{{ logModal.summary.title }}</b>
            <span class="spacer"></span>
            <b v-if="logModal.summary.status === 'running'" class="sum-pct">{{ (logModal.summary.percent || 0).toFixed(1) }}%</b>
          </div>
          <div class="log-oneline">
            <span v-if="logModal.summary.one_line">{{ logModal.summary.one_line }}</span>
            <span v-if="logModal.summary.speed" class="dim">{{ logModal.summary.speed }} · 运行中…</span>
          </div>
          <div class="log-timeline" v-if="logModal.summary.nodes && logModal.summary.nodes.length">
            <div class="lt-row" v-for="(n, i) in logModal.summary.nodes" :key="i" :class="{ warn: n.type === 'warn' }">
              <span class="lt-t">{{ n.time }}</span>
              <span class="lt-dot" :class="'d-' + n.type"></span>
              <span class="lt-msg">{{ n.msg }}</span>
            </div>
          </div>
          <details class="log-err" v-if="logModal.summary.err_detail">
            <summary>查看错误详情</summary>
            <pre>{{ logModal.summary.err_detail }}</pre>
          </details>
          <div class="log-foot">
            <span class="spacer"></span>
            <span class="raw-toggle" @click="toggleRaw">{{ logModal.raw ? "收起原始日志 ▴" : "原始日志 ▾" }}</span>
          </div>
        </div>
        <pre class="log-box mono" v-if="logModal.raw">{{ logModal.content || "（暂无内容）" }}</pre>
      </div>
    </div>

    <!-- 编辑备份来源地（仿飞牛） -->
    <div class="mask top" v-if="srcEdit.show">
      <div class="modal">
        <div class="modal-head sheet-head">
          <h3>编辑备份来源地</h3>
          <button class="x" @click="srcEdit.show = false">✕</button>
        </div>
        <div class="modal-body">
          <div class="src-sum"><span>备份来源地</span><span>共 {{ srcEdit.paths.length }} 个目录</span></div>
          <div class="sec-card">
            <div class="src-row host"><span class="host-ic"><Icon name="server" :size="22" /></span><b>fnos（本地）</b></div>
            <div class="src-path" v-for="(p, i) in srcEdit.paths" :key="p">
              <Icon class="mini-fold" name="folder" :size="24" />
              <span class="sp-name">{{ dispPath(p) }}</span>
              <b class="rm" @click="srcEdit.paths.splice(i, 1)">✕</b>
            </div>
            <button class="add-path" @click="openBrowser"><Icon name="plus" :size="14" /> 添加文件路径</button>
          </div>
        </div>
        <div class="modal-foot pair">
          <button class="btn ghost block" @click="srcEdit.show = false">取消</button>
          <button class="btn primary block" @click="confirmSrc">确定</button>
        </div>
      </div>
    </div>

    <!-- 目录选择器（仿飞牛：大图标，点行选中，› 进子级） -->
    <div class="mask top2" v-if="browser.show">
      <div class="modal tall">
          <div class="modal-head sheet-head">
            <h3>{{ !browser.path ? "选择目录（已授权 " + USER_ROOTS.length + " 个）" : (atScopeRoot() ? scopeRootName(browser.path) : crumbSegs().slice(-1)[0]) }}</h3>
            <button class="x" @click="browser.show = false">✕</button>
          </div>
          <div class="modal-body">
            <div class="dir-list">
              <!-- 已授权的根目录列表：不管授权了 1 个还是多个，都先在这里列出来 -->
              <template v-if="!browser.path">
                <div class="frow big" v-for="r in USER_ROOTS" :key="r" :class="{ dis: srcEdit.paths.includes(r) }" @click="pickPath(r)">
                  <Icon class="fold-ic" name="folder" :size="30" />
                  <span class="nm"><b>{{ scopeRootName(r) }}</b><span class="sub">{{ r }} · 整行点选，或点 › 进入子目录</span></span>
                  <i class="chev" @click.stop="browseTo(r)">›</i>
                </div>
                <div class="empty sm" v-if="!USER_ROOTS.length"><p>当前账号还没有可访问的授权目录</p></div>
              </template>
              <template v-else>
                <div class="frow big" @click="browseTo(parentPath())">
                  <span class="fold-ic back"><Icon name="arrow-back-up" :size="20" /></span>
                  <span class="nm"><b>{{ atScopeRoot() ? "返回授权目录列表" : "返回上一级" }}</b></span>
                </div>
              <div class="frow big" v-for="d in browser.dirs" :key="d.name" :class="{ dis: srcEdit.paths.includes(pathFull(d)) }" @click="pickSrc(d)">
                <Icon class="fold-ic" name="folder" :size="30" />
                <span class="nm">
                  <b>{{ d.name }}</b>
                  <span class="sub">存储空间1 {{ fmtMtime(d.mtime) }}</span>
                </span>
                <i class="chev" v-if="d.count > 0" @click.stop="browseTo(pathFull(d))">›</i>
              </div>
            </template>
            <div class="empty sm" v-if="!browser.dirs.length && !browser.loading && browser.path"><p>没有子目录</p></div>
          </div>
          <div class="hide-sys" v-if="browser.path && atScopeRoot()">
            <span class="tg" :class="{ off: browser.showAll }" @click="browser.showAll = !browser.showAll; browseTo(browser.path)"></span>
            显示系统目录（docker、@app* 等）
          </div>
        </div>
        <!-- 当前所在目录本身也能选（例如授权了「下载」，就要能直接选它，而不只是它的子目录） -->
        <div class="modal-foot" v-if="browser.path">
          <button class="btn primary block" :disabled="srcEdit.paths.includes(browser.path)" @click="pickPath(browser.path)">
            {{ srcEdit.paths.includes(browser.path) ? "已添加该目录" : "使用当前目录：" + browser.path }}
          </button>
        </div>
        <!-- 授权目录会实时同步，这里只是手动催一下，不用重启应用 -->
        <div class="modal-foot" v-else>
          <button class="btn ghost block" :disabled="reloading" @click="reloadScope">
            {{ reloading ? "正在读取…" : "刚在应用中心改过授权目录？点此立即生效" }}
          </button>
        </div>
      </div>
    </div>

    <!-- 云盘目录浏览器（仓库云端目录） -->
    <div class="mask" v-if="cloudBrowser.show">
      <div class="modal">
        <div class="modal-head sheet-head">
          <h3>选择云端目录</h3>
          <button class="x" @click="cloudBrowser.show = false">✕</button>
        </div>
        <div class="modal-body">
          <div class="crumbC">
            <span class="cloudic" @click.prevent="browseCloud('')">☁ {{ cloudBrowser.cloud }}</span>
            <template v-for="(seg, i) in cloudSegs" :key="i">
              <span class="sl">/</span>
              <a href="#" :class="{ cur: i === cloudSegs.length - 1 }" @click.prevent="browseCloud(cloudSegs.slice(0, i + 1).join('/'))">{{ seg }}</a>
            </template>
          </div>
          <div class="dir-list">
            <div class="frow big" v-if="cloudBrowser.path" @click="browseCloud(cloudParent())">
              <span class="fold-ic back"><Icon name="arrow-back-up" :size="20" /></span>
              <span class="nm"><b>返回上一级</b></span>
            </div>
            <div class="frow big" v-for="d in cloudBrowser.dirs" :key="d" @click="pickCloudDir(d)">
              <Icon class="fold-ic" name="folder" :size="30" />
              <span class="nm"><b>{{ d }}</b><span class="sub">点选为云端目录，右侧 › 进入</span></span>
              <i class="chev" @click.stop="browseCloud(cloudBrowser.path ? cloudBrowser.path + '/' + d : d)">›</i>
            </div>
            <div class="empty sm" v-if="!cloudBrowser.dirs.length && !cloudBrowser.loading"><p>没有子目录</p></div>
          </div>
        </div>
        <div class="modal-foot col">
          <div class="toolbarC">
            <button type="button" class="mkdir-btn" :disabled="mkdirDlg.creating" @click="openMkdirDlg">{{ mkdirDlg.creating ? "创建中…" : "新建文件夹" }}</button>
            <button class="btn primary block" :disabled="cloudBrowser.mode === 'dest' && !cloudBrowser.path" @click="chooseCloudDir">使用当前目录</button>
          </div>
        </div>
      </div>
    </div>

    <!-- 新建云端目录弹窗 -->
    <div class="dlg-mask" v-if="mkdirDlg.show">
      <div class="dlg">
        <h4>新建文件夹</h4>
        <div class="dsub">将创建在：{{ cloudBrowser.path || (cloudBrowser.cloud + " 根目录") }}</div>
        <input id="mkdirName" v-model="mkdirDlg.name" placeholder="请输入文件夹名称" @keyup.enter="confirmMkdir" />
        <div class="drow">
          <button type="button" class="cancel" @click="mkdirDlg.show = false">取消</button>
          <button type="button" class="ok" :disabled="!mkdirDlg.name.trim() || mkdirDlg.creating" @click="confirmMkdir">{{ mkdirDlg.creating ? "创建中…" : "创建" }}</button>
        </div>
      </div>
    </div>

    <!-- 手机下拉刷新：顶部细条 -->
    <div class="ptr-bar" v-if="isNarrow && (ptr.dist > 0 || ptr.refreshing)" :class="{ load: ptr.refreshing, hot: ptr.dist >= PTR_TRIGGER }">
      <i class="ptr-fill" :style="{ width: ptrPct + '%' }"></i>
    </div>
    <div
      class="ptr-tx"
      v-if="isNarrow && (ptr.dist > 0 || ptr.refreshing)"
      :class="{ hot: ptr.dist >= PTR_TRIGGER || ptr.refreshing }"
      :style="{ opacity: ptrTxOpacity }"
    >{{ ptr.refreshing ? "正在刷新…" : ptr.dist >= PTR_TRIGGER ? "松开刷新" : "下拉刷新" }}</div>

    <!-- 手机底部导航（仿飞牛虚拟机 App） -->
    <nav class="bnav">
      <button :class="{ on: nav === 'tasks' }" @click="nav = 'tasks'">
        <Icon name="folder" :size="23" />
        <span>备份</span>
      </button>
      <button :class="{ on: nav === 'snap' }" @click="nav = 'snap'">
        <Icon name="stack-2" :size="23" />
        <span>快照</span>
      </button>
      <button :class="{ on: nav === 'hist' }" @click="nav = 'hist'">
        <Icon name="history" :size="23" />
        <span>历史</span>
      </button>
    </nav>

    <!-- 手机悬浮新建按钮：只在备份页出现 -->
    <button class="fab" v-if="nav === 'tasks'" aria-label="新建" @click.stop="openWizard(null)">
      <Icon name="plus" :size="26" />
    </button>

    <!-- 删除任务确认 -->
    <div class="mask top2" v-if="delDlg.show" @click.self="!delDlg.busy && (delDlg.show = false)">
      <div class="modal del-dlg">
        <div class="modal-body">
          <div class="del-ic">
            <Icon name="trash" :size="24" />
          </div>
          <h3>删除任务「{{ delDlg.task ? delDlg.task.name : "" }}」？</h3>
          <p>任务配置和备份计划会被移除。<br />云端已有的快照会保留，仍可在「快照」中浏览和还原。</p>
        </div>
        <div class="modal-foot pair">
          <button class="btn ghost" :disabled="delDlg.busy" @click="delDlg.show = false">取消</button>
          <button class="btn danger-fill" :disabled="delDlg.busy" @click="doDelTask">{{ delDlg.busy ? "删除中…" : "删除" }}</button>
        </div>
      </div>
    </div>

    <!-- Toast -->
    <transition name="fade">
      <div class="toast" :class="toast.type" v-if="toast.show">
        <i class="toast-ic">{{ toast.type === "bad" ? "!" : "✓" }}</i>{{ toast.msg }}
      </div>
    </transition>

    <!-- ==================== 还原 · 文件选择器 ==================== -->
    <div class="mask top" style="z-index:3200;" v-if="rstPick.show">
      <div class="modal pk-modal">
        <div class="modal-head">
          <h3>选择要还原的内容 <span class="pk-snap mono" v-if="rst.cur">快照 {{ (rst.cur.short_id || rst.cur.id).slice(0, 8) }}</span></h3>
          <button class="x" @click="closeRstPick">✕</button>
        </div>
        <div class="modal-body">
          <div class="crumbC">
            <span class="cloudic" @click.prevent="loadRstDir('')">📦 快照根</span>
            <template v-for="(c, i) in rstPick.crumb" :key="i">
              <span class="sl">/</span>
              <a href="#" :class="{ cur: i === rstPick.crumb.length - 1 }" @click.prevent="loadRstDir(c.path)">{{ c.label }}</a>
            </template>
            <span class="selpill" v-if="rstPick.sel.length">已选 {{ rstPick.sel.length }} 项</span>
          </div>

          <!-- 首次读取大快照清单：说明要等多久，并实时报进度 -->
          <div class="pk-tip" v-if="!rstPick.building && !rstPick.err">
            点目录名进入子目录；勾选右侧方框可只还原这个目录。也可以直接「还原整份快照」。
          </div>
          <div class="pk-build" v-if="rstPick.building">
            <i class="mini-spin"></i>
            <div>
              <b>正在读取这份快照的完整文件清单</b>
              <span>已解析 {{ rstPick.parsed.toLocaleString() }} 条 · {{ rstPick.elapsed }} 秒。大快照首次约需 1 分钟，读完会缓存，之后秒开。</span>
            </div>
          </div>
          <div class="pk-build err" v-if="rstPick.err"><b>读取失败</b><span>{{ rstPick.err }}</span></div>

          <div class="tray" v-if="rstPick.sel.length">
            <span class="t" v-for="p in rstPick.sel" :key="p"><b>{{ p.split("/").pop() || p }}</b><i @click="togglePick({ path: p })">×</i></span>
          </div>

          <div class="pk-curdir" v-if="rstPick.dir">
            <span>当前目录 <b class="mono">{{ rstPick.dir }}</b></span>
            <button type="button" class="btn ghost sm" @click="pickCurDir">选择整个目录</button>
          </div>

          <div class="dir-list">
            <div class="frow big" v-if="rstPick.dir" @click="loadRstDir(pickUpDir())">
              <span class="fold-ic back"><Icon name="arrow-back-up" :size="20" /></span>
              <span class="nm"><b>返回上一级</b></span>
            </div>
            <div class="frow big" v-for="it in rstPick.items" :key="it.path" :class="{ sel: isPicked(it.path), dir: it.is_dir }" @click="it.is_dir ? loadRstDir(it.path) : togglePick(it)">
              <Icon class="fold-ic" v-if="it.is_dir" name="folder" :size="30" color="var(--ic)" />
              <Icon class="fold-ic" v-else name="file" :size="22" color="var(--ic)" />
              <span class="nm">
                <b>{{ it.name }}<i v-if="it.badge === 'add'" class="dif add">+ 新增</i><i v-else-if="it.badge === 'mod'" class="dif mod">~ 修改</i></b>
              </span>
              <span class="fsize" v-if="!it.is_dir">{{ fmtBytes(it.size) }}</span>
              <span class="pk-enter" v-if="it.is_dir">›</span>
              <span class="cbx" :class="{ on: isPicked(it.path), half: isHalf(it.path) }" @click.stop="togglePick(it)">✓</span>
            </div>
            <div class="empty sm" v-if="!rstPick.items.length && !rstPick.loading && !rstPick.building && !rstPick.err"><p>此目录为空</p></div>
            <div class="frow big" v-if="rstPick.loading"><span class="nm"><b><i class="mini-spin"></i> 加载中…</b></span></div>
          </div>
        </div>
        <div class="modal-foot">
          <div class="toolbarC">
            <button type="button" class="btn ghost" @click="pickWholeSnap">还原整份快照</button>
            <button type="button" class="btn ghost" @click="rstPick.sel = []">清空</button>
            <button type="button" class="btn primary" @click="confirmRstPick">确定（已选 {{ rstPick.sel.length }} 项）</button>
          </div>
        </div>
      </div>
    </div>

    <!-- ==================== 还原 · 本地目录浏览 ==================== -->
    <div class="mask top" style="z-index:3200;" v-if="tb.show">
      <div class="modal">
        <div class="modal-head">
          <h3>选择还原目标目录</h3>
          <button class="x" @click="tb.show = false">✕</button>
        </div>
        <div class="modal-body">
          <div class="crumbC">
            <span class="cloudic" @click.prevent="browseTarget('')">💾 NAS</span>
            <template v-if="tb.path">
              <span class="sl">/</span>
              <a href="#" :class="{ cur: !tbSegs().length }" @click.prevent="tbCrumbRoot()">{{ scopeRootName(activeRoot(tb.path)) }}</a>
              <template v-for="(seg, i) in tbSegs()" :key="i">
                <span class="sl">/</span>
                <a href="#" :class="{ cur: i === tbSegs().length - 1 }" @click.prevent="tbCrumbTo(i)">{{ seg }}</a>
              </template>
            </template>
          </div>
          <div class="dir-list">
            <template v-if="!tb.path">
              <div class="frow big" v-for="r in USER_ROOTS" :key="r" @click="browseTarget(r)">
                <Icon class="fold-ic" name="folder" :size="30" />
                <span class="nm"><b>{{ scopeRootName(r) }}</b><span class="sub">{{ r }}</span></span>
                <i class="chev" @click.stop="browseTarget(r)">›</i>
              </div>
            </template>
            <template v-else>
              <div class="frow big" @click="tbUp">
                <span class="fold-ic back"><Icon name="arrow-back-up" :size="20" /></span>
                <span class="nm"><b>{{ isScopeRoot(tb.path) ? "返回授权目录列表" : "返回上一级" }}</b></span>
              </div>
              <div class="frow big" v-for="d in tb.dirs" :key="d.name" @click="browseTarget(tb.path.replace(/\/$/, '') + '/' + d.name)">
                <Icon class="fold-ic" name="folder" :size="30" />
                <span class="nm"><b>{{ d.name }}</b><span class="sub" v-if="d.count >= 0">{{ d.count }} 项</span></span>
              </div>
            </template>
            <div class="empty sm" v-if="tb.path && !tb.dirs.length && !tb.loading"><p>没有子目录</p></div>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn primary block" :disabled="!tb.path" @click="pickTarget">使用当前目录：{{ tb.path || "请先选择" }}</button>
        </div>
      </div>
    </div>

    <!-- 移除云端确认弹窗（z-index 须高于 bdrop-pop 的 3000；点遮罩只关本弹窗，下拉保持不动） -->
    <div class="mask top" style="z-index:3100;" v-if="rmDlg.show" @click="rmDlg.show = false">
      <div class="modal" style="width:400px;" @click.stop>
        <div class="modal-body" style="padding-top:20px;">
          <h3 style="font-size:16px; text-align:center; margin-bottom:6px;">移除「{{ rmDlg.opt ? rmDlg.opt.name : "" }}」？</h3>
          <p style="font-size:13px; color:#6b7280; text-align:center; margin-bottom:14px;">已备份的数据会保留在云盘；<b>未备份的空仓库将一并清理</b></p>
        </div>
        <div class="modal-foot pair">
          <button class="btn ghost block" @click="rmDlg.show = false">取消</button>
          <button class="btn primary block" style="background:#dc2626; border-color:#dc2626;" :disabled="rmDlg.busy" @click="confirmRmRepo">{{ rmDlg.busy ? "移除中…" : "确认移除" }}</button>
        </div>
      </div>
    </div>

    <!-- 在线更新 -->
    <div class="mask top" style="z-index:3100;" v-if="updDlg.show" @click="!updWorking && (updDlg.show = false)">
      <div class="modal upd-modal" @click.stop>
        <div class="modal-head">
          <h3>在线更新</h3>
          <button class="x" v-if="!updWorking" @click="updDlg.show = false"><Icon name="x" :size="18" /></button>
        </div>
        <div class="modal-body">
          <div class="upd-vers">
            <div><span>当前版本</span><b>v{{ upd?.current || status?.version }}</b></div>
            <div v-if="upd?.from_overlay"><span>商店版本</span><b>v{{ upd.builtin }}</b></div>
            <div v-if="upd?.latest"><span>最新版本</span><b class="new">v{{ upd.latest }}</b></div>
          </div>

          <p class="upd-tip" v-if="upd && !upd.enabled">{{ upd.disabled_reason }}</p>
          <p class="upd-tip bad" v-else-if="upd?.failed">上次更新到 {{ upd.failed }} 后未能正常启动，已自动回滚。</p>
          <p class="upd-tip" v-if="upd?.check_error">检查失败：{{ upd.check_error }}</p>
          <p class="upd-tip warn" v-if="upd?.blocked">{{ upd.blocked }}</p>
          <p class="upd-tip ok" v-if="upd?.enabled && upd.checked_at && !upd.latest && !upd.check_error">
            <Icon name="circle-check" :size="15" /> 已是最新版本
          </p>

          <div class="upd-notes" v-if="upd?.notes?.length">
            <div class="upd-note" v-for="n in upd.notes" :key="n.version">
              <div class="upd-note-h">v{{ n.version }}<span v-if="n.date">{{ n.date }}</span></div>
              <ul><li v-for="(it, i) in n.items" :key="i">{{ it }}</li></ul>
            </div>
          </div>

          <div class="upd-prog" v-if="updWorking">
            <div class="upd-prog-bar"><i :style="{ width: (upd.phase === 'downloading' ? (upd.progress || 0) * 100 : 100) + '%' }" :class="{ pulse: upd.phase !== 'downloading' }"></i></div>
            <div class="upd-prog-t">{{ updPhaseText }}</div>
          </div>
          <p class="upd-tip bad" v-if="upd?.phase === 'error' && upd.error">{{ upd.error }}</p>

          <p class="upd-foot-tip" v-if="upd?.available && !updWorking">
            更新包经过签名校验；更新期间应用会重启约 5 秒，已有任务与数据不受影响。新版本若无法启动会自动回滚。
          </p>
          <div class="upd-reset" v-if="upd?.from_overlay && !updWorking && isAdmin">
            <template v-if="!updDlg.resetAsk">
              <button class="link-btn" @click="updDlg.resetAsk = true">恢复为商店版本 v{{ upd.builtin }}</button>
            </template>
            <template v-else>
              <span>确定放弃在线更新、回到 v{{ upd.builtin }}？</span>
              <button class="link-btn" @click="updDlg.resetAsk = false">取消</button>
              <button class="link-btn danger" :disabled="updDlg.busy" @click="resetUpdate">确定</button>
            </template>
          </div>
        </div>
        <div class="modal-foot pair" v-if="upd?.enabled && !updWorking">
          <button class="btn ghost block" :disabled="updDlg.busy" @click="checkUpdate">
            {{ updDlg.busy && !upd.available ? "检查中…" : "检查更新" }}
          </button>
          <button class="btn primary block" v-if="upd.available && isAdmin" :disabled="updDlg.busy || !!upd.blocked" @click="applyUpdate">
            立即更新
          </button>
        </div>
      </div>
    </div>

    <!-- 选择云端：卡片式弹层（方案A，参考飞牛备份来源地样式） -->
    <CloudPicker
      :show="cloudPick.show"
      :repos="pickerRepos"
      :fetch-pw="fetchRepoPw"
      :toast="showToast"
      @update:show="cloudPick.show = $event"
      @select="onCloudPick"
      @add="onCloudPickAdd"
      @remove="delRepo"
    />
  </div>
</template>

<style>
/* 弹窗打开时锁住底层页面滚动（scoped 下选择器作用不到 html，故用非 scoped 块） */
html.scroll-locked,
html.scroll-locked body {
  overflow: hidden !important;
  overscroll-behavior: none;
}

/* 尊重系统「减少动态效果」设置；加载指示器保留旋转，否则会被误读成卡死。
   放在非 scoped 块里，才能覆盖到 CloudPicker / BDrop 等子组件 */
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: .01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: .01ms !important;
    scroll-behavior: auto !important;
  }
  .mini-spin, .ov-spin, .spin {
    animation-duration: 1.1s !important;
    animation-iteration-count: infinite !important;
  }
}
</style>

<style scoped>
* { box-sizing: border-box; }
.page {
  min-height: 100vh;
  background: #f2f4f7;
  color: #1f2937;
  font-family: system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif;
  display: flex;
}
.mono { font-family: ui-monospace, Menlo, monospace; font-size: 12px; }

/* ==================== 左侧导航（桌面） ==================== */
.sidebar {
  width: 200px; flex-shrink: 0; background: #fff; border-right: 1px solid #ebedf0;
  display: flex; flex-direction: column; position: sticky; top: 0; height: 100vh;
}
.sb-nav { display: flex; flex-direction: column; gap: 2px; padding: 18px 10px 8px; }
.sb-item {
  display: flex; align-items: center; gap: 10px; border: none; background: transparent;
  padding: 10px 12px; border-radius: 9px; font-size: 14px; color: #4b5563; cursor: pointer; text-align: left;
}
.sb-item:hover { background: #f5f7fa; }
.sb-item.on { background: #eef4ff; color: #3370ff; font-weight: 500; }
.sb-foot { margin-top: auto; padding: 14px 18px; font-size: 11px; color: #b0b7c3; line-height: 1.8; }

.main { flex: 1; min-width: 0; }

/* ==================== 手机顶栏 ==================== */
.mhead { display: none; }
.fab { display: none; }
.bnav { display: none; }

.content { max-width: 980px; margin: 0 auto; padding: 24px 28px 60px; }
.dhead { display: flex; align-items: center; justify-content: space-between; margin-bottom: 18px; }
.dhead h1 { margin: 0; font-size: 20px; font-weight: 600; }
.plus { font-weight: 600; margin-right: 2px; }
.card { background: #fff; border: 1px solid #e5e7eb; border-radius: 12px; padding: 18px 22px; }
.card h2 { margin: 0; font-size: 15px; font-weight: 600; }
.card-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }

/* ==================== 运行横幅 ==================== */
.run-banner {
  background: #fff; border: 1px solid #bfdbfe; border-radius: 12px; padding: 14px 18px; margin-bottom: 16px;
}
.run-head { display: flex; align-items: center; gap: 10px; font-size: 14px; }
.run-pct { font-weight: 700; color: #2563eb; margin-left: auto; }
.spin {
  width: 14px; height: 14px; border: 2px solid #93c5fd; border-top-color: #2563eb;
  border-radius: 50%; animation: rot 0.9s linear infinite; flex-shrink: 0;
}
@keyframes rot { to { transform: rotate(360deg); } }
.bar { height: 8px; background: #dbeafe; border-radius: 6px; margin: 10px 0 8px; overflow: hidden; }
.bar-in { height: 100%; background: #3370ff; border-radius: 6px; transition: width 1.5s; }
.run-meta { display: flex; flex-wrap: wrap; gap: 6px 20px; font-size: 12px; color: #4b5563; }
.run-err { color: #d97706; }

/* ==================== 任务卡片内进度区 ==================== */
.tc-progress {
  margin-top: 14px; padding-top: 13px; border-top: 1px solid #f5f6f8;
}
.tc-progress .pg-head {
  display: flex; align-items: center; gap: 8px;
  font-size: 12.5px; color: #4b5563; margin-bottom: 0;
}
.tc-progress .pg-head .spin { width: 12px; height: 12px; }
.tc-progress .pg-pct {
  font-weight: 700; color: #3370ff; font-size: 13px; margin-left: auto;
  font-variant-numeric: tabular-nums;
}
.tc-progress .bar { margin: 9px 0 8px; }
.tc-progress .pg-meta {
  display: flex; flex-wrap: wrap; gap: 4px 22px; font-size: 12px; color: #4b5563;
  font-variant-numeric: tabular-nums;
}
.tc-progress .pg-k { color: #8b9098; margin-right: 4px; }
.tc-progress .pg-err { color: #d97706; margin-left: auto; }

/* ==================== 任务卡片（仿原生桌面） ==================== */
.task-list { display: flex; flex-direction: column; gap: 14px; }
.task-card { background: #fff; border-radius: 12px; padding: 16px 20px 14px; box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05); }
.tc-top { display: flex; align-items: flex-start; gap: 14px; }
.tc-icon { position: relative; flex-shrink: 0; width: 40px; height: 40px; }
.badge {
  position: absolute; right: -4px; bottom: -3px; width: 16px; height: 16px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  font-size: 11px; font-weight: 700; color: #fff; border: 2px solid #fff;
}
.badge.b-warn { background: #f59e0b; }
.badge.b-minus { background: #9ca3af; }
.badge.b-ok { background: #22c55e; }
.badge.b-run { background: #3370ff; }
.badge.b-none { display: none; }
.mini-spin {
  width: 7px; height: 7px; border: 1.5px solid #bfdbfe; border-top-color: #fff;
  border-radius: 50%; display: inline-block; animation: rot 0.9s linear infinite;
}

/* ==================== 还原功能 ==================== */
.dhead-acts { display: flex; align-items: center; gap: 8px; }

/* 新建还原 · 任务选择弹窗 */
.rpick-modal { width: 430px; }
.rpick-list { display: flex; flex-direction: column; gap: 8px; padding: 6px 20px 18px; max-height: 54vh; overflow: auto; }
.rpick-item {
  display: flex; align-items: center; gap: 12px; text-align: left;
  border: 1px solid #e5e7eb; background: #fff; border-radius: 12px;
  padding: 12px 14px; cursor: pointer; transition: border-color .15s, background .15s;
}
.rpick-item:hover { border-color: #3370ff; background: #f7faff; }
.rpick-item:hover .rp-arrow { stroke: #3370ff; }
.rp-ic {
  flex: none; width: 36px; height: 36px; border-radius: 10px;
  background: #eef4ff; color: #3370ff;
  display: flex; align-items: center; justify-content: center; font-size: 18px; font-weight: 600;
}
.rp-txt { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.rp-txt b { font-size: 14px; color: #1f2937; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rp-txt small { font-size: 12px; color: #9ca3af; }
.rp-arrow { flex: none; }

.rst-modal { width: 560px; }
/* 方案A：顶部步骤条 */
.rst-stepper { display: flex; align-items: flex-start; padding: 14px 18px; border-bottom: 1px solid #f3f4f6; background: #fafbfd; }
.rst-step-dot { flex: 1; display: flex; flex-direction: column; align-items: center; gap: 4px; }
.rst-step-dot .n {
  width: 22px; height: 22px; border-radius: 50%; background: #e5e9f0; color: #9ca3af;
  font-size: 12px; font-weight: 600; display: flex; align-items: center; justify-content: center;
}
.rst-step-dot .lb { font-size: 10.5px; color: #9ca3af; white-space: nowrap; }
.rst-step-dot.on .n { background: #3370ff; color: #fff; }
.rst-step-dot.on .lb { color: #3370ff; font-weight: 600; }
.rst-step-dot.done .n { background: #e8f4ec; color: #16803a; }
.rst-step-dot.done .lb { color: #16803a; }
.rst-step-line { flex: none; width: 18px; height: 1.5px; background: #e5e9f0; margin-top: 10px; }
.rst-step-line.done { background: #3370ff; }
.rst-step-title { font-size: 13px; font-weight: 600; color: #111827; margin-bottom: 12px; }
.rst-step-title em { font-style: normal; color: #9ca3af; font-weight: 400; font-size: 12px; margin-left: 6px; }
.rstep-tools { display: flex; align-items: center; flex-wrap: wrap; margin-bottom: 8px; }
.rstep-tools .snap-cache { margin-left: 4px; }
.rstep-tools .snap-refresh { margin-left: auto; }
/* flow-line */
.rst-flow { display: flex; align-items: center; gap: 7px; padding: 11px 20px; border-top: 1px solid #f3f4f6; flex-wrap: wrap; background: #fff; }
.rst-flow-node { background: #f4f6fa; border-radius: 8px; padding: 5px 10px; font-size: 12px; color: #374151; }
.rst-flow-node.hi { background: #eef4ff; color: #3370ff; font-weight: 600; }
.rst-flow-lk { color: #c2c7d0; font-size: 11px; font-style: normal; }

.fld-hint { font-style: normal; font-size: 11.5px; color: #9ca3af; margin-left: 6px; }
.snap-cache { font-style: normal; font-size: 11px; color: #9ca3af; margin-left: 6px; }
.snap-refresh { display: inline-flex; align-items: center; gap: 4px; margin-left: 8px; padding: 2px 8px; font-size: 11px; color: #6b7280; background: #f9fafb; border: 1px solid #e5e7eb; border-radius: 20px; cursor: pointer; }
.snap-refresh:hover { color: #4f6ef7; border-color: #c7d2fe; background: #f5f7ff; }
.snap-refresh:disabled { opacity: 0.5; cursor: default; }
.snap-timeline { max-height: 190px; overflow-y: auto; overscroll-behavior: contain; border: 1px solid #f3f4f6; border-radius: 8px; }
.snap-row {
  display: flex; align-items: center; gap: 9px; padding: 9px 12px;
  border-bottom: 1px solid #f3f4f6; font-size: 13.5px; cursor: pointer;
}
.snap-row:last-child { border-bottom: none; }
.snap-row:hover { background: #f7f9ff; }
.snap-row.on { background: #eef4ff; }
.snap-dot { width: 9px; height: 9px; border-radius: 50%; background: #cbd5e1; flex: none; }
.snap-row.on .snap-dot { background: #3370ff; }
.snap-t { font-weight: 600; color: #1f2937; flex: none; }
.snap-files { font-size: 11.5px; color: #9ca3af; flex: 1; text-align: right; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.snap-badge { font-size: 10.5px; padding: 2px 8px; border-radius: 6px; font-weight: 600; flex: none; }
.snap-badge.chg { background: #e7f8ee; color: #16a34a; }
.snap-badge.idle { background: #f3f4f6; color: #9ca3af; }
.snap-loading { padding: 14px; font-size: 12.5px; color: #9ca3af; display: flex; align-items: center; gap: 7px; }
.pick-files-btn {
  width: 100%; display: flex; align-items: center; justify-content: center; gap: 8px;
  padding: 10px; border: 1.5px dashed #c6d4f5; border-radius: 9px; background: #f8faff;
  color: #3370ff; font-size: 13.5px; cursor: pointer; font-family: inherit; font-weight: 600;
}
.pick-files-btn:hover { background: #eef4ff; }
.pick-hint { margin-top: 10px; font-size: 12px; color: #9ca3af; line-height: 1.7; word-break: break-all; }
.inc-tray { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
.inc-tray .t, .tray .t {
  display: inline-flex; align-items: center; gap: 5px; background: #fff;
  border: 1px solid #e2e6ee; border-radius: 7px; padding: 3px 9px;
  font-family: ui-monospace, Menlo, monospace; font-size: 11px; color: #4b5563;
}
.inc-tray .t i, .tray .t i { color: #9ca3af; cursor: pointer; font-style: normal; padding: 0 1px; }
.inc-tray .t i:hover, .tray .t i:hover { color: #dc2626; }
.seg { display: flex; background: #eef0f4; border-radius: 10px; padding: 3px; gap: 2px; }
.seg button {
  flex: 1; border: none; background: transparent; padding: 8px 10px; border-radius: 8px;
  font-size: 13px; cursor: pointer; color: #6b7280; font-family: inherit;
}
.seg button.on { background: #fff; color: #3370ff; font-weight: 600; box-shadow: 0 1px 3px rgba(16,24,40,.08); }
.seg.wrap { flex-wrap: wrap; }
.seg.wrap button { flex: 1 1 40%; }
/* 冲突策略：2×2 卡片单选 */
.conf-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 9px; }
.conf-card {
  position: relative; display: flex; flex-direction: column; align-items: flex-start; gap: 6px;
  text-align: left; padding: 12px 14px; border: 1.5px solid #e5e7eb; border-radius: 10px;
  background: #fff; cursor: pointer; font-family: inherit; transition: border-color .15s, background .15s;
}
.conf-card:hover { border-color: #c6d4f5; }
.conf-card.on { border-color: #3370ff; background: #f7f9ff; }
.conf-card .conf-rd {
  position: absolute; top: 10px; right: 10px; width: 18px; height: 18px; border-radius: 50%;
  border: 1.5px solid #cbd5e1; display: flex; align-items: center; justify-content: center; flex: none;
}
.conf-card .conf-rd::after { content: ""; width: 10px; height: 10px; border-radius: 50%; background: transparent; }
.conf-card.on .conf-rd { border-color: #3370ff; }
.conf-card.on .conf-rd::after { background: #3370ff; }
.conf-card .conf-tx { display: flex; flex-direction: column; gap: 2px; min-width: 0; padding-right: 22px; }
.conf-card .conf-tx b { font-size: 13.5px; font-weight: 600; color: #1f2937; }
.conf-card .conf-tx small { font-size: 11.5px; color: #9ca3af; line-height: 1.4; }
.conf-card.on .conf-tx b { color: #3370ff; }
.dest-note {
  margin-top: 8px; padding: 8px 11px; background: #f7f8fa; border-radius: 8px;
  font-size: 12px; color: #6b7280; line-height: 1.7;
  word-break: break-all; max-height: 88px; overflow-y: auto; overscroll-behavior: contain;
}
.target-row { display: flex; gap: 8px; margin-top: 8px; }
.target-row input {
  flex: 1; padding: 9px 12px; border: 1px solid #d1d5db; border-radius: 8px;
  font-size: 13px; font-family: ui-monospace, Menlo, monospace; min-width: 0;
}
/* 文件选择器 */
.pk-snap { font-size: 12px; color: #9ca3af; font-weight: 400; margin-left: 6px; }
.selpill { margin-left: auto; flex-shrink: 0; font-size: 11.5px; background: #e8f0ff; color: #3370ff; border-radius: 999px; padding: 2px 10px; font-weight: 600; }
.tray {
  display: flex; align-items: center; gap: 6px; padding: 8px 11px;
  background: #f7f8fa; border-radius: 9px; margin-bottom: 10px; font-size: 12.5px;
  flex-wrap: wrap;
}
.tray .clr { margin-left: auto; color: #9ca3af; cursor: pointer; font-size: 11.5px; }
.cbx {
  width: 20px; height: 20px; border: 1.7px solid #cbd2dd; border-radius: 6px; flex: none;
  background: #fff; cursor: pointer; display: flex; align-items: center; justify-content: center;
  color: #fff; font-size: 12px; font-weight: 700; transition: background .12s, border-color .12s;
  margin-right: 5px;
}
.cbx:hover { border-color: #3370ff; }
.cbx.on { background: #3370ff; border-color: #3370ff; }
.cbx.half { background: #3370ff; border-color: #3370ff; opacity: .5; }
.fsize { font-size: 11.5px; color: #9ca3af; flex: none; }
.dif { font-style: normal; font-size: 10px; padding: 1px 6px; border-radius: 5px; font-weight: 700; margin-left: 6px; vertical-align: 1.5px; }
.dif.add { background: #e7f8ee; color: #16a34a; }
.dif.mod { background: #fef3e2; color: #d97706; }
.frow.big.sel { background: #eef4ff; }
/* 文件选择器：目录进入 / 清单读取进度 */
.modal.pk-modal { width: 620px; }
.pk-tip { font-size: 12px; color: #9ca3af; line-height: 1.7; margin: 2px 0 8px; }
.pk-build {
  display: flex; align-items: flex-start; gap: 10px; padding: 11px 13px; margin-bottom: 10px;
  background: #f5f8ff; border: 1px solid #dbe6ff; border-radius: 9px;
}
.pk-build > div { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.pk-build b { font-size: 13px; color: #1f2937; font-weight: 600; }
.pk-build span { font-size: 12px; color: #6b7280; line-height: 1.6; }
.pk-build.err { background: #fdf2f2; border-color: #f8d4d4; }
.pk-build.err b { color: #dc2626; }
.pk-curdir {
  display: flex; align-items: center; gap: 10px; justify-content: space-between;
  padding: 8px 11px; margin-bottom: 8px; background: #f7f8fa; border-radius: 9px;
  font-size: 12.5px; color: #6b7280;
}
.pk-curdir b { color: #1f2937; font-weight: 600; word-break: break-all; }
.pk-curdir .btn { flex: none; }
.pk-enter { flex: none; color: #c4cad4; font-size: 19px; line-height: 1; margin-left: 2px; }
.frow.big.dir:hover .pk-enter { color: #3370ff; }
/* 还原历史页 */
.rst-list { display: flex; flex-direction: column; gap: 12px; }
.rst-card { background: #fff; border-radius: 12px; padding: 14px 18px; box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05); }
.rst-top { display: flex; align-items: center; gap: 10px; }
.rst-name { font-size: 14.5px; font-weight: 600; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rst-pill { font-size: 11px; font-weight: 600; padding: 3px 10px; border-radius: 20px; flex: none; display: inline-flex; align-items: center; gap: 5px; }
.rst-pill.ok { background: #e7f8ec; color: #16803a; }
.rst-pill.bad, .rst-pill.failed { background: #fdecec; color: #dc2626; }
.rst-pill.run, .rst-pill.running { background: #eef4ff; color: #3370ff; }
.rst-pill.idle, .rst-pill.stopped { background: #f1f3f7; color: #6b7280; }
.rst-rows { margin-top: 9px; border-top: 1px solid #f3f5f9; padding-top: 9px; display: flex; flex-direction: column; gap: 5px; }
.rst-row { display: flex; align-items: center; gap: 8px; font-size: 12.5px; color: #6b7280; }
.rst-row .k { color: #9ca3af; width: 34px; flex: none; }
.rst-row .v { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rst-progress { display: flex; align-items: center; gap: 10px; padding: 10px 0 4px; }
.rst-bar { flex: 1; height: 8px; background: #eef0f4; border-radius: 999px; overflow: hidden; }
.rst-bar i { display: block; height: 100%; background: #3370ff; border-radius: 999px; transition: width .5s; }
.rst-pct { font-size: 11.5px; color: #6b7280; flex: none; white-space: nowrap; }
@media (max-width: 640px) {
  .rst-modal { width: 100%; }
}
.tc-title { flex: 1; min-width: 0; }
.tc-title > b { font-size: 15px; }
.tc-status { display: flex; align-items: baseline; gap: 10px; font-size: 12.5px; margin-top: 3px; flex-wrap: wrap; }
.st-main { font-weight: 500; }
.tc-status.success .st-main { color: #16a34a; }
.tc-status.failed .st-main { color: #ea8c00; }
.tc-status.running .st-main { color: #3370ff; }
.tc-status.disabled .st-main, .tc-status.idle .st-main { color: #9ca3af; }
.st-detail { color: #6b7280; }
.st-link { color: #3370ff; cursor: pointer; text-decoration: none; }
.st-link:hover { text-decoration: underline; }
.tc-acts { display: flex; align-items: center; gap: 4px; position: relative; }
.icon-btn {
  border: none; background: transparent; width: 32px; height: 32px; border-radius: 8px;
  display: flex; align-items: center; justify-content: center; color: #4b5563; cursor: pointer;
}
.icon-btn:hover { background: #f3f4f6; color: #1f2937; }
.menu {
  position: absolute; right: 0; top: 36px; background: #fff; border-radius: 10px;
  box-shadow: 0 8px 28px rgba(16, 24, 40, 0.16); padding: 6px; z-index: 30; min-width: 132px;
  display: flex; flex-direction: column;
}
.menu button {
  border: none; background: transparent; text-align: left; padding: 9px 14px;
  border-radius: 7px; font-size: 13px; color: #374151; cursor: pointer; white-space: nowrap;
}
.menu button:hover { background: #f5f7fa; }
.menu button.danger { color: #dc2626; }

/* 第二行：来源→目的地→下次 */
.tc-grid {
  display: grid; grid-template-columns: 1.2fr 44px 1.3fr 0.9fr;
  align-items: center; gap: 8px; margin-top: 12px; padding-top: 12px;
  border-top: 1px solid #f2f4f7; padding-left: 54px;
}
.tc-cell { min-width: 0; }
.cell-head { display: flex; align-items: center; gap: 8px; font-size: 13px; }
.cell-head b { font-weight: 500; color: #374151; }
.cell-head b.dim { color: #9ca3af; }
.cell-sub { font-size: 12px; color: #9ca3af; margin-top: 4px; padding-left: 26px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.tc-arrow { display: flex; justify-content: center; }
.tc-cell.next { text-align: left; }

/* ==================== 手机卡片（仿原生） ==================== */
.mcard-list { display: none; }
.mc-more { border: none; background: transparent; font-size: 20px; color: #9ca3af; cursor: pointer; padding: 0 4px; }

/* ==================== 仓库 ==================== */
.repo-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 12px; }
.repo-item { border: 1px solid #e5e7eb; border-radius: 10px; padding: 12px 14px; background: #fff; }
.repo-top { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.repo-cloud { font-size: 11px; padding: 2px 8px; border-radius: 999px; color: #fff; }
.repo-cloud.tianyi { background: #f43f5e; }
.repo-cloud.y115 { background: #1684fc; }
.repo-cloud.other { background: #6b7280; }
.repo-uri { color: #6b7280; margin-bottom: 8px; word-break: break-all; }
.repo-acts { display: flex; gap: 8px; }

/* ==================== 仓库 Tab（仓库 → 任务分组 → 快照） ==================== */
.rp-list { display: flex; flex-direction: column; gap: 11px; }
.rp-card {
  display: flex; align-items: center; gap: 14px; background: #fff; border-radius: 12px;
  padding: 16px 18px; box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05); cursor: pointer;
  transition: box-shadow 0.15s;
}
.rp-card:hover { box-shadow: 0 3px 14px rgba(16, 24, 40, 0.1); }
.rp-ic { width: 40px; height: 40px; flex: none; border-radius: 10px; background: #eef4ff; display: flex; align-items: center; justify-content: center; }
.rp-body { flex: 1; min-width: 0; }
.rp-name { font-size: 14.5px; font-weight: 600; display: flex; align-items: center; gap: 8px; }
.rp-pill { font-size: 10.5px; font-weight: 600; padding: 2px 8px; border-radius: 20px; flex: none; }
.rp-pill.ok { background: #e7f8ec; color: #16803a; }
.rp-pill.gray { background: #f3f4f6; color: #9ca3af; }
.rp-path { font-size: 11.5px; color: #9ca3af; margin-top: 3px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rp-meta { display: flex; gap: 18px; margin-top: 9px; flex-wrap: wrap; }
.rp-meta span { font-size: 11.5px; color: #8b9098; }
.rp-meta b { color: #1f2937; font-size: 13px; font-weight: 600; margin-right: 4px; }
.rp-go { flex: none; font-size: 12.5px; color: #3370ff; font-weight: 600; }

.rp-bar { display: flex; align-items: center; gap: 14px; margin-bottom: 14px; }
.rp-back { display: none; border: none; background: transparent; font-size: 15px; color: #3370ff; cursor: pointer; padding: 0; font-family: inherit; }
.rp-crumb { font-size: 13px; color: #9ca3af; }
.rp-crumb b { color: #1f2937; font-weight: 600; }
.rp-tools { margin-left: auto; display: flex; align-items: center; gap: 8px; }
.rp-search { display: flex; align-items: center; gap: 7px; border: 1px solid #e5e7eb; border-radius: 8px; padding: 7px 11px; color: #b0b7c3; background: #fff; }
.rp-search input { border: none; outline: none; background: transparent; font-family: inherit; font-size: 12.5px; color: #1f2937; width: 150px; }

.rp-sum { display: flex; gap: 26px; background: #fff; border-radius: 10px; padding: 13px 18px; margin-bottom: 14px; box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05); flex-wrap: wrap; }
.rp-sum > div { display: flex; flex-direction: column; gap: 3px; }
.rp-sum b { font-size: 15px; font-weight: 600; color: #1f2937; }
.rp-sum span { font-size: 11.5px; color: #9ca3af; }

.rp-grp { background: #fff; border-radius: 12px; box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05); margin-bottom: 14px; overflow: hidden; }
.rp-grp-head { display: flex; align-items: center; gap: 10px; padding: 12px 16px; border-bottom: 1px solid #f3f4f6; cursor: pointer; }
.rp-grp-head:hover { background: #fafbfd; }
.rp-dot { width: 8px; height: 8px; border-radius: 50%; flex: none; }
.rp-gn { font-size: 13.5px; font-weight: 600; }
.rp-gp { color: #b0b7c3; font-size: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 260px; }
.rp-gc { margin-left: auto; font-size: 11.5px; color: #8b9098; }
.rp-fold { font-size: 11.5px; color: #3370ff; flex: none; }
.rp-tbl { width: 100%; border-collapse: collapse; font-size: 12.5px; }
.rp-tbl th { text-align: left; font-weight: 500; color: #9ca3af; font-size: 11.5px; padding: 9px 16px; background: #fcfcfd; border-bottom: 1px solid #f3f4f6; white-space: nowrap; }
.rp-tbl td { padding: 11px 16px; border-bottom: 1px solid #f6f7f9; color: #374151; }
.rp-tbl tbody tr:last-child td { border-bottom: none; }
.rp-tbl tbody tr:hover td { background: #f9fbff; }
.rp-tbl .t { font-weight: 600; color: #1f2937; white-space: nowrap; font-variant-numeric: tabular-nums; }
.rp-tbl .dim { color: #9ca3af; }
.rp-tbl .acts { text-align: right; }
.rp-tbl .nowrap { white-space: nowrap; }
.rp-tbl .ellip { max-width: 230px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rp-tbl .ellip-sm { max-width: 100px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rp-link { border: none; background: transparent; color: #3370ff; font-size: 12.5px; font-weight: 600; cursor: pointer; padding: 4px 6px; font-family: inherit; white-space: nowrap; }
.rp-link:hover { text-decoration: underline; }

.rp-mlist { display: none; }
.rp-mgrp-t { display: flex; align-items: center; gap: 8px; }
.rp-mcard { background: #fff; border-radius: 12px; padding: 12px 14px; margin-bottom: 8px; box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05); }
.rp-mtop { display: flex; align-items: center; }
.rp-mtop b { font-size: 13.5px; font-weight: 600; font-variant-numeric: tabular-nums; }
.rp-mgo { margin-left: auto; font-size: 12px; color: #3370ff; font-weight: 600; }
.rp-msub { font-size: 11.5px; color: #9ca3af; margin-top: 5px; }

/* ==================== 按钮 ==================== */
.btn {
  border: none; border-radius: 8px; padding: 9px 18px; font-size: 14px; cursor: pointer;
  transition: opacity 0.15s;
}
.btn:hover { opacity: 0.85; }
.btn.primary { background: #3370ff; color: #fff; }
.btn.warn { background: #f59e0b; color: #fff; }
.btn.ghost { background: #f3f4f6; color: #374151; }
.btn.ghost.danger { color: #dc2626; }
.btn.sm { padding: 6px 12px; font-size: 12px; border-radius: 7px; }

/* ==================== 空态 ==================== */
.empty { text-align: center; padding: 48px 0 32px; color: #6b7280; }
.empty.sm { padding: 16px 0; font-size: 13px; }
.empty-icon { font-size: 38px; margin-bottom: 8px; }
.empty p { margin: 4px 0; }
.hint { font-size: 12px; color: #9ca3af; }

/* ==================== 还原占位 ==================== */
.soon-tag { font-size: 12px; background: #fef3c7; color: #92400e; padding: 4px 12px; border-radius: 999px; }
.steps-preview { display: flex; align-items: center; justify-content: space-between; margin: 18px 0 22px; flex-wrap: wrap; gap: 8px; }
.wiz-step { display: flex; flex-direction: column; align-items: center; gap: 6px; opacity: 0.45; }
.wiz-step.cur { opacity: 1; }
.ws-num {
  width: 34px; height: 34px; border-radius: 50%; background: #e5e7eb; color: #6b7280;
  display: flex; align-items: center; justify-content: center; font-weight: 600;
}
.wiz-step.cur .ws-num { background: #3370ff; color: #fff; }
.ws-t { font-size: 12px; color: #4b5563; }
.ws-arrow { color: #d1d5db; }
.mock-block { border: 1px dashed #e5e7eb; border-radius: 10px; padding: 16px; margin-bottom: 16px; }
.mock-row { display: flex; align-items: center; gap: 14px; padding: 8px 0; font-size: 13px; }
.mock-row .k { color: #9ca3af; width: 90px; flex-shrink: 0; }
.mock-row select { flex: 1; padding: 8px; border: 1px solid #e5e7eb; border-radius: 8px; background: #f9fafb; }
.timeline-mock { flex: 1; display: flex; align-items: center; gap: 40px; padding: 10px 0; position: relative; }
.timeline-mock::before { content: ""; position: absolute; left: 0; right: 0; top: 50%; height: 2px; background: #e5e7eb; }
.tl-dot { width: 12px; height: 12px; border-radius: 50%; background: #c7d2fe; position: relative; }
.tree-mock { flex: 1; display: flex; flex-direction: column; gap: 8px; }
.tr-line { height: 10px; border-radius: 5px; background: #f3f4f6; }
.w60 { width: 60%; } .w40 { width: 40%; } .w75 { width: 75%; } .w50 { width: 50%; }

/* ==================== 弹窗 ==================== */
.mask {
  position: fixed; inset: 0; background: rgba(0, 0, 0, 0.45); z-index: 50;
  display: flex; align-items: center; justify-content: center; padding: 16px;
  overscroll-behavior: contain;
}
.modal {
  background: #fff; border-radius: 14px; width: 560px; max-width: 100%;
  max-height: 88vh; display: flex; flex-direction: column;
}
.modal.wide { width: 820px; height: 80vh; }
.modal.tall { height: 88vh; }
/* 滚动容器必须是 dir-list 自己：modal-body 改成 flex 列、不滚动，
   否则 dir-list 的 max-height:none 会把它撑到超出容器，
   在移动端 WebView 里表现为「看得到下面的文件夹但手指滑不动」 */
.modal.tall .modal-body { flex: 1; min-height: 0; display: flex; flex-direction: column; overflow: hidden; }
.modal.tall .dir-list { flex: 1; min-height: 0; max-height: none; overflow-y: auto; -webkit-overflow-scrolling: touch; touch-action: pan-y; }
.modal.tall .hide-sys { flex: 0 0 auto; }
.modal-head { display: flex; align-items: center; justify-content: space-between; padding: 16px 20px 10px; }
.modal-head h3 { margin: 0; font-size: 16px; }
.x { border: none; background: none; font-size: 16px; cursor: pointer; color: #6b7280; padding: 4px 8px; }
.auto-lb { font-size: 12px; color: #6b7280; display: flex; align-items: center; gap: 4px; }
.modal-body { padding: 8px 20px; overflow-y: auto; overscroll-behavior: contain; -webkit-overflow-scrolling: touch; }
.modal-foot { display: flex; align-items: center; gap: 10px; padding: 14px 20px; border-top: 1px solid #f3f4f6; }
.spacer { flex: 1; }
/* 保存中的进度提示条（仓库保存/init 可能耗时十几秒） */
.saving-tip { display: flex; align-items: center; gap: 9px; padding: 10px 20px 0; font-size: 13px; color: #2563eb; }
.saving-tip .spin { width: 14px; height: 14px; border: 2px solid #bfdbfe; border-top-color: #2563eb; border-radius: 50%; animation: sp 0.7s linear infinite; }
@keyframes sp { to { transform: rotate(360deg); } }
/* even：底部按钮等宽均分（上一步/下一步） */
.modal-foot.even .btn { flex: 1 1 0; min-width: 0; padding: 10px 18px; }

/* ---- 方案B 单页表单：飞牛式分区 + 内联展开 ---- */
.modal-body.single { padding-bottom: 8px; }
.sec { margin-bottom: 18px; }
.sec-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
.sec-head b { font-size: 14.5px; color: #1f2937; }
.link-btn {
  border: none; background: transparent; color: #3370ff; font-size: 13.5px;
  cursor: pointer; padding: 2px 4px;
}
.link-btn.more { display: block; margin: 8px 4px 0; padding: 4px 0; font-size: 13.5px; }
.sec-card { background: #fff; border: 1px solid #eef0f4; border-radius: 12px; overflow: hidden; }
.sec-sub { font-size: 12.5px; color: #9ca3af; display: block; margin-top: 2px; }
.sec-sub.pad { padding: 14px 16px; }
.src-row { display: flex; align-items: center; gap: 10px; padding: 12px 16px; border-bottom: 1px solid #f3f4f6; }
.src-row .src-ico { font-size: 20px; }
.src-row b { font-size: 14px; color: #1f2937; }
.src-path {
  display: flex; align-items: center; justify-content: space-between;
  padding: 9px 16px; border-bottom: 1px solid #f6f7f9; font-size: 13px;
}
.src-path:last-child { border-bottom: none; }
.src-path .rm { color: #c2c7d0; cursor: pointer; font-weight: 400; padding: 0 2px; }
.src-path .rm:active { color: #ef4444; }
.pick-row {
  display: flex; align-items: center; gap: 12px; padding: 12px 16px;
  cursor: pointer; border-bottom: 1px solid #f3f4f6;
}
.pick-row:last-child { border-bottom: none; }
.pick-row .pick-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.pick-row b { font-size: 14px; color: #1f2937; }
.pick-row.sel { background: #f5f8ff; }
.pick-dot {
  width: 18px; height: 18px; flex: 0 0 18px; border-radius: 50%;
  border: 1.5px solid #d4d7de; position: relative;
}
.pick-dot.on { border-color: #3370ff; background: #3370ff; }
.pick-dot.on::after {
  content: ""; position: absolute; left: 5.5px; top: 2.5px;
  width: 4px; height: 8px; border: solid #fff; border-width: 0 2px 2px 0; transform: rotate(45deg);
}
.pick-row input[type="radio"] { display: none; }
.opt-row {
  display: flex; align-items: center; gap: 10px; padding: 13px 16px;
  border-bottom: 1px solid #f3f4f6; cursor: pointer; font-size: 14px;
}
.opt-row.last { border-bottom: none; }
.opt-row > span { color: #1f2937; }
.opt-row em { font-style: normal; margin-left: auto; color: #9ca3af; font-size: 13px; }
.opt-row.open em { color: #3370ff; }
.opt-row .arr {
  width: 8px; height: 8px; border-right: 1.5px solid #c2c7d0; border-bottom: 1.5px solid #c2c7d0;
  transform: rotate(45deg); transition: transform .18s; flex: 0 0 8px;
}
.opt-row .arr.up { transform: rotate(-135deg); border-color: #3370ff; }
.opt-body { padding: 4px 16px 14px; background: #fafbfc; border-bottom: 1px solid #f3f4f6; }
.fld.slim { margin: 10px 0 4px; }
.fld.slim span { font-size: 12.5px; color: #6b7280; }
.keep-chips { display: flex; gap: 8px; margin: 12px 0 2px; }
.keep-chips button {
  flex: 1; border: 1px solid #e5e7eb; background: #fff; border-radius: 10px;
  padding: 8px 0; font-size: 13px; color: #4b5563; cursor: pointer;
}
.keep-chips button.on { border-color: #3370ff; color: #3370ff; background: #f0f5ff; font-weight: 600; }
.btn.block { width: 100%; flex: none; }
.btn.block:disabled { opacity: .45; cursor: not-allowed; }
/* 目录浏览器（用户视角多选） */
.frow { display: flex; align-items: center; gap: 11px; padding: 11px 4px; border-bottom: 1px solid #f3f4f6; font-size: 14px; cursor: pointer; }
.frow .ic { font-size: 18px; flex: 0 0 auto; }
.frow .nm { flex: 1; min-width: 0; display: flex; align-items: baseline; gap: 8px; }
.frow .cnt { font-size: 11.5px; color: #9ca3af; }
.frow:active { background: #f7f8fa; }
/* 旧 .crumb/.csep 已由 .crumbC（方案C）替代 */
.hide-sys { display: flex; align-items: center; gap: 8px; font-size: 12px; color: #9ca3af; margin-top: 12px; }
.tg { width: 34px; height: 19px; background: #3370ff; border-radius: 999px; position: relative; flex: 0 0 34px; cursor: pointer; }
.tg::after { content: ""; position: absolute; right: 2.5px; top: 2.5px; width: 14px; height: 14px; border-radius: 50%; background: #fff; }
.tg.off { background: #d4d7de; }
.tg.off::after { right: auto; left: 2.5px; }
.modal-foot.col { flex-direction: column; align-items: stretch; gap: 9px; }
/* 目的地：云盘图标 + 链路可视化 */
.cloud-ic { font-size: 19px; flex: 0 0 auto; }
.flow-line { display: flex; align-items: center; gap: 7px; padding: 11px 16px; border-top: 1px solid #f3f4f6; flex-wrap: wrap; }
.flow-line .node { background: #f4f6fa; border-radius: 8px; padding: 5px 10px; font-size: 12px; color: #374151; }
.flow-line .node.hi { background: #eef4ff; color: #3370ff; font-weight: 600; }
.flow-line .lk { color: #c2c7d0; font-size: 11px; font-style: normal; }
/* 备份规则预设 */
.chips2 { display: flex; flex-wrap: wrap; gap: 9px; margin: 12px 0 4px; }
.chips2 button {
  font-size: 13px; border: 1px solid #e2e5eb; border-radius: 999px; padding: 7px 14px;
  color: #4b5563; background: #fff; cursor: pointer;
}
.chips2 button.on { border-color: #3370ff; color: #3370ff; background: #f0f5ff; font-weight: 600; }
.rule-detail { background: #fafbfc; border: 1px solid #f0f1f4; border-radius: 10px; padding: 10px 12px; margin: 10px 0; }
.rule-detail .rt { font-size: 12px; color: #6b7280; margin-bottom: 5px; }
.rule-detail code { display: inline-block; font-size: 11px; background: #eef0f4; border-radius: 5px; padding: 2px 7px; margin: 2px 4px 2px 0; color: #4b5563; font-family: ui-monospace, Menlo, monospace; }
.rule-prev { background: #f0f5ff; border: 1px dashed #b9cdfd; border-radius: 10px; padding: 9px 12px; font-size: 12px; color: #3370ff; line-height: 1.6; margin-bottom: 4px; }
.adv-row { display: flex; align-items: center; justify-content: space-between; padding: 11px 2px 4px; font-size: 13px; color: #6b7280; cursor: pointer; }
/* 仓库弹窗 */
.inrow { display: flex; gap: 8px; align-items: center; }
.inrow input { flex: 1; }
.tst-ok { color: #16a34a; font-size: 12.5px; }
.tst-bad { color: #dc2626; font-size: 12.5px; }
.pwrow { display: flex; gap: 6px; align-items: center; }
.pwrow input { flex: 1; }
.pwrow .eye {
  flex-shrink: 0; width: 34px; height: 34px; border: none; background: #f1f2f4;
  border-radius: 9px; cursor: pointer;
  display: flex; align-items: center; justify-content: center; color: #6b7280;
}
.pwrow .eye:hover { background: #e7e9ec; color: #374151; }
.pwrow .btn.sm { flex-shrink: 0; }
.uri-prev { font-size: 11.5px; color: #9ca3af; background: #f7f8fa; border-radius: 8px; padding: 7px 10px; margin: -4px 0 12px; word-break: break-all; }
/* 编辑备份来源地（仿飞牛） */
.mask.top { z-index: 70; }
.mask.top2 { z-index: 80; }

/* ==================== 任务操作面板（⋯ 菜单） ==================== */
.sheet {
  background: #fff; border-radius: 16px; width: 100%; max-width: 420px;
  padding: 10px 14px 14px; display: flex; flex-direction: column; gap: 2px;
  overscroll-behavior: contain;
}
.sheet-title {
  position: relative; display: flex; align-items: center; justify-content: center;
  text-align: center; font-size: 13px; color: #9ca3af; padding: 8px 34px 10px; min-height: 34px;
}
.sheet-title span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sheet-x {
  position: absolute; right: 0; top: 50%; transform: translateY(-50%);
  width: 32px; height: 32px; border: none; background: none; font-size: 16px;
  color: #9ca3af; cursor: pointer; border-radius: 9px; line-height: 1;
}
.sheet-x:hover, .sheet-x:active { background: #f4f5f7; color: #1f2937; }
.sheet-btn {
  border: none; background: #f5f7fa; border-radius: 12px; padding: 14px;
  font-size: 15px; color: #1f2937; cursor: pointer; margin-top: 8px;
  font-family: inherit; text-align: center;
}
.sheet-btn.primary { background: #eef4ff; color: #3370ff; font-weight: 600; }
.sheet-btn.danger { color: #dc2626; }
.src-sum { display: flex; justify-content: space-between; font-size: 12.5px; color: #6b7280; margin: 2px 2px 8px; }
.src-row.host { gap: 10px; }
.host-ic { font-size: 21px; }
.sp-name { flex: 1; min-width: 0; font-size: 13.5px; color: #1f2937; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mini-fold { flex: 0 0 auto; }
.add-path {
  display: block; width: 100%; border: none; background: none; cursor: pointer;
  color: #3370ff; font-size: 13.5px; text-align: left; padding: 12px 16px;
}
.add-path:active { background: #f7f8fa; }
.modal-foot.pair { display: flex; gap: 10px; align-items: stretch; }
.modal-foot.pair .btn { flex: 1; }
/* 目录选择器（仿飞牛大图标） */
.frow.big { padding: 8px 4px; gap: 13px; }
.frow.big .nm { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.frow.big .nm b { font-size: 14px; color: #1f2937; font-weight: 600; }
.frow.big .sub { font-size: 12px; color: #9ca3af; }
.frow.big.dis { opacity: .38; pointer-events: none; }
.fold-ic { flex: 0 0 auto; }
.fold-ic.back {
  width: 30px; height: 30px; border-radius: 8px; background: #eef0f4; color: #6b7280;
  display: flex; align-items: center; justify-content: center; font-size: 16px; font-style: normal;
}
.chev { font-style: normal; font-size: 22px; color: #c2c7d0; padding: 4px 8px; flex: 0 0 auto; }
.btn.block:disabled { opacity: .45; cursor: not-allowed; }

.fld { display: block; margin-bottom: 14px; font-size: 13px; }
.fld > span { display: block; color: #4b5563; margin-bottom: 6px; }
.fld input[type="text"], .fld input:not([type]), .fld input[type="password"],
.fld input[type="time"], .fld input[type="number"], .fld select, .fld textarea {
  width: 100%; box-sizing: border-box; padding: 9px 12px; border: 1px solid #d1d5db;
  border-radius: 8px; font-size: 14px;
}
.fld.row { display: flex; align-items: center; gap: 8px; }
.fld.row input { width: auto; }
/* 目的地两步式：目录选择按钮 + 云盘新建目录行 */
.dir-pick-btn {
  display: flex; align-items: center; justify-content: space-between; gap: 8px;
  width: 100%; box-sizing: border-box; padding: 10px 12px;
  border: 1px solid #d1d5db; border-radius: 8px; background: #fff;
  font-size: 14px; color: #111827; cursor: pointer; text-align: left;
}
.dir-pick-btn:disabled { color: #9ca3af; background: #f9fafb; cursor: not-allowed; }
.dir-pick-btn .chev-r { color: #c2c7d0; font-style: normal; font-size: 18px; }
.dir-pick-btn .ph { color: #9ca3af; }

/* 选择云端触发字段：卡片式（与弹层卡片同风格） */
.cloud-pick-btn {
  display: flex; align-items: center; gap: 10px; width: 100%; box-sizing: border-box;
  padding: 9px 12px; border: 1px solid #e5e7eb; border-radius: 10px;
  background: #fff; cursor: pointer; text-align: left; transition: border-color 0.15s, background 0.15s;
}
.cloud-pick-btn:hover { border-color: #3370ff; background: #fbfcff; }
.cloud-pick-btn .ph { color: #9ca3af; font-size: 14px; flex: 1; }
.cloud-pick-btn .chev-r { color: #c2c7d0; font-style: normal; font-size: 18px; flex-shrink: 0; }
.cpb-logo { max-width: 32px; max-height: 26px; object-fit: contain; flex-shrink: 0; display: block; }
.cpb-txt { flex: 1; min-width: 0; }
.cpb-txt b { display: block; font-size: 13.5px; font-weight: 600; color: #111827; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.cpb-txt small { display: block; font-size: 12px; color: #8b9098; margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
/* 云端目录：大面包屑 + 底部工具栏（方案C） */
.crumbC {
  display: flex; align-items: center; gap: 6px; padding: 9px 12px;
  background: #f7f8fa; border-radius: 10px; margin-bottom: 10px;
  font-size: 14px; overflow-x: auto; white-space: nowrap; scrollbar-width: none;
}
.crumbC::-webkit-scrollbar { display: none; }
.crumbC .cloudic {
  display: inline-flex; align-items: center; gap: 5px; flex-shrink: 0;
  color: #3370ff; font-weight: 600; cursor: pointer;
}
.crumbC a { color: #4b5563; text-decoration: none; cursor: pointer; flex-shrink: 0; }
.crumbC a:hover { color: #3370ff; }
.crumbC a.cur { color: #1f2937; font-weight: 700; cursor: default; }
.crumbC .sl { color: #c3c9d4; flex-shrink: 0; }
.toolbarC { display: flex; gap: 8px; flex: 1; }
.toolbarC .btn { flex: 1 1 0; min-width: 0; padding: 8px 14px; font-size: 13px; border-radius: 7px; }
.mkdir-btn {
  flex-shrink: 0; display: inline-flex; align-items: center; font-family: inherit;
  border: 1px solid #e2e6ee; background: #fff; color: #4b5563;
  font-size: 13.5px; padding: 0 14px; border-radius: 10px; cursor: pointer;
  transition: all .15s;
}
.mkdir-btn:hover { border-color: #3370ff; color: #3370ff; }
.mkdir-btn:disabled { color: #9ca3af; cursor: not-allowed; }
/* 新建云端目录弹窗 */
.dlg-mask {
  position: fixed; inset: 0; background: rgba(17,24,39,.42); z-index: 60;
  display: flex; align-items: center; justify-content: center;
}
.dlg {
  width: 320px; background: #fff; border-radius: 16px; padding: 20px 20px 16px;
  box-shadow: 0 18px 50px rgba(0,0,0,.25);
}
.dlg h4 { font-size: 15.5px; text-align: center; margin-bottom: 4px; }
.dlg .dsub { font-size: 12px; color: #9ca3af; text-align: center; margin-bottom: 14px; word-break: break-all; }
.dlg input {
  width: 100%; box-sizing: border-box; border: 1.5px solid #e2e6ee; border-radius: 10px;
  font-size: 14px; padding: 10px 12px; outline: none; transition: border .15s;
}
.dlg input:focus { border-color: #3370ff; }
.dlg .drow { display: flex; gap: 10px; margin-top: 14px; }
.dlg .drow button {
  flex: 1; padding: 9px 0; border-radius: 10px; font-size: 14px;
  cursor: pointer; border: 0; font-family: inherit;
}
.dlg .cancel { background: #f1f4f9; color: #4b5563; }
.dlg .ok { background: #3370ff; color: #fff; }
.dlg .ok:disabled { background: #a9c3f2; cursor: not-allowed; }
/* 任务执行计划：分段切换（方案A） */
.sched-seg { display: flex; background: #eef0f4; border-radius: 10px; padding: 3px; margin: 6px 0 14px; }
.sched-seg button {
  flex: 1; border: 0; background: transparent; padding: 8px 0; border-radius: 8px;
  font-size: 13px; color: #6b7280; cursor: pointer; transition: all .15s; font-family: inherit;
}
.sched-seg button.on { background: #fff; color: #3370ff; font-weight: 600; box-shadow: 0 1px 4px rgba(0,0,0,.08); }
.sched-inline { display: flex; align-items: center; gap: 8px; font-size: 13px; color: #374151; margin-bottom: 12px; }
.sched-inline input { width: 64px; border: 1px solid #d1d5db; border-radius: 8px; padding: 8px 10px; font-size: 14px; }
.sched-chips button { min-width: 48px; }
.cfg-label { font-size: 12px; color: #6b7280; margin: 4px 0 8px; }
.path-chips { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.chip {
  background: #eff6ff; color: #1d4ed8; border-radius: 8px; padding: 6px 10px; font-size: 12px;
  display: inline-flex; align-items: center; gap: 6px;
}
.chip b { cursor: pointer; opacity: 0.6; }
.repo-pick { display: flex; flex-direction: column; gap: 8px; margin-bottom: 12px; }
.repo-opt {
  display: flex; gap: 10px; align-items: flex-start; border: 1px solid #e5e7eb;
  border-radius: 10px; padding: 12px; cursor: pointer;
}
.repo-opt.sel { border-color: #3370ff; background: #eff6ff; }
.kv { display: flex; justify-content: space-between; padding: 9px 0; border-bottom: 1px dashed #f0f1f3; font-size: 13px; gap: 16px; }
.kv .k { color: #6b7280; flex-shrink: 0; }
.hint-box {
  font-size: 12px; color: #92400e; background: #fffbeb; border: 1px solid #fde68a;
  border-radius: 8px; padding: 10px 12px; margin-top: 10px;
}
.fld-hint { font-size: 11.5px; color: var(--t3); line-height: 1.6; margin-top: 6px; }

/* ==================== 日志 ==================== */
.runs-list { display: flex; flex-wrap: wrap; gap: 8px; padding: 0 20px 10px; align-items: center; }
.run-chip {
  border: 1px solid #e5e7eb; background: #f9fafb; border-radius: 999px; padding: 5px 12px;
  font-size: 12px; cursor: pointer; color: #4b5563;
}
.run-chip.on { background: #3370ff; color: #fff; border-color: #3370ff; }
.run-chip.current { background: #eef4ff; border-color: #3370ff; color: #3370ff; font-weight: 600; display: inline-flex; align-items: center; gap: 6px; }
.run-chip.current .run-chip-label { font-size: 11px; color: #6b7a99; font-weight: 500; }
.run-chip.current .run-chip-name { color: #3370ff; }
.runs-hist { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.hist-toggle {
  border: 1px dashed #d1d5db; background: transparent; border-radius: 999px; padding: 5px 12px;
  font-size: 12px; cursor: pointer; color: #6b7280;
}
.hist-toggle:hover { color: #3370ff; border-color: #3370ff; }
.log-box {
  flex: 1; margin: 0 20px 16px; padding: 12px; background: #0f172a; color: #e2e8f0;
  border-radius: 10px; overflow: auto; font-size: 11px; line-height: 1.6; white-space: pre-wrap; word-break: break-all;
}

/* 精简日志视图 */
.log-card { margin: 4px 20px 16px; border: 1px solid #eceef1; border-radius: 12px; background: #fff; overflow: hidden; }
.log-head { display: flex; align-items: center; gap: 8px; padding: 12px 16px; border-bottom: 1px solid #eceef1; font-size: 13px; }
.log-head b { font-size: 13px; }
.log-head .spacer { flex: 1; }
.lg-chip { font-size: 11px; padding: 3px 9px; border-radius: 20px; font-weight: 600; flex-shrink: 0; }
.lg-chip.ok { background: #e7f8ec; color: #16803a; }
.lg-chip.bad { background: #fdeaea; color: #c62828; }
.lg-chip.run { background: #e8f0ff; color: #2563eb; }
.sum-pct { font-weight: 700; color: #3370ff; font-variant-numeric: tabular-nums; }
.log-oneline { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; padding: 12px 16px; font-size: 13.5px; border-bottom: 1px solid #eceef1; }
.log-oneline .dim { color: #8b9098; font-size: 12px; }
.log-timeline { padding: 8px 16px 12px; display: flex; flex-direction: column; }
.lt-row { display: flex; align-items: center; gap: 10px; font-size: 13px; padding: 7px 0; position: relative; }
.lt-row:not(:last-child)::before { content: ""; position: absolute; left: 3px; top: 25px; bottom: -8px; width: 2px; background: #eceef1; }
.lt-t { color: #8b9098; font-variant-numeric: tabular-nums; flex-shrink: 0; width: 42px; font-size: 12px; }
.lt-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.lt-dot.d-start { background: #3370ff; }
.lt-dot.d-upload { background: #3370ff; }
.lt-dot.d-done { background: #22c55e; }
.lt-dot.d-warn { background: #f59e0b; }
.lt-dot.d-fail { background: #dc2626; }
.lt-msg { color: #1c1f23; }
.lt-row.warn .lt-msg { color: #b45309; }
.log-err { border-top: 1px solid #eceef1; }
.log-err summary { cursor: pointer; font-size: 12.5px; color: #c62828; font-weight: 600; padding: 10px 16px; list-style: none; }
.log-err pre { font-family: ui-monospace, Menlo, monospace; font-size: 11px; color: #7c3a3a; padding: 0 16px 12px; white-space: pre-wrap; word-break: break-all; }
.log-foot { display: flex; align-items: center; gap: 8px; padding: 10px 16px; border-top: 1px solid #eceef1; font-size: 12px; color: #8b9098; }
.log-foot .spacer { flex: 1; }
.raw-toggle { cursor: pointer; color: #8b9098; }
.raw-toggle:hover { color: #3370ff; }

/* ==================== 路径浏览器 ==================== */
.dir-list { max-height: 58vh; overflow-y: auto; overscroll-behavior: contain; -webkit-overflow-scrolling: touch; touch-action: pan-y; border: 1px solid #f3f4f6; border-radius: 8px; }
.dir-item { padding: 10px 12px; border-bottom: 1px solid #f9fafb; cursor: pointer; font-size: 14px; }
.dir-item:hover { background: #f5f7fa; }
.cur-path { color: #6b7280; }

/* ==================== Toast ==================== */
.toast {
  position: fixed; bottom: 32px; left: 50%; transform: translateX(-50%);
  background: #1f2937; color: #fff; padding: 11px 22px; border-radius: 10px;
  font-size: 14px; z-index: 9999; box-shadow: 0 8px 24px rgba(0, 0, 0, 0.2);
}
.toast.bad { background: #dc2626; }
.fade-enter-active, .fade-leave-active { transition: opacity 0.25s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }


/* ==================== 快照 Tab：仓库 → 快照 → 详情 → 还原 ==================== */
.sn-list { display: flex; flex-direction: column; gap: 11px; }

.sn-repo {
  background: #fff; border-radius: 12px; overflow: hidden;
  box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05);
}
.sn-repo.open { box-shadow: 0 3px 12px rgba(16, 24, 40, 0.09); }

.sn-head {
  display: flex; align-items: center; gap: 14px; padding: 15px 17px;
  cursor: pointer; transition: background 0.15s;
}
.sn-head:hover { background: #fbfcfe; }
.sn-repo.open .sn-head { border-bottom: 1px solid #f1f3f6; }

.sn-ic {
  width: 40px; height: 40px; flex: none; border-radius: 10px;
  background: #eef4ff; display: flex; align-items: center; justify-content: center;
}
.sn-body { flex: 1; min-width: 0; }
.sn-name { font-size: 14.5px; font-weight: 600; display: flex; align-items: center; gap: 9px; }
.sn-path {
  font-size: 11.5px; color: #9ca3af; margin-top: 3px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.sn-meta { display: flex; gap: 16px; margin-top: 8px; flex-wrap: wrap; }
.sn-meta span { font-size: 11.5px; color: #8b9098; }
.sn-meta b { color: #1f2937; font-size: 13px; font-weight: 600; margin-right: 3px; }

.sn-pill {
  font-size: 10.5px; font-weight: 600; padding: 2px 8px; border-radius: 20px; white-space: nowrap;
}
.sn-pill.ok { background: #e7f8ec; color: #16803a; }
.sn-pill.gray { background: #f3f4f6; color: #9ca3af; }
.sn-pill.blue { background: #eef4ff; color: #3370ff; }

.sn-chev {
  width: 24px; height: 24px; flex: none; display: flex; align-items: center; justify-content: center;
  color: #c0c4cc; transition: transform 0.18s, color 0.18s;
}
.sn-repo.open .sn-chev { transform: rotate(90deg); color: #3370ff; }

.sn-drop { padding: 6px 0 4px; }
.sn-loading { padding: 18px; text-align: center; font-size: 12.5px; color: #9ca3af; }
.sn-empty { padding: 22px; text-align: center; font-size: 12.5px; color: #9ca3af; }

.sn-grp { margin-bottom: 4px; }
.sn-grp-h { display: flex; align-items: center; gap: 9px; padding: 10px 17px 8px; }
.sn-dot { width: 7px; height: 7px; border-radius: 50%; flex: none; }
.sn-gn { font-size: 13px; font-weight: 600; color: #1f2937; }
.sn-gp {
  font-size: 11px; color: #b0b7c3; overflow: hidden; text-overflow: ellipsis;
  white-space: nowrap; max-width: 340px;
}
.sn-gc { margin-left: auto; font-size: 11.5px; color: #9ca3af; }

.sn-tbl { width: 100%; border-collapse: collapse; font-size: 12.5px; }
.sn-tbl th {
  text-align: left; font-weight: 500; color: #9ca3af; font-size: 11px; padding: 8px 17px;
  background: #fcfcfd; border-top: 1px solid #f3f4f6; border-bottom: 1px solid #f3f4f6; white-space: nowrap;
}
.sn-tbl td { padding: 10px 17px; border-bottom: 1px solid #f6f7f9; color: #374151; vertical-align: middle; }
.sn-tbl tbody tr { cursor: pointer; }
.sn-tbl tbody tr:hover td { background: #f7faff; }
.sn-tbl tbody tr:last-child td { border-bottom: none; }
.sn-tbl .t { font-weight: 600; color: #1f2937; white-space: nowrap; font-variant-numeric: tabular-nums; }
.sn-tbl .dim { color: #9ca3af; }
.sn-tbl .acts { text-align: right; white-space: nowrap; }
.sn-tbl .nowrap { white-space: nowrap; }
.sn-tbl .ellip { max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sn-tbl .ellip-sm { max-width: 138px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sn-link { color: #3370ff; font-size: 12px; font-weight: 600; white-space: nowrap; }

.sn-mlist { display: none; }

.sn-foot {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 17px 12px; border-top: 1px solid #f3f4f6; font-size: 11px; color: #b0b7c3;
}

/* 运行中的还原条 */
.sn-run {
  display: flex; align-items: center; gap: 11px; background: #fff; border-radius: 10px;
  padding: 11px 15px; margin-bottom: 11px; font-size: 12.5px;
  box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05);
}
.sn-run-t { font-weight: 600; color: #1f2937; white-space: nowrap; }
.sn-run-bar { flex: 1; height: 5px; border-radius: 4px; background: #eef0f3; overflow: hidden; }
.sn-run-bar i { display: block; height: 100%; border-radius: 4px; background: linear-gradient(90deg, #3370ff, #5ca0ff); }
.sn-run-p { font-variant-numeric: tabular-nums; color: #8b9098; }

/* 快照详情 */
.sd-bar { display: flex; align-items: center; gap: 7px; font-size: 12.5px; color: #9ca3af; margin-bottom: 12px; }
.sd-back {
  border: none; background: none; color: #3370ff; font-size: 12.5px; font-weight: 600;
  cursor: pointer; padding: 3px 6px; font-family: inherit; border-radius: 6px;
}
.sd-back:hover { background: #eef4ff; }
.sd-sep { color: #d5d9e0; }
.sd-crumb { color: #374151; font-weight: 600; }

.sd-card {
  background: #fff; border-radius: 12px; padding: 17px 19px; margin-bottom: 12px;
  box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05);
}
.sd-top { display: flex; align-items: flex-start; gap: 16px; }
.sd-l { min-width: 0; }
.sd-time { font-size: 20px; font-weight: 700; letter-spacing: -0.2px; font-variant-numeric: tabular-nums; }
.sd-sub { display: flex; align-items: center; gap: 9px; margin-top: 6px; font-size: 12px; color: #8b9098; flex-wrap: wrap; }
.sd-id { color: #9ca3af; }
.sd-acts { margin-left: auto; flex: none; display: flex; gap: 8px; }
.sd-kv {
  display: grid; grid-template-columns: repeat(4, 1fr); gap: 14px;
  margin-top: 16px; padding-top: 15px; border-top: 1px solid #f3f4f6;
}
.sd-kv span { display: block; font-size: 11px; color: #9ca3af; margin-bottom: 4px; }
.sd-kv b { font-size: 13.5px; font-weight: 600; color: #1f2937; font-variant-numeric: tabular-nums; }

.sd-panel {
  background: #fff; border-radius: 12px; margin-bottom: 12px; overflow: hidden;
  box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05);
}
.sd-panel.pad { padding: 16px 18px; }
.sd-ph {
  padding: 12px 17px; border-bottom: 1px solid #f3f4f6; font-size: 12.5px;
  font-weight: 600; color: #374151; display: flex; align-items: center; gap: 8px;
}
.sd-ph .cnt { font-weight: 400; color: #9ca3af; font-size: 11.5px; }
.sd-diff { display: flex; align-items: center; gap: 16px; padding: 13px 17px; font-size: 12.5px; flex-wrap: wrap; }
.sd-diff .add { color: #16803a; font-weight: 600; }
.sd-diff .mod { color: #b45309; font-weight: 600; }
.sd-diff .del { color: #b91c1c; font-weight: 600; }
.sd-diff .tot { margin-left: auto; color: #9ca3af; font-size: 11.5px; }
.sd-paths { padding: 5px 0; }
.sd-path { display: flex; align-items: center; gap: 10px; padding: 9px 17px; font-size: 12px; color: #374151; }
.sd-path .mono { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sd-tags { display: flex; gap: 7px; flex-wrap: wrap; padding: 13px 17px; }
.sd-tag { font-size: 11px; color: #6b7280; background: #f5f6f8; padding: 3px 9px; border-radius: 6px; }

/* 快照详情 · 目录树（桌面） */
.sd-tree-acts { margin-left: auto; display: flex; gap: 12px; }
.sd-tree-acts a { font-size: 12px; color: #3370ff; cursor: pointer; }
.sd-tree-acts a:hover { text-decoration: underline; }
.sd-build {
  display: flex; align-items: flex-start; gap: 10px; margin: 4px 16px 10px;
  padding: 10px 12px; background: #f5f8ff; border: 1px solid #dbe6ff; border-radius: 9px;
}
.sd-build > div { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.sd-build b { font-size: 12.5px; color: #1f2937; font-weight: 600; }
.sd-build span { font-size: 11.5px; color: #6b7280; line-height: 1.6; }
.sd-build.err { background: #fdf2f2; border-color: #f8d4d4; }
.sd-build.err b { color: #dc2626; }
.sdt-list { padding: 2px 10px 10px; }
.sdt-row {
  display: flex; align-items: center; gap: 7px; padding: 6px 8px 6px 6px;
  border-radius: 7px; font-size: 13px; cursor: pointer; user-select: none;
}
.sdt-row:hover { background: #f7f9fc; }
.sdt-row.on { background: #f0f6ff; }
.sdt-caret { width: 12px; flex: none; text-align: center; color: #b6bcc7; font-size: 9px; transition: transform .13s; }
.sdt-caret.open { transform: rotate(90deg); }
.sdt-caret.leaf { visibility: hidden; }
.sdt-ic { flex: none; }
.sdt-name { color: #1f2937; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sdt-name.sel { color: #3370ff; font-weight: 600; }
.sdt-meta { font-size: 11px; color: #b6bcc7; flex: none; }
.sdt-row .cbx { margin-left: auto; }
.sdt-row.file .sdt-name { color: #4b5563; font-weight: 400; }
.sdt-row.file .sdt-meta { color: #b6bcc7; }

/* ==================== 下拉刷新（顶部细条） ==================== */
.ptr-bar {
  position: fixed; top: 0; left: 0; right: 0; height: 3px; z-index: 2600;
  background: transparent; overflow: hidden; pointer-events: none;
}
.ptr-fill {
  display: block; height: 100%; width: 0;
  background: #9aa1ab; border-radius: 0 3px 3px 0;
  transition: width .05s linear, background .15s;
}
.ptr-bar.hot .ptr-fill { background: #3370ff; }
.ptr-bar.load .ptr-fill {
  width: 100% !important;
  background: linear-gradient(90deg, #dbe6ff 0%, #3370ff 45%, #dbe6ff 100%);
  background-size: 45% 100%;
  animation: ptrslide 1.1s linear infinite;
}
@keyframes ptrslide {
  from { background-position: -45% 0; }
  to { background-position: 145% 0; }
}
.ptr-tx {
  position: fixed; top: 9px; left: 0; right: 0; z-index: 2600;
  text-align: center; font-size: 11px; font-weight: 600; color: #8b9098;
  pointer-events: none; transition: opacity .16s, color .16s;
}
.ptr-tx.hot { color: #3370ff; }

/* ==================== 历史（备份 + 还原） ==================== */
.hist-bar { display: flex; align-items: center; gap: 10px; margin-bottom: 6px; }
.hist-seg {
  display: flex; gap: 4px; background: #eceef2; border-radius: 10px;
  padding: 3px; flex: 0 1 300px;
}
.hist-bar { width: 100%; }
.hist-seg button {
  flex: 1; border: none; background: transparent; font: inherit; font-size: 13px; font-weight: 600;
  color: #6b7280; padding: 7px 0; border-radius: 8px; cursor: pointer;
  display: flex; align-items: center; justify-content: center; gap: 6px;
}
.hist-seg button.on { background: #fff; color: #3370ff; box-shadow: 0 1px 2px rgba(16,24,40,.07); }
.hist-seg .n { font-size: 10.5px; font-weight: 700; color: #9aa1ab; background: #e4e7ec; border-radius: 20px; padding: 0 6px; line-height: 16px; }
.hist-seg button.on .n { color: #3370ff; background: #eef4ff; }

.hist-wrap { display: flex; gap: 18px; align-items: flex-start; }
.hist-wrap.narrow { display: block; }
.hist-list { flex: 1; min-width: 0; }
.hist-det {
  width: 340px; flex: none; background: #fff; border-radius: 12px; padding: 16px 18px;
  box-shadow: 0 1px 3px rgba(16,24,40,.06); position: sticky; top: 16px;
}

.hist-day { display: flex; align-items: center; gap: 10px; font-size: 11.5px; font-weight: 600; color: #9aa1ab; padding: 14px 4px 8px; }
.hist-day:first-child { padding-top: 4px; }
.hist-day i { flex: 1; height: 1px; background: #e8ebf0; }

.hist-item {
  background: #fff; border-radius: 11px; padding: 11px 14px; margin-bottom: 8px; cursor: pointer;
  box-shadow: 0 1px 2px rgba(16,24,40,.05); border: 1px solid transparent; transition: border-color .12s, background .12s;
}
.hist-item:hover { border-color: #e3ebff; }
.hist-item.on { border-color: #cfe0ff; background: #fafcff; }
.hi-top { display: flex; align-items: center; gap: 9px; }
.hi-nm { font-size: 13.5px; font-weight: 600; color: #1f2937; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hi-tm { margin-left: auto; font-size: 11.5px; color: #9aa1ab; flex: none; }
.hi-sub { font-size: 11.5px; color: #9aa1ab; margin-top: 5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hi-st { font-size: 10.5px; font-weight: 700; padding: 1.5px 7px; border-radius: 5px; flex: none; }
.hi-st.ok { background: #e7f8ee; color: #16803a; }
.hi-st.bad { background: #fdeaea; color: #b91c1c; }
.hi-st.run { background: #eef4ff; color: #3370ff; }
.hi-st.gray { background: #f2f4f7; color: #6b7280; }

.hi-open { border-top: 1px solid #f1f3f6; margin-top: 10px; padding-top: 10px; }
.hi-kv { display: flex; gap: 12px; font-size: 12px; line-height: 2; }
.hi-kv .k { color: #9aa1ab; width: 44px; flex: none; }
.hi-kv .v { color: #4b5563; min-width: 0; word-break: break-all; white-space: pre-line; }
.hi-tl { margin-top: 9px; border-top: 1px dashed #eef0f3; padding-top: 8px; }
.hi-tl .n { font-size: 11.5px; color: #6b7280; line-height: 1.95; display: flex; gap: 9px; }
.hi-tl .n i { font-style: normal; color: #b6bcc7; flex: none; }
.hi-ops { margin-top: 11px; display: flex; gap: 8px; }

.hd-top { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.hd-top b { font-size: 14px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hd-time { font-size: 11.5px; color: #9aa1ab; margin-bottom: 12px; }
.hd-foot { margin-top: 15px; display: flex; gap: 8px; }
.hist-loading { font-size: 12.5px; color: #9aa1ab; padding: 24px 4px; display: flex; align-items: center; gap: 8px; }

/* 快照详情 · 逐层下钻（手机） */
.sdc-crumb {
  display: flex; align-items: center; gap: 8px;
  padding: 7px 8px 7px 12px; margin: 0 0 8px; background: #f7f8fa; border-radius: 9px;
  font-size: 12.5px; color: #9ca3af;
}
.sdc-crumb-p { flex: 1; min-width: 0; display: flex; align-items: center; gap: 5px; flex-wrap: wrap; }
.sdc-crumb a { color: #3370ff; cursor: pointer; }
.sdc-crumb a.cur { color: #1f2937; font-weight: 600; }
.sdc-crumb .sl { color: #d5d9e0; }
.sdc-crumb .cbx { margin: 0; flex: none; }
.sdc-list { padding-bottom: 4px; }
.sdc-row {
  display: flex; align-items: center; gap: 10px; padding: 11px 12px;
  border-radius: 8px; font-size: 14px; cursor: pointer;
}
.sdc-row:active { background: #f7f9fc; }
.sdc-row.on { background: #f0f6ff; }
.sdc-back { width: 16px; text-align: center; color: #b6bcc7; }
.sdc-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: #1f2937; }
.sdc-name.sel { color: #3370ff; font-weight: 600; }
.sdc-go { color: #c4cad4; font-size: 18px; line-height: 1; }
.sdc-size { font-size: 11px; color: #b6bcc7; flex: none; margin-right: 4px; }
.sdc-more { padding: 10px 12px; font-size: 11.5px; color: #9ca3af; line-height: 1.7; background: #fafbfc; border-radius: 8px; margin-top: 8px; }
.sdc-row .cbx { margin-left: 0; }

.sd-foot { display: flex; align-items: center; gap: 9px; padding: 15px 0 4px; }
.sd-foot .sp { flex: 1; }

.rst-ops { padding: 0 16px 12px; display: flex; justify-content: flex-end; }

/* 还原配置：整页形态下收窄，步骤条改成不拉伸的卡片 */
.sn-rst { max-width: 780px; }
.sn-rst .rst-stepper {
  display: flex; align-items: flex-start; justify-content: center;
  padding: 15px 22px; margin-bottom: 14px; background: #fff; border: none;
  border-radius: 12px; box-shadow: 0 1px 3px rgba(16, 24, 40, 0.05);
}
.sn-rst .rst-step-dot { flex: none; min-width: 62px; }
.sn-rst .rst-step-line { width: 44px; }
.sn-rst .rst-step-title { margin: 14px 0 10px; }
.sn-rst .seg { max-width: 300px; margin: 0 auto; }
.sn-rst .conf-grid { max-width: 640px; grid-template-columns: repeat(3, 1fr); }

/* =====================================================================
   方案 B · 概览仪表盘（飞牛原生风格统一层）
   放在响应式块之前：桌面生效，手机规则仍可覆盖
   ===================================================================== */
.page {
  --c-blue: #3370ff; --c-blue-h: #2b5fe0; --c-blue-50: #eef4ff; --c-blue-100: #dce7ff;
  --c-green: #18a058; --c-green-50: #e8f7ee;
  --c-orange: #f08a24; --c-orange-50: #fff3e6;
  --c-red: #e5484d; --c-red-50: #fdecec;
  --c-purple: #7b61ff; --c-purple-50: #f1ecff;
  --t1: #1f2329; --t2: #4e5969; --t3: #86909c; --t4: #c0c6cf;
  --ln: #eef0f3; --ln2: #e5e6eb;
  --ic: #1f2329;
  color: var(--t1);
}
.num { font-variant-numeric: tabular-nums; }
.content { max-width: 1200px; padding: 24px 32px 60px; }
.dhead h1 { font-size: 20px; }

/* 侧边栏 */
.sb-item { border-radius: 8px; padding: 9px 12px; color: var(--t2); font-family: inherit; }
.sb-item.on { background: var(--c-blue-50); color: var(--c-blue); }
.sb-cnt { margin-left: auto; font-size: 12px; color: var(--t3); font-weight: 400; font-variant-numeric: tabular-nums; }
.sb-item.on .sb-cnt { color: var(--c-blue); }
.sb-cnt.run { background: var(--c-blue); color: #fff !important; border-radius: 9px; padding: 0 6px; line-height: 17px; font-size: 11px; }

/* 按钮：飞牛式描边次按钮 */
.btn {
  display: inline-flex; align-items: center; justify-content: center; gap: 4px;
  border: 1px solid transparent; border-radius: 6px; padding: 6px 15px; font-size: 13px; line-height: 18px;
  font-family: inherit; white-space: nowrap; transition: background .15s, border-color .15s, color .15s;
}
.btn:hover { opacity: 1; }
.btn:disabled { opacity: .5; cursor: not-allowed; }
.btn.primary { background: var(--c-blue); border-color: var(--c-blue); }
.btn.primary:hover:not(:disabled) { background: var(--c-blue-h); border-color: var(--c-blue-h); }
.btn.ghost { background: #fff; border-color: #dcdfe6; color: var(--t1); }
.btn.ghost:hover:not(:disabled) { border-color: var(--c-blue); color: var(--c-blue); }
.btn.ghost.danger { color: var(--c-red); }
.btn.ghost.danger:hover:not(:disabled) { border-color: var(--c-red); color: var(--c-red); }
.btn.sm { padding: 4px 10px; font-size: 12px; border-radius: 6px; }
.icon-btn { width: 30px; height: 30px; border-radius: 6px; color: var(--t2); }
.icon-btn:hover { background: #f2f3f5; color: var(--c-blue); }
.menu { border-radius: 8px; padding: 4px; box-shadow: 0 8px 24px rgba(15, 23, 42, .14); top: 34px; }
.menu button { border-radius: 5px; padding: 7px 12px; font-family: inherit; }

/* 通用面板 */
.panel { background: #fff; border-radius: 12px; margin-bottom: 16px; }
.panel-h { display: flex; align-items: center; gap: 14px; padding: 14px 20px; border-bottom: 1px solid var(--ln); }
.panel-h h3 { margin: 0; font-size: 15px; font-weight: 600; }
.panel-foot { padding: 10px 20px; font-size: 12px; color: var(--t3); border-top: 1px solid var(--ln); }
.pseg { display: inline-flex; background: #f2f3f5; border-radius: 6px; padding: 2px; }
.pseg button {
  border: none; background: transparent; padding: 3px 12px; border-radius: 4px; font-size: 12px;
  color: var(--t2); cursor: pointer; font-family: inherit; font-variant-numeric: tabular-nums;
}
.pseg button.on { background: #fff; color: var(--t1); font-weight: 500; box-shadow: 0 1px 2px rgba(0, 0, 0, .08); }

/* 统计卡 */
.kpis { display: grid; grid-template-columns: repeat(4, 1fr); gap: 14px; margin-bottom: 16px; }
.kpi { background: #fff; border-radius: 12px; padding: 16px 18px; display: flex; align-items: center; gap: 14px; min-width: 0; }
.kpi > div { min-width: 0; }
.kpi > div > span { display: block; font-size: 12px; color: var(--t3); }
.kpi b { display: block; font-size: 20px; font-weight: 600; margin-top: 2px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.kpi b.sm { font-size: 16px; }
.kpi b small { font-size: 12px; font-weight: 400; color: var(--t3); margin-left: 3px; }
.kpi-ic { width: 40px; height: 40px; border-radius: 10px; display: flex; align-items: center; justify-content: center; flex: none; }
.c-blue { background: var(--c-blue-50); color: var(--c-blue); }
.c-green { background: var(--c-green-50); color: var(--c-green); }
.c-orange { background: var(--c-orange-50); color: var(--c-orange); }
.c-red { background: var(--c-red-50); color: var(--c-red); }
.c-purple { background: var(--c-purple-50); color: var(--c-purple); }

/* 备份页 · 概览横幅 */
.ov-hero {
  position: relative; overflow: hidden; display: flex; align-items: center; gap: 26px;
  background: linear-gradient(120deg, #fff 0%, #f4f8ff 60%, #e9f1ff 100%);
  border-radius: 12px; padding: 22px 26px; margin-bottom: 16px;
  --tone: var(--c-green); --tone-bg: #dce7ff;
}
.ov-hero.tone-warn { --tone: var(--c-orange); --tone-bg: #fde7cf; background: linear-gradient(120deg, #fff 0%, #fffaf3 60%, #fff1e0 100%); }
.ov-hero.tone-run { --tone: var(--c-blue); }
.ov-hero::after {
  content: ""; position: absolute; right: -60px; top: -60px; width: 240px; height: 240px; border-radius: 50%;
  background: radial-gradient(circle, rgba(51, 112, 255, .10), transparent 70%); pointer-events: none;
}
.ov-ring { position: relative; width: 96px; height: 96px; flex: none; }
.ov-ring-svg { width: 96px; height: 96px; transform: rotate(-90deg); }
.ov-ring-bg { stroke: var(--tone-bg); }
.ov-ring-fg { stroke: var(--tone); transition: stroke-dashoffset .6s; }
.ov-ring-c { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; color: var(--tone); }
.ov-spin { width: 26px; height: 26px; border: 3px solid var(--c-blue-100); border-top-color: var(--c-blue); border-radius: 50%; animation: rot .9s linear infinite; }
.ov-txt { position: relative; z-index: 1; min-width: 0; }
.ov-txt h2 { margin: 0; font-size: 20px; font-weight: 600; }
.ov-txt p { margin: 6px 0 0; font-size: 13px; color: var(--t2); line-height: 1.6; }
.ov-acts { margin-top: 12px; display: flex; gap: 8px; }
.ov-kpis { margin-left: auto; display: flex; position: relative; z-index: 1; flex: none; }
.ov-kpi { padding: 0 24px; border-left: 1px solid #dfe6f3; }
.ov-kpi:first-child { border-left: none; }
.ov-kpi span { display: block; font-size: 12px; color: var(--t3); white-space: nowrap; }
.ov-kpi b { display: block; font-size: 22px; font-weight: 600; margin-top: 4px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.ov-kpi b.ok { color: var(--c-green); }
.ov-kpi b.sm { font-size: 16px; }
.ov-kpi b small { font-size: 12px; font-weight: 400; color: var(--t3); margin-left: 2px; }

/* 备份页 · 任务表格 */
.ttbl { width: 100%; border-collapse: collapse; }
.ttbl th { text-align: left; font-weight: 400; font-size: 12px; color: var(--t3); padding: 9px 16px; background: #fafbfc; white-space: nowrap; }
.ttbl th:first-child, .ttbl td:first-child { padding-left: 20px; }
.ttbl th:last-child, .ttbl td:last-child { padding-right: 16px; }
.ttbl td { padding: 13px 16px; border-top: 1px solid var(--ln); vertical-align: middle; font-size: 13px; }
.ttbl tbody tr { cursor: pointer; }
.ttbl tbody tr:hover td { background: #f8faff; }
.ttbl .r { text-align: right; }
.ttbl .tc-acts { justify-content: flex-end; }
.tt-empty td { text-align: center; color: var(--t3); padding: 36px 0; cursor: default; }
.tt-name { display: flex; align-items: center; gap: 12px; min-width: 0; }
.tt-ic { width: 36px; height: 36px; border-radius: 8px; display: flex; align-items: center; justify-content: center; flex: none; }
.tt-ic.c-ok { background: var(--c-green-50); color: var(--c-green); }
.tt-ic.c-warn { background: var(--c-orange-50); color: var(--c-orange); }
.tt-ic.c-run { background: var(--c-blue-50); color: var(--c-blue); }
.tt-ic.c-gray { background: #f2f3f5; color: var(--t3); }
.tt-nm { min-width: 0; }
.tt-nm b { display: block; font-weight: 500; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tt-nm small { display: block; font-size: 12px; color: var(--t3); margin-top: 2px; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tt-route { display: flex; align-items: center; gap: 6px; color: var(--t4); }
.chip2 { background: #f5f7fa; border-radius: 4px; padding: 2px 8px; font-size: 12px; color: var(--t2); white-space: nowrap; max-width: 120px; overflow: hidden; text-overflow: ellipsis; }
.tag2 {
  display: inline-flex; align-items: center; gap: 5px; height: 22px; padding: 0 8px; border-radius: 4px;
  font-size: 12px; white-space: nowrap; max-width: 220px; overflow: hidden; text-overflow: ellipsis;
}
.tag2 .d { width: 6px; height: 6px; border-radius: 50%; background: currentColor; flex: none; }
.tag2.ok { background: var(--c-green-50); color: var(--c-green); }
.tag2.warn { background: var(--c-orange-50); color: var(--c-orange); }
.tag2.run { background: var(--c-blue-50); color: var(--c-blue); }
.tag2.gray { background: #f2f3f5; color: var(--t2); }
.tt-prog { width: 170px; }
.tt-prog-t { display: flex; justify-content: space-between; align-items: center; font-size: 12px; color: var(--c-blue); margin-bottom: 5px; }
.tt-prog-t span { display: inline-flex; align-items: center; gap: 5px; }
.tt-prog-t b { font-variant-numeric: tabular-nums; }
.tt-prog .spin { width: 10px; height: 10px; border-width: 2px; }
.tt-prog .bar { height: 6px; margin: 0; background: var(--c-blue-100); }
.tt-prog-s { font-size: 11px; color: var(--t3); margin-top: 4px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.spark { display: flex; gap: 3px; align-items: center; height: 22px; }
.spark i { width: 6px; height: 18px; border-radius: 2px; background: var(--c-green); opacity: .85; }
.spark i.f { background: var(--c-red); }
.spark i.r { background: var(--c-blue); }
.spark i.n { background: var(--t4); }
.spark i.e { background: #eef0f3; opacity: 1; }

/* 备份页 · 空态 */
.ov-empty { text-align: center; padding: 64px 20px; }
.ov-empty-ic { width: 88px; height: 88px; border-radius: 24px; background: var(--c-blue-50); color: var(--c-blue); margin: 0 auto 18px; display: flex; align-items: center; justify-content: center; }
.ov-empty b { display: block; font-size: 17px; font-weight: 600; }
.ov-empty p { margin: 8px 0 22px; font-size: 13px; color: var(--t3); }
.ov-empty-steps { display: flex; justify-content: center; gap: 36px; margin-top: 36px; font-size: 12px; color: var(--t3); flex-wrap: wrap; }

/* 快照页 */
.sn-list { gap: 16px; }
.sn-repo, .sn-repo.open { box-shadow: none; }
.sn-head { padding: 16px 20px; }
.sn-ic { width: 44px; height: 44px; }
.sn-logo { max-width: 42px; max-height: 30px; object-fit: contain; display: block; }
.sn-name { font-size: 15px; }
.sn-pill { font-size: 12px; font-weight: 400; padding: 0 8px; line-height: 20px; border-radius: 4px; }
.sn-pill.ok { background: var(--c-green-50); color: var(--c-green); }
.sn-pill.gray { background: #f2f3f5; color: var(--t2); }
.sn-pill.blue { background: var(--c-blue-50); color: var(--c-blue); }
.sn-meta { display: none; }
.sn-stats { display: flex; gap: 28px; margin-right: 6px; flex: none; }
.sn-stats div { text-align: right; }
.sn-stats span { display: block; font-size: 12px; color: var(--t3); }
.sn-stats b { display: block; font-size: 14px; font-weight: 600; margin-top: 2px; font-variant-numeric: tabular-nums; }
.sn-grp-h { padding: 12px 20px 8px; border-top: 1px solid var(--ln); }
.sn-grp:first-child .sn-grp-h { border-top: none; }
.sn-dot { width: 8px; height: 8px; }
.sn-tbl { font-size: 13px; }
.sn-tbl th { font-weight: 400; font-size: 12px; color: var(--t3); padding: 9px 20px; background: #fafbfc; border-color: var(--ln); }
.sn-tbl td { padding: 12px 20px; border-color: var(--ln); }
.sn-tbl tbody tr:hover td { background: #f8faff; }
.sn-link { font-weight: 400; font-size: 13px; }
.sn-foot { padding: 10px 20px; background: #fafbfc; font-size: 12px; color: var(--t3); border-color: var(--ln); }
.sn-run { border: 1px solid var(--c-blue-100); box-shadow: none; padding: 11px 16px; margin-bottom: 16px; font-size: 13px; }
.sn-run-t { font-weight: 500; }

/* 快照详情 */
.sd-bar { font-size: 13px; margin-bottom: 14px; }
.sd-back { font-weight: 500; font-size: 13px; }
.sd-card, .sd-panel { box-shadow: none; margin-bottom: 16px; }
.sd-card { padding: 20px 24px; }
.sd-top { align-items: center; gap: 16px; }
.sd-ic {
  width: 52px; height: 52px; border-radius: 12px; flex: none; color: #fff;
  background: linear-gradient(135deg, #8a7bff, #7b61ff); box-shadow: 0 6px 14px rgba(123, 97, 255, .28);
  display: flex; align-items: center; justify-content: center;
}
.sd-time { font-size: 18px; font-weight: 600; }
.sd-kv { margin-top: 18px; padding-top: 16px; border-color: var(--ln); }
.sd-kv span { font-size: 12px; color: var(--t3); }
.sd-kv b { font-size: 16px; }
.sd-ph { padding: 14px 20px; font-size: 14px; color: var(--t1); border-color: var(--ln); }
.sd-diff { padding: 14px 20px; gap: 10px; }
.sd-diff .add, .sd-diff .mod, .sd-diff .del { font-size: 12px; font-weight: 500; padding: 2px 10px; border-radius: 4px; }
.sd-diff .add { background: var(--c-green-50); color: var(--c-green); }
.sd-diff .mod { background: var(--c-blue-50); color: var(--c-blue); }
.sd-diff .del { background: var(--c-red-50); color: var(--c-red); }
.sd-diff .z { background: #f2f3f5; color: var(--t3); }
.sdt-list { padding: 4px 12px 12px; }
.sdt-row { padding: 7px 8px 7px 6px; border-radius: 6px; }
.sdt-row:hover { background: #f8faff; }
.sdt-row.on { background: var(--c-blue-50); }
.cbx { width: 16px; height: 16px; border-radius: 4px; border-width: 1.5px; font-size: 10px; }
.cbx.half { opacity: .55; }

/* 还原向导 */
.sn-rst { max-width: 900px; }
.sn-rst .rst-stepper { padding: 20px 28px; margin-bottom: 16px; box-shadow: none; justify-content: space-between; }
.sn-rst .rst-step-dot { flex-direction: row; gap: 8px; min-width: 0; }
.sn-rst .rst-step-dot .n { width: 28px; height: 28px; font-size: 13px; background: #f2f3f5; color: var(--t3); }
.sn-rst .rst-step-dot .lb { font-size: 13px; color: var(--t3); }
.sn-rst .rst-step-dot.on .n { background: var(--c-blue); color: #fff; box-shadow: 0 0 0 4px var(--c-blue-50); }
.sn-rst .rst-step-dot.on .lb { color: var(--t1); font-weight: 600; }
.sn-rst .rst-step-dot.done .n { background: var(--c-blue-50); color: var(--c-blue); }
.sn-rst .rst-step-dot.done .lb { color: var(--t2); }
.sn-rst .rst-step-line { flex: 1; width: auto; height: 2px; margin: 13px 12px 0; background: var(--ln); }
.sn-rst .rst-step-line.done { background: var(--c-blue); }
.sn-rst .rst-step-title { font-size: 16px; font-weight: 600; margin: 0 0 12px; }
.sn-rst .sd-panel.pad { padding: 22px 26px; }
.rpick-item { border-radius: 10px; }
.rpick-item:hover { border-color: var(--c-blue); background: #f7faff; }
.conf-card { border-width: 1px; border-color: var(--ln2); }
.conf-card.on { box-shadow: 0 0 0 3px var(--c-blue-50); }
.snap-timeline { border-color: var(--ln); border-radius: 10px; max-height: 300px; }
.snap-row { padding: 11px 14px; border-color: var(--ln); }
.snap-row.on { background: var(--c-blue-50); }
.pick-files-btn { font-weight: 500; border-radius: 10px; padding: 16px; }
.seg { background: #f2f3f5; border-radius: 6px; padding: 2px; }
.seg button { border-radius: 4px; padding: 6px 10px; }
.seg button.on { color: var(--t1); font-weight: 500; box-shadow: 0 1px 2px rgba(0, 0, 0, .08); }

/* 历史页 */
.hist-seg { background: #f2f3f5; border-radius: 6px; padding: 2px; flex: 0 1 240px; }
.hist-seg button { border-radius: 4px; padding: 5px 0; font-weight: 500; }
.hist-seg button.on { color: var(--t1); box-shadow: 0 1px 2px rgba(0, 0, 0, .08); }
.hist-seg .n { background: transparent; color: var(--t3); padding: 0; }
.hist-seg button.on .n { background: transparent; color: var(--t3); }
.hist-bar { margin-bottom: 4px; }
.hist-wrap { gap: 16px; }
.hist-det { width: 380px; padding: 20px; box-shadow: none; }
.hist-day { font-size: 12px; font-weight: 500; color: var(--t3); }
.hist-item { border-radius: 10px; padding: 12px 16px; box-shadow: none; }
.hist-item:hover { border-color: var(--c-blue-100); }
.hist-item.on { border-color: var(--c-blue); background: #f8faff; box-shadow: 0 0 0 3px var(--c-blue-50); }
.hi-nm { font-weight: 500; font-size: 14px; }
.hi-st { font-size: 12px; font-weight: 400; padding: 0 8px; line-height: 20px; border-radius: 4px; }
.hi-st.ok { background: var(--c-green-50); color: var(--c-green); }
.hi-st.bad { background: var(--c-red-50); color: var(--c-red); }
.hi-st.run { background: var(--c-blue-50); color: var(--c-blue); }
.hd-top b { font-size: 16px; }
.hi-kv { font-size: 13px; line-height: 1.6; padding: 7px 0; border-bottom: 1px dashed var(--ln); }
.hi-kv .k { width: 64px; color: var(--t3); }
.hi-kv .v { color: var(--t1); }
.hi-tl .n { font-size: 12px; }

/* 弹窗 */
.mask { background: rgba(15, 23, 42, .38); }
.modal { border-radius: 12px; box-shadow: 0 20px 50px rgba(15, 23, 42, .25); }
.modal-head { padding: 16px 20px; border-bottom: 1px solid var(--ln); }
.modal-head h3 { font-size: 16px; font-weight: 600; }
.x { border-radius: 6px; color: var(--t3); }
.x:hover { background: #f2f3f5; color: var(--t1); }
.modal-body { padding: 16px 20px; }
.modal-foot { border-color: var(--ln); justify-content: flex-end; }
.fld > span { color: var(--t2); }
.fld input[type="text"], .fld input:not([type]), .fld input[type="password"],
.fld input[type="time"], .fld input[type="number"], .fld select, .fld textarea {
  border-color: #dcdfe6; border-radius: 6px; font-size: 13px; padding: 8px 11px; outline: none; font-family: inherit;
}
.fld input:focus, .fld select:focus, .fld textarea:focus { border-color: var(--c-blue); box-shadow: 0 0 0 3px var(--c-blue-50); }
.sec-card { border-color: var(--ln); border-radius: 10px; }
.sec-head b { font-size: 14px; font-weight: 600; }
.chips2 button, .keep-chips button { border-radius: 6px; font-family: inherit; }
.chips2 button.on, .keep-chips button.on { font-weight: 500; background: var(--c-blue-50); }
.sched-seg { background: #f2f3f5; border-radius: 6px; padding: 2px; }
.sched-seg button { border-radius: 4px; padding: 6px 0; }
.sched-seg button.on { color: var(--t1); font-weight: 500; box-shadow: 0 1px 2px rgba(0, 0, 0, .08); }
.rule-prev { border-radius: 8px; }
.run-chip { border-radius: 4px; background: #f2f3f5; border-color: transparent; color: var(--t2); }
.run-chip.on { background: var(--c-blue); color: #fff; }
.run-chip.current { border-radius: 4px; border-color: transparent; background: var(--c-blue-50); }
.log-card { border-color: var(--ln); border-radius: 10px; }
.log-box { background: #1f2329; border-radius: 8px; }
.lg-chip { font-size: 12px; font-weight: 400; border-radius: 4px; }
.toast { border-radius: 8px; font-size: 13px; }
.dlg { border-radius: 12px; }
.dlg .drow button { border-radius: 6px; }

/* ---------- 方案 B 补齐：搜索 / 快照表格 / 文件表格 / 向导 / 历史 / 弹窗 ---------- */
.psearch {
  margin-left: auto; height: 30px; width: 220px; border: 1px solid var(--ln2); border-radius: 6px;
  display: flex; align-items: center; gap: 6px; padding: 0 10px; color: var(--t4); background: #fff; cursor: text;
}
.psearch:focus-within { border-color: var(--c-blue); box-shadow: 0 0 0 3px var(--c-blue-50); }
.psearch input { flex: 1; min-width: 0; border: none; outline: none; background: transparent; font: inherit; font-size: 12px; color: var(--t1); }
.psearch i { font-style: normal; font-size: 11px; color: var(--t3); cursor: pointer; }
.menu hr { border: none; border-top: 1px solid var(--ln); margin: 4px 0; }
.tag2.pur { background: var(--c-purple-50); color: var(--c-purple); }
.dhead { gap: 12px; justify-content: flex-start; }
.dhead .dhead-acts { margin-left: auto; }
.dhead > .btn.primary { margin-left: auto; }
.dhead-sub { font-size: 13px; color: var(--t3); }

/* 快照表格：较上一份 */
.dl { display: inline-block; font-size: 12px; padding: 0 6px; line-height: 20px; border-radius: 4px; margin-right: 4px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.dl.a { background: var(--c-green-50); color: var(--c-green); }
.dl.d { background: var(--c-red-50); color: var(--c-red); }
.dl.z { background: #f2f3f5; color: var(--t3); }
.dl.dim { padding: 0; color: var(--t4); }
.sn-sep { font-style: normal; color: var(--ln2); margin: 0 8px; }
.sn-run-go { color: var(--c-blue); cursor: pointer; margin-left: 4px; white-space: nowrap; }

/* 快照详情：标题区内联统计（桌面），顶部还原按钮移到底部操作栏 */
.sd-card { display: flex; align-items: center; gap: 20px; }
.sd-card .sd-top { flex: 1; min-width: 0; }
.sd-card .sd-acts { display: none; }
.sd-card .sd-kv { display: flex; margin: 0; padding: 0; border: none; flex: none; }
.sd-card .sd-kv > div { padding: 0 20px; border-left: 1px solid var(--ln); }
.sd-card .sd-kv > div:first-child { border-left: none; }

/* 快照详情：文件表格 */
.sd-files { overflow: visible; }
.sd-files .sd-ph { gap: 12px; }
.fpath { display: flex; align-items: center; gap: 4px; font-size: 13px; font-weight: 400; flex-wrap: wrap; min-width: 0; }
.fpath a { color: var(--t2); padding: 2px 4px; border-radius: 4px; cursor: pointer; }
.fpath a:hover { background: #f2f3f5; color: var(--c-blue); }
.fpath a.cur { color: var(--t1); font-weight: 500; cursor: default; background: none; }
.fpath .sl { color: var(--t4); }
.fpath b { font-weight: 500; }
.fpath .cnt { font-size: 12px; color: var(--t3); margin-left: 6px; }
.ftbl { width: 100%; border-collapse: collapse; font-size: 13px; }
.ftbl th { text-align: left; font-weight: 400; font-size: 12px; color: var(--t3); padding: 9px 12px; background: #fafbfc; }
.ftbl td { padding: 10px 12px; border-top: 1px solid var(--ln); }
.ftbl tbody tr { cursor: pointer; }
.ftbl tbody tr:hover td { background: #f8faff; }
.ftbl tbody tr.on td { background: var(--c-blue-50); }
.ftbl .c { width: 44px; padding-left: 20px; padding-right: 0; }
.ftbl .w { width: 140px; white-space: nowrap; font-variant-numeric: tabular-nums; }
.ftbl .g { width: 36px; color: var(--t4); font-size: 16px; text-align: center; }
.ftbl .dim { color: var(--t3); }
.ftbl .ellip { max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ftbl .cbx { margin: 0; }
.ftbl .emp td { text-align: center; color: var(--t3); padding: 28px 0; cursor: default; }
.ftbl .emp td .mini-spin { vertical-align: -1px; }
.fn { display: inline-flex; align-items: center; gap: 10px; min-width: 0; }
.fn .nm { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 520px; }
.fn .nm.sel { color: var(--c-blue); font-weight: 500; }
.fn.back { color: var(--t3); }
.sd-files .sdc-more { margin: 8px 16px; }
.sd-selbar {
  position: sticky; bottom: 0; z-index: 3; display: flex; align-items: center; gap: 12px;
  padding: 12px 20px; background: #fff; border-top: 1px solid var(--ln);
  border-radius: 0 0 12px 12px; box-shadow: 0 -4px 12px rgba(15, 23, 42, .04); font-size: 13px;
}
.sd-selbar .cnt-t b { color: var(--c-blue); }
.sd-selbar .sp { flex: 1; }
.sd-selbar .dim { color: var(--t3); }
.sel-chips { display: flex; gap: 6px; flex-wrap: wrap; min-width: 0; }
.sel-chips span { background: var(--c-blue-50); color: var(--c-blue); border-radius: 4px; padding: 2px 8px; font-size: 12px; display: inline-flex; gap: 6px; align-items: center; max-width: 180px; }
.sel-chips span i { font-style: normal; cursor: pointer; opacity: .7; }
.sel-chips span.more { background: #f2f3f5; color: var(--t2); }
.sd-selbar .clr { font-size: 12px; color: var(--c-blue); cursor: pointer; }

/* 还原向导：步骤摘要 / 任务卡 / 目的地卡 */
.sn-rst .rst-step-dot { cursor: default; }
.sn-rst .rst-step-dot.done { cursor: pointer; }
.sn-rst .rst-step-dot.done:hover .lb { color: var(--c-blue); }
.lbw { display: flex; flex-direction: column; min-width: 0; }
.lbw .sm { font-size: 11px; color: var(--t3); max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-top: 1px; }
.sn-rst .rpick-list { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; padding: 0; max-height: none; }
.rpick-item { padding: 14px 16px; gap: 14px; }
.rpick-item.on { border-color: var(--c-blue); background: #f7faff; box-shadow: 0 0 0 3px var(--c-blue-50); }
.rp-ic { width: 40px; height: 40px; }
.rp-ic.c-ok { background: var(--c-green-50); color: var(--c-green); }
.rp-ic.c-warn { background: var(--c-orange-50); color: var(--c-orange); }
.rp-ic.c-run { background: var(--c-blue-50); color: var(--c-blue); }
.rp-ic.c-gray { background: #f2f3f5; color: var(--t3); }
.dest-opts { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 14px; }
.dest-opt {
  display: flex; align-items: center; gap: 14px; padding: 14px 16px; text-align: left; font-family: inherit;
  border: 1px solid var(--ln2); border-radius: 10px; background: #fff; cursor: pointer; transition: .15s;
}
.dest-opt:hover { border-color: var(--c-blue); }
.dest-opt.on { border-color: var(--c-blue); background: #f7faff; box-shadow: 0 0 0 3px var(--c-blue-50); }
.dest-opt .ic { width: 40px; height: 40px; border-radius: 10px; display: flex; align-items: center; justify-content: center; flex: none; }
.dest-opt .tx { flex: 1; min-width: 0; }
.dest-opt b { display: block; font-size: 14px; font-weight: 500; color: var(--t1); }
.dest-opt small { display: block; font-size: 12px; color: var(--t3); margin-top: 3px; }
.dest-opt .rd { width: 16px; height: 16px; border-radius: 50%; border: 1.5px solid #c9cdd4; flex: none; position: relative; }
.dest-opt.on .rd { border-color: var(--c-blue); }
.dest-opt.on .rd::after { content: ""; position: absolute; inset: 3px; border-radius: 50%; background: var(--c-blue); }
.sn-rst .dest-note, .sn-rst .target-row { margin-top: 0; }
.sn-rst .sd-foot { gap: 10px; }

/* 历史：失败筛选 / 列表图标 / 详情 */
.hist-bar { gap: 12px; }
.hist-fail { display: inline-flex; align-items: center; gap: 6px; font-size: 13px; color: var(--t2); cursor: pointer; user-select: none; }
.hist-fail input { accent-color: var(--c-red); margin: 0; }
.hist-fail.on { color: var(--c-red); }
.hi-row { display: flex; align-items: center; gap: 12px; }
.hi-ic { width: 32px; height: 32px; border-radius: 8px; flex: none; display: flex; align-items: center; justify-content: center; }
.hi-ic.k-ok { background: var(--c-green-50); color: var(--c-green); }
.hi-ic.k-bad { background: var(--c-red-50); color: var(--c-red); }
.hi-ic.k-run { background: var(--c-blue-50); color: var(--c-blue); }
.hi-ic.k-gray { background: #f2f3f5; color: var(--t3); }
.hi-main { flex: 1; min-width: 0; }
.hd-tags { display: flex; gap: 6px; margin-bottom: 10px; }
.hd-top { margin-bottom: 4px; }
.hd-prog { display: flex; align-items: center; gap: 10px; margin: 0 0 12px; }
.hd-prog .bar { flex: 1; margin: 0; height: 6px; background: var(--c-blue-100); }
.hd-prog b { font-size: 12px; color: var(--c-blue); font-variant-numeric: tabular-nums; }

/* 任务向导：编号分区 / 目的地双列 / 底部三按钮 */
.sec-head b { display: inline-flex; align-items: center; gap: 8px; }
.sec-n {
  width: 22px; height: 22px; border-radius: 6px; background: var(--c-blue-50); color: var(--c-blue);
  font-style: normal; font-size: 12px; font-weight: 600; display: inline-flex; align-items: center; justify-content: center;
}
.dest-card { padding: 14px 16px 4px; }
.dest-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 14px; }
.dest-card > .fld, .dest-grid > .fld { margin-bottom: 12px; }
.dest-card .flow-line { margin: 0 -16px; }
.pw-note { font-size: 12px; color: var(--t3); margin-top: 6px; line-height: 1.6; }
.modal.tall { width: 640px; }
.wiz-foot { gap: 10px; }

/* 日志：右侧抽屉 + 数字块 */
.drawer-mask { justify-content: flex-end; align-items: stretch; padding: 0; }
.modal.log-drawer { width: 600px; max-width: 100%; height: 100vh; max-height: none; border-radius: 12px 0 0 12px; animation: drawerin .2s ease-out; }
@keyframes drawerin { from { transform: translateX(40px); opacity: .4; } to { transform: none; opacity: 1; } }
.log-drawer .modal-head { gap: 12px; }
.log-drawer .modal-head h3 { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.log-drawer .runs-list { padding: 12px 20px; border-bottom: 1px solid var(--ln); background: #fafbfc; }
.log-stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(110px, 1fr)); gap: 10px; margin: 16px 20px 4px; }
.log-stats div { background: #f7f8fa; border-radius: 8px; padding: 10px 12px; min-width: 0; }
.log-stats span { display: block; font-size: 12px; color: var(--t3); }
.log-stats b { display: block; font-size: 15px; font-weight: 600; margin-top: 3px; font-variant-numeric: tabular-nums; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.log-drawer .log-card { margin-top: 12px; }

/* 删除确认 */
.modal.del-dlg { width: 420px; }
.del-dlg .modal-body { text-align: center; padding: 26px 24px 8px; }
.del-ic { width: 52px; height: 52px; border-radius: 50%; background: var(--c-red-50); color: var(--c-red); margin: 0 auto 14px; display: flex; align-items: center; justify-content: center; }
.del-dlg h3 { margin: 0; font-size: 16px; font-weight: 600; word-break: break-all; }
.del-dlg p { margin: 8px 0 0; font-size: 13px; color: var(--t3); line-height: 1.7; }
.del-dlg .modal-foot { border-top: none; justify-content: center; padding-bottom: 22px; }
.del-dlg .modal-foot .btn { flex: 0 0 120px; }
.btn.danger-fill { background: var(--c-red); border-color: var(--c-red); color: #fff; }
.btn.danger-fill:hover:not(:disabled) { background: #d23c41; border-color: #d23c41; }

/* 选择目录 / 还原文件选择器：行样式对齐表格 */
.frow { border-color: var(--ln); padding: 10px 8px; border-radius: 0; }
.frow:hover { background: #f8faff; }
.frow.big.sel { background: var(--c-blue-50); }
.crumbC { border-radius: 8px; background: #fafbfc; border: 1px solid var(--ln); }
.dir-list { border-color: var(--ln); border-radius: 8px; }
.pk-build, .sd-build { border-radius: 8px; }

/* 提示条：顶部白底 + 图标 */
.toast {
  top: 24px; bottom: auto; background: #fff; color: var(--t1); border-radius: 8px;
  padding: 10px 16px; display: flex; align-items: center; gap: 8px;
  box-shadow: 0 8px 24px rgba(15, 23, 42, .16); max-width: calc(100vw - 32px);
}
.toast.bad { background: #fff; color: var(--t1); }
.toast-ic { width: 18px; height: 18px; border-radius: 50%; background: var(--c-green); color: #fff; font-style: normal; font-size: 11px; font-weight: 700; display: flex; align-items: center; justify-content: center; flex: none; }
.toast.bad .toast-ic { background: var(--c-red); }

/* ==================== 响应式：≤768px 切换为原生手机布局 ==================== */
@media (max-width: 768px) {
  .page { display: block; background: #f2f4f7; }
  .sidebar { display: none; }
  .content { padding: 12px 14px 80px; }

  /* 手机：底部导航（备份任务/云盘仓库/还原）+ 右下角悬浮 ⊕，仿飞牛虚拟机 App */
  .bnav {
    display: flex; position: fixed; left: 0; right: 0; bottom: 0; z-index: 25;
    background: #fff; border-top: 1px solid #eef0f3;
    padding-bottom: env(safe-area-inset-bottom);
  }
  .bnav button {
    flex: 1; border: none; background: none; cursor: pointer;
    display: flex; flex-direction: column; align-items: center; gap: 3px;
    padding: 8px 0 9px; font-size: 11.5px; color: #6b7280;
  }
  .bnav button.on { color: #3370ff; font-weight: 600; }
  .fab {
    display: flex; position: fixed; right: 20px; bottom: 86px; z-index: 30;
    width: 54px; height: 54px; border-radius: 50%; border: none;
    background: #3370ff; color: #fff; cursor: pointer;
    align-items: center; justify-content: center;
    box-shadow: 0 6px 18px rgba(51, 112, 255, .42);
  }
  .fab:active { transform: scale(.94); }
  .content { padding-bottom: 140px; }

  /* ===== 弹窗改底部抽屉（仿虚拟机「导入虚拟机」样式） ===== */
  .mask { align-items: flex-end; padding: 0; }
  .mask .modal {
    width: 100%; max-height: 93%; border-radius: 16px 16px 0 0;
    animation: sheetup .22s ease-out;
  }
  @keyframes sheetup { from { transform: translateY(45%); opacity: .5; } to { transform: none; opacity: 1; } }
  .sheet-head { justify-content: center; position: relative; padding: 18px 20px 12px; }
  .sheet-head h3 { font-size: 17px; }
  .sheet-head .x { position: absolute; right: 14px; top: 14px; font-size: 18px; }
  .modal .fld > span { font-weight: 600; color: #1f2937; font-size: 14px; margin-bottom: 8px; }
  .modal .fld input[type="text"], .modal .fld input:not([type]), .modal .fld input[type="password"],
  .modal .fld input[type="time"], .modal .fld input[type="number"], .modal .fld select, .modal .fld textarea {
    background: #f5f6f8; border: none; border-radius: 12px; padding: 14px; font-size: 15px;
  }
  .modal .fld input:focus, .modal .fld select:focus, .modal .fld textarea:focus { outline: 2px solid #b8d0ff; }
  .modal-foot { padding: 12px 16px calc(14px + env(safe-area-inset-bottom)); }
  .modal-foot .btn.primary { flex: 2; padding: 13px; border-radius: 12px; font-size: 15px; }
  .modal-foot .btn.ghost { flex: 1; padding: 13px; border-radius: 12px; }
  /* even：两个按钮等宽 */
  .modal-foot.even .spacer { flex: 0 0 auto; }
  .modal-foot.even .btn { flex: 1 1 0; min-width: 0; }
  /* 文件选择器：手机上目录行与「选择整个目录」竖排 */
  .pk-curdir { flex-direction: column; align-items: flex-start; gap: 8px; }
  .pk-curdir .btn { width: 100%; }
  .pk-modal .toolbarC { flex-wrap: wrap; }
  .pk-modal .toolbarC .btn.primary { flex: 1 1 100%; }
  .frow.big .nm .sub { display: none; }
  .hide-m { display: none !important; }
  .dhead { display: none; }
  /* 历史：两个子页签平分整行 */
  .hist-bar { gap: 0; }
  .hist-seg { flex: 1 1 100%; }

  /* 表格卡片流 */
  .task-list { display: none; }
  .mcard-list { display: flex; flex-direction: column; gap: 12px; margin-top: 12px; }
  .mcard { background: #fff; border-radius: 14px; padding: 16px 16px 8px; }
  .mc-top { display: flex; align-items: flex-start; gap: 12px; margin-bottom: 10px; }
  .mc-title { flex: 1; min-width: 0; }
  .mc-title > b { font-size: 15px; }
  .mc-row {
    display: flex; justify-content: space-between; gap: 14px;
    font-size: 13.5px; padding: 7px 0; border-top: 1px solid #f7f8fa;
  }
  .mc-row span:first-child { color: #9ca3af; flex-shrink: 0; }
  .mc-row span:last-child { color: #1f2937; text-align: right; word-break: break-all; }

  /* 弹窗全屏化（底部滑出） */
  .mask { padding: 0; align-items: flex-end; }
  .modal { width: 100%; border-radius: 16px 16px 0 0; max-height: 92vh; }
  .modal.tall { height: 92vh; }
  .modal.wide { height: 92vh; }
  .sheet-mask { align-items: flex-end; background: rgba(0, 0, 0, 0.4); }
  .sheet {
    background: #fff; border-radius: 16px 16px 0 0; width: 100%; padding: 10px 14px calc(14px + env(safe-area-inset-bottom));
    display: flex; flex-direction: column; gap: 2px;
  }
  .sheet-title {
    position: relative; text-align: center; font-size: 13px; color: #9ca3af;
    padding: 8px 34px 10px; display: flex; align-items: center; justify-content: center;
    min-height: 34px;
  }
  .sheet-title span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sheet-x {
    position: absolute; right: 0; top: 50%; transform: translateY(-50%);
    width: 32px; height: 32px; border: none; background: none; font-size: 16px;
    color: #9ca3af; cursor: pointer; border-radius: 9px; line-height: 1;
  }
  .sheet-x:hover, .sheet-x:active { background: #f4f5f7; color: #1f2937; }
  .sheet-btn {
    border: none; background: #f5f7fa; border-radius: 12px; padding: 14px;
    font-size: 15px; color: #1f2937; cursor: pointer; margin-top: 8px;
  }
  .sheet-btn.primary { background: #eef4ff; color: #3370ff; font-weight: 600; }
  .sheet-btn.danger { color: #dc2626; }
  .sheet-btn.cancel { background: transparent; color: #9ca3af; }

  .repo-grid { grid-template-columns: 1fr; }

  /* 仓库 Tab：桌面表格 → 手机分组卡片 */
  .rp-crumb { display: none; }
  .rp-back { display: block; }
  .rp-search input { width: 92px; }
  .rp-card { padding: 14px; gap: 12px; }
  .rp-go { display: none; }
  .rp-meta { gap: 14px; }
  .rp-sum { gap: 18px; padding: 12px 14px; }
  .rp-sum b { font-size: 14px; }
  .rp-grp { display: none; }
  .rp-mlist { display: block; }
  .rp-mgrp { margin-bottom: 14px; }
  .rp-mgrp-t { font-size: 13px; font-weight: 600; padding: 0 2px 8px; color: #1f2937; }
  .rp-mgrp-t em { margin-left: auto; font-style: normal; font-size: 11.5px; color: #9ca3af; font-weight: 400; }
  .run-cur { display: none; }
  .steps-preview { justify-content: center; }
  .ws-arrow { display: none; }
  .wiz-step { flex-direction: row; }
  .ws-t { font-size: 11px; }
  /* 快照 Tab：仓库卡 + 快照卡片 */
  .sn-head { padding: 13px 14px; gap: 11px; }
  .sn-ic { width: 38px; height: 38px; border-radius: 9px; }
  .sn-name { font-size: 13.5px; gap: 7px; }
  .sn-meta { gap: 13px; margin-top: 6px; }
  .sn-tbl { display: none; }
  .sn-grp { display: none; }
  .sn-mlist { display: block; padding: 2px 0 6px; }
  .sn-grp-h { padding: 8px 14px 6px; }
  .sn-gp { display: none; }
  .sn-mgrp { padding: 0 14px; margin-bottom: 10px; }
  .sn-mgrp-t { display: flex; align-items: center; gap: 7px; font-size: 12px; font-weight: 600; padding: 4px 0 7px; color: #1f2937; }
  .sn-mgrp-t em { margin-left: auto; font-style: normal; font-size: 11px; color: #9ca3af; font-weight: 400; }
  .sn-mcard { background: #fafbfc; border-radius: 9px; padding: 10px 12px; margin-bottom: 6px; }
  .sn-mtop { display: flex; align-items: center; }
  .sn-mtop b { font-size: 12.5px; font-variant-numeric: tabular-nums; }
  .sn-mgo { margin-left: auto; font-size: 11.5px; color: #3370ff; font-weight: 600; }
  .sn-msub { font-size: 11px; color: #9ca3af; margin-top: 4px; }
  .sn-foot { padding: 8px 14px 11px; }
  .sn-run { padding: 10px 13px; gap: 9px; }

  /* 快照详情 */
  .sd-card { padding: 14px; }
  .sd-top { flex-direction: row; align-items: flex-start; gap: 10px; }
  .sd-time { font-size: 17px; }
  .sd-acts { margin-left: auto; width: auto; flex: none; }
  .sd-acts .btn { flex: 0 0 auto; justify-content: center; padding: 8px 14px; font-size: 12.5px; }
  .sd-kv { grid-template-columns: 1fr 1fr; gap: 12px; }
  .sd-diff { gap: 12px; padding: 12px 14px; }
  .sd-diff .tot { margin-left: 0; width: 100%; }
  .sd-ph { padding: 11px 14px; }
  .sd-path { padding: 8px 14px; }
  .sd-tags { padding: 12px 14px; }
  .sd-panel.pad { padding: 14px; }
  .sdc-row { padding: 12px 6px; }
  .sd-build { margin: 4px 10px 10px; }

  /* 还原配置：步骤条左右对称、位置 tab 撑满、上一步/下一步固定到底部 */
  .sn-rst .rst-stepper { padding: 15px 16px; }
  .sn-rst .rst-step-dot { flex: 1 1 0; min-width: 0; }
  .sn-rst .rst-step-line { width: 18px; }
  .sn-rst .seg { max-width: none; width: 100%; margin: 0; }
  .sn-rst .sd-foot {
    position: fixed; left: 0; right: 0; z-index: 2400;
    bottom: calc(59px + env(safe-area-inset-bottom));
    margin: 0; padding: 10px 14px;
    background: #fff; border-top: 1px solid #eef0f3;
    box-shadow: 0 -2px 12px rgba(16, 24, 40, .06);
  }
  .sn-rst .sd-foot .sp { display: none; }
  .sn-rst .sd-foot .btn { flex: 1 1 0; min-width: 0; justify-content: center; padding: 12px; border-radius: 12px; font-size: 15px; }
  /* 冲突策略：手机上改单列横排，避免文字被挤成两行 */
  .sn-rst .conf-grid { grid-template-columns: 1fr; gap: 8px; max-width: none; }
  .sn-rst .conf-card { flex-direction: row; align-items: center; gap: 10px; padding: 13px 14px; }
  .sn-rst .conf-card .conf-tx { flex: 1; padding-right: 26px; }
  .sn-rst .conf-card .conf-tx b { font-size: 14px; }
  .sn-rst .conf-card .conf-rd { top: 50%; right: 12px; transform: translateY(-50%); }


  /* ===== 方案 B · 手机适配 ===== */
  .task-panel { display: none; }
  .ov-hero { padding: 16px; gap: 14px; flex-wrap: wrap; margin-bottom: 4px; }
  .ov-hero::after { display: none; }
  .ov-ring, .ov-ring-svg { width: 60px; height: 60px; }
  .ov-ring-c svg { width: 26px; height: 26px; }
  .ov-spin { width: 20px; height: 20px; }
  .ov-txt { flex: 1; }
  .ov-txt h2 { font-size: 17px; }
  .ov-txt p { font-size: 12px; margin-top: 4px; }
  .ov-acts { display: none; }
  .ov-kpis { margin-left: 0; width: 100%; padding-top: 12px; border-top: 1px solid #e3e9f5; }
  .ov-kpi { flex: 1; padding: 0 10px; }
  .ov-kpi:first-child { padding-left: 0; }
  .ov-kpi b { font-size: 16px; }
  .ov-kpi span { font-size: 11px; }
  .ov-empty { padding: 44px 16px; }
  .ov-empty-steps { flex-direction: column; gap: 8px; margin-top: 20px; }
  .kpis { grid-template-columns: 1fr 1fr; gap: 10px; margin-bottom: 12px; }
  .kpi { padding: 12px; gap: 10px; }
  .kpi-ic { width: 34px; height: 34px; border-radius: 9px; }
  .kpi-ic svg { width: 17px; height: 17px; }
  .kpi b { font-size: 17px; }
  .kpi b.sm { font-size: 14px; }
  .sn-stats { display: none; }
  .sn-meta { display: flex; }
  .sn-list { gap: 11px; }
  .sd-ic { display: none; }
  .hist-det { width: auto; }
  .btn.sm { padding: 5px 10px; }
  .modal-foot { justify-content: stretch; }
  .sn-rst .rst-step-dot { flex-direction: column; gap: 4px; }
  .sn-rst .rst-step-dot .lb { font-size: 10.5px; }
  .sn-rst .rst-step-dot .n { width: 22px; height: 22px; font-size: 12px; }
  .sn-rst .rst-step-dot.on .n { box-shadow: none; }
  .sn-rst .rst-step-line { flex: 0 0 14px; margin: 10px 0 0; }
  .sn-rst .sd-panel.pad { padding: 14px; }
  /* ===== 方案 B 补齐 · 手机 ===== */
  .dhead-sub { display: none; }
  .psearch { width: 140px; }
  .sd-card { display: block; }
  .sd-card .sd-acts { display: flex; }
  .sd-card .sd-kv { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--ln); }
  .sd-card .sd-kv > div { padding: 0; border-left: none; }
  .lbw .sm { display: none; }
  .sn-rst .rpick-list, .dest-opts { grid-template-columns: 1fr; }
  .hist-bar { flex-wrap: wrap; }
  .hist-fail { margin-left: auto; }
  .dest-grid { grid-template-columns: 1fr; }
  .modal.tall { width: 100%; }
  .wiz-foot .btn { flex: 1 1 0; min-width: 0; padding: 12px 8px; border-radius: 12px; font-size: 14px; }
  .drawer-mask { align-items: flex-end; }
  .modal.log-drawer { width: 100%; height: 92vh; border-radius: 16px 16px 0 0; animation: sheetup .22s ease-out; }
  .log-stats { grid-template-columns: 1fr 1fr; }
  .modal.del-dlg { width: 100%; }
  .toast { top: calc(12px + env(safe-area-inset-top)); }

}

/* =====================================================================
   图标统一：无底色 + 单一墨色
   所有内容区图标去掉底色块，颜色一律取 --ic（墨黑），不随备份状态变化；
   状态信息只由右侧状态标签表达。
   ===================================================================== */
.tt-ic, .hi-ic, .rp-ic, .sn-ic, .sd-ic, .ic, .ov-empty-ic, .ov-ring-c,
.fold-ic, .src-ico, .host-ic, .sdt-ic, .mini-fold,
.tc-icon, .empty-icon { background: none; box-shadow: none; color: var(--ic); }
/* 带状态色的容器：同样清空底色，只留尺寸和图标 */
.tt-ic.c-ok, .tt-ic.c-warn, .tt-ic.c-run, .tt-ic.c-gray,
.hi-ic.k-ok, .hi-ic.k-bad, .hi-ic.k-run, .hi-ic.k-gray,
.rp-ic.c-ok, .rp-ic.c-warn, .rp-ic.c-run, .rp-ic.c-gray,
.ic.c-blue, .ic.c-green, .ic.c-orange, .ic.c-red, .ic.c-purple,
.fold-ic.back, .dest-opt .ic { background: none; box-shadow: none; color: var(--ic); }
/* 图标本体兜底：即使容器没写颜色也统一 */
.page .tt-ic svg, .page .hi-ic svg, .page .rp-ic svg, .page .sn-ic svg, .page .sd-ic svg,
.page .ic svg, .page .ov-empty-ic svg, .page .ov-ring-c svg,
.page .fold-ic svg, .page .src-ico svg, .page .host-ic svg,
.page .sdt-ic svg, .page .tc-icon svg, .page .empty-icon svg { color: var(--ic); }

/* =====================================================================
   动效层
   原则：只动 transform / opacity；微交互 100ms、小元素 180ms；
   进场快出慢停 cubic-bezier(.16,1,.3,1)，退场先慢后快（本版仅入场）。
   ===================================================================== */
@keyframes mask-in {
  from { opacity: 0; }
}
@keyframes modal-in {
  from { opacity: 0; transform: translateY(6px) scale(.96); }
}
@keyframes toast-in {
  /* Toast 自身靠 translateX(-50%) 居中，关键帧里必须带上，否则动画期间会横向跳位 */
  from { opacity: 0; transform: translate(-50%, -8px) scale(.98); }
  to { opacity: 1; transform: translate(-50%, 0) scale(1); }
}
@keyframes drop-in {
  from { opacity: 0; transform: translateY(-4px); }
}

/* 遮罩淡入；带自己动画的 sheet / drawer 只淡遮罩，不碰面板 */
.mask { animation: mask-in .16s ease-out; }
.mask > .modal:not(.log-drawer) { animation: modal-in .18s cubic-bezier(.16, 1, .3, 1); }

.toast { animation: toast-in .18s cubic-bezier(.16, 1, .3, 1); }

/* 仓库行展开：内容淡入并轻微下移，高度不做动画（需要 JS 测高，留到下一档） */
.sn-drop { animation: drop-in .2s cubic-bezier(.16, 1, .3, 1); }

/* 按压反馈：瞬时缩放，不加 transition，按下即响应、松开即回弹 */
.btn:active:not(:disabled),
.sb-item:active,
.run-chip:active,
.hist-toggle:active,
.eye:active,
.x:active,
.dest-opt:active {
  transform: scale(.97);
}
/* 步骤点是整列容器，只缩里面那个 22px 的圆 */
.rst-step-dot:active .n { transform: scale(.88); }

/* 窄屏弹窗本来就是底部上滑的 sheet，用回它自己的动画，
   否则会被上面那条 specificity 更高的 modal-in 压成居中缩放 */
@media (max-width: 768px) {
  .mask > .modal:not(.log-drawer) { animation: sheetup .22s ease-out; }
}
/* 侧栏是整块，缩得再轻一点（FAB 在移动端已有自己的 scale(.94)，这里不覆盖） */
.sb-item:active { transform: scale(.98); }


/* ==================== 在线更新 ==================== */
.upd-link { display: inline-flex; align-items: center; gap: 6px; margin: 2px 0 4px; padding: 0; border: none; background: none;
  font: inherit; color: var(--c-blue, #3370ff); cursor: pointer; }
.upd-link:hover { text-decoration: underline; }
.upd-link.hot { font-weight: 600; }
.upd-dot { width: 7px; height: 7px; border-radius: 50%; background: #e5484d; }
.upd-bar { display: flex; align-items: center; gap: 8px; margin: 0 0 14px; padding: 9px 14px; border-radius: 10px;
  font-size: 13px; cursor: pointer; border: 1px solid transparent; }
.upd-bar.tone-info { background: #eef4ff; color: #2b5fe0; border-color: #dce7ff; }
.upd-bar.tone-warn { background: #fff3e6; color: #b45309; border-color: #fde2c4; }
.upd-bar.tone-bad { background: #fdecec; color: #c2353a; border-color: #f8d0d1; }
.upd-bar-go { margin-left: auto; display: inline-flex; align-items: center; gap: 2px; font-weight: 600; white-space: nowrap; }
.upd-modal { width: 480px; }
.upd-vers { display: flex; gap: 10px; margin-bottom: 12px; }
.upd-vers > div { flex: 1; background: #f7f8fa; border-radius: 10px; padding: 10px 12px; display: flex; flex-direction: column; gap: 4px; }
.upd-vers span { font-size: 12px; color: #6b7280; }
.upd-vers b { font-size: 15px; color: #1f2937; }
.upd-vers b.new { color: #3370ff; }
.upd-tip { font-size: 13px; color: #6b7280; margin: 0 0 10px; display: flex; align-items: center; gap: 6px; }
.upd-tip.warn { color: #b45309; }
.upd-tip.bad { color: #c2353a; }
.upd-tip.ok { color: #18a058; }
.upd-notes { max-height: 42vh; overflow-y: auto; border: 1px solid #f0f1f3; border-radius: 10px; padding: 4px 14px; margin-bottom: 12px; }
.upd-note { padding: 10px 0; }
.upd-note + .upd-note { border-top: 1px dashed #eceef1; }
.upd-note-h { font-weight: 600; font-size: 13.5px; color: #1f2937; margin-bottom: 6px; display: flex; gap: 8px; align-items: baseline; }
.upd-note-h span { font-weight: 400; font-size: 12px; color: #9ca3af; }
.upd-note ul { margin: 0; padding-left: 18px; }
.upd-note li { font-size: 12.5px; color: #4b5563; line-height: 1.65; }
.upd-prog { margin: 6px 0 12px; }
.upd-prog-bar { height: 6px; border-radius: 3px; background: #eef0f3; overflow: hidden; }
.upd-prog-bar i { display: block; height: 100%; background: #3370ff; border-radius: 3px; transition: width .3s; }
.upd-prog-bar i.pulse { animation: updpulse 1.2s ease-in-out infinite; }
@keyframes updpulse { 50% { opacity: .45; } }
.upd-prog-t { font-size: 12.5px; color: #6b7280; margin-top: 6px; }
.upd-foot-tip { font-size: 12px; color: #9ca3af; margin: 0 0 8px; line-height: 1.6; }
.upd-reset { display: flex; align-items: center; gap: 10px; font-size: 12.5px; color: #6b7280; margin-bottom: 6px; flex-wrap: wrap; }
.upd-reset .link-btn.danger { color: #c2353a; }
@media (max-width: 768px) {
  .upd-modal { width: 100%; }
  .upd-vers { flex-wrap: wrap; }
}
</style>
