<template>
  <Teleport to="body">
    <div class="cp-mask" v-if="show" @click.self="close">
      <div class="cp-sheet" ref="sheetEl">
        <div class="cp-head">
          <h3>备份目的地</h3>
          <button class="cp-x" @click="close">✕</button>
        </div>

        <div class="cp-body" ref="bodyEl">
          <!-- 新增云端 -->
          <div class="cp-item add" @click="onAdd">
            <div class="cp-ibox"><Icon name="plus" :size="14" /></div>
            <div class="cp-itxt"><div class="nm">新增云端</div></div>
          </div>

          <!-- 已有仓库 -->
          <div class="cp-item" v-for="r in repos" :key="r.id" @click="onPick(r)">
            <div class="cp-ibox">
              <img v-if="cloudLogo(r)" class="cp-logo" :src="cloudLogo(r)" :alt="cloudLabel(r)" :title="cloudLabel(r)" />
              <Icon v-else :name="cloudIcon(r)" :size="22" />
            </div>
            <div class="cp-itxt">
              <div class="nm">{{ r.name }}</div>
              <div class="ds">{{ subOf(r) }}</div>
            </div>
            <button class="cp-more" title="更多" @click.stop="openMenu($event, r)"><Icon name="dots" :size="16" /></button>
          </div>

          <div class="cp-empty" v-if="!repos.length">
            <p>还没有云端仓库</p>
            <p class="hint">点上方「新增云端」添加天翼云盘 / 115 / WebDAV 仓库</p>
          </div>
        </div>
      </div>
    </div>

    <!-- <Icon name="dots" :size="16" /> 菜单 -->
    <div class="cp-ctx" v-if="menu.show" :style="{ left: menu.x + 'px', top: menu.y + 'px' }" @click.stop>
      <button @click="doReveal">
        <Icon name="eye" :size="15" />
        查看仓库密码
      </button>
      <button @click="doCopy">
        <Icon name="copy" :size="15" />
        复制仓库密码
      </button>
      <div class="sep"></div>
      <button class="danger" @click="doRemove">
        <Icon name="trash" :size="15" />
        移除云端仓库
      </button>
    </div>

    <!-- 密码气泡遮罩：点外部只关气泡，不穿透到下层 -->
    <div class="cp-pw-mask" v-if="pw.show" @click="onMaskClick"></div>

    <!-- 密码气泡 -->
    <div class="cp-pw" v-if="pw.show" :style="{ left: pw.x + 'px', top: pw.y + 'px' }" @click.stop>
      <span>
        <span class="lb">{{ pw.name }}</span>
        <code>{{ pw.loading ? "读取中…" : pw.value || "（空）" }}</code>
      </span>
      <button class="cp-btn" :disabled="pw.loading || !pw.value" @click="copyPw">复制</button>
    </div>
  </Teleport>
</template>

<script setup>
import Icon from "./Icon.vue";
import { cloudLogo, cloudLabel, cloudIcon } from "./cloudLogo.js";
import { reactive, ref, nextTick, onBeforeUnmount } from "vue";

const props = defineProps({
  show: { type: Boolean, default: false },
  repos: { type: Array, default: () => [] },
  fetchPw: { type: Function, required: true }, // (repoId) => Promise<string>
  toast: { type: Function, default: () => {} }, // (msg, type)
});
const emit = defineEmits(["update:show", "select", "add", "remove"]);

const sheetEl = ref(null);
const menu = reactive({ show: false, x: 0, y: 0, repo: null });
const pw = reactive({ show: false, x: 0, y: 0, name: "", value: "", loading: false });

