<template>
  <div class="tpick">
    <div class="tp-dd" ref="hRoot">
      <button type="button" class="tp-btn" ref="hBtn" @click="toggle('h')">
        <span>{{ pad(h) }} 时</span><i class="tp-arr">▾</i>
      </button>
    </div>
    <span class="tp-sep">:</span>
    <div class="tp-dd" ref="mRoot">
      <button type="button" class="tp-btn" ref="mBtn" @click="toggle('m')">
        <span>{{ pad(m) }} 分</span><i class="tp-arr">▾</i>
      </button>
    </div>
    <Teleport to="body">
      <div class="tp-pop" ref="popRef" :class="{ open }">
        <div class="tp-item" v-for="v in opts" :key="v" :class="{ on: v === sel }" @click="pick(v)">{{ pad(v) }}</div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";

const props = defineProps({
  modelValue: { type: String, default: "03:00" },
});
const emit = defineEmits(["update:modelValue"]);

const pad = (n) => String(n).padStart(2, "0");
const parts = computed(() => (props.modelValue || "03:00").split(":"));
const h = computed(() => Math.min(23, Math.max(0, parseInt(parts.value[0], 10) || 0)));
const m = computed(() => Math.min(55, Math.max(0, Math.round((parseInt(parts.value[1], 10) || 0) / 5) * 5)));

const open = ref(false);
const which = ref("h");
const sel = ref(0);
const opts = computed(() => (which.value === "h" ? [...Array(24).keys()] : [...Array(12).keys()].map((i) => i * 5)));
const rootRef = ref(null);
const btnRef = computed(() => (which.value === "h" ? hBtn.value : mBtn.value));
const hRoot = ref(null), mRoot = ref(null), hBtn = ref(null), mBtn = ref(null), popRef = ref(null);

function toggle(w) {
  if (open.value && which.value === w) { open.value = false; return; }
  which.value = w;
  sel.value = w === "h" ? h.value : m.value;
  open.value = true;
  nextTick(placePop);
}
function placePop() {
  const btn = btnRef.value, pop = popRef.value;
  if (!btn || !pop || !open.value) return;
  const r = btn.getBoundingClientRect();
  pop.style.left = r.left + "px";
  pop.style.width = r.width + "px";
  pop.style.top = r.bottom + 6 + "px";
  const ph = pop.offsetHeight;
  if (r.bottom + 6 + ph > window.innerHeight - 8 && r.top - 6 - ph > 8) {
    pop.style.top = r.top - 6 - ph + "px";
  }
}
function pick(v) {
  const nh = which.value === "h" ? v : h.value;
  const nm = which.value === "m" ? v : m.value;
  emit("update:modelValue", pad(nh) + ":" + pad(nm));
  open.value = false;
}
function onDocClick(e) {
  if (!open.value) return;
  if (hRoot.value?.contains(e.target) || mRoot.value?.contains(e.target)) return;
  if (popRef.value?.contains(e.target)) return;
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

<style scoped>
.tpick { display: flex; align-items: center; gap: 8px; }
.tp-dd { flex: 1; }
.tp-btn {
  display: flex; align-items: center; justify-content: space-between; width: 100%;
  border: 1px solid #d1d5db; border-radius: 10px; padding: 9px 13px; background: #fff;
  font-size: 15px; font-weight: 600; cursor: pointer; font-family: inherit;
  font-variant-numeric: tabular-nums; transition: border-color .15s, box-shadow .15s;
}
.tp-btn:hover { border-color: #3370ff; }
.tp-arr { color: #c2c7d0; font-style: normal; font-size: 12px; }
.tp-sep { color: #9ca3af; font-weight: 700; }
.tp-pop {
  position: fixed; z-index: 3000;
  background: #fff; border: 1px solid #e5e7eb; border-radius: 12px;
  box-shadow: 0 8px 28px rgba(0,0,0,.14); overflow-y: auto; max-height: 264px;
  overscroll-behavior: contain; /* 滚到头不带动背后页面 */
  opacity: 0; transform: translateY(-6px); pointer-events: none; transition: opacity .18s, transform .18s;
}
.tp-pop.open { opacity: 1; transform: none; pointer-events: auto; }
.tp-item {
  padding: 10px 0; text-align: center; font-size: 14px; cursor: pointer;
  font-variant-numeric: tabular-nums;
}
.tp-item:hover { background: #f0f5ff; color: #3370ff; }
.tp-item.on { background: #eef4ff; color: #3370ff; font-weight: 600; }
</style>
