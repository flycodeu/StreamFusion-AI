<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, useId, watch } from "vue";
import { request } from "./api";
import type { CredentialReference, ParameterDefinition } from "./softwareManagement";

interface Credential { id: string; revision: string; name: string; kind: string; revokedAt?: string }
const props = defineProps<{ fields: ParameterDefinition[]; modelValue: Record<string, string>; credentialRefs?: Record<string, CredentialReference>; pathOptions?: Record<string, string[]>; disabled?: boolean; contextName?: string }>();
const emit = defineEmits<{ "update:modelValue": [Record<string, string>]; "update:credentialRefs": [Record<string, CredentialReference>]; busy: [boolean]; draft: [boolean]; error: [unknown] }>();
const credentials = ref<Credential[]>([]), saving = ref(false), editing = ref("");
const fresh = reactive<Record<string, { name: string; secret: string }>>({});
const reveal = reactive<Record<string, boolean>>({});
let disposed = false;
let fieldRevision = 0;
const options = computed(() => credentials.value.filter(item => item.kind === "PASSWORD" && !item.revokedAt));
const inputId = useId();
watch(fresh, () => emit("draft", Object.values(fresh).some(value => !!value.secret)), { deep: true });
function value(field: ParameterDefinition) { return props.modelValue[field.key] ?? field.default ?? ""; }
function setValue(key: string, value: string) { emit("update:modelValue", { ...props.modelValue, [key]: value }); }
function selectCredential(key: string, choice: string) {
  const refs = { ...props.credentialRefs };
  if (choice) { const [id, revision] = choice.split(":"); refs[key] = { id: id!, revision: revision! }; }
  else delete refs[key];
  delete fresh[key];
  emit("update:credentialRefs", refs);
}
function newCredential(field: ParameterDefinition) {
  if (!fresh[field.key]) fresh[field.key] = { name: `${props.contextName || "软件"} · ${field.label}`.slice(0, 60), secret: "" };
  editing.value = editing.value === field.key ? "" : field.key;
}
function isBase64TokenField(field: ParameterDefinition): boolean {
  return field.key === "AUTH_TOKEN" || field.label.includes("Base64");
}
function generateFieldSecret(field: ParameterDefinition) {
  if (!fresh[field.key]) newCredential(field);
  if (isBase64TokenField(field)) {
    const raw = new Uint8Array(32);
    crypto.getRandomValues(raw);
    let bin = "";
    for (const b of raw) bin += String.fromCharCode(b);
    fresh[field.key]!.secret = btoa(bin);
  } else {
    const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789@#_-";
    const raw = new Uint8Array(24);
    crypto.getRandomValues(raw);
    let out = "";
    for (const b of raw) out += alphabet[b % alphabet.length];
    fresh[field.key]!.secret = out;
  }
  reveal[field.key] = true;
}
function base64SecretCheck(val: string): { valid: boolean; bytes: number; message: string } | undefined {
  const trimmed = val.trim();
  if (!trimmed) return undefined;
  try {
    const decoded = atob(trimmed);
    const bytes = decoded.length;
    if (bytes < 32) {
      return { valid: false, bytes, message: `当前解码仅 ${bytes} 字节，Nacos 要求至少 32 字节 Base64 密钥（请点击「随机生成」）` };
    }
    return { valid: true, bytes, message: `有效 Base64 密钥（${bytes} 字节 / ${bytes * 8} 位）` };
  } catch {
    return { valid: false, bytes: 0, message: "不是有效的 Base64 编码字符串，建议使用「随机生成」" };
  }
}
async function saveCredentials() {
  if (saving.value) return false;
  const revision = fieldRevision;
  saving.value = true; emit("busy", true);
  try {
    const refs = { ...props.credentialRefs };
    for (const field of props.fields.filter(field => field.type === "CREDENTIAL")) {
      const input = fresh[field.key];
      if (!input?.secret) continue;
      const saved = await request<Credential>("/credentials", "POST", { name: input.name.trim() || field.label, kind: "PASSWORD", secret: input.secret });
      if (disposed || revision !== fieldRevision) return false;
      credentials.value.unshift({ ...saved, kind: "PASSWORD", name: input.name });
      refs[field.key] = { id: saved.id, revision: String(saved.revision) };
      input.secret = "";
      delete fresh[field.key];
      // Keep accepted references even if a later credential save fails.
      emit("update:credentialRefs", { ...refs });
    }
    editing.value = "";
    return true;
  } catch (error) { if (!disposed) emit("error", error); return false; }
  finally { saving.value = false; if (!disposed) emit("busy", false); }
}
async function loadCredentials() {
  if (!props.fields.some(field => field.type === "CREDENTIAL")) return;
  try { const items = await request<Credential[]>("/credentials"); if (!disposed) credentials.value = items; }
  catch (error) { if (!disposed) emit("error", error); }
}
onMounted(loadCredentials);
watch(() => props.fields.map(field => `${field.key}:${field.type}`).join("|"), () => { fieldRevision++; editing.value = ""; for (const key of Object.keys(fresh)) delete fresh[key]; void loadCredentials(); });
onUnmounted(() => { disposed = true; for (const item of Object.values(fresh)) item.secret = ""; });
defineExpose({ saveCredentials });
</script>

