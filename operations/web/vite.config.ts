import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  plugins: [vue()],
  base: "/",
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            {
              name: "vendor",
              test: /node_modules/,
              priority: 20,
            },
            {
              name: "ops-workspace",
              test: /NodeEnvironment|OpsWorkspace/,
              priority: 15,
            },
            {
              name: "ops-software",
              test: /Software|ServiceInstall/,
              priority: 10,
            },
          ],
        },
      },
    },
  },
});
