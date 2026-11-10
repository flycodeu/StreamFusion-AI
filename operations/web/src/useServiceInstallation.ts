import { computed, onUnmounted, ref, watch } from "vue";
import { ApiError, request, upload } from "./api";
import { installationPreparation, type AutomaticPreparationUpdate, type PreparationUpdateCheck } from "./preparationUpdate";
import { parameterError } from "./softwareManagement";
import type { Host, SSHCredential } from "./hosts";
import type { SoftwareInstance } from "./software";
import type { BlockingJob } from "./BlockingJobAction.vue";
import {
  initialServiceConfiguration,
  initialOfficialConfiguration,
  installationNeedsComponentUpdate,
  serviceBindAddressValid,
  materialSize,
  materialNextAction,
  isTool,
  isDockerEngine,
  requiresServicePort,
  preferredInstallationMaterial,
  recipeCatalogKey,
  visibleServicePorts,
  storageDirectoryValid,
  type InstallationMaterial,
  type InstallationSource,
  type InstallationMethod,
  type ServiceConfiguration,
  type ServiceInstallJob,
  type ServiceInstallPlan,
  type ServiceInstallReceipt,
  type ServiceRecipe,
  type RuntimeMode,
} from "./serviceInstallation";

export interface ServiceInstallationProps {
  open: boolean;
  recipe?: ServiceRecipe;
  host?: Host;
  method?: InstallationMethod;
  runtimeMode?: RuntimeMode;
  sourceMode?: InstallationSource["type"];
  existing?: SoftwareInstance;
  includeLogs?: boolean;
}

