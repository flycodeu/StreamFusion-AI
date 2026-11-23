<script setup lang="ts">
import { computed } from "vue";
import type { Host } from "./hosts";
import { shellArgument } from "./officialInstallation";
import type { SoftwareInstance } from "./software";
import { softwareConnectionCommands, softwareDockerCommands } from "./softwareConnectionCommands";
import TerminalCommandEntry from "./TerminalCommandEntry.vue";

const props = defineProps<{ detail: SoftwareInstance; host?: Host }>();
const commands = computed(() => softwareConnectionCommands(props.detail));
const dockerCommands = computed(() => softwareDockerCommands(props.detail));
const mysql = computed(() => {
  const { softwareType, managementMode, installation } = props.detail;
  if (softwareType === "MYSQL" && managementMode === "MANAGED" && installation?.definition?.key === "mysql" &&
    (!installation.definition.commands || installation.definition.commands.builtin)) return installation;
});
const configDir = computed(() => {
  const path = props.detail.installationPaths?.config;
  return path?.startsWith("/") && path !== "/" && !/[\u0000-\u001f\u007f]/.test(path) ? path.replace(/\/+$/, "") : "";
});
</script>

<template>
  <section v-if="commands.length || dockerCommands.length" class="terminal-commands" aria-label="终端命令">
    <header><h3>终端命令</h3><span>在 {{ detail.hostName || host?.name || "软件所在主机" }} 的终端执行</span></header>
    <details v-if="mysql" class="command-help">
      <summary>MySQL 账号、初始密码与建库权限</summary>
      <div class="help-content">
        <dl v-if="mysql.config?.username || mysql.config?.database" class="mysql-account">
          <template v-if="mysql.config?.username"><dt>初始业务账号</dt><dd><code>{{ mysql.config.username }}</code></dd></template>
          <template v-if="mysql.config?.database"><dt>初始数据库</dt><dd><code>{{ mysql.config.database }}</code></dd></template>
        </dl>
        <p>忘记密码时，打开本实例的「登录账号」，在对应账号行点「复制密码」，输入 Ops 管理员登录密码后点「验证并复制」。安装凭据对应安装时设置的密码。</p>
        <p v-if="mysql.definition?.commands?.builtin"><strong>建库提示 1044</strong> 时，到「数据库管理 → 授予数据库权限」，按报错填写业务账号和允许来源（例如 <code>@'%'</code> 的来源填 <code>%</code>）。数据库填要创建的库名，权限选「读写」，确认执行后再建库。要用该账号创建外键，还需请数据库管理员授予对应库的 <code>REFERENCES</code> 权限。</p>
        <p v-if="mysql.official">Ubuntu 官方软件包可用下方「系统管理员本机连接」命令以 root 登录。</p>
        <details v-else-if="detail.runtimeKind === 'DOCKER' || detail.runtimeKind === 'SYSTEMD'" class="host-password-help">
          <summary>从主机查看安装密码</summary>
          <div v-if="detail.runtimeKind === 'DOCKER'" class="help-content">
            <p>使用主机管理员账号，在「配置与运行」显示的配置目录中查看 <code>root-password</code><span v-if="mysql.config?.username">（root）或 <code>user-password</code>（业务账号）</span>。</p>
            <template v-if="configDir">
              <TerminalCommandEntry v-if="mysql.config?.username" :label="`查看 ${mysql.config.username} 的安装密码`" :command="'sudo cat -- ' + shellArgument(configDir + '/user-password')" />
              <TerminalCommandEntry label="查看 root 的安装密码" :command="'sudo cat -- ' + shellArgument(configDir + '/root-password')" />
            </template>
          </div>
          <p v-else>主机管理员可从「配置与运行」显示的配置目录中查看 <code>health.cnf</code> 里的 root 登录资料。<span v-if="mysql.config?.username && mysql.definition?.commands?.builtin">业务账号可到「数据库管理 → 修改业务账号密码」设置新密码。</span></p>
        </details>
      </div>
    </details>
    <TerminalCommandEntry v-for="entry in commands" :key="entry.label" v-bind="entry" />
    <details v-if="dockerCommands.length" class="docker-commands" :open="!commands.length">
      <summary>Docker 维护</summary>
      <div class="docker-command-list">
        <p>使用有 Docker 权限的主机账号执行。如通过 sudo 访问 Docker，请在命令前加 sudo。</p>
        <TerminalCommandEntry v-for="entry in dockerCommands" :key="entry.label" v-bind="entry" />
      </div>
    </details>
  </section>
</template>

<style scoped>
.terminal-commands {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 16px;
  margin-bottom: 24px;
  min-width: 0;
}
.terminal-commands header { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 4px 12px; }
.terminal-commands h3 { margin: 0; font-size: 15px; }
.terminal-commands header span { color: var(--muted); font-size: 12px; }
.command-help, .docker-commands { min-width: 0; border: 1px solid var(--border); border-radius: var(--control-radius); }
.terminal-commands summary { padding: 10px 14px; font-size: 13px; font-weight: 600; cursor: pointer; }
.help-content, .docker-command-list { display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; padding: 0 14px 14px; }
.help-content p, .docker-command-list p { margin: 0; color: var(--muted); font-size: 12px; line-height: 1.6; }
.host-password-help > summary { padding: 0; }
.host-password-help > .help-content, .host-password-help > p { padding: 10px 0 0; }
.mysql-account { display: grid; grid-template-columns: max-content 1fr; gap: 4px 16px; margin: 0; font-size: 13px; }
.mysql-account dt { color: var(--muted); }
.mysql-account dd { margin: 0; overflow-wrap: anywhere; }
</style>
