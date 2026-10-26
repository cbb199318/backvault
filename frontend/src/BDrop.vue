<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";

const props = defineProps({
  modelValue: { type: [String, Number], default: "" },
  options: { type: Array, default: () => [] }, // { value, title, sub, icon, iconClass }
  placeholder: { type: String, default: "请选择" },
  disabled: { type: Boolean, default: false },
  keepOpen: { type: Boolean, default: false }, // true 时点面板外不关闭（供上层确认弹窗接管点击，如移除云端弹窗）
});
const emit = defineEmits(["update:modelValue", "change", "eye", "remove", "copypw"]);

const open = ref(false);
const root = ref(null);
const btnRef = ref(null);
const popRef = ref(null);
const cur = computed(() => props.options.find((o) => o.value === props.modelValue) || null);

function pick(o) {
  emit("update:modelValue", o.value);
  emit("change", o.value);
  open.value = false;
}
// 面板 Teleport 到 body，用 fixed 定位避开 sec-card/modal-body 的 overflow 裁剪
function placePop() {
  const btn = btnRef.value, pop = popRef.value;
  if (!btn || !pop || !open.value) return;
  const r = btn.getBoundingClientRect();
  pop.style.left = r.left + "px";
  pop.style.width = r.width + "px";
  pop.style.top = r.bottom + 6 + "px";
  const h = pop.offsetHeight;
  if (r.bottom + 6 + h > window.innerHeight - 8 && r.top - 6 - h > 8) {
    pop.style.top = r.top - 6 - h + "px";
  }
}
function toggle() {
  if (props.disabled) return;
  open.value = !open.value;
  if (open.value) nextTick(placePop);
}
function onDocClick(e) {
  if (!open.value) return;
  if (props.keepOpen) return; // 上层有确认弹窗时由它接管：关弹窗、下拉保持
  if (root.value && root.value.contains(e.target)) return;
  if (popRef.value && popRef.value.contains(e.target)) return;
  open.value = false;
}
function onScroll() { if (open.value) placePop(); }
onMounted(() => {
  document.addEventListener("click", onDocClick);
  window.addEventListener("scroll", onScroll, true);
  window.addEventListener("resize", onScroll);
});
onUnmounted(() => {
  document.removeEventListener("click", onDocClick);
  window.removeEventListener("scroll", onScroll, true);
  window.removeEventListener("resize", onScroll);
});
</script>

<template>
  <div class="bdrop" :class="{ open, disabled }" ref="root">
    <button type="button" class="bdrop-btn" ref="btnRef" :disabled="disabled" @click="toggle">
      <template v-if="cur">
        <span v-if="cur.icon" class="bdrop-ic" :class="cur.iconClass">{{ cur.icon }}</span>
        <span class="bdrop-tx"><b>{{ cur.title }}</b><span v-if="cur.sub">{{ cur.sub }}</span></span>
      </template>
      <template v-else>
        <span class="bdrop-tx"><span class="ph">{{ placeholder }}</span></span>
      </template>
      <i class="bdrop-arr">▾</i>
    </button>
    <Teleport to="body">
      <div class="bdrop-pop" ref="popRef" :class="{ open }" v-show="open">
        <div class="bdrop-item" v-for="o in options" :key="o.value" :class="{ on: o.value === modelValue }" @click="pick(o)">
          <span v-if="o.icon" class="bdrop-ic" :class="o.iconClass">{{ o.icon }}</span>
          <span class="bdrop-tx">
            <b>{{ o.title }}<i v-if="o.dot" class="dot" :class="o.dot"></i></b>
            <span v-if="o.pwShown" class="bdrop-pw mono">{{ o.pw }}<button type="button" class="bdrop-cp" @click.stop="emit('copypw', o)">复制</button></span>
            <span v-else-if="o.sub">{{ o.sub }}</span>
          </span>
          <span v-if="o.eyeable || o.xable" class="bdrop-acts">
            <button v-if="o.eyeable" type="button" class="bdrop-act" :title="o.pwShown ? '隐藏密码' : '查看密码'" @click.stop="emit('eye', o)">
              <svg v-if="o.pwShown" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z"/><circle cx="12" cy="12" r="3"/><path d="M4 4l16 16"/></svg>
              <svg v-else viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z"/><circle cx="12" cy="12" r="3"/></svg>
            </button>
            <button v-if="o.xable" type="button" class="bdrop-act bdrop-x" title="移除" @click.stop="emit('remove', o)">
              <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
            </button>
          </span>
        </div>
        <div class="bdrop-empty" v-if="!options.length">{{ placeholder }}</div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.bdrop { position: relative; user-select: none; }