// 副标题：WebDAV 显示 账号@地址；云盘显示 URI 的 rclone 路径
function subOf(r) {
  if (r.cloud === "webdav") {
    const u = (r.dav_url || "").replace(/^https?:\/\//, "");
    return (r.dav_user ? r.dav_user + "@" : "") + (u || "WebDAV");
  }
  const uri = (r.uri || "").replace(/^rclone:/, "");
  return uri || "—";
}

function close() {
  menu.show = false;
  pw.show = false;
  emit("update:show", false);
}
function onAdd() {
  close();
  emit("add");
}
function onPick(r) {
  emit("select", r);
  close();
}

function openMenu(e, r) {
  pw.show = false;
  const rc = e.currentTarget.getBoundingClientRect();
  menu.repo = r;
  menu.x = Math.min(Math.max(10, rc.right - 160), window.innerWidth - 176);
  menu.y = rc.bottom + 6;
  menu.show = true;
  nextTick(() => {
    const el = document.querySelector(".cp-ctx");
    if (!el) return;
    const h = el.offsetHeight;
    if (menu.y + h > window.innerHeight - 10) menu.y = Math.max(10, rc.top - h - 6);
  });
  // 菜单与密码气泡共用“点外部关闭”监听
  bindDocClick();
}
function bindDocClick() {
  document.removeEventListener("click", onDocClick, true);
  // 捕获阶段监听：能在点击落到目标元素之前拦下来
  setTimeout(() => document.addEventListener("click", onDocClick, true), 0);
}
function onDocClick(ev) {
  const t = ev.target;
  if (t.closest && (t.closest(".cp-pw") || t.closest(".cp-ctx"))) return;
  const pwOpen = pw.show;
  pw.show = false;
  menu.show = false;
  if (pwOpen) {
    // 密码气泡开着时：这次点击只用于关气泡，不触发其它任何操作（选仓库 / 关弹层 / 搜索等）
    ev.stopPropagation();
    if (ev.stopImmediatePropagation) ev.stopImmediatePropagation();
  }
  document.removeEventListener("click", onDocClick, true);
}
// 气泡遮罩点击：只关气泡，不穿透
function onMaskClick(ev) {
  ev.stopPropagation();
  if (ev.stopImmediatePropagation) ev.stopImmediatePropagation();
  pw.show = false;
  menu.show = false;
  document.removeEventListener("click", onDocClick, true);
}
async function loadPw(anchorEl) {
  const r = menu.repo;
  if (!r) return "";
  const rc = anchorEl.getBoundingClientRect();
  // 同一仓库密码已读过 → 直接复用，但气泡必须重新显示（原来这里直接 return 导致第二次点没反应）
  const cached = pw.value && pw.name === r.name ? pw.value : "";
  pw.name = r.name;
  pw.x = Math.min(Math.max(10, rc.left - 40), window.innerWidth - 270);
  pw.y = rc.bottom + 8;
  pw.show = true;
  bindDocClick(); // 气泡一出现就能“点外部关闭”，不等密码读回
  if (cached) {
    pw.value = cached;
    pw.loading = false;
    nextTick(flipPw);
    return cached;
  }
  pw.value = "";
  pw.loading = true;
  try {
    const v = await props.fetchPw(r.id);
    pw.value = v || "";
  } catch (e) {
    props.toast(e.message || "读取密码失败", "bad");
    pw.show = false;
  }
  pw.loading = false;
  nextTick(flipPw);
  return pw.value;
}

// 气泡超出视口时向上/向左翻，避免被边缘截断
function flipPw() {
  const el = document.querySelector(".cp-pw");
  if (!el) return;
  const h = el.offsetHeight;
  const w = el.offsetWidth;
  if (pw.y + h > window.innerHeight - 10) pw.y = Math.max(10, pw.y - h - 44);
  if (pw.x + w > window.innerWidth - 10) pw.x = Math.max(10, window.innerWidth - w - 10);
}
async function doReveal() {
  const el = document.querySelector(".cp-ctx button");
  menu.show = false;
  await loadPw(el || sheetEl.value);
}
async function doCopy() {
  const el = document.querySelector(".cp-ctx button");
  menu.show = false;
  const v = await loadPw(el || sheetEl.value);
  if (v) copyPw();
}
async function copyPw() {
  try {
    await navigator.clipboard.writeText(pw.value);
    props.toast("密码已复制到剪贴板");
  } catch {
    props.toast("复制失败，请手动选择复制", "bad");
  }
}
function doRemove() {
  const r = menu.repo;
  menu.show = false;
  if (r) emit("remove", r);
}

onBeforeUnmount(() => document.removeEventListener("click", onDocClick, true));
</script>

<style scoped>
.cp-mask {
  position: fixed; inset: 0; z-index: 3000;
  background: rgba(17, 20, 26, 0.45);
  display: flex; align-items: center; justify-content: center; padding: 16px;
}
.cp-sheet {
  background: var(--cell); border-radius: 20px; width: 430px; max-width: 100%;
  max-height: 86vh; display: flex; flex-direction: column; overflow: hidden;
  box-shadow: 0 20px 60px rgba(20, 25, 35, 0.24);
}
.cp-head { position: relative; padding: 18px 20px 12px; text-align: center; flex-shrink: 0; }
.cp-head h3 { font-size: 16.5px; font-weight: 600; color: var(--t1); margin: 0; }
.cp-x {
  position: absolute; right: 14px; top: 13px; width: 32px; height: 32px;
  border: none; background: none; font-size: 18px; color: var(--t3);
  cursor: pointer; border-radius: 9px; line-height: 1;
}
.cp-x:hover { background: var(--fill); color: var(--t1); }
.cp-body {
  padding: 4px 16px 20px; overflow-y: auto; overscroll-behavior: contain;
  display: flex; flex-direction: column; gap: 11px;
}
.cp-item {
  display: flex; align-items: center; gap: 14px; background: var(--cell);
  border-radius: 16px; padding: 14px 14px; cursor: pointer;
  box-shadow: 0 1px 3px rgba(20, 25, 35, 0.07);
  transition: background 0.14s, transform 0.1s;
}
.cp-item:hover { background: var(--fill-subtle); }
.cp-item:active { transform: scale(0.98); }
.cp-ibox {
  width: 46px; height: 46px; flex-shrink: 0;
  display: flex; align-items: center; justify-content: center;
}
.cp-logo { max-width: 40px; max-height: 28px; object-fit: contain; display: block; }
.cp-itxt { flex: 1; min-width: 0; }
.cp-itxt .nm {
  font-size: 14.5px; font-weight: 600; color: var(--t1);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.cp-itxt .ds {
  font-size: 12.5px; color: var(--t3); margin-top: 4px;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.cp-more {
  width: 34px; height: 34px; border: none; background: none; color: var(--t3);
  font-size: 19px; line-height: 1; border-radius: 9px; cursor: pointer; flex-shrink: 0;
}
.cp-more:hover { background: var(--fill); color: var(--t1); }
.cp-empty { text-align: center; padding: 26px 10px 10px; color: var(--t3); font-size: 13px; }
.cp-empty .hint { font-size: 12px; margin-top: 6px; color: var(--t3); }

/* 徽标配色 */

/* <Icon name="dots" :size="16" /> 菜单 */
.cp-ctx {
  position: fixed; z-index: 3100; background: var(--cell); border-radius: 12px;
  box-shadow: 0 10px 34px rgba(20, 25, 35, 0.2); padding: 6px; min-width: 160px;
}
.cp-ctx button {
  display: flex; align-items: center; gap: 10px; width: 100%; border: none;
  background: none; padding: 10px 12px; border-radius: 8px; font-size: 13.5px;
  color: var(--t1); cursor: pointer; text-align: left;
}
.cp-ctx button:hover { background: var(--fill); }
.cp-ctx button.danger { color: var(--c-red); }
.cp-ctx .sep { height: 1px; background: var(--fill); margin: 4px 8px; }

/* 密码气泡遮罩：气泡打开时覆盖弹层之上，点它只关气泡、不穿透 */
.cp-pw-mask {
  position: fixed; inset: 0; z-index: 3050;
  background: rgba(17, 20, 26, 0.28);
}
/* 密码气泡 */
.cp-pw {
  position: fixed; z-index: 3100; background: #23262b; color: #e9ebee;
  border-radius: 12px; padding: 11px 14px; display: flex; align-items: center;
  gap: 12px; box-shadow: 0 10px 30px rgba(0, 0, 0, 0.32); max-width: 320px;
}
.cp-pw .lb { color: var(--t3); font-size: 11.5px; display: block; margin-bottom: 3px; }
.cp-pw code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  letter-spacing: 0.3px; font-size: 13px; word-break: break-all;
}
.cp-pw .cp-btn {
  border: none; background: rgba(255, 255, 255, 0.14); color: #fff;
  border-radius: 7px; padding: 5px 10px; font-size: 12px; cursor: pointer; flex-shrink: 0;
}
.cp-pw .cp-btn:hover { background: rgba(255, 255, 255, 0.24); }

@media (max-width: 560px) {
  .cp-mask { align-items: flex-end; padding: 0; }
  .cp-sheet { width: 100%; max-width: 100%; border-radius: 20px 20px 0 0; max-height: 88vh; }
  .cp-head { padding: 20px 20px 12px; }
}
</style>
