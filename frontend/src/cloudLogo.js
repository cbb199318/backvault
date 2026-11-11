// 云盘品牌标识
// ---------------------------------------------------------------------------
// 品牌 logo 位图源自各官方渠道（官网静态资源 / App Store 图标），WebDAV、
// OpenList 这类没有公开品牌资源包的类型走线性图标。
//
// 识别优先级（高 → 低）：
//   1. repo.brand        用户在建仓库时手动指定，最可靠
//   2. repo.cloud        天翼 / 115 是程序内固定枚举，直接映射
//   3. dav_url 域名      直连官网地址时能直接认出来
//   4. 子目录第一段      OpenList 把每个挂载点暴露成顶层目录，路径自带网盘名
//   5. 仓库名关键字      用户自己起的名字里带了「百度」「115」之类
//   6. OpenList 特征     认不出上游但能确认是中转，显示待定云图标
//   7. 兜底              WebDAV → server，其余 → cloud
// ---------------------------------------------------------------------------
import tianyi from "./assets/cloud/tianyi.png";
import y115 from "./assets/cloud/y115.png";
import baidu from "./assets/cloud/baidu.png";
import aliyun from "./assets/cloud/aliyun.png";

export const BRANDS = {
  tianyi: { label: "天翼云盘", logo: tianyi, icon: "cloud" },
  y115: { label: "115", logo: y115, icon: "cloud" },
  baidu: { label: "百度网盘", logo: baidu, icon: "cloud" },
  aliyun: { label: "阿里云盘", logo: aliyun, icon: "cloud" },
  openlist: { label: "OpenList 中转", logo: "", icon: "cloud-question" },
  webdav: { label: "WebDAV", logo: "", icon: "server" },
  other: { label: "云端", logo: "", icon: "cloud" },
};

// 新建仓库时可手动选择的项（auto 表示交给自动识别）
export const BRAND_OPTIONS = [
  ["auto", "自动识别"],
  ["tianyi", "天翼云盘"],
  ["y115", "115 网盘"],
  ["baidu", "百度网盘"],
  ["aliyun", "阿里云盘"],
  ["openlist", "OpenList 中转"],
  ["webdav", "通用 WebDAV"],
];

// 域名 → 品牌。直连云盘官网时命中，走 OpenList 时不会命中。
const HOST_RULES = [
  [/(^|\.)189\.cn$|cloud\.189\.cn/, "tianyi"],
  [/(^|\.)115\.com$|(^|\.)115cdn\.com$/, "y115"],
  [/(^|\.)baidu\.com$|(^|\.)baidupcs\.com$/, "baidu"],
  [/(^|\.)alipan\.com$|(^|\.)aliyundrive\.com$/, "aliyun"],
];

// 关键字 → 品牌。用于路径段和仓库名。
const WORD_RULES = [
  [/天翼|189|tianyi|telecom/i, "tianyi"],
  [/115/, "y115"],
  [/百度|baidu|pandownload/i, "baidu"],
  [/阿里|aliyun|alipan|aliyundrive/i, "aliyun"],
];

function matchRules(text, rules) {
  const t = text || "";
  if (!t) return "";
  for (const [re, key] of rules) if (re.test(t)) return key;
  return "";
}

function hostOf(url) {
  if (!url) return "";
  try {
    return new URL(url).hostname.toLowerCase();
  } catch {
    return "";
  }
}

// 取路径段。兼容两种存法：
//   rclone:<远端名>:<子目录>   后端实际存的形式
//   https://host:port/dav/xxx  mock 里出现过、以及部分旧数据
function pathSegments(repo) {
  let raw = (repo.uri || "").replace(/^rclone:/, "");
  if (/^https?:\/\//i.test(raw)) {
    try {
      return new URL(raw).pathname.split("/").filter(Boolean);
    } catch {
      return [];
    }
  }
  const i = raw.indexOf(":");
  if (i >= 0) raw = raw.slice(i + 1);
  return raw.split("/").filter(Boolean);
}

// OpenList / AList 的特征：默认端口 5244，或名字、地址里直接写了
function looksLikeOpenList(repo) {
  const url = repo.dav_url || "";
  if (/:5244(\/|$)/.test(url)) return true;
  const hay = `${repo.name || ""} ${url}`;
  return /openlist|alist/i.test(hay);
}

/** 判定品牌 key */
export function brandOf(repo) {
  if (!repo) return "other";

  const explicit = repo.brand;
  if (explicit && explicit !== "auto" && BRANDS[explicit]) return explicit;

  if (repo.cloud === "tianyi" || repo.cloud === "y115") return repo.cloud;

  const byHost = matchRules(hostOf(repo.dav_url) || hostOf(repo.uri), HOST_RULES);
  if (byHost) return byHost;

  const seg = pathSegments(repo)[0] || "";
  const byPath = matchRules(seg, WORD_RULES);
  if (byPath) return byPath;

  const byName = matchRules(repo.name, WORD_RULES);
  if (byName) return byName;

  if (looksLikeOpenList(repo)) return "openlist";

  return repo.cloud === "webdav" ? "webdav" : "other";
}

/** 从任意文本（目录名、备注等）推断品牌，供上层在仓库信息不足时补信号 */
export function brandFromText(text) {
  return matchRules(text, WORD_RULES);
}

// 同时接受品牌 key 和 repo 对象，调用方不用关心
function resolve(x) {
  if (!x) return "other";
  if (typeof x === "string") return BRANDS[x] ? x : "other";
  return brandOf(x);
}

/** 品牌 logo 地址，空串表示没有图片、调用方回退到线性图标 */
export function cloudLogo(x) {
  const b = BRANDS[resolve(x)];
  return (b && b.logo) || "";
}

/** 回退用的线性图标名 */
export function cloudIcon(x) {
  const b = BRANDS[resolve(x)];
  return (b && b.icon) || "cloud";
}

/** 品牌名，用于 alt 和提示 */
export function cloudLabel(x) {
  const b = BRANDS[resolve(x)];
  return (b && b.label) || "云端";
}