export function useServiceInstallation(
  props: Readonly<ServiceInstallationProps>,
  onClose: () => void,
  onSubmitted: (receipt: ServiceInstallReceipt) => void,
) {
  type CredentialRole = "primary" | "access" | "user";
  const step = ref(0);
  const busy = ref(false);
  const configuration = ref<ServiceConfiguration>();
  const sourceType = ref<InstallationSource["type"]>("ARCHIVE");
  const official = computed(() => props.method === "OFFICIAL");
  const preparationOpen = ref(false);
  const automaticUpdate = ref<AutomaticPreparationUpdate>();
  const preparationAttempts = new Set<string>();
  const readinessHost = ref<Host>();
  const materials = ref<InstallationMaterial[]>([]);
  const materialId = ref("");
  const credentials = ref<SSHCredential[]>([]);
  const credentialChoices = ref({ primary: "", access: "", user: "" });
  const credentialRole = ref<CredentialRole>();
  const credentialName = ref("");
  const credentialSecret = ref("");
  const revealSecret = ref(false);
  const selectedFile = ref<File>();
  const archiveSHA = ref("");
  const materialKey = ref("");
  const officialKey = ref("");
  const officialProgress = ref("");
  const officialNeedsImport = ref(false);
  const materialProgress = ref("");
  const materialNeedsImport = ref(false);
  const uploadController = ref<AbortController>();
  const plan = ref<ServiceInstallPlan>();
  const submitKey = ref("");
  const receipt = ref<ServiceInstallReceipt>();
  const submissionAttempted = ref(false);
  const job = ref<ServiceInstallJob>();
  const logs = ref("");
  const logsLimited = ref(false);
  const logsAt = ref("");
  const logsLoading = ref(false);
  const logsPaused = ref(false);
  const logError = ref<{ message: string; code: string; traceId: string; at: string }>();
  let logFailures = 0;
  let finalLogsRead = false;
  let executionAbort: AbortController | undefined;
  const now = ref(Date.now());
  const fieldError = ref("");
  const actionError = ref<{
    message: string;
    code: string;
    traceId: string;
    at: string;
    blockingJob?: BlockingJob;
  }>();
  let generation = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let disposed = false;
  let draftContext: string | undefined;
  let defaultName = "";
  let recipeDefaults: Record<string, string> = {};

  const credentialRoles = computed<CredentialRole[]>(() => {
    if (official.value || !props.recipe || (props.recipe.commands && !props.recipe.commands.builtin) || isTool(props.recipe) || isDockerEngine(props.recipe) || props.recipe.key === "nginx")
      return [];
    if (props.recipe?.key === "seaweedfs") return ["access", "primary"];
    return props.recipe?.key === "mysql" && configuration.value?.username
      ? ["primary", "user"]
      : ["primary"];
  });
  const selectedMaterial = computed(() =>
    materials.value.find((item) => item.id === materialId.value),
  );
  watch(archiveSHA, () => {
    materialKey.value = crypto.randomUUID();
    materialNeedsImport.value = false;
  });
  watch(
    () => configuration.value?.username,
    (username) => {
      if (!username) credentialChoices.value.user = "";
    },
  );
  const planExpired = computed(
    () => !plan.value || Date.parse(plan.value.expiresAt) <= now.value,
  );
  const submitted = computed(() => !!receipt.value);
  const pending = computed(
    () =>
      !!job.value &&
      (["QUEUED", "RUNNING", "UNKNOWN"].includes(job.value.status) ||
        job.value.targetAckPending),
  );
  const canRetryInstallation = computed(() => job.value?.status === "FAILED" && !job.value.targetAckPending && !busy.value);
  const credentialLabel = (role: CredentialRole) =>
    role === "access"
      ? "S3 Access Key"
      : role === "user"
        ? "业务账号密码"
        : props.recipe?.key === "mysql"
          ? "root 初始密码"
          : props.recipe?.key === "redis"
            ? "Redis 密码"
            : props.recipe?.key === "seaweedfs"
              ? "S3 Secret Key"
              : props.recipe?.key === "zlm"
                ? "API Secret"
                : "服务账号密码";
  const credentialKind = (role: CredentialRole) =>
    role === "access" ? "TOKEN" : "PASSWORD";
  function credentialOptions(role: CredentialRole) {
    return credentials.value.filter(
      (item) => item.kind === credentialKind(role) && !item.revokedAt,
    );
  }
  function clearError() {
    fieldError.value = "";
    actionError.value = undefined;
  }
  function report(error: unknown) {
    actionError.value = {
      message:
        error instanceof Error ? error.message : "请求未完成，请检查连接后重试",
      code: error instanceof ApiError ? error.code : "REQUEST_NOT_CONFIRMED",
      traceId: error instanceof ApiError ? error.traceId : "",
      at:
        error instanceof ApiError ? error.timestamp : new Date().toISOString(),
      blockingJob: error instanceof ApiError && error.code === "OPERATION_LOCKED" ? error.data as BlockingJob | undefined : undefined,
    };
  }
  async function act(action: () => Promise<void>) {
    if (busy.value) return;
    const revision = generation;
    busy.value = true;
    clearError();
    try {
      await action();
    } catch (error) {
      if (current(revision)) report(error);
    } finally {
      if (current(revision)) busy.value = false;
    }
  }
  function current(value: number) {
    return !disposed && props.open && value === generation;
  }
  function stopPolling() {
    if (timer) clearTimeout(timer);
    timer = undefined;
  }
  async function loadMaterials() {
    if (!props.recipe) return;
    const revision = generation;
    const items = await request<InstallationMaterial[]>(
      `/installation-materials?recipeKey=${encodeURIComponent(props.recipe.key)}`,
    );
    if (current(revision)) {
      materials.value = items.filter(
        (item) =>
          item.recipeKey === props.recipe?.key &&
          item.recipeVersion === props.recipe?.version &&
          item.recipeRevision === props.recipe?.revision &&
          (item.runtimeMode || "DOCKER") === configuration.value?.runtimeMode,
      );
      if (!materials.value.some((item) => item.id === materialId.value && item.available))
        materialId.value = preferredInstallationMaterial(materials.value)?.id || "";
    }
  }
  async function refreshJob(signal?: AbortSignal) {
    if (!receipt.value) return;
    const revision = generation;
    const id = receipt.value.jobId;
    const actual = await request<ServiceInstallJob>(
      `/jobs/${id}`, "GET", undefined, signal,
    );
    if (current(revision) && receipt.value?.jobId === id) job.value = actual;
  }
  async function refreshExecution(retry = false) {
    if (!receipt.value || logsLoading.value) return;
    const revision = generation, id = receipt.value.jobId;
    const abort = new AbortController();
    executionAbort = abort;
    if (retry) { logFailures = 0; logsPaused.value = false; }
    logsLoading.value = true;
    try {
      await refreshJob(abort.signal);
      if (!current(revision) || receipt.value?.jobId !== id) return;
      // Queued work has no target log yet. Do not call an unstarted job a failure.
      if (props.includeLogs !== false && job.value?.status !== "QUEUED") {
        const result = await request<{ text: string; limited?: boolean }>(`/jobs/${id}/logs`, "GET", undefined, abort.signal);
        if (!current(revision) || receipt.value?.jobId !== id) return;
        logs.value = result.text;
        logsLimited.value = !!result.limited;
        logsAt.value = new Date().toISOString();
      }
      finalLogsRead = !pending.value;
      logError.value = undefined;
      logFailures = 0;
    } catch (error) {
      if (!current(revision) || receipt.value?.jobId !== id) return;
      logError.value = {
        message: error instanceof Error ? error.message : "执行输出暂不可用",
        code: error instanceof ApiError ? error.code : "REQUEST_NOT_CONFIRMED",
        traceId: error instanceof ApiError ? error.traceId : "",
        at: error instanceof ApiError ? error.timestamp : new Date().toISOString(),
      };
      if (++logFailures >= 3) logsPaused.value = true;
    } finally {
      if (executionAbort === abort) executionAbort = undefined;
      if (current(revision)) logsLoading.value = false;
    }
  }
  function poll() {
    stopPolling();
    if (!props.open || disposed) return;
    timer = setTimeout(async () => {
      now.value = Date.now();
      if (!document.hidden && receipt.value && (pending.value || !finalLogsRead) && !busy.value && !logsPaused.value)
        await refreshExecution();
      poll();
    }, 3000);
  }
  watch(
    [
      () => props.open,
      () => props.recipe?.key,
      () => props.recipe?.version,
      () => props.recipe?.revision,
      () => props.host?.id,
      () => props.method,
      () => props.runtimeMode,
      () => props.existing?.id,
      () => props.existing?.installationJobId,
    ],
    async () => {
      const previous = !receipt.value && !submissionAttempted.value ? configuration.value : undefined;
      const previousContext = draftContext;
      const previousDefaults = recipeDefaults;
      recipeDefaults = Object.fromEntries((props.recipe?.parameters || []).filter(field => field.type !== "CREDENTIAL").map(field => [field.key, field.default || ""]));
      draftContext = undefined;
      generation += 1;
      busy.value = false;
      stopPolling();
      executionAbort?.abort();
      uploadController.value?.abort();
      credentialSecret.value = "";
      credentialRole.value = undefined;
      preparationOpen.value = false;
      automaticUpdate.value = undefined;
      preparationAttempts.clear();
      logsLoading.value = false;
      if (!props.open || !props.recipe || !props.host) return;
      const revision = generation;
      configuration.value = official.value ? initialOfficialConfiguration(props.recipe) :
        initialServiceConfiguration(props.recipe, props.host.address, props.runtimeMode);
      const previousDefaultName = defaultName;
      defaultName = configuration.value.name;
      if (props.recipe.commands && !props.recipe.commands.builtin && props.recipe.commands.steps.some(step => step.runAs === "ACCOUNT"))
        configuration.value.account = props.host.sshUser || "";
      if (!props.existing) {
        draftContext = JSON.stringify([props.host.id, recipeCatalogKey(props.recipe), props.recipe.kind]);
        const next = configuration.value;
        if (previous && previousContext === draftContext && previous.runtimeMode === next.runtimeMode) {
          if (previous.name !== previousDefaultName) next.name = previous.name;
          for (const field of ["storageRoot", "account", "memoryMiB"] as const)
            if (previous[field] !== undefined) Object.assign(next, { [field]: previous[field] });
          if (serviceBindAddressValid(props.recipe, previous.bindAddress)) next.bindAddress = previous.bindAddress;
          if (requiresServicePort(props.recipe) && Number.isInteger(previous.hostPort) && previous.hostPort >= 1 && previous.hostPort <= 65535)
            next.hostPort = previous.hostPort;
          for (const port of Object.keys(next.ports))
            if (previous.ports[port] !== undefined) next.ports[port] = previous.ports[port];
          for (const parameter of props.recipe.parameters || []) {
            if (previous.parameters?.[parameter.key] !== undefined && previous.parameters[parameter.key] !== previousDefaults[parameter.key])
              (next.parameters ||= {})[parameter.key] = previous.parameters[parameter.key]!;
            if (parameter.type === "CREDENTIAL" && previous.parameterCredentials?.[parameter.key])
              (next.parameterCredentials ||= {})[parameter.key] = previous.parameterCredentials[parameter.key]!;
          }
        }
      }
      readinessHost.value = undefined;
      step.value = 0;
      plan.value = undefined;
      receipt.value = undefined;
      submissionAttempted.value = false;
      job.value = undefined;
      logs.value = "";
      logsLimited.value = false; logsAt.value = ""; logsPaused.value = false;
      logError.value = undefined; logFailures = 0; finalLogsRead = false;
      materialId.value = "";
      materials.value = [];
      selectedFile.value = undefined;
      archiveSHA.value = "";
      materialKey.value = crypto.randomUUID();
      officialKey.value = crypto.randomUUID();
      officialProgress.value = "";
      officialNeedsImport.value = false;
      materialProgress.value = "";
      materialNeedsImport.value = false;
      sourceType.value = official.value ? "OFFICIAL" : props.sourceMode || (props.runtimeMode === "DOCKER" ? "REGISTRY" : "ARCHIVE");
      credentialChoices.value = { primary: "", access: "", user: "" };
      clearError();
      if (props.existing) {
        const item = props.existing;
        if (item.hostId !== props.host.id || !item.installationJobId || !item.installation?.config ||
            item.installation.definition?.revision !== props.recipe.revision) {
          fieldError.value = "安装记录与当前主机或软件不匹配，请刷新后重试";
          return;
        }
        configuration.value = { ...item.installation.config, ports: { ...item.installation.config.ports } };
        receipt.value = { jobId: item.installationJobId, softwareInstanceId: item.id };
        step.value = 4;
        await refreshExecution();
        if (current(revision)) poll();
        return;
      }
      try {
        const [items] = await Promise.all([
          official.value || props.method === "SELF" || (props.recipe.commands && !props.recipe.commands.builtin) ? Promise.resolve([]) : request<SSHCredential[]>("/credentials"),
          sourceType.value === "ARCHIVE" && props.method !== "SELF" ? loadMaterials() : Promise.resolve(),
        ]);
        if (current(revision)) credentials.value = items;
      } catch (error) {
        if (current(revision)) report(error);
      }
      poll();
    },
    { immediate: true },
  );
  watch(
    () => configuration.value?.runtimeMode,
    (mode, previous) => {
      if (props.existing || official.value || !mode || !previous || mode === previous || !props.recipe) return;
      sourceType.value = props.sourceMode || (props.runtimeMode === "DOCKER" ? "REGISTRY" : "ARCHIVE");
      materialId.value = "";
      selectedFile.value = undefined;
      archiveSHA.value = "";
      materialProgress.value = "";
      materialKey.value = crypto.randomUUID();
      if (configuration.value)
        configuration.value.ports = Object.fromEntries(
          visibleServicePorts(props.recipe, mode).map((port) => [
            port.key,
            port.required ? port.defaultPort : 0,
          ]),
        );
      if (sourceType.value === "ARCHIVE") void loadMaterials().catch(report);
    },
  );
  watch(() => props.sourceMode, (source) => {
    if (!source || props.existing || submitted.value || source === sourceType.value) return;
    sourceType.value = source;
    if (source === "ARCHIVE") void loadMaterials().catch(report);
  });
  watch(
    () => [configuration.value, sourceType.value, materialId.value],
    () => {
      if (submitted.value) return;
      plan.value = undefined;
      submitKey.value = "";
      if (step.value === 3) step.value = official.value ? 0 : 2;
    },
    { deep: true },
  );
  watch(
    credentialChoices,
    (choices) => {
      const config = configuration.value;
      if (!config) return;
      for (const role of ["primary", "access", "user"] as const) {
        const [id = "", revision = ""] = choices[role].split(":");
        if (role === "primary") {
          config.credentialId = id;
          config.credentialRevision = revision;
        } else if (role === "access") {
          config.accessKeyCredentialId = id;
          config.accessKeyCredentialRevision = revision;
        } else {
          config.userCredentialId = id;
          config.userCredentialRevision = revision;
        }
      }
    },
    { deep: true },
  );

  function editCredential(role: CredentialRole) {
    credentialRole.value = role;
    credentialSecret.value = "";
    revealSecret.value = false;
    credentialName.value =
      `${configuration.value?.name || props.recipe?.name} · ${credentialLabel(role)}`.slice(
        0,
        60,
      );
  }
  function generateSecret() {
    const alphabet =
      credentialRole.value === "access"
        ? "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
        : "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#_-";
    const bytes = crypto.getRandomValues(
      new Uint8Array(credentialRole.value === "access" ? 20 : 32),
    );
    credentialSecret.value = Array.from(
      bytes,
      (value) => alphabet[value % alphabet.length],
    ).join("");
    revealSecret.value = true;
  }
  async function saveCredential() {
    const role = credentialRole.value;
    if (!role || !credentialSecret.value || !credentialName.value.trim()) {
      fieldError.value = "填写凭据名称与内容，或生成新值";
      return;
    }
    await act(async () => {
      const revision = generation;
      const secret = credentialSecret.value;
      const kind = credentialKind(role);
      const name = credentialName.value.trim();
      const saved = await request<{ id: string; revision: string }>(
        "/credentials",
        "POST",
        { name, kind, secret },
      );
      if (!current(revision)) return;
      credentials.value.unshift({ ...saved, name, kind, revokedAt: "" });
      credentialChoices.value[role] = `${saved.id}:${saved.revision}`;
      credentialSecret.value = "";
      credentialRole.value = undefined;
      revealSecret.value = false;
    });
  }
  function fileChanged(event: Event) {
    selectedFile.value = (event.target as HTMLInputElement).files?.[0];
    archiveSHA.value =
      configuration.value?.runtimeMode === "NATIVE" &&
      props.recipe?.native?.parts.length === 1
        ? props.recipe.native.parts[0]!.sha256
        : "";
    materialKey.value = crypto.randomUUID();
    materialProgress.value = "";
    materialNeedsImport.value = false;
  }
  watch(
    () => [sourceType.value, configuration.value?.runtimeMode],
    () => {
      if (
        sourceType.value === "ARCHIVE" &&
        configuration.value?.runtimeMode === "NATIVE" &&
        props.recipe?.native?.parts.length === 1 &&
        !selectedFile.value
      )
        archiveSHA.value = props.recipe.native.parts[0]!.sha256;
    },
  );
  function prepareMaterialImport() {
    materialKey.value = crypto.randomUUID();
    materialNeedsImport.value = false;
    materialProgress.value =
      "将创建新的材料记录；原缺失记录与其引用保留。请重新上传并校验。";
    clearError();
  }

  async function pollMaterial(id: string, signal: AbortSignal) {
    const revision = generation;
    await new Promise<void>((resolve, reject) => {
      const abort = () => {
        clearTimeout(timer);
        reject(new Error("材料操作已中断"));
      };
      const timer = setTimeout(() => {
        signal.removeEventListener("abort", abort);
        resolve();
      }, 1000);
      signal.addEventListener("abort", abort, { once: true });
      if (signal.aborted) abort();
    });
    const items = await request<InstallationMaterial[]>(
      `/installation-materials?recipeKey=${encodeURIComponent(props.recipe!.key)}`,
      "GET",
      undefined,
      signal,
    );
    const material = items.find((item) => item.id === id);
    if (!material)
      throw new Error("原材料记录已不可读取，请刷新材料列表；不要覆盖原归档。");
    if (current(revision) && !signal.aborted)
      materials.value = items.filter(
        (item) =>
          item.recipeKey === props.recipe?.key &&
          item.recipeVersion === props.recipe?.version &&
          item.recipeRevision === props.recipe?.revision &&
          (item.runtimeMode || "DOCKER") === configuration.value?.runtimeMode,
      );
    return material;
  }

  async function finishMaterial(
    material: InstallationMaterial,
    file: File,
    signal: AbortSignal,
  ) {
    const deadline = Date.now() + 900000;
    for (;;) {
      signal.throwIfAborted();
      const action = materialNextAction(material);
      if (action === "READY") return material;
      if (Date.now() >= deadline)
        throw new Error("原材料操作尚未确认，请稍后重试以核对同一记录。");
      if (action === "REIMPORT") {
        materialNeedsImport.value = true;
        throw new Error(
          "原材料文件缺失，不能覆盖不可变记录。请明确选择“重新导入为新材料”。",
        );
      }
      if (action === "UPLOAD") {
        materialProgress.value = `上传 ${materialSize(file.size)}`;
        await upload(
          `/installation-materials/${material.id}/content`,
          file,
          signal,
        );
        material = { ...material, status: "UPLOADED", available: false };
      } else if (action === "CONFIRM") {
        materialProgress.value =
          configuration.value?.runtimeMode === "NATIVE"
            ? "校验原生材料与固定配方"
            : "校验归档与镜像身份";
        material = await request<InstallationMaterial>(
          `/installation-materials/${material.id}/confirm`,
          "POST",
          {},
          signal,
        );
        if (material.status === "UPLOADED")
          throw new Error("原材料校验尚未确认，请重试核对原记录。");
      } else if (action === "WAIT") {
        materialProgress.value =
          material.status === "VERIFYING" ? "等待原校验结果" : "等待原上传结果";
        material = await pollMaterial(material.id, signal);
      } else {
        throw new Error(
          "此材料当前无法继续上传，请刷新状态或重新添加安装包。",
        );
      }
    }
  }
  async function uploadMaterial() {
    const file = selectedFile.value;
    const recipe = props.recipe;
    if (!recipe || !file) {
      fieldError.value = "请选择与当前运行方式匹配的安装归档";
      return;
    }
    if (file.size < 1 || file.size > 2 * 1024 ** 3) {
      fieldError.value = "安装归档须大于0且不超过2 GiB";
      return;
    }
    if (!/^[a-f0-9]{64}$/i.test(archiveSHA.value.trim())) {
      fieldError.value = "填写归档的64位 SHA-256，用于校验上传内容";
      return;
    }
    await act(async () => {
      const revision = generation;
      const controller = new AbortController();
      uploadController.value = controller;
      materialProgress.value = "登记归档";
      try {
        const material = await request<InstallationMaterial>(
          "/installation-materials",
          "POST",
          {
            recipeKey: recipe.key,
            recipeVersion: recipe.version,
            recipeRevision: recipe.revision,
            runtimeMode: configuration.value?.runtimeMode,
            name: file.name,
            sha256: archiveSHA.value.trim().toLowerCase(),
            byteSize: file.size,
            idempotencyKey: materialKey.value,
          },
        );
        await finishMaterial(material, file, controller.signal);
        if (!current(revision)) return;
        await loadMaterials();
        if (!current(revision)) return;
        if (
          !materials.value.some(
            (item) => item.id === material.id && item.available,
          )
        )
          throw new Error("材料可用性已变化，请重新读取材料列表后选择。");
        materialId.value = material.id;
        selectedFile.value = undefined;
        materialProgress.value = "归档已校验，可用于离线安装";
      } catch (error) {
        if (!current(revision)) return;
        materialProgress.value = materialNeedsImport.value
          ? "原材料缺失，等待确认新导入"
          : "操作未完成；重试将先核对原材料状态";
        if (controller.signal.aborted)
          throw new Error(
            "材料操作已中断。重试将核对原记录；已上传内容不重复传输。",
          );
        throw error;
      } finally {
        if (uploadController.value === controller) uploadController.value = undefined;
      }
    });
  }
  async function prepareOfficialMaterial() {
    if (!props.recipe || configuration.value?.runtimeMode !== "NATIVE") return;
    await act(async () => {
      const revision = generation;
      const controller = new AbortController();
      uploadController.value = controller;
      officialProgress.value = "Ops正在准备官方材料";
      const deadline = Date.now() + 900000;
      try {
        let material = await request<InstallationMaterial>(
          "/installation-materials/official",
          "POST",
          {
            recipeKey: props.recipe!.key,
            recipeVersion: props.recipe!.version,
            recipeRevision: props.recipe!.revision,
            idempotencyKey: officialKey.value,
          },
          controller.signal,
        );
        if (!current(revision)) return;
        while (material.status !== "AVAILABLE" || !material.available) {
          controller.signal.throwIfAborted();
          if (
            !["DOWNLOADING", "VERIFYING", "UPLOADING", "UPLOADED"].includes(
              material.status,
            ) ||
            Date.now() >= deadline
          )
            throw new ApiError(
              "官方材料未就绪，请查看错误详情",
              material.errorCode || material.status,
              "",
              new Date().toISOString(),
            );
          officialProgress.value = "等待原材料下载与校验结果";
          material = await pollMaterial(material.id, controller.signal);
        }
        if (!current(revision)) return;
        await loadMaterials();
        if (!current(revision)) return;
        if (
          !materials.value.some(
            (item) => item.id === material.id && item.available,
          )
        )
          throw new Error("官方材料可用性发生变化，请重新核对");
        materialId.value = material.id;
        officialProgress.value = "材料已就绪，目标服务器无需访问外网";
      } catch (error) {
        if (!current(revision)) return;
        if (
          error instanceof ApiError &&
          error.code === "INSTALL_MATERIAL_REIMPORT_REQUIRED"
        )
          officialNeedsImport.value = true;
        officialProgress.value = "原材料操作未确认，重试将核对同一记录";
        throw error;
      } finally {
        if (uploadController.value === controller) uploadController.value = undefined;
      }
    });
  }
  function prepareOfficialAgain() {
    if (busy.value || !officialNeedsImport.value) return;
    officialKey.value = crypto.randomUUID();
    officialNeedsImport.value = false;
    void prepareOfficialMaterial();
  }
  function validateStep(section = step.value) {
    const config = configuration.value;
    if (!config || !props.host || !props.recipe) return "请选择软件和目标主机";
    if (props.host.maintenance) return "主机维护中，请恢复后再安装";
    if (official.value) return !config.name.trim() || new TextEncoder().encode(config.name).length > 120 ? "实例名称须为1–120字节" : "";
    if (
      section === 0 &&
      sourceType.value === "ARCHIVE" &&
      !selectedMaterial.value?.available
    )
      return "先选择已校验的离线归档";
    if (section === 1) {
      if (props.recipe.commands && !props.recipe.commands.builtin && props.recipe.commands.steps.some(step => step.runAs === "ACCOUNT") &&
          (!/^[a-z_][a-z0-9_-]{0,31}$/.test(config.account || "") || config.account === "root"))
        return "填写目标服务器的普通执行账号";
      if (credentialRole.value && credentialSecret.value)
        return "请先保存新凭据，或收起编辑后使用已选凭据";
      if (
        !config.name.trim() ||
        new TextEncoder().encode(config.name).length > 120
      )
        return "实例名称须为1–120字节";
      for (const role of credentialRoles.value)
        if (!credentialChoices.value[role])
          return `请选择或新建${credentialLabel(role)}`;
      if (props.recipe.parameters?.length) {
        const message = parameterError(props.recipe.parameters, config.parameters, config.parameterCredentials);
        if (message) return message;
      }
      if (
        props.recipe.key === "mysql" &&
        (!!config.database !== !!config.username ||
          config.username.toLowerCase() === "root")
      )
        return "业务数据库和账号需同时填写，业务账号不能为root";
      if (
        props.recipe.key === "mysql" &&
        config.username &&
        (!/^[a-zA-Z][a-zA-Z0-9_]{0,31}$/.test(config.username) ||
          !/^[a-zA-Z][a-zA-Z0-9_]{0,31}$/.test(config.database))
      )
        return "业务库和账号以字母开头，仅含字母、数字、下划线，最多32位";
      if (
        props.recipe.key === "mediamtx" &&
        !/^[a-zA-Z][a-zA-Z0-9_]{0,31}$/.test(config.username)
      )
        return "MediaMTX账号以字母开头，仅含字母、数字和下划线，最多32位";
    }
    if (section === 2) {
      if (!storageDirectoryValid(config.storageRoot))
        return "存储根目录须为/opt、/srv、/var/lib、/data或/mnt下的专用规范目录";
      if (requiresServicePort(props.recipe) && !serviceBindAddressValid(props.recipe, config.bindAddress))
        return props.recipe.web ? "填写有效的 IPv4 监听地址" : "填写服务器明确的IPv4地址，不能使用0.0.0.0";
      if (
        requiresServicePort(props.recipe) &&
        (!Number.isInteger(config.hostPort) ||
          config.hostPort < 1 ||
          config.hostPort > 65535)
      )
        return "宿主端口须为1–65535的整数";
      for (const port of visibleServicePorts(
        props.recipe,
        config.runtimeMode,
      )) {
        const value =
          config.ports[port.key] ?? (port.required ? port.defaultPort : 0);
        if (
          !Number.isInteger(value) ||
          value < (port.required ? 1 : 0) ||
          value > 65535
        )
          return `${port.label}须为${port.required ? "1" : "0"}–65535的整数`;
      }
      if (
        props.recipe.key === "zlm" &&
        (config.ports.rtpTcp || 0) !== (config.ports.rtpUdp || 0)
      )
        return "RTP 的 TCP 与 UDP 须使用同一个端口，或同时关闭";
      if (
        props.recipe.key === "mediamtx" &&
        Boolean(config.ports.webrtc) !== Boolean(config.ports.webrtcUdp)
      )
        return "WebRTC 信令与媒体端口须同时启用";
      if (!isDockerEngine(props.recipe) && (
        !Number.isInteger(config.memoryMiB) ||
        config.memoryMiB < 128 ||
        config.memoryMiB > 16384
      ))
        return "内存上限须为128–16384 MiB";
    }
    return "";
  }
  async function next() {
    clearError();
    fieldError.value = [0, 1, 2].map(validateStep).find(Boolean) || "";
    if (fieldError.value) return;
    credentialSecret.value = "";
    credentialRole.value = undefined;
    await preflight();
  }
  async function preflight() {
    preparationAttempts.clear();
    await runPreflight();
  }
  async function runPreflight() {
    if (props.existing || receipt.value) return;
    if (!props.host || !props.recipe || !configuration.value || props.method === "SELF") return;
    if (official.value) {
      fieldError.value = validateStep();
      if (fieldError.value) return;
    }
    await act(async () => {
      const revision = generation;
      let result: ServiceInstallPlan;
      try {
        result = await request<ServiceInstallPlan>(
          `/hosts/${props.host!.id}/software-install-plans`,
          "POST",
          {
            expectedHostVersion: readinessHost.value?.version || props.host!.version,
            recipeKey: props.recipe!.key,
            recipeVersion: props.recipe!.version,
            recipeRevision: props.recipe!.revision,
            config: official.value ? {
              name: configuration.value!.name.trim(),
              runtimeMode: "NATIVE",
              bindAddress: configuration.value!.bindAddress,
              hostPort: configuration.value!.hostPort,
              memoryMiB: 512,
            } : {
              ...configuration.value,
              name: configuration.value!.name.trim(),
            },
            source: official.value ? { type: "OFFICIAL" } :
              sourceType.value === "ARCHIVE"
                ? { type: "ARCHIVE", materialId: materialId.value }
                : { type: sourceType.value },
          },
        );
      } catch (cause) {
        if (!current(revision)) return;
        if (props.host!.kind !== "SSH" || !(cause instanceof ApiError)) throw cause;
        const kind = cause.code === "COMMAND_TRUST_REQUIRED" ? "AUTHORIZE" :
          installationNeedsComponentUpdate(cause.code) ? "UPDATE" :
          ["EXECUTOR_MISSING", "SUDO_AUTHORIZATION_REQUIRED", "EXECUTOR_PRIVILEGE_REQUIRED"].includes(cause.code) ? "INSTALL" : "";
        if (!kind || preparationAttempts.has(kind)) throw cause;
        const checked = await request<PreparationUpdateCheck>(`/hosts/${props.host!.id}/preparation?checkUpdate=1`);
        if (!current(revision)) return;
        if (checked.hostId !== props.host!.id || checked.expectedHostVersion !== (readinessHost.value?.version || props.host!.version)) {
          throw new ApiError("主机配置已变化，请重新选择主机", "VERSION_CONFLICT", "", new Date().toISOString());
        }
        const update = installationPreparation(checked, cause.code);
        if (update && !preparationAttempts.has(update.mode || "UPDATE")) {
          preparationAttempts.add(update.mode || "UPDATE");
          automaticUpdate.value = update;
          preparationOpen.value = true;
          return;
        }
        if (checked.update && checked.update.status !== "CURRENT" && checked.update.code !== "PREPARATION_READY") {
          throw new ApiError("安装环境尚未就绪，请查看检查结果", checked.update.code || cause.code, "", new Date().toISOString());
        }
        throw cause;
      }
      if (!current(revision)) return;
      plan.value = result;
      submitKey.value = crypto.randomUUID();
      now.value = Date.now();
      step.value = 3;
    });
  }
  async function submit() {
    if (
      props.existing ||
      !plan.value ||
      (planExpired.value && !submissionAttempted.value) ||
      receipt.value
    )
      return;
    await act(async () => {
      const revision = generation;
      // A lost reply can follow an accepted installation. Reuse its plan/key
      // when retrying so the controller returns the original receipt.
      submissionAttempted.value = true;
      let result: ServiceInstallReceipt;
      try {
        result = await request<ServiceInstallReceipt>(
          `/software-install-plans/${plan.value!.id}/submit`,
          "POST",
          { idempotencyKey: submitKey.value },
        );
      } catch (error) {
        if (!current(revision)) return;
        // These controller rejections occur before creating the job. A lost
        // reply, server error or idempotency conflict must keep the original key.
        if (error instanceof ApiError && error.status === 409 && [
          "INSTALL_PLAN_STALE", "INSTALL_MATERIAL_NOT_READY", "INSTALL_MATERIAL_CHANGED",
          "OFFICIAL_PLAN_CHANGED", "INSTALL_PREFLIGHT_MISMATCH", "QUEUE_FULL",
          "TOOL_ALREADY_INSTALLED", "OPERATION_LOCKED",
        ].includes(error.code)) {
          submissionAttempted.value = false;
          submitKey.value = "";
          plan.value = undefined;
          step.value = 0;
        }
        throw error;
      }
      onSubmitted(result);
      if (!current(revision)) return;
      receipt.value = result;
      step.value = 4;
      await refreshExecution();
    });
  }

  async function preparationChecked() {
    if (!props.host) return;
    let refreshed = false;
    await act(async () => {
      const revision = generation;
      const hosts = await request<Host[]>("/hosts");
      if (!current(revision)) return;
      const updated = hosts.find((host) => host.id === props.host!.id);
      if (!updated || updated.capabilities?.mutation.status !== "AVAILABLE") return;
      readinessHost.value = updated;
      preparationOpen.value = false;
      plan.value = undefined;
      refreshed = true;
    });
    if (refreshed && !receipt.value) await runPreflight();
  }
  async function jobAction(action: "reconcile" | "resume" | "cancel") {
    if (!receipt.value) return;
    await act(async () => {
      const revision = generation, id = receipt.value!.jobId;
      await request(`/jobs/${id}/${action}`, "POST", {});
      if (current(revision) && receipt.value?.jobId === id) await refreshExecution(true);
    });
  }
  function retryInstallation() {
    if (!canRetryInstallation.value) return false;
    executionAbort?.abort();
    receipt.value = undefined;
    job.value = undefined;
    plan.value = undefined;
    submitKey.value = "";
    submissionAttempted.value = false;
    logs.value = "";
    logError.value = undefined;
    logsLoading.value = false;
    logsPaused.value = false;
    logFailures = 0;
    finalLogsRead = false;
    clearError();
    step.value = 0;
    return true;
  }
  async function loadLogs() {
    await refreshExecution(true);
  }
  function close() {
    onClose();
  }
  onUnmounted(() => {
    disposed = true;
    generation += 1;
    stopPolling();
    executionAbort?.abort();
    uploadController.value?.abort();
    credentialSecret.value = "";
  });

  return {
    step,
    busy,
    configuration,
    sourceType,
    official,
    preparationOpen,
    automaticUpdate,
    readinessHost,
    preparationChecked,
    materials,
    materialId,
    credentialChoices,
    credentialRole,
    credentialName,
    credentialSecret,
    revealSecret,
    selectedFile,
    archiveSHA,
    materialProgress,
    officialProgress,
    prepareOfficialMaterial,
    officialNeedsImport,
    prepareOfficialAgain,
    materialNeedsImport,
    prepareMaterialImport,
    uploadController,
    plan,
    receipt,
    submissionAttempted,
    job,
    logs,
    logsLimited, logsAt, logsLoading, logsPaused, logError,
    fieldError,
    actionError,
    credentialRoles,
    selectedMaterial,
    planExpired,
    submitted,
    credentialLabel,
    credentialOptions,
    editCredential,
    generateSecret,
    saveCredential,
    fileChanged,
    uploadMaterial,
    next,
    preflight,
    submit,
    jobAction,
    canRetryInstallation,
    retryInstallation,
    loadLogs,
    act,
    refreshJob,
    clearError,
    close,
  };
}
