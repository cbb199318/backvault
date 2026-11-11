import { createApp } from "vue";
import App from "./App.vue";
import { installMock } from "./mock.js";

// 预览构建（VITE_MOCK=1）时启用本地 Mock 数据；正式构建下该分支会被 tree-shake 掉
if (import.meta.env.VITE_MOCK === "1") installMock();

createApp(App).mount("#app");
