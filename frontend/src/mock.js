// 预览用 Mock 数据：仅在 VITE_MOCK=1 构建时启用，不会进入正式包
// 覆盖 App.vue 里用到的全部 /api/* 接口，返回与 Go 后端一致的结构

const NOW = new Date();
const pad = (n) => String(n).padStart(2, "0");
const fmt = (d) =>
  `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
const daysAgo = (n, h = 3, m = 12) => {
  const d = new Date(NOW);
  d.setDate(d.getDate() - n);
  d.setHours(h, m, 47);
  return fmt(d);
};
const daysAhead = (n, h = 3, m = 0) => {
  const d = new Date(NOW);
  d.setDate(d.getDate() + n);
  d.setHours(h, m, 0);
  return fmt(d);
};

const GB = 1024 ** 3;

const repos = [
  {
    id: "r1",
    name: "天翼云盘 · BackVault",
    cloud: "tianyi",
    uri: "189cloud:/个人云/BackVault",
    count: 12,
    task_count: 2,
    latest_time: daysAgo(0, 3, 12),
    cached: true,
  },
  {
    id: "r2",
    name: "115 网盘 · backup",
    cloud: "y115",
    uri: "115:/backup/documents",
    count: 8,
    task_count: 1,
    latest_time: daysAgo(0, 2, 41),
    cached: true,
  },
  {
    id: "r3",
    name: "WebDAV · 极空间",
    cloud: "webdav",
    uri: "rclone:wd-3f8a1c:nas/video",
    dav_url: "https://dav.example.com:5006/dav",
    dav_user: "caobingbing",
    count: 0,
    task_count: 1,
    latest_time: "",
    cached: true,
  },
  // OpenList 中转，名字里没写网盘名，靠子目录第一段（baidu）识别出百度网盘
  {
    id: "r4",
    name: "云盘备份",
    cloud: "webdav",
    uri: "rclone:wd-9b2e40:baidu/相册",
    dav_url: "http://192.168.31.9:5244/dav",
    dav_user: "admin",
    count: 5,
    task_count: 1,
    latest_time: daysAgo(1, 2, 10),
    cached: true,
  },
  // 同一个 OpenList，但挂载名被改过、认不出上游 → 显示「待定云」图标
  {
    id: "r5",
    name: "家庭备份",
    cloud: "webdav",
    uri: "rclone:wd-71c5aa:mydisk/photo",
    dav_url: "http://192.168.31.9:5244/dav",
    dav_user: "admin",
    count: 3,
    task_count: 0,
    latest_time: daysAgo(2, 5, 0),
    cached: true,
  },
];

const tasks = [
  {
    id: "t1",
    name: "相册备份",
    repo_id: "r1",
    paths: ["/vol1/1000/Photos", "/vol1/1000/Camera"],
    dest_cloud: "tianyi",
    dest_subdir: "个人云/BackVault/photos",
    excludes: ["**/.thumbnails", "**/*.tmp"],
    last_status: "success",
    last_done_at: daysAgo(0, 3, 12),
    last_run_at: daysAgo(0, 3, 8),
    last_bytes: 214 * GB,
    last_files: 12483,
    last_duration_sec: 252,
    next_run_at: daysAhead(1, 3, 0),
    enabled: true,
    sched_mode: "daily",
    daily_days: 1,
    daily_time: "03:00",
    keep_last: 5,
  },
  {
    id: "t2",
    name: "文档资料",
    repo_id: "r2",
    paths: ["/vol1/1000/Documents"],
    dest_cloud: "y115",
    dest_subdir: "backup/documents",
    excludes: ["**/node_modules", "**/*.tmp"],
    last_status: "running",
    last_done_at: daysAgo(1, 2, 41),
    last_run_at: daysAgo(0, 9, 32),
    last_bytes: 62 * GB,
    last_files: 8421,
    last_duration_sec: 96,
    next_run_at: daysAhead(1, 2, 30),
    enabled: true,
    sched_mode: "daily",
    daily_days: 1,
    daily_time: "02:30",
    keep_last: 3,
  },
  {
    id: "t3",
    name: "家庭视频",
    repo_id: "r3",
    paths: ["/vol1/2000/Videos"],
    dest_cloud: "webdav",
    dest_subdir: "nas/video",
    excludes: [],
    last_status: "failed",
    last_run_at: daysAgo(1, 4, 0),
    last_message: "云端空间不足：需要 512 GB，可用 96 GB",
    next_run_at: daysAhead(5, 4, 0),
    enabled: true,
    sched_mode: "weekly",
    weekly_days: [0],
    daily_time: "04:00",
    keep_last: 2,
  },
  {
    id: "t4",
    name: "工作目录",
    repo_id: "r1",
    paths: ["/vol1/1000/Work"],
    dest_cloud: "tianyi",
    dest_subdir: "个人云/BackVault/work",
    excludes: [],
    last_status: "success",
    last_done_at: daysAgo(5, 22, 18),
    last_bytes: 8 * GB,
    last_files: 1204,
    last_duration_sec: 88,
    next_run_at: "",
    enabled: false,
    sched_mode: "manual",
    keep_last: 0,
  },
];

const snapsR1 = [
  { n: 0, files: 12483, bytes: 214 * GB, tag: "daily", task: "t1", name: "相册备份" },
  { n: 1, files: 12355, bytes: 213 * GB, tag: "daily", task: "t1", name: "相册备份" },
  { n: 2, files: 12227, bytes: 212 * GB, tag: "daily", task: "t1", name: "相册备份" },
  { n: 3, files: 12227, bytes: 212 * GB, tag: "daily", task: "t1", name: "相册备份" },
  { n: 4, files: 12185, bytes: 211.6 * GB, tag: "weekly", task: "t1", name: "相册备份" },
  { n: 6, files: 11980, bytes: 209 * GB, tag: "daily", task: "t4", name: "工作目录" },
  { n: 7, files: 11940, bytes: 208.4 * GB, tag: "daily", task: "t4", name: "工作目录" },
];

const snapshots = snapsR1.map((s, i) => ({
  id: `a7f3c2e1${i}9d4b`,
  short_id: `a7f3c2e${i}`,
  time: daysAgo(s.n, 3, 12 - i),
  hostname: "fnos",
  paths: s.task === "t1" ? ["/vol1/1000/Photos", "/vol1/1000/Camera"] : ["/vol1/1000/Work"],
  tags: [s.tag],
  task_id: s.task,
  task_name: s.name,
  summary: { total_files: s.files, total_bytes: Math.round(s.bytes) },
}));

const historyItems = [
  {
    kind: "backup", id: "b1", task_id: "t1", task_name: "相册备份",
    time: daysAgo(0, 3, 12), status: "success",
    one_line: "新增 128 个文件 · 1.2 GB · 用时 4 分 12 秒",
    file: "2026-11-10_031247_t1.log",
    nodes: [
      { time: "03:12:47", msg: "开始备份，读取源目录" },
      { time: "03:12:49", msg: "仓库已锁定，载入索引" },
      { time: "03:13:20", msg: "扫描完成，发现 134 个变动文件" },
      { time: "03:16:52", msg: "上传完成，写入快照 a7f3c2e1" },
      { time: "03:16:59", msg: "备份成功" },
    ],
  },
  {
    kind: "backup", id: "b2", task_id: "t2", task_name: "文档资料",
    time: daysAgo(0, 2, 41), status: "success",
    one_line: "新增 36 个文件 · 84 MB · 用时 1 分 03 秒",
    file: "2026-11-10_024100_t2.log",
    nodes: [{ time: "02:41:00", msg: "开始备份" }, { time: "02:42:03", msg: "备份成功" }],
  },
  {
    kind: "restore", id: "j1", task_id: "t1", task_name: "相册备份",
    time: daysAgo(0, 9, 41), status: "running",
    snapshot: "a7f3c2e19d4b", dest: "custom", target: "/vol1/1000/Photos-restore",
    conflict: "newer", includes: [],
    one_line: "已还原 4,693 / 12,355 个文件 · 38%",
    nodes: [{ time: "09:41:02", msg: "开始还原到 /vol1/1000/Photos-restore" }],
  },
  {
    kind: "backup", id: "b3", task_id: "t1", task_name: "相册备份",
    time: daysAgo(1, 3, 12), status: "success",
    one_line: "新增 128 个文件 · 1.2 GB · 用时 4 分 05 秒",
    file: "2026-11-09_031247_t1.log",
  },
  {
    kind: "backup", id: "b4", task_id: "t3", task_name: "家庭视频",
    time: daysAgo(1, 4, 0), status: "failed",
    one_line: "云端空间不足：需要 512 GB，可用 96 GB",
    file: "2026-11-09_040000_t3.log",
    nodes: [{ time: "04:00:00", msg: "开始备份" }, { time: "04:00:41", msg: "错误：云端空间不足" }],
  },
  {
    kind: "restore", id: "j2", task_id: "t2", task_name: "文档资料",
    time: daysAgo(1, 15, 22), status: "success",
    snapshot: "b2e19c44", dest: "original", conflict: "skip",
    includes: ["/vol1/1000/Documents/报价单"], one_line: "还原 1 个目录 · 12 个文件 · 86 MB",
    nodes: [{ time: "15:22:10", msg: "开始还原" }, { time: "15:22:58", msg: "还原完成" }],
  },
  {
    kind: "backup", id: "b5", task_id: "t2", task_name: "文档资料",
    time: daysAgo(2, 2, 30), status: "success",
    one_line: "新增 12 个文件 · 18 MB · 用时 41 秒",
    file: "2026-11-08_023000_t2.log",
  },
];

const dirs = [
  { name: "Photos", is_dir: true },
  { name: "Documents", is_dir: true },
  { name: "Work", is_dir: true },
  { name: "Videos", is_dir: true },
];

const routes = [
  [/^\/api\/status/, () => ({
    version: "0.3.9",
    restic_version: "0.17.3",
    rclone_version: "v1.68.1",
    user_root: "/vol1/1000",
    home: "/vol1/1000",
    roots: ["/vol1/1000", "/vol1/2000"],
    running_task: "t2",
  })],
  [/^\/api\/config/, () => ({ repos, tasks, restores: [] })],
  [/^\/api\/update/, () => ({
    enabled: true, current: "0.4.4", builtin: "0.4.4", from_overlay: false,
    checked_at: NOW.toISOString(), latest: "0.4.6", available: true, need_store: false,
    size: 3231778, phase: "idle", progress: 0,
    notes: [
      { version: "0.4.6", date: "2026-11-20", items: ["快照页支持按日期筛选", "修复 115 网盘大文件上传偶发超时"] },
      { version: "0.4.5", date: "2026-11-15", items: ["历史页新增导出 CSV", "还原向导记住上次的冲突策略"] },
    ],
  })],
  [/^\/api\/progress/, () => ({
    task_id: "t2",
    percent: 62.4,
    bytes_done: 38.4 * GB,
    total_bytes: 62 * GB,
    started_at: daysAgo(0, 9, 32),
    error_count: 0,
  })],
  [/^\/api\/repos\/summary/, () => ({ repos })],
  [/^\/api\/repos\/[^/]+\/snapshots/, (u) => {
    const id = u.split("/")[3];
    if (id === "r1") {
      return {
        snapshots,
        tasks: [
          { id: "t1", name: "相册备份", paths: ["/vol1/1000/Photos", "/vol1/1000/Camera"] },
          { id: "t4", name: "工作目录", paths: ["/vol1/1000/Work"] },
        ],
        cached_at: "2026-11-10T09:20:11+08:00",
        total_bytes: 214 * GB,
        latest_time: daysAgo(0, 3, 12),
      };
    }
    if (id === "r2") {
      return {
        snapshots: snapshots.slice(0, 3).map((s) => ({ ...s, task_id: "t2", task_name: "文档资料" })),
        tasks: [{ id: "t2", name: "文档资料", paths: ["/vol1/1000/Documents"] }],
        cached_at: "2026-11-10T09:20:11+08:00",
        total_bytes: 62 * GB,
        latest_time: daysAgo(0, 2, 41),
      };
    }
    return { snapshots: [], tasks: [{ id: "t3", name: "家庭视频", paths: ["/vol1/2000/Videos"] }], cached_at: "", total_bytes: 0, latest_time: "" };
  }],
  [/^\/api\/repos/, () => ({ repos })],
  [/^\/api\/history/, () => ({ items: historyItems })],
  [/^\/api\/restores/, () => ({
    restores: [
      {
        id: "j1", task_id: "t1", task_name: "相册备份", status: "running", pct: 38,
        snapshot: "a7f3c2e19d4b", dest: "custom", target: "/vol1/1000/Photos-restore",
        conflict: "newer", started_at: daysAgo(0, 9, 41), includes: [],
      },
    ],
  })],
  [/\/snapdiff/, () => ({ added: 128, changed: 6, removed: 2 })],
  [/\/files\?/, (u) => {
    const q = new URLSearchParams(u.split("?")[1] || "");
    const dir = q.get("dir") || "";
    const isRoot = !dir;
    const items = isRoot
      ? [
          { path: "/vol1/1000/Photos", name: "Photos", is_dir: true, size: 0 },
          { path: "/vol1/1000/Camera", name: "Camera", is_dir: true, size: 0 },
        ]
      : [
          { path: dir + "/2026", name: "2026", is_dir: true, size: 0 },
          { path: dir + "/2025", name: "2025", is_dir: true, size: 0 },
          { path: dir + "/手机导出", name: "手机导出", is_dir: true, size: 0 },
          { path: dir + "/相机原片", name: "相机原片", is_dir: true, size: 0 },
          { path: dir + "/相册索引.db", name: "相册索引.db", is_dir: false, size: 19 * 1024 * 1024 },
          { path: dir + "/2026-11-08 家庭聚会.mp4", name: "2026-11-08 家庭聚会.mp4", is_dir: false, size: 2.4 * GB },
        ];
    return { items, dir_total: items.length, truncated: false, parsed: 12483, elapsed: 1, roots: ["/vol1/1000/Photos", "/vol1/1000/Camera"] };
  }],
  [/\/tasks\/[^/]+\/snapshots/, () => ({ snapshots, cached_at: "", total_bytes: 214 * GB, latest_time: daysAgo(0, 3, 12) })],
  [/\/tasks\/[^/]+\/runs/, () => [
    { file: "2026-11-10_031247.log", time: daysAgo(0, 3, 12), status: "success", size: 8421 },
    { file: "2026-11-09_031247.log", time: daysAgo(1, 3, 12), status: "success", size: 8180 },
  ]],
  [/\/log\/summary/, () => ({
    status: "success",
    one_line: "新增 128 · 修改 6 · 删除 0 · 1.2 GB",
    files: 134,
    bytes: 1.2 * GB,
    duration_sec: 252,
    nodes: [
      { time: "03:12:47", msg: "开始备份，读取源目录" },
      { time: "03:12:49", msg: "仓库已锁定，载入索引" },
      { time: "03:13:20", msg: "扫描完成，发现 134 个变动文件" },
      { time: "03:16:52", msg: "上传完成，写入快照 a7f3c2e1" },
      { time: "03:16:59", msg: "备份成功" },
    ],
  })],
  [/\/log$/, () => ({
    content: [
      "03:12:47 INFO  使用仓库 189cloud:/个人云/BackVault",
      "03:12:47 INFO  source: /vol1/1000/Photos",
      "03:12:48 INFO  已加载排除规则 2 条（系统缓存、临时文件）",
      "03:12:49 INFO  repository a7f3c2e1 opened (restic 0.17.3)",
      "03:13:20 INFO  scan finished: 134 files, 1.2 GiB",
      "03:13:21 INFO  uploading 128 new blobs (concurrency 4)",
      "03:14:38 WARN  retry 1/3: chunk 4f9a… connection reset",
      "03:14:41 INFO  chunk 4f9a… uploaded (12.4 MiB)",
      "03:16:52 INFO  snapshot a7f3c2e1 saved",
      "03:16:59 OK    backup finished successfully in 4m12s",
    ].join("\n"),
  })],
  [/\/api\/browse/, (u) => ({ path: "/vol1/1000", dirs, user_root: "/vol1/1000" })],
  [/^\/api\/browse-cloud/, () => ({
    path: "/个人云/BackVault",
    dirs: [
      { name: "photos", is_dir: true },
      { name: "documents", is_dir: true },
      { name: "videos", is_dir: true },
      { name: "work", is_dir: true },
    ],
  })],
  [/^\/api\/scope/, () => ({ dirs })],
  [/^\/api\/reload-scope/, () => ({ ok: true })],
  [/^\/api\/settings/, () => ({ upload_limit_kb: 5120, limit_day_only: true, limit_from: "08:00", limit_to: "23:00", config_saved_at: "", recovery_saved_at: "2026-11-01T10:20:00+08:00" })],
  [/^\/api\/export\/recovery/, () => "量子密备 · 恢复信息（预览模式示例）"],
  [/^\/api\/export\/config/, () => ({ app: "encbackup", kind: "config", version: "0.4.6", repos: [], tasks: [] })],
  [/^\/api\/import\/config/, () => ({ ok: true, repos: 4, tasks: 4 })],
  [/^\/api\/test-dav/, () => ({ ok: true, msg: "连接成功 · 可写 · 剩余 812 GB" })],
  [/^\/api\/mkdir-cloud/, () => ({ ok: true })],
  [/\/(run|stop|delete|restore|verify|password)$/, () => ({ ok: true, id: "j-new" })],
];

export function installMock() {
  const real = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const url = typeof input === "string" ? input : input.url;
    const raw = url.replace(/^https?:\/\/[^/]+/, "");
    const at = raw.indexOf("/api/");
    if (at < 0) return real(input, init);
    const path = raw.slice(at);
    for (const [re, make] of routes) {
      if (re.test(path)) {
        const data = make(path);
        await new Promise((r) => setTimeout(r, 120));
        return new Response(JSON.stringify(data), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
    }
    return new Response(JSON.stringify({ error: "mock: 未实现的接口 " + path }), {
      status: 404,
      headers: { "Content-Type": "application/json" },
    });
  };
  // eslint-disable-next-line no-console
  console.info("[量子密备] 预览模式：已启用 Mock 数据");
}