.bdrop-btn {
  width: 100%; display: flex; align-items: center; gap: 12px;
  border: 1px solid #e5e7eb; border-radius: 12px; padding: 10px 14px;
  background: #fafbfc; cursor: pointer; text-align: left; font: inherit; color: inherit;
  transition: border-color .15s, box-shadow .15s, background .15s;
}
.bdrop-btn:hover { border-color: #c8d4f0; }
.bdrop.open .bdrop-btn { border-color: #3370ff; box-shadow: 0 0 0 3px rgba(51,112,255,.12); background: #fff; }
.bdrop.disabled { opacity: .55; }
.bdrop.disabled .bdrop-btn { cursor: not-allowed; }
.bdrop-ic {
  width: 34px; height: 34px; border-radius: 8px; flex: none;
  display: flex; align-items: center; justify-content: center; font-size: 17px;
}
.bdrop-ic.tianyi { background: #eef4ff; }
.bdrop-ic.y115 { background: #ecfdf5; }
.bdrop-ic.other { background: #f3f4f6; }
.bdrop-tx { flex: 1; min-width: 0; }
.bdrop-tx b { display: block; font-size: 14px; font-weight: 600; color: #1f2937; }
.bdrop-tx span { display: block; font-size: 12px; color: #9ca3af; margin-top: 1px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.bdrop-tx .ph { font-size: 14px; color: #9ca3af; }
.bdrop-arr { color: #9ca3af; font-size: 12px; transition: transform .2s; }
.bdrop.open .bdrop-arr { transform: rotate(180deg); }
.bdrop-pop {
  position: fixed; z-index: 3000;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  box-shadow: 0 8px 28px rgba(0,0,0,.14);
  max-height: 62vh; overflow-y: auto; overscroll-behavior: contain; /* 滚到头不带动背后页面 */
  opacity: 0; transform: translateY(-6px); pointer-events: none; transition: opacity .18s, transform .18s;
}
.bdrop-pop.open { opacity: 1; transform: none; pointer-events: auto; }
.bdrop-item { display: flex; align-items: center; gap: 12px; padding: 10px 14px; cursor: pointer; }
.bdrop-item + .bdrop-item { border-top: 1px solid #f3f4f6; }
.bdrop-item:hover { background: #f5f8ff; }
.bdrop-item.on { background: #eef4ff; }
/* 标题旁健康状态圆点（b-3） */
.dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-left: 7px; vertical-align: 1.5px; }
.dot.ok { background: #22c55e; }
.dot.bad { background: #ef4444; }
.dot.run { background: #3370ff; animation: dotPulse 1.2s ease-in-out infinite; }
.dot.idle { background: #d1d5db; }
@keyframes dotPulse { 0%,100% { opacity: 1; } 50% { opacity: .35; } }
/* 操作列：靠最右，桌面端悬停行才浮现（平时收起不占宽度） */
.bdrop-acts { display: flex; align-items: center; gap: 4px; flex: none; justify-content: flex-end; }
.bdrop-act {
  width: 24px; height: 24px; border-radius: 50%; border: none; flex: none;
  background: transparent; color: #b6bdc9; cursor: pointer; line-height: 1;
  display: flex; align-items: center; justify-content: center;
}
.bdrop-act svg { display: block; }
@media (hover: hover) {
  .bdrop-acts { width: 0; overflow: hidden; transition: width .15s; }
  .bdrop-item:hover .bdrop-acts, .bdrop-item:focus-within .bdrop-acts { width: auto; overflow: visible; }
  .bdrop-act:hover { background: #e8f0ff; color: #3370ff; }
  .bdrop-act.bdrop-x:hover { background: #fee2e2; color: #dc2626; }
}
@media (hover: none) {
  .bdrop-item:hover .bdrop-act { background: #f0f2f6; }
}
.bdrop-pw { font-family: ui-monospace, Menlo, monospace; color: #1f2937; font-size: 12px; letter-spacing: .4px; display: flex; align-items: center; gap: 8px; }
.bdrop-cp { border: none; background: #e8f0ff; color: #3370ff; font-size: 11px; border-radius: 6px; padding: 2px 8px; cursor: pointer; }
.bdrop-empty { padding: 14px; text-align: center; font-size: 13px; color: #9ca3af; }
</style>
