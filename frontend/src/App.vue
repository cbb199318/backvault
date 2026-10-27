<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import BDrop from "./BDrop.vue";
import CloudPicker from "./CloudPicker.vue";
import TPick from "./TPick.vue";

// ---------------------------------------------------------------------------
// 基础
// ---------------------------------------------------------------------------
// API 前缀：网关下页面路径为 /app/encbackup/，直连/开发时为 /，从地址栏自动推导
const API = ref(location.pathname.replace(/\/index\.html$/, "").replace(/\/+$/, ""));
const nav = ref("tasks"); // tasks | repos | restore
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

async function loadAll() {
  try {
    const [st, cf] = await Promise.all([api("/api/status"), api("/api/config")]);
    status.value = st;
    if (st.user_root) USER_ROOT.value = st.user_root;
    cfg.repos = cf.repos || [];
    cfg.tasks = cf.tasks || [];
    if (st.running_task) startPolling();
  } catch (e) {
    showToast("无法连接后端：" + e.message, "bad");
  }
}

function startPolling() {
  if (pollTimer) return;
  pollTimer = setInterval(async () => {
    try {
      const p = await api("/api/progress");
      Object.assign(progress, p);
      if (!p.task_id) {
        clearInterval(pollTimer);
        pollTimer = null;
        loadAll();
      }
    } catch (e) { /* 忽略 */ }
  }, 2000);
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
async function delTask(t) {
  if (!confirm(`确定删除任务「${t.name}」吗？（不影响云端已有快照）`)) return;
  try {
    await api(`/api/tasks/${t.id}/delete`, { method: "POST" });
    showToast("任务已删除");
    loadAll();
  } catch (e) {
    showToast(e.message, "bad");
  }
  menuOpen.value = "";
  sheet.show = false;
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
  show: false, editing: null, saving: false, openRule: false, openPlan: false, openKeep: false, openAdv: false,
  form: {
    id: "", name: "", paths: [], excludes: "", repo_id: "",
    sched_mode: "manual", daily_time: "03:00", interval_hrs: 24, keep_last: 3, enabled: true,
    daily_days: 1, weekly_days: [1], monthly_days: [1],
  },
});
function openWizard(task) {
  wiz.editing = !!task;
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
    });
  } else {
    Object.assign(wiz.form, {
      id: "", name: "", paths: [], excludes: SYS_TMP_DEFAULT.join("\n"), repo_id: "",
      dest_cloud: "", dest_subdir: "",
      sched_mode: "manual", daily_time: "03:00", interval_hrs: 24, keep_last: 3, enabled: true,
      daily_days: 1, weekly_days: [1], monthly_days: [1],
    });
  }
}
const destReady = computed(() => !!(wiz.form.dest_cloud && wiz.form.dest_subdir));
const wizReady = computed(() => !!(wiz.form.name.trim() && wiz.form.paths.length && (destReady.value || wiz.form.repo_id)));
function closeWizard() { wiz.show = false; }

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
  return "存储空间1/我的文件/" + shortPath(p);
}

