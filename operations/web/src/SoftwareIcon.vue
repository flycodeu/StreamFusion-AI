<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{ type: string }>();
const kind = computed(() => props.type.toUpperCase());
const initials: Record<string, string> = {
  GIT: "Git",
  NODE: "Js",
  DOCKER: "Dk",
  MINIO: "Mio",
  CODEX: "Cx",
  CLAUDE: "Cl",
  MYSQL: "My",
  REDIS: "R",
  NGINX: "N",
  PYTHON: "Py",
  ZLM: "Z",
  FFMPEG: "FF",
  SEAWEEDFS: "S3",
  MEDIAMTX: "MTX",
  JENKINS: "Jk",
  POSTGRESQL: "Pg",
  GITLAB: "Gl",
  PROMETHEUS: "Pm",
  NACOS: "Na",
  LABELSTUDIO: "Ls",
  LABEL_STUDIO: "Ls",
  "LABEL-STUDIO": "Ls",
  YTLABEL: "Yt",
};
</script>

<template>
  <span
    class="software-icon"
    :class="`icon-${kind.toLowerCase()}`"
    aria-hidden="true"
  >
    <svg v-if="kind === 'JAVA'" viewBox="0 0 40 40" fill="none">
      <path
        d="M13 20h15l-2 10H15zM28 21h3c5 0 3 7-4 7M11 34h21"
        stroke="currentColor"
        stroke-width="2.3"
        stroke-linejoin="round"
        stroke-linecap="round"
      />
      <path
        d="M19 16c-7-7 9-6 3-13M25 17c-4-4 5-6 5-10"
        stroke="#e88142"
        stroke-width="2"
        stroke-linecap="round"
      />
    </svg>
    <svg v-else-if="kind === 'MAVEN'" viewBox="0 0 40 40" fill="none">
      <path d="M9 32 29 6l-5 20-9 3 8-13-14 16Z" fill="currentColor" />
      <path
        d="m12 32 17-26"
        stroke="#e47e58"
        stroke-width="2"
        stroke-linecap="round"
      />
    </svg>
    <span v-else-if="initials[kind]">{{ initials[kind] }}</span>
    <svg v-else viewBox="0 0 40 40" fill="none"><path d="m20 7 12 7v13l-12 7-12-7V14l12-7Zm0 14 12-7M20 21 8 14m12 7v13M14 11l12 7" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" /></svg>
  </span>
</template>

<style scoped>
.software-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  flex: 0 0 auto;
  border-radius: 12px;
  background: #edf4fc;
  color: #3672b4;
  font:
    600 21px "Bahnschrift",
    sans-serif;
}
.software-icon svg {
  width: 38px;
  height: 38px;
}
.icon-maven {
  background: #f9edf1;
  color: #aa3f60;
}
.icon-git {
  background: #fff0e9;
  color: #be5738;
}
.icon-redis {
  background: #fdf0ef;
  color: #c34d46;
}
.icon-nginx {
  background: #eaf6ef;
  color: #2f8759;
}
.icon-labelstudio, .icon-label-studio, .icon-label_studio { background: #fff1ec; color: #b75c3b; }
.icon-ytlabel { background: #edf5f2; color: #397864; }
.icon-python {
  background: #fdf7e9;
  color: #3877a2;
}
.icon-zlm,
.icon-mediamtx,
.icon-ffmpeg {
  background: #eaf6f2;
  color: #2c8b70;
}
.icon-seaweedfs {
  background: #eef7f4;
  color: #438572;
}

.icon-codex {
  background: #edf3f1;
  color: #326c58;
}
.icon-claude {
  background: #faf0e9;
  color: #a96a48;
}
.icon-minio {
  background: #faedf1;
  color: #b33959;
}
</style>
