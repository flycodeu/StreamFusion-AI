import { createApp, h } from "vue";
import { ElConfigProvider } from "element-plus";
import zhCn from "element-plus/es/locale/lang/zh-cn";
import "element-plus/dist/index.css";
import "./style.css";
import App from "./App.vue";

createApp(() => h(ElConfigProvider, { locale: zhCn }, () => h(App))).mount("#app");