<template>
  <fieldset class="software-parameters" :disabled="disabled || saving">
    <div v-for="field in fields" :key="field.key" class="parameter-field">
      <label v-if="field.type === 'BOOLEAN'" class="parameter-checkbox"><input type="checkbox" :checked="value(field) === 'true'" :aria-label="field.label" @change="setValue(field.key, String(($event.target as HTMLInputElement).checked))" />{{ field.label }}</label>
      <template v-else-if="field.type === 'CREDENTIAL'">
        <label>{{ field.label }}<select :aria-label="field.label" :value="credentialRefs?.[field.key] ? `${credentialRefs[field.key]!.id}:${credentialRefs[field.key]!.revision}` : ''" @change="selectCredential(field.key, ($event.target as HTMLSelectElement).value)"><option value="">{{ field.required ? '选择凭据或填写新值' : '可选' }}</option><option v-for="item in options" :key="`${item.id}:${item.revision}`" :value="`${item.id}:${item.revision}`">{{ item.name }}</option></select></label>
        <button type="button" class="quiet" @click="newCredential(field)">{{ editing === field.key ? '收起' : '填写新值' }}</button>
        <div v-if="editing === field.key && fresh[field.key]" class="parameter-secret">
          <label>凭据名称<input v-model="fresh[field.key]!.name" maxlength="60" /></label>
          <div class="secret-head-row">
            <span>{{ field.label }}</span>
            <button type="button" class="quiet" @click="generateFieldSecret(field)">随机生成</button>
          </div>
          <div class="secret-inline-row">
            <input v-model="fresh[field.key]!.secret" :type="reveal[field.key] ? 'text' : 'password'" :aria-label="field.label + '新值'" autocomplete="new-password" />
            <button type="button" class="secondary" @click="reveal[field.key] = !reveal[field.key]">{{ reveal[field.key] ? '隐藏' : '显示' }}</button>
          </div>
          <small
            v-if="isBase64TokenField(field) && base64SecretCheck(fresh[field.key]!.secret)"
            :class="base64SecretCheck(fresh[field.key]!.secret)?.valid ? 'token-valid' : 'token-invalid'"
          >
            {{ base64SecretCheck(fresh[field.key]!.secret)?.message }}
          </small>
        </div>
      </template>
      <template v-else>
        <label>{{ field.label }}<select v-if="field.type === 'SELECT'" :value="value(field)" :aria-label="field.label" @change="setValue(field.key, ($event.target as HTMLSelectElement).value)"><option value="">请选择</option><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select><input v-else :value="value(field)" :type="['INTEGER', 'PORT'].includes(field.type) ? 'number' : 'text'" :aria-label="field.label" :list="field.type === 'PATH' && pathOptions?.[field.key]?.length ? `${inputId}-${field.key}` : undefined" :min="field.type === 'PORT' ? 1 : field.min" :max="field.type === 'PORT' ? 65535 : field.max" maxlength="8192" :placeholder="field.default || (field.type === 'PATH' ? '/srv/app' : field.required ? '' : '可选')" @input="setValue(field.key, ($event.target as HTMLInputElement).value)" /></label>
        <div v-if="field.type === 'PATH' && pathOptions?.[field.key]?.length" class="path-suggestions">
          <small class="muted">已检测路径：</small>
          <button
            v-for="cand in pathOptions[field.key]"
            :key="cand"
            type="button"
            class="path-chip"
            @click="setValue(field.key, cand)"
          >
            {{ cand }}
          </button>
        </div>
      </template>
      <datalist v-if="field.type === 'PATH' && pathOptions?.[field.key]?.length" :id="`${inputId}-${field.key}`"><option v-for="path in pathOptions[field.key]" :key="path" :value="path" /></datalist>
    </div>
  </fieldset>
</template>

<style scoped>
.software-parameters { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 18px 20px; border: 0; padding: 0; margin: 0; min-width: 0; }
.parameter-field { min-width: 0; }
.parameter-field label { display: grid; gap: 8px; font-size: 13px; }
.parameter-field .quiet { margin-top: 6px; padding-left: 0; }
.parameter-checkbox { display: flex !important; align-items: center; padding-top: 25px; }
.parameter-checkbox input { width: 16px; min-height: 16px; margin: 0; }
.parameter-secret { display: grid; gap: 10px; margin-top: 10px; padding: 12px; background: var(--bg-page); border: 1px solid var(--border); border-radius: var(--control-radius); }
.secret-head-row { display: flex; align-items: center; justify-content: space-between; font-size: 12px; font-weight: 500; color: #334155; }
.secret-head-row .quiet { margin-top: 0; padding: 2px 6px; }
.secret-inline-row { display: flex; gap: 6px; align-items: center; }
.secret-inline-row input { flex: 1; margin-top: 0; }
.secret-inline-row button { flex-shrink: 0; min-height: 36px; }
.token-valid { color: var(--success); }
.token-invalid { color: var(--error); }
.path-suggestions { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-top: 6px; }
.path-chip { border: 1px solid var(--border); background: #f8fafc; color: #334155; font: 11px var(--mono); padding: 2px 7px; border-radius: 4px; }
.path-chip:hover { border-color: var(--accent); color: var(--accent); background: var(--accent-soft); }
@media (max-width: 700px) { .software-parameters { grid-template-columns: 1fr; } .parameter-checkbox { padding-top: 0; } }
</style>
