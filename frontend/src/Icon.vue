<script setup>
import PATHS from "./icons.js";

const props = defineProps({
  name: { type: String, required: true },
  size: { type: [Number, String], default: 16 },
  color: { type: String, default: "" },
});
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    aria-hidden="true"
    :style="color ? { color } : null"
    v-html="PATHS[name] || ''"
  />
</template>
