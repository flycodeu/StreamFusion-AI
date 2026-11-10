<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, useId, watch } from "vue";
import { request } from "./api";
import type { CredentialReference, ParameterDefinition } from "./softwareManagement";

interface Credential { id: string; revision: string; name: string; kind: string; revokedAt?: string }
const props = defineProps<{ fields: ParameterDefinition[]; modelValue: Record<string, string>; credentialRefs?: Record<string, CredentialReference>; pathOptions?: Record<string, string[]>; disabled?: boolean; contextName?: string }>();
const emit = defineEmits<{ "update:modelValue": [Record<string, string>]; "update:credentialRefs": [Record<string, CredentialReference>]; busy: [boolean]; draft: [boolean]; error: [unknown] }>();
const credentials = ref<Credential[]>([]), saving = ref(false), editing = ref("");
const fresh = reactive<Record<string, { name: string; secret: string }>>({});
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
        <div v-if="editing === field.key && fresh[field.key]" class="parameter-secret"><label>凭据名称<input v-model="fresh[field.key]!.name" maxlength="60" /></label><label>{{ field.label }}<input v-model="fresh[field.key]!.secret" type="password" :aria-label="field.label + '新值'" autocomplete="new-password" /></label></div>
      </template>
      <label v-else>{{ field.label }}<select v-if="field.type === 'SELECT'" :value="value(field)" :aria-label="field.label" @change="setValue(field.key, ($event.target as HTMLSelectElement).value)"><option value="">请选择</option><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select><input v-else :value="value(field)" :type="['INTEGER', 'PORT'].includes(field.type) ? 'number' : 'text'" :aria-label="field.label" :list="field.type === 'PATH' && pathOptions?.[field.key]?.length ? `${inputId}-${field.key}` : undefined" :min="field.type === 'PORT' ? 1 : field.min" :max="field.type === 'PORT' ? 65535 : field.max" maxlength="8192" :placeholder="field.default || (field.type === 'PATH' ? '/srv/app' : field.required ? '' : '可选')" @input="setValue(field.key, ($event.target as HTMLInputElement).value)" /></label>
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
.parameter-secret { display: grid; gap: 12px; margin-top: 10px; padding: 12px; background: var(--bg-page); border-radius: var(--control-radius); }
@media (max-width: 700px) { .software-parameters { grid-template-columns: 1fr; } .parameter-checkbox { padding-top: 0; } }
</style>