// 目录选择器（仿飞牛：大图标列表，点行=选中，右侧 › 进入子级）
// 用户根目录从后端 status.user_root 读取（fpk=/vol1/1000，Docker=USER_ROOT 环境变量）
const USER_ROOT = ref("/vol1/1000");
const browser = reactive({ show: false, path: "/", dirs: [], loading: false, showAll: false });
async function openBrowser() {
  browser.show = true;
  browser.showAll = false;
  await browseTo(USER_ROOT.value);
}
async function browseTo(p) {
  browser.loading = true;
  try {
    const r = await api("/api/browse", { method: "POST", body: JSON.stringify({ path: p, show_all: browser.showAll }) });
    browser.dirs = r.dirs || [];
    browser.path = r.path;
  } catch (e) { showToast(e.message, "bad"); }
  browser.loading = false;
}
function pathFull(d) {
  const root = USER_ROOT.value;
  return (browser.path === root ? root : browser.path) + "/" + d.name;
}
function pickSrc(d) {
  const np = pathFull(d);
  if (!srcEdit.paths.includes(np)) srcEdit.paths.push(np);
  browser.show = false;
}
function fmtMtime(ts) {
  if (!ts) return "";
  const d = new Date(ts * 1000);
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function parentPath() {
  const root = USER_ROOT.value;
  const p = browser.path.split("/").slice(0, -1).join("/");
  return p.startsWith(root) ? p : root;
}
function crumbSegs() {
  return browser.path.replace(USER_ROOT.value, "").split("/").filter(Boolean);
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
  const root = USER_ROOT.value;
  return p.startsWith(root + "/") ? p.slice(root.length + 1) : p;
}

async function wizSave() {
  if (wiz.saving) return; // 防重复提交（init 走云盘网络可能耗时十几秒）
  if (!wizReady.value) {
    showToast("请完善任务名称、源目录和目标仓库", "bad");
    return;
  }
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
    loadAll();
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
// 云类型徽标（与 CloudPicker 一致）
function badgeCls(c) {
  return c === "tianyi" ? "b-ty" : c === "y115" ? "b-115" : c === "webdav" ? "b-dav" : "b-other";
}
function badgeTxt(c) {
  return c === "tianyi" ? "天翼" : c === "y115" ? "115" : c === "webdav" ? "DAV" : "云";
}
const cloudOptions = [
  { value: "webdav", title: "WebDAV", sub: "直接填写地址/账号/密码" },
  { value: "other", title: "自定义（rclone URI）", sub: "手动填写 rclone 远端地址" },
];
const repoModal = reactive({
  show: false,
  form: { id: "", name: "", cloud: "webdav", subdir: "", uri: "", password: "", remote: "", dav_url: "", dav_user: "", dav_pass: "", dav_vendor: "other" },
});
// WebDAV 连接测试
const davTest = reactive({ busy: false, ok: null, msg: "" });
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
      dav_url: repo.dav_url || "", dav_user: repo.dav_user || "", dav_pass: "", dav_vendor: repo.dav_vendor || "other",
    });
  } else {
    Object.assign(repoModal.form, { id: "", name: "", cloud: "webdav", subdir: "", uri: "", password: "", remote: "", dav_url: "", dav_user: "", dav_pass: "", dav_vendor: "other" });
    davTest.ok = null; davTest.msg = "";
  }
}
function onCloudChange() {
  if (repoModal.form.cloud !== "other" && repoModal.form.cloud !== "webdav") repoModal.form.subdir = "飞牛加密备份";
}
// 云盘目录浏览（选云端目录：mode = repo 仓库设置 / dest 任务目的地）；remote 为 rclone 远端名（ol:天翼云盘 / wd-xxx）
// base：浏览起点根（WebDAV 仓库 = 其 URI 限定的子目录，用户只能在仓库范围内选）
const cloudBrowser = reactive({ show: false, path: "", dirs: [], loading: false, cloud: "", remote: "", mode: "repo", creating: false, base: "" });
function remoteOf(cloudKey) {
  const r = cfg.repos.find((x) => x.id === cloudKey); // 下拉新 value：仓库 id
  if (r) return r.cloud === "webdav" ? (r.remote || "") : remoteOf(r.cloud);
  if (cloudKey === "tianyi" || cloudKey === "y115") return "ol:" + cloudLabels[cloudKey];
  return cloudKey; // webdav：即 wd-xxx 远端名
}
async function openCloudBrowser() {
  cloudBrowser.show = true;
  cloudBrowser.mode = "repo";
  cloudBrowser.cloud = cloudLabels[repoModal.form.cloud] || repoModal.form.cloud;
  // WebDAV 仓库用真实远端名（wd-xxx），ol 云盘用 ol:云盘名；否则 rclone 找不到 config 段
  cloudBrowser.remote = repoModal.form.cloud === "webdav" ? (repoModal.form.remote || "") : remoteOf(repoModal.form.cloud);
  cloudBrowser.base = ""; // 仓库模式下不限定根（在服务器范围内选仓库目录）
  cloudBrowser.path = "";
  await browseCloud("");
}
function openDestBrowser() {
  cloudBrowser.show = true;
  cloudBrowser.mode = "dest";
  cloudBrowser.cloud = cloudLabels[wiz.form.dest_cloud] || destCloudLabel(wiz.form.dest_cloud);
  cloudBrowser.remote = remoteOf(wiz.form.dest_cloud);
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
      body: JSON.stringify({ cloud: cloudBrowser.cloud, remote: cloudBrowser.remote, path: p }),
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
    const r = await api("/api/mkdir-cloud", { method: "POST", body: JSON.stringify({ cloud: cloudBrowser.cloud, remote: cloudBrowser.remote, path: full }) });
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
  const f = repoModal.form;
  f.uri = f.cloud === "webdav" ? (f.subdir || "").replace(/^\/+|\/+$/g, "") : buildURI();
  if (!f.name || !f.uri || (!f.password && !f.id)) {
    showToast("仓库名称、云端目录、密码均必填", "bad");
    return;
  }
  if (f.cloud === "webdav" && (!f.dav_url.trim() || !f.dav_user.trim())) {
    showToast("WebDAV 地址与账号必填", "bad");
    return;
  }
  try {
    await api("/api/repos", { method: "POST", body: JSON.stringify(f) });
    showToast("仓库已保存");
    repoModal.show = false;
    loadAll();
    // 从「选择云端」里点「新增云端」进来的：保存后回到仓库列表继续选
    if (cloudPick.reopenAfterAdd) {
      cloudPick.reopenAfterAdd = false;
      setTimeout(() => { cloudPick.show = true; }, 260);
    }
  } catch (e) {
    showToast(e.message, "bad");
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
    showToast("云端已移除（云盘上的数据不受影响）");
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
// 还原功能：快照时间线 + 文件选择 + 目的地/冲突策略 + 还原历史
// ---------------------------------------------------------------------------

const rst = reactive({
  show: false, task: null, busy: false,
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
});
// 还原向导步骤游标（方案A线性向导）：0=选任务 1=快照 2=文件 3=目的地 4=冲突
const rstStep = ref(0);
const rstSteps = [
  { id: "task", label: "选任务", title: "选择备份任务", hint: "选择要还原哪个备份任务的数据" },
  { id: "snap", label: "选快照", title: "选择快照", hint: "选择要还原到的时间点" },
  { id: "files", label: "选文件", title: "选择要还原的文件", hint: "可进入子目录勾选" },
  { id: "dest", label: "目的地", title: "还原到哪里", hint: "写回原位置或指定新路径" },
  { id: "conf", label: "冲突", title: "遇到同名文件", hint: "已有同名文件时的处理方式" },
];
const tb = reactive({ show: false, path: "", dirs: [], user_root: "", loading: false });
const restores = ref([]);
let rstTimer = null;

// 「新建还原」入口：直接打开还原向导（第 0 步 = 选任务）
const rpick = reactive({ show: false });
function openRpick() { openRestoreWizard(); }
function rpickGo(t) { selectTask(t); }

// 打开还原向导（从第 0 步「选任务」开始）
function openRestoreWizard() {
  rst.task = null;
  rst.cur = null;
  rst.snaps = [];
  rst.diffs = {};
  rst.cachedAt = "";
  rst.dest = "original";
  rst.target = "";
  rst.conflict = "if-changed";
  rst.includes = [];
  rstStep.value = 0;
  rst.show = true;
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
  if (rstStep.value > 0) rstStep.value--;
}
// 当前步骤是否满足「下一步」条件
function rstStepReady(i) {
  if (i === 0) return !!rst.task;
  if (i === 1) return !!rst.cur;
  if (i === 2) return rst.includes.length > 0;
  if (i === 3) return rst.dest === "original" || (rst.target || "").startsWith("/");
  return true; // 冲突策略总有默认值
}
// 用于任务卡片「还原」按钮：已知道任务，直接进第 1 步（快照）
function openRestore(t) {
  openRestoreWizard();
  selectTask(t);
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
  loadRstDir("");
}
async function loadRstDir(dir) {
  if (!rst.cur) return;
  rstPick.loading = true;
  try {
    const sid = rst.cur.short_id || rst.cur.id;
    const r = await api(`/api/tasks/${rst.task.id}/files?snapshot=${encodeURIComponent(sid)}&dir=${encodeURIComponent(dir)}`);
    rstPick.items = r.items || [];
    rstPick.dir = dir;
    rstPick.crumb = dir ? dir.split("/").filter(Boolean) : [];
    rstPick.q = "";
  } catch (e) { showToast(e.message, "bad"); }
    rstPick.loading = false;
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
  rst.includes = rstPick.sel.map((p) => ({ path: p, name: p.split("/").pop(), is_dir: !p.split("/").pop().includes(".") || false }));
  // is_dir 仅供展示：以选择器 items 为准
  const map = {};
  for (const it of rstPick.items) map[it.path] = it;
  rst.includes = rstPick.sel.map((p) => map[p] ? { path: p, name: map[p].name, is_dir: map[p].is_dir } : { path: p, name: p.split("/").pop(), is_dir: false });
  rstPick.show = false;
}
function removeInclude(x) {
  const at = rst.includes.indexOf(x);
  if (at >= 0) rst.includes.splice(at, 1);
}

// ---- 本地目标目录浏览 ----
async function openTargetBrowser() {
  tb.show = true;
  browseTarget(tb.path || "");
}
async function browseTarget(p) {
  tb.loading = true;
  try {
    const r = await api("/api/browse", { method: "POST", body: JSON.stringify({ path: p }) });
    tb.dirs = r.dirs || [];
    tb.path = r.path;
    tb.user_root = r.user_root || "";
  } catch (e) { showToast(e.message, "bad"); }
  tb.loading = false;
}
function tbUp() {
  const p = tb.path.split("/").filter(Boolean).slice(0, -1).join("/");
  browseTarget(p ? "/" + p : "/");
}
function tbSegs() { return tb.path.split("/").filter(Boolean); }
function tbCrumbTo(i) { browseTarget("/" + tbSegs().slice(0, i + 1).join("/")); }
function pickTarget() { rst.target = tb.path; tb.show = false; }

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
    showToast("还原已开始，可在「还原」页查看进度");
    rst.show = false;
    if (nav.value === "restore") loadRestores();
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
    logModal.show || rst.show || rstPick.show || tb.show || rpick.show
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
// 切到还原页时立即加载（首次或存在运行中还原才真正请求，否则用缓存）
watch(nav, (n) => {
  if (n === "restore") {
    if (!restoresLoaded || hasRunningRestore()) loadRestores();
  }
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

onMounted(() => {
  loadAll();
});
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer);
  if (logTimer) clearInterval(logTimer);
  if (rstTimer) clearInterval(rstTimer);
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
          <svg viewBox="0 0 24 24" width="17" height="17"><path fill="currentColor" d="M4 5.5A2.5 2.5 0 0 1 6.5 3h3.67a2 2 0 0 1 1.41.59l1.42 1.41H19.5A2.5 2.5 0 0 1 22 7.5v10a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 4 17.5v-12Z"/></svg>
          备份
        </button>
        <button class="sb-item" :class="{ on: nav === 'restore' }" @click="nav = 'restore'">
          <svg viewBox="0 0 24 24" width="17" height="17"><path fill="currentColor" d="M12 5V2L7 6l5 4V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8Z"/></svg>
          还原
        </button>
      </nav>
      <div class="sb-foot" v-if="status">
        <div>restic {{ status.restic_version || "未找到" }}</div>
        <div>rclone {{ status.rclone_version || "未找到" }}</div>
      </div>
    </aside>

    <div class="main">

      <main class="content">
        <!-- ==================== 备份任务（仿飞牛原生） ==================== -->
        <template v-if="nav === 'tasks'">
          <!-- 桌面页头 -->
          <div class="dhead">
            <h1>{{ nav === 'tasks' ? '备份任务' : '' }}</h1>
            <button class="btn primary" @click="openWizard(null)">
              <span class="plus">＋</span> 创建新任务
            </button>
          </div>

          <!-- 任务卡片列表 -->
          <div class="task-list" v-if="cfg.tasks.length">
            <div class="task-card" v-for="t in cfg.tasks" :key="t.id">
              <!-- 第一行：图标 + 名称 + 状态 -->
              <div class="tc-top">
                <div class="tc-icon">
                  <svg viewBox="0 0 48 48" width="40" height="40">
                    <path fill="#5ca0ff" d="M4 12a4 4 0 0 1 4-4h10l4 5h18a4 4 0 0 1 4 4v21a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4V12Z"/>
                    <path fill="#3370ff" d="M4 17h40v21a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4V17Z"/>
                  </svg>
                  <span class="badge" :class="'b-' + statusInfo(t).badge">
                    <template v-if="statusInfo(t).badge === 'warn'">!</template>
                    <template v-else-if="statusInfo(t).badge === 'minus'">–</template>
                    <template v-else-if="statusInfo(t).badge === 'ok'">✓</template>
                    <template v-else-if="statusInfo(t).badge === 'run'"><i class="mini-spin"></i></template>
                  </span>
                </div>
                <div class="tc-title">
                  <b>{{ t.name }}</b>
                  <div class="tc-status" :class="statusInfo(t).cls">
                    <span class="st-main">{{ statusInfo(t).text }}</span>
                    <span class="st-detail" v-if="statusInfo(t).detail">{{ statusInfo(t).detail }}</span>
                    <a class="st-link" @click.prevent="openLogs(t)">查看详情</a>
                  </div>
                </div>
                <div class="tc-acts">
                  <button class="icon-btn" v-if="t.last_status === 'running'" title="停止" @click="stopTask(t)">
                    <svg viewBox="0 0 24 24" width="16" height="16"><rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor"/></svg>
                  </button>
                  <button class="icon-btn" v-else-if="t.enabled !== false" title="立即备份" @click="runTask(t)">
                    <svg viewBox="0 0 24 24" width="16" height="16"><path fill="currentColor" d="M8 5.5v13a1 1 0 0 0 1.53.85l10.2-6.5a1 1 0 0 0 0-1.7L9.53 4.65A1 1 0 0 0 8 5.5Z"/></svg>
                  </button>
                  <button class="icon-btn" title="还原" @click.stop="openRestore(t)">
                    <svg viewBox="0 0 24 24" width="16" height="16"><path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" d="M12 5.5V2.8L7.6 6.4 12 10V7.5a6.5 6.5 0 1 1-6.4 7.7"/></svg>
                  </button>
                  <button class="icon-btn" @click.stop="toggleMenu(t.id)">
                    <svg viewBox="0 0 24 24" width="16" height="16"><circle cx="5" cy="12" r="1.8" fill="currentColor"/><circle cx="12" cy="12" r="1.8" fill="currentColor"/><circle cx="19" cy="12" r="1.8" fill="currentColor"/></svg>
                  </button>
                  <!-- 下拉菜单 -->
                  <div class="menu" v-if="menuOpen === t.id" @click.stop>
                    <button @click="openLogs(t)">查看日志</button>
                    <button @click="openWizard(t)">编辑任务</button>
                    <button class="danger" @click="delTask(t)">删除任务</button>
                  </div>
                </div>
              </div>
              <!-- 第二行：来源 → 目的地 → 下次 -->
              <div class="tc-grid">
                <div class="tc-cell">
                  <div class="cell-head">
                    <svg viewBox="0 0 24 24" width="18" height="18"><rect x="3" y="4" width="18" height="13" rx="2" fill="#8ab4f8"/><rect x="7" y="19" width="10" height="2" rx="1" fill="#c3c9d4"/><path fill="#5f6b7d" d="M7 8h10v2H7zM7 12h6v2H7z" opacity=".55"/></svg>
                    <b>fnos（本地）</b>
                  </div>
                  <div class="cell-sub">{{ pathSummary(t) }}</div>
                </div>
                <div class="tc-arrow">
                  <svg viewBox="0 0 24 24" width="22" height="22"><path fill="none" stroke="#9db8e8" stroke-width="2" stroke-linecap="round" d="M4 12h14m-5-5 5 5-5 5" stroke-dasharray="1 3"/><path fill="#9db8e8" d="M17 8.5 21.5 12 17 15.5z"/></svg>
                </div>
                <div class="tc-cell">
                  <div class="cell-head">
                    <svg viewBox="0 0 24 24" width="18" height="18"><path fill="#8ab4f8" d="M7 18a5 5 0 0 1-.58-9.97 6.5 6.5 0 0 1 12.6 1.62A4.5 4.5 0 0 1 17.5 18H7Z"/></svg>
                    <b>{{ repoName(t.repo_id) }}</b>
                  </div>
                  <div class="cell-sub mono">{{ destPath(repoObj(t.repo_id)) }}</div>
                </div>
                <div class="tc-cell next">
                  <div class="cell-head"><b class="dim">下次</b></div>
                  <div class="cell-sub">{{ t.next_run_at ? fmtNextRun(t.next_run_at) : "–" }}</div>
                </div>
              </div>

              <!-- 第三行：运行中进度（仅当前任务） -->
              <div class="tc-progress" v-if="t.id === progress.task_id">
                <div class="pg-head">
                  <span class="spin"></span> 正在备份
                  <span class="pg-pct">{{ progress.percent.toFixed(1) }}%</span>
                </div>
                <div class="bar"><div class="bar-in" :style="{ width: progress.percent + '%' }"></div></div>
                <div class="pg-meta">
                  <span><span class="pg-k">开始时间</span>{{ fmtClock(progress.started_at) }}</span>
                  <span><span class="pg-k">数据量</span>{{ fmtBytes(progress.bytes_done) }} / {{ fmtBytes(progress.total_bytes) }}</span>
                  <span v-if="progress.error_count" class="pg-err">⚠ {{ progress.error_count }} 个错误</span>
                </div>
              </div>
            </div>
          </div>

          <!-- 手机卡片 -->
          <div class="mcard-list" v-if="cfg.tasks.length">
            <div class="mcard" v-for="t in cfg.tasks" :key="'m' + t.id" @click="openSheet(t)">
              <div class="mc-top">
                <div class="tc-icon">
                  <svg viewBox="0 0 48 48" width="38" height="38">
                    <path fill="#5ca0ff" d="M4 12a4 4 0 0 1 4-4h10l4 5h18a4 4 0 0 1 4 4v21a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4V12Z"/>
                    <path fill="#3370ff" d="M4 17h40v21a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4V17Z"/>
                  </svg>
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
                <button class="mc-more" @click.stop="openSheet(t)">⋯</button>
              </div>
              <div class="mc-row"><span>来源地</span><span>fnos（本地）</span></div>
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
                  <span v-if="progress.error_count" class="pg-err">⚠ {{ progress.error_count }} 个错误</span>
                </div>
              </div>
            </div>
          </div>

          <!-- 空态 -->
          <div class="empty" v-if="!cfg.tasks.length">
            <div class="empty-icon">📦</div>
            <p>暂无备份任务</p>
            <p class="hint">点击「创建新任务」：选择源目录 → 目标云盘仓库 → 备份计划</p>
          </div>

          <!-- 手机操作面板 -->
          <div class="mask sheet-mask" v-if="sheet.show" @click.self="sheet.show = false">
            <div class="sheet">
              <div class="sheet-title">
                <span>{{ sheet.task ? sheet.task.name : "" }}</span>
                <button class="sheet-x" aria-label="关闭" @click="sheet.show = false">✕</button>
              </div>
              <template v-if="sheet.task && sheet.task.last_status === 'running'">
                <button class="sheet-btn" @click="sheet.task && stopTask(sheet.task)">■ 停止备份</button>
              </template>
              <template v-else-if="sheet.task && sheet.task.enabled !== false">
                <button class="sheet-btn primary" @click="sheet.task && runTask(sheet.task)">▶ 立即备份</button>
              </template>
              <button class="sheet-btn" @click="sheet.task && openLogs(sheet.task)">查看日志</button>
              <button class="sheet-btn" @click="sheet.task && openWizard(sheet.task)">编辑任务</button>
              <button class="sheet-btn danger" @click="sheet.task && delTask(sheet.task)">删除任务</button>
            </div>
          </div>
        </template>

        <!-- ==================== 云盘仓库管理已并入：创建任务向导「① 选择云端」下拉（👁 密码 / ✕ 移除） ==================== -->

        <!-- ==================== 还原历史 ======================== -->
        <template v-else>
          <div class="dhead">
            <h1>还原</h1>
            <div class="dhead-acts">
              <button class="btn ghost" @click="loadRestores">刷新</button>
              <button class="btn primary" @click="openRpick">＋ 新建还原</button>
            </div>
          </div>
          <div class="rst-list" v-if="restores.length">
            <div class="rst-card" v-for="j in restores" :key="j.id">
              <div class="rst-top">
                <span class="rst-name">{{ j.task_name }}</span>
                <span class="rst-pill" :class="rstStatusCls(j)">
                  <template v-if="j.status === 'running'"><i class="mini-spin"></i></template>{{ rstStatusText(j) }}
                </span>
              </div>
              <div class="rst-rows">
                <div class="rst-row"><span class="k">快照</span><span class="v">{{ (j.snapshot || "").slice(0, 8) || "–" }}</span></div>
                <div class="rst-row"><span class="k">目标</span><span class="v">{{ j.dest === "original" ? "⌖ 原位置" : "➝ " + (j.target || "") }}</span></div>
                <div class="rst-row" v-if="j.started_at"><span class="k">时间</span><span class="v">{{ j.started_at }}</span></div>
                <div class="rst-row" v-if="j.status !== 'running'"><span class="k">结果</span><span class="v">{{ rstResultText(j) }}</span></div>
              </div>
              <div class="rst-progress" v-if="j.status === 'running'">
                <div class="rst-bar"><i :style="{ width: (j.pct || 0) + '%' }"></i></div>
                <span class="rst-pct">{{ Math.floor(j.pct || 0) }}% · {{ j.files || 0 }} 个文件 / {{ fmtBytes(j.bytes) }}</span>
              </div>
            </div>
          </div>
          <div class="empty" v-else>
            <div class="empty-icon">♻️</div>
            <p>还没有还原记录</p>
            <button class="btn primary" style="margin: 14px 0 10px;" @click="openRpick">＋ 新建还原</button>
            <p class="hint">也可以在「备份」页的任务卡上点 <b>↺</b> 按钮，选择快照和文件进行还原</p>
          </div>
        </template>
      </main>
    </div>

    <!-- ======================== 任务弹窗（方案B：单页 + 内联展开） ======================== -->
    <div class="mask" v-if="wiz.show">
      <div class="modal tall">
        <div class="modal-head sheet-head">
          <h3>{{ wiz.editing ? "编辑任务" : "新建备份任务" }}</h3>
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
              <b>备份来源地</b>
              <button class="link-btn" @click="openSrcEdit">设置</button>
            </div>
            <div class="sec-card">
              <div class="src-row">
                <div class="src-ico">🖥️</div>
                <div><b>fnos</b><span class="sec-sub">本地设备</span></div>
              </div>
              <div class="src-paths" v-if="wiz.form.paths.length">
                <div class="src-path" v-for="(p, i) in wiz.form.paths" :key="p">
                  <span>📁 {{ shortPath(p) }}</span>
                  <b class="rm" @click="removePath(i)">✕</b>
                </div>
              </div>
              <div class="sec-sub pad" v-else>还未选择目录，点右上角「设置」开始选择</div>
            </div>
          </div>

          <!-- 备份目的地：两步式（选云端 → 选/新建目录） -->
          <div class="sec">
            <div class="sec-head"><b>备份目的地</b><button type="button" class="link-btn" @click="openRepo(null)">新增云端</button></div>
            <div class="sec-card">
              <div class="fld">
                <span>① 选择云端</span>
                <button type="button" class="cloud-pick-btn" @click="openCloudPick">
                  <template v-if="destRepo">
                    <span class="cpb-badge" :class="badgeCls(destRepo.cloud)">{{ badgeTxt(destRepo.cloud) }}</span>
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
              <div class="flow-line" v-if="destReady && wiz.form.paths.length">
                <span class="node">fnos 本地</span><i class="lk">──▶</i>
                <span class="node hi">{{ wiz.form.dest_subdir }}</span>
              </div>
            </div>
          </div>

          <!-- 备份设置 -->
          <div class="sec">
            <div class="sec-head"><b>备份设置</b></div>
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

        <div class="modal-foot">
          <button class="btn primary block" @click="wizSave" :disabled="!wizReady || wiz.saving">{{ wiz.saving ? "保存中…" : (wiz.editing ? "保存" : "创建任务") }}</button>
        </div>
      </div>
    </div>

    <!-- ======================== 仓库弹窗 ======================== -->
    <div class="mask" v-if="repoModal.show">
      <div class="modal">
        <div class="modal-head">
          <h3>云盘仓库</h3>
          <button class="x" @click="repoModal.show = false">✕</button>
        </div>
        <div class="modal-body">
          <label class="fld">
            <span>仓库类型</span>
            <BDrop v-model="repoModal.form.cloud" :options="cloudOptions" placeholder="选择云盘" @change="onCloudChange" />
          </label>
          <label class="fld">
            <span>仓库名称</span>
            <input v-model="repoModal.form.name" placeholder="例如：我的云盘仓库" />
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
              <input type="password" v-model="repoModal.form.dav_pass" @input="davTest.msg = ''" autocomplete="new-password" placeholder="WebDAV 密码" />
            </label>
            <div class="fld">
              <span>连接测试</span>
              <div class="inrow">
                <button type="button" class="btn ghost" :disabled="davTest.busy || !repoModal.form.dav_url.trim()" @click="testDav">{{ davTest.busy ? "测试中…" : "测试连接" }}</button>
                <span v-if="davTest.msg" :class="davTest.ok ? 'tst-ok' : 'tst-bad'">{{ davTest.msg }}</span>
              </div>
            </div>
            <div class="fld">
              <span>云端目录（可选）</span>
              <div class="inrow">
                <input v-model="repoModal.form.subdir" placeholder="默认根目录，可手动输入子目录" />
                <button v-if="repoModal.form.remote" class="btn ghost sm" @click="openCloudBrowser">浏览</button>
              </div>
            </div>
            <div class="uri-prev mono">rclone:{{ repoModal.form.remote || "<保存时生成远端>" }}:{{ repoModal.form.subdir || "" }}</div>
          </template>
          <template v-else-if="repoModal.form.cloud !== 'other'">
            <div class="fld">
              <span>云端目录</span>
              <div class="inrow">
                <input v-model="repoModal.form.subdir" placeholder="例如：backup" />
                <button class="btn ghost sm" @click="openCloudBrowser">浏览</button>
              </div>
            </div>
            <div class="uri-prev mono">{{ buildURI() }}</div>
          </template>
          <label class="fld" v-else>
            <span>仓库 URI</span>
            <input v-model="repoModal.form.uri" class="mono" placeholder="rclone:远端名:路径" />
          </label>
          <label class="fld">
            <span>仓库密码{{ repoModal.form.id ? "（留空保持不变）" : "" }}</span>
            <input type="password" v-model="repoModal.form.password" placeholder="restic 仓库密码" />
          </label>
          <div class="hint-box">保存后可点「验证」确认密码与云端连接；restic 会自动创建缺失的仓库目录。</div>
        </div>
        <div class="modal-foot even">
          <span class="spacer"></span>
          <button class="btn ghost" @click="repoModal.show = false">取消</button>
          <button class="btn primary" @click="saveRepo">保存</button>
        </div>
      </div>
    </div>

    <!-- ======================== 日志抽屉 ======================== -->
    <div class="mask" v-if="logModal.show">
      <div class="modal wide">
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
            <div class="src-row host"><span class="host-ic">🗄️</span><b>fnos（本地）</b></div>
            <div class="src-path" v-for="(p, i) in srcEdit.paths" :key="p">
              <svg class="mini-fold" viewBox="0 0 40 32" width="26" height="21"><path fill="#4a9eff" d="M2 6a4 4 0 0 1 4-4h9.5l4 4H34a4 4 0 0 1 4 4v16a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V6Z"/><path fill="#8ec7ff" d="M2 12h36v14a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V12Z"/></svg>
              <span class="sp-name">{{ dispPath(p) }}</span>
              <b class="rm" @click="srcEdit.paths.splice(i, 1)">✕</b>
            </div>
            <button class="add-path" @click="openBrowser">＋ 添加文件路径</button>
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
          <h3>{{ browser.path === USER_ROOT ? "我的文件" : crumbSegs().slice(-1)[0] }}</h3>
          <button class="x" @click="browser.show = false">✕</button>
        </div>
        <div class="modal-body">
          <div class="dir-list">
            <div class="frow big" v-if="browser.path !== USER_ROOT" @click="browseTo(parentPath())">
              <span class="fold-ic back">↩</span>
              <span class="nm"><b>返回上一级</b></span>
            </div>
            <div class="frow big" v-for="d in browser.dirs" :key="d.name" :class="{ dis: srcEdit.paths.includes(pathFull(d)) }" @click="pickSrc(d)">
              <svg class="fold-ic" viewBox="0 0 40 32" width="36" height="29"><path fill="#4a9eff" d="M2 6a4 4 0 0 1 4-4h9.5l4 4H34a4 4 0 0 1 4 4v16a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V6Z"/><path fill="#8ec7ff" d="M2 12h36v14a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V12Z"/></svg>
              <span class="nm">
                <b>{{ d.name }}</b>
                <span class="sub">存储空间1 {{ fmtMtime(d.mtime) }}</span>
              </span>
              <i class="chev" v-if="d.count > 0" @click.stop="browseTo(pathFull(d))">›</i>
            </div>
            <div class="empty sm" v-if="!browser.dirs.length && !browser.loading"><p>没有子目录</p></div>
          </div>
          <div class="hide-sys" v-if="browser.path === USER_ROOT">
            <span class="tg" :class="{ off: browser.showAll }" @click="browser.showAll = !browser.showAll; browseTo(browser.path)"></span>
            显示系统目录（docker、@app* 等）
          </div>
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
              <span class="fold-ic back">↩</span>
              <span class="nm"><b>返回上一级</b></span>
            </div>
            <div class="frow big" v-for="d in cloudBrowser.dirs" :key="d" @click="pickCloudDir(d)">
              <svg class="fold-ic" viewBox="0 0 40 32" width="36" height="29"><path fill="#4a9eff" d="M2 6a4 4 0 0 1 4-4h9.5l4 4H34a4 4 0 0 1 4 4v16a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V6Z"/><path fill="#8ec7ff" d="M2 12h36v14a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V12Z"/></svg>
              <span class="nm"><b>{{ d }}</b><span class="sub">点选为云端目录，右侧 › 进入</span></span>
              <i class="chev" @click.stop="browseCloud(cloudBrowser.path ? cloudBrowser.path + '/' + d : d)">›</i>
            </div>
            <div class="empty sm" v-if="!cloudBrowser.dirs.length && !cloudBrowser.loading"><p>没有子目录</p></div>
          </div>
        </div>
        <div class="modal-foot col">
          <div class="toolbarC">
            <button type="button" class="mkdir-btn" v-if="cloudBrowser.mode === 'dest'" :disabled="mkdirDlg.creating" @click="openMkdirDlg">{{ mkdirDlg.creating ? "创建中…" : "新建文件夹" }}</button>
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

    <!-- 手机底部导航（仿飞牛虚拟机 App） -->
    <nav class="bnav">
      <button :class="{ on: nav === 'tasks' }" @click="nav = 'tasks'">
        <svg viewBox="0 0 24 24" width="22" height="22"><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" d="M3.5 6.5A2 2 0 0 1 5.5 4.5h4.2l1.8 1.8h7A2 2 0 0 1 20.5 8.3v9.2a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V6.5Z"/></svg>
        <span>备份</span>
      </button>
      <button :class="{ on: nav === 'restore' }" @click="nav = 'restore'">
        <svg viewBox="0 0 24 24" width="22" height="22"><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" d="M12 5.5V2.8L7.6 6.4 12 10V7.5a6.5 6.5 0 1 1-6.4 7.7"/></svg>
        <span>还原</span>
      </button>
    </nav>

    <!-- 手机悬浮新建按钮：备份页=新建任务，还原页=新建还原 -->
    <button class="fab" aria-label="新建" @click.stop="nav === 'restore' ? openRpick() : openWizard(null)">
      <svg viewBox="0 0 24 24" width="26" height="26"><path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" fill="none"/></svg>
    </button>

    <!-- Toast -->
    <transition name="fade">
      <div class="toast" :class="toast.type" v-if="toast.show">{{ toast.msg }}</div>
    </transition>

    <!-- ==================== 新建还原 · 分步向导（方案A线性） ==================== -->
    <div class="mask" v-if="rst.show">
      <div class="modal rst-modal">
        <div class="modal-head">
          <h3>新建还原</h3>
          <button class="x" @click="rst.show = false">✕</button>
        </div>

        <!-- 顶部步骤条（5 步） -->
        <div class="rst-stepper">
          <template v-for="(st, i) in rstSteps" :key="st.id">
            <div class="rst-step-line" v-if="i > 0" :class="{ done: i <= rstStep }"></div>
            <div class="rst-step-dot" :class="{ on: i === rstStep, done: i < rstStep }">
              <span class="n">{{ i < rstStep ? "✓" : i + 1 }}</span>
              <span class="lb">{{ st.label }}</span>
            </div>
          </template>
        </div>

        <!-- 中间：单步内容 -->
        <div class="modal-body single">
          <div class="rst-step-title">第 {{ rstStep + 1 }} 步 · {{ rstSteps[rstStep].title }}<em>{{ rstSteps[rstStep].hint }}</em></div>

          <!-- 第 0 步：选任务 -->
          <div v-if="rstStep === 0">
            <div class="rpick-list" v-if="cfg.tasks.length">
              <button class="rpick-item" v-for="t in cfg.tasks" :key="t.id" @click="selectTask(t)">
                <span class="rp-ic">↺</span>
                <span class="rp-txt">
                  <b>{{ t.name }}</b>
                  <small>{{ t.last_done_at ? "上次备份 " + fmtShort(t.last_done_at) : "从未备份" }}</small>
                </span>
                <svg viewBox="0 0 24 24" width="16" height="16" class="rp-arrow"><path fill="none" stroke="#c0c4cc" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="m9 5 7 7-7 7"/></svg>
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
              <em class="snap-cache" v-if="rst.cachedAt" :title="'本地缓存快照列表，避免每次实时查云盘'">缓存于 {{ rst.cachedAt.slice(5) }}</em>
              <button type="button" class="snap-refresh" title="强制实时查询云盘并更新缓存" @click="loadRestoreSnaps(rst.task, true)" :disabled="rst.snapsLoading">
                <svg viewBox="0 0 24 24" width="13" height="13"><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" d="M20 12a8 8 0 1 1-2.34-5.66M20 4v4h-4"/></svg>
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

          <!-- 第 2 步：选文件（点卡片弹独立选择器） -->
          <div v-else-if="rstStep === 2">
            <button type="button" class="pick-files-btn" @click="openRstPick">
              <svg viewBox="0 0 24 24" width="17" height="17"><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" d="M3.5 6.5A2 2 0 0 1 5.5 4.5h4.2l1.8 1.8h7A2 2 0 0 1 20.5 8.3v9.2a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V6.5Z"/></svg>
              {{ rst.includes.length ? "已选 " + rst.includes.length + " 项 · 点击修改" : "浏览快照文件…" }}
            </button>
            <div class="inc-tray" v-if="rst.includes.length">
              <span class="t" v-for="x in rst.includes" :key="x.path"><b>{{ x.is_dir ? "📁" : "📄" }} {{ x.name }}</b><i @click="removeInclude(x)">×</i></span>
            </div>
          </div>

          <!-- 第 3 步：目的地 -->
          <div v-else-if="rstStep === 3">
            <div class="seg">
              <button type="button" :class="{ on: rst.dest === 'original' }" @click="rst.dest = 'original'">原位置</button>
              <button type="button" :class="{ on: rst.dest === 'custom' }" @click="rst.dest = 'custom'">新路径</button>
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

        <!-- flow-line（仅最后一步作为最终确认摘要） -->
        <div class="rst-flow" v-if="rstStep === 4 && rst.cur && rst.includes.length">
          <span class="rst-flow-node">📁 快照 {{ (rst.cur.short_id || rst.cur.id).slice(0, 8) }}</span>
          <i class="rst-flow-lk">──▶</i>
          <span class="rst-flow-node hi">{{ rst.dest === "original" ? "⌖ 原位置" : "➝ " + (rst.target || "新路径") }}</span>
        </div>

        <!-- 底部：上一步 / 下一步 -->
        <div class="modal-foot even">
          <button class="btn ghost" :disabled="rstStep === 0" @click="rstPrev">上一步</button>
          <button v-if="rstStep < 4" class="btn primary" :disabled="!rstStepReady(rstStep)" @click="rstNext">下一步</button>
          <button v-else class="btn primary" :disabled="!rstReady || rst.busy" @click="doRestore">{{ rst.busy ? "启动中…" : "开始还原" }}</button>
        </div>
      </div>
    </div>

    <!-- ==================== 还原 · 文件选择器 ==================== -->
    <div class="mask top" style="z-index:3200;" v-if="rstPick.show">
      <div class="modal">
        <div class="modal-head">
          <h3>选择要还原的文件 <span class="pk-snap mono" v-if="rst.cur">快照 {{ (rst.cur.short_id || rst.cur.id).slice(0, 8) }}</span></h3>
          <button class="x" @click="rstPick.show = false">✕</button>
        </div>
        <div class="modal-body">
          <div class="crumbC">
            <span class="cloudic" @click.prevent="loadRstDir('')">📦 快照根</span>
            <template v-for="(seg, i) in rstPick.crumb" :key="i">
              <span class="sl">/</span>
              <a href="#" :class="{ cur: i === rstPick.crumb.length - 1 }" @click.prevent="loadRstDir(rstPick.crumb.slice(0, i + 1).join('/'))">{{ seg }}</a>
            </template>
            <span class="selpill" v-if="rstPick.sel.length">已选 {{ rstPick.sel.length }} 项</span>
          </div>
          <div class="tray" v-if="rstPick.sel.length">
            <span class="t" v-for="p in rstPick.sel" :key="p"><b>{{ p.split("/").pop() }}</b><i @click="togglePick({ path: p })">×</i></span>
          </div>
          <div class="dir-list">
            <div class="frow big" v-if="rstPick.dir" @click="loadRstDir(rstPick.dir.split('/').slice(0, -1).join('/'))">
              <span class="fold-ic back">↩</span>
              <span class="nm"><b>返回上一级</b></span>
            </div>
            <div class="frow big" v-for="it in rstPick.items" :key="it.path" :class="{ sel: isPicked(it.path) }" @click="it.is_dir ? loadRstDir(it.path) : null">
              <svg class="fold-ic" v-if="it.is_dir" viewBox="0 0 40 32" width="30" height="24"><path fill="#4a9eff" d="M2 6a4 4 0 0 1 4-4h9.5l4 4H34a4 4 0 0 1 4 4v16a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V6Z"/><path fill="#8ec7ff" d="M2 12h36v14a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V12Z"/></svg>
              <svg class="fold-ic" v-else viewBox="0 0 24 24" width="22" height="22"><path fill="#e5e9f0" d="M6 2h9l5 5v15H6V2Z"/><path fill="#c9d2e0" d="M15 2l5 5h-5V2Z"/></svg>
              <span class="nm">
                <b>{{ it.name }}<i v-if="it.badge === 'add'" class="dif add">+ 新增</i><i v-else-if="it.badge === 'mod'" class="dif mod">~ 修改</i></b>
              </span>
              <span class="fsize" v-if="!it.is_dir">{{ fmtBytes(it.size) }}</span>
              <span class="cbx" :class="{ on: isPicked(it.path), half: isHalf(it.path) }" @click.stop="togglePick(it)">✓</span>
            </div>
            <div class="empty sm" v-if="!rstPick.items.length && !rstPick.loading"><p>此目录为空</p></div>
            <div class="frow big" v-if="rstPick.loading"><span class="nm"><b><i class="mini-spin"></i> 加载中…</b></span></div>
          </div>
        </div>
        <div class="modal-foot">
          <div class="toolbarC">
            <button type="button" class="btn ghost" @click="rstPick.sel = []">清空选择</button>
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
            <span class="cloudic" @click.prevent="browseTarget('/')">💾 NAS</span>
            <template v-for="(seg, i) in tbSegs()" :key="i">
              <span class="sl">/</span>
              <a href="#" :class="{ cur: i === tbSegs().length - 1 }" @click.prevent="tbCrumbTo(i)">{{ seg }}</a>
            </template>
          </div>
          <div class="dir-list">
            <div class="frow big" v-if="tb.path && tb.path !== '/'" @click="tbUp">
              <span class="fold-ic back">↩</span>
              <span class="nm"><b>返回上一级</b></span>
            </div>
            <div class="frow big" v-for="d in tb.dirs" :key="d.name" @click="browseTarget(tb.path === '/' ? '/' + d.name : tb.path + '/' + d.name)">
              <svg class="fold-ic" viewBox="0 0 40 32" width="36" height="29"><path fill="#4a9eff" d="M2 6a4 4 0 0 1 4-4h9.5l4 4H34a4 4 0 0 1 4 4v16a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V6Z"/><path fill="#8ec7ff" d="M2 12h36v14a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4V12Z"/></svg>
              <span class="nm"><b>{{ d.name }}</b><span class="sub" v-if="d.count >= 0">{{ d.count }} 项</span></span>
            </div>
            <div class="empty sm" v-if="!tb.dirs.length && !tb.loading"><p>没有子目录</p></div>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn primary block" :disabled="!tb.path || tb.path === '/'" @click="pickTarget">使用当前目录：{{ tb.path }}</button>
        </div>
      </div>
    </div>

    <!-- 移除云端确认弹窗（z-index 须高于 bdrop-pop 的 3000；点遮罩只关本弹窗，下拉保持不动） -->
    <div class="mask top" style="z-index:3100;" v-if="rmDlg.show" @click="rmDlg.show = false">
      <div class="modal" style="width:400px;" @click.stop>
        <div class="modal-body" style="padding-top:20px;">
          <h3 style="font-size:16px; text-align:center; margin-bottom:6px;">移除「{{ rmDlg.opt ? rmDlg.opt.name : "" }}」？</h3>
          <p style="font-size:13px; color:#6b7280; text-align:center; margin-bottom:14px;">仅从本应用移除，<b>不会删除云盘上的数据</b></p>
        </div>
        <div class="modal-foot pair">
          <button class="btn ghost block" @click="rmDlg.show = false">取消</button>
          <button class="btn primary block" style="background:#dc2626; border-color:#dc2626;" :disabled="rmDlg.busy" @click="confirmRmRepo">{{ rmDlg.busy ? "移除中…" : "确认移除" }}</button>
        </div>
      </div>
    </div>

    <!-- 选择云端：卡片式弹层（方案A，参考飞牛备份来源地样式） -->
    <CloudPicker
      :show="cloudPick.show"
      :repos="cfg.repos"
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
.sb-brand { display: flex; align-items: center; gap: 10px; padding: 18px 18px 14px; font-weight: 600; font-size: 15px; }
.sb-logo { width: 34px; height: 34px; border-radius: 9px; background: #eef4ff; display: flex; align-items: center; justify-content: center; }
.sb-nav { display: flex; flex-direction: column; gap: 2px; padding: 8px 10px; }
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
.modal.tall .modal-body { flex: 1; min-height: 0; }
.modal.tall .dir-list { flex: 1; max-height: none; }
.modal-head { display: flex; align-items: center; justify-content: space-between; padding: 16px 20px 10px; }
.modal-head h3 { margin: 0; font-size: 16px; }
.x { border: none; background: none; font-size: 16px; cursor: pointer; color: #6b7280; padding: 4px 8px; }
.auto-lb { font-size: 12px; color: #6b7280; display: flex; align-items: center; gap: 4px; }
.modal-body { padding: 8px 20px; overflow-y: auto; overscroll-behavior: contain; }
.modal-foot { display: flex; align-items: center; gap: 10px; padding: 14px 20px; border-top: 1px solid #f3f4f6; }
.spacer { flex: 1; }
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
.cpb-badge {
  width: 34px; height: 34px; border-radius: 9px; display: flex; align-items: center; justify-content: center;
  color: #fff; font-size: 11px; font-weight: 700; flex-shrink: 0;
}
.cpb-txt { flex: 1; min-width: 0; }
.cpb-txt b { display: block; font-size: 13.5px; font-weight: 600; color: #111827; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.cpb-txt small { display: block; font-size: 12px; color: #8b9098; margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.b-ty { background: linear-gradient(140deg, #4aa8ff, #1668e3); }
.b-115 { background: linear-gradient(140deg, #3ec777, #14944b); }
.b-dav { background: linear-gradient(140deg, #939bab, #5d6675); }
.b-other { background: linear-gradient(140deg, #939bab, #5d6675); }
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
.dir-list { max-height: 58vh; overflow-y: auto; overscroll-behavior: contain; border: 1px solid #f3f4f6; border-radius: 8px; }
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
  .hide-m { display: none !important; }
  .dhead { display: none; }

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
  .run-cur { display: none; }
  .steps-preview { justify-content: center; }
  .ws-arrow { display: none; }
  .wiz-step { flex-direction: row; }
  .ws-t { font-size: 11px; }
}
</style>
