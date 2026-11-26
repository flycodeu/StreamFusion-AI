import { computed, onUnmounted, ref, watch, type Ref } from "vue";
import { request } from "../../api";
import {
  moduleDirectory,
  moduleEdit,
  newModuleDraft,
  type ApplicationProject,
  type InspectedModule,
  type InspectionSource,
  type ProjectModule,
} from "../../applicationProjects";

export function useProjectInspection(
  project: Ref<ApplicationProject | undefined>,
  saving: Ref<boolean>,
  formError: Ref<string>,
  describeError: (error: unknown, fallback: string) => string,
) {
  const inspectionOpen = ref(false);
  const sourcesLoading = ref(false);
  const inspecting = ref(false);
  const inspected = ref(false);
  const inspectionMode = ref<"repo" | "local">("repo");
  const inspectionRepoUrl = ref("");
  const inspectionBranch = ref("");
  const inspectionGitAccountId = ref("");
  const inspectionModuleId = ref("");
  const sourceId = ref("");
  const inspectionRepository = ref("");
  const localRegistrationRepository = ref("");
  const registrationRepository = computed(() =>
    inspectionMode.value === "local" ? localRegistrationRepository.value : inspectionRepository.value,
  );
  const inspectionRepoMismatch = computed(() => {
    if (inspectionMode.value !== "repo" || !inspectionRepository.value) return false;
    const repository = project.value?.repositories.find((item) => item.id === inspectionRepository.value);
    return repository?.url.trim() !== inspectionRepoUrl.value.trim();
  });
  const inspectionSources = ref<InspectionSource[]>([]);
  const inspectionResults = ref<InspectedModule[]>([]);
  const selectedDirectories = ref<string[]>([]);
  let sourcesReader: AbortController | undefined;
  let scanner: AbortController | undefined;
  let preferLocal = false;

  function matchingModule(directory: string) {
    const modules = project.value?.modules ?? [];
    if (inspectionModuleId.value)
      return modules.find((item) => item.id === inspectionModuleId.value);
    const atDirectory = modules.filter(
      (item) => moduleDirectory(item.directory) === directory,
    );
    return (
      atDirectory.find((item) => item.repositoryId === registrationRepository.value) ??
      (registrationRepository.value === project.value?.repositories[0]?.id
        ? atDirectory.find((item) => !item.repositoryId)
        : undefined)
    );
  }

  function syncInspectionRepoFields(repoId: string) {
    const repo =
      project.value?.repositories.find((item) => item.id === repoId) ??
      project.value?.repositories[0];
    inspectionRepoUrl.value = repo?.url ?? "";
    inspectionBranch.value = repo?.branch ?? "";
    inspectionGitAccountId.value = repo?.gitAccountId ?? "";
  }

  function cancelRequests() {
    sourcesReader?.abort();
    sourcesReader = undefined;
    sourcesLoading.value = false;
    scanner?.abort();
    scanner = undefined;
    inspecting.value = false;
  }

  function clearResults() {
    scanner?.abort();
    scanner = undefined;
    inspecting.value = false;
    inspectionResults.value = [];
    selectedDirectories.value = [];
    formError.value = "";
    inspected.value = false;
  }

  async function openInspection(item?: ProjectModule) {
    cancelRequests();
    inspectionModuleId.value = item?.id ?? "";
    inspectionRepository.value =
      item?.repositoryId ?? project.value?.repositories[0]?.id ?? "";
    localRegistrationRepository.value = inspectionRepository.value;
    syncInspectionRepoFields(inspectionRepository.value);
    inspectionMode.value = "repo";
    inspectionSources.value = [];
    sourceId.value = "";
    clearResults();
    preferLocal = true;
    inspectionOpen.value = true;
    sourcesLoading.value = true;
    const controller = (sourcesReader = new AbortController());
    try {
      const { items } = await request<{ items: InspectionSource[] }>(
        "/application-projects/inspection-sources",
        "GET",
        undefined,
        controller.signal,
      );
      if (controller.signal.aborted) return;
      inspectionSources.value = items;
      if (items.length === 1) sourceId.value = items[0].id;
      if (items.length > 0 && preferLocal) inspectionMode.value = "local";
    } catch (error) {
      if (!controller.signal.aborted && preferLocal)
        formError.value = describeError(error, "识别来源读取失败，可以手动填写模块");
    } finally {
      if (sourcesReader === controller) {
        sourcesReader = undefined;
        sourcesLoading.value = false;
      }
    }
  }

  function selectNewDirectories() {
    selectedDirectories.value = inspectionRepoMismatch.value
      ? []
      : inspectionResults.value
          .filter((item) => !matchingModule(item.directory))
          .map((item) => item.directory);
  }

  async function inspect() {
    if (!inspectionOpen.value || inspecting.value || saving.value) return;
    preferLocal = false;
    if (inspectionMode.value === "local" && !sourceId.value) return;
    if (inspectionMode.value === "repo" && !inspectionRepoUrl.value.trim()) {
      formError.value = "请先填写要识别的 Git 仓库地址";
      return;
    }
    const module = project.value?.modules.find((item) => item.id === inspectionModuleId.value);
    const directory = inspectionModuleId.value ? module?.directory || "." : undefined;
    const body =
      inspectionMode.value === "local"
        ? { sourceId: sourceId.value, directory }
        : {
            projectId: project.value?.id,
            repositoryId: inspectionRepository.value || undefined,
            repositoryUrl: inspectionRepoUrl.value.trim(),
            branch: inspectionBranch.value.trim() || undefined,
            gitAccountId: inspectionGitAccountId.value || undefined,
            directory,
          };
    clearResults();
    const controller = (scanner = new AbortController());
    inspecting.value = true;
    try {
      const { modules } = await request<{ modules: InspectedModule[] }>(
        "/application-projects/inspect",
        "POST",
        body,
        controller.signal,
      );
      if (controller.signal.aborted) return;
      inspectionResults.value = modules;
      selectNewDirectories();
      inspected.value = true;
    } catch (error) {
      if (!controller.signal.aborted)
        formError.value = describeError(error, "环境识别失败，可以手动填写模块");
    } finally {
      if (scanner === controller) {
        scanner = undefined;
        inspecting.value = false;
      }
    }
  }

  function reviewableModule(suggestion: InspectedModule) {
    return inspectionOpen.value && !inspectionRepoMismatch.value && inspectionResults.value.includes(suggestion)
      ? matchingModule(suggestion.directory)
      : undefined;
  }

  function selectedModuleEdits() {
    if (!inspectionOpen.value || inspectionRepoMismatch.value) return [];
    return inspectionResults.value
      .filter(
        (item) =>
          selectedDirectories.value.includes(item.directory) &&
          !matchingModule(item.directory),
      )
      .map((item) =>
        moduleEdit({
          ...newModuleDraft(registrationRepository.value),
          ...item,
          ...(item.recommendedRuntimeMode
            ? { runtimeMode: item.recommendedRuntimeMode }
            : {}),
        }),
      );
  }

  watch(
    [
      inspectionMode,
      () => inspectionMode.value === "local" ? sourceId.value : inspectionRepository.value,
      () => inspectionMode.value === "repo" ? inspectionRepoUrl.value.trim() : "",
      () => inspectionMode.value === "repo" ? inspectionBranch.value.trim() : "",
      () => inspectionMode.value === "repo" ? inspectionGitAccountId.value : "",
    ],
    () => {
      preferLocal = false;
      if (inspectionOpen.value) clearResults();
    },
    { flush: "sync" },
  );
  watch(inspectionOpen, (open) => { if (!open) cancelRequests(); }, { flush: "sync" });
  watch(() => project.value?.id, () => { inspectionOpen.value = false; }, { flush: "sync" });
  onUnmounted(cancelRequests);

  return {
    inspectionOpen,
    sourcesLoading,
    inspecting,
    inspected,
    inspectionMode,
    inspectionRepoUrl,
    inspectionBranch,
    inspectionGitAccountId,
    inspectionModuleId,
    sourceId,
    inspectionRepository,
    inspectionRepoMismatch,
    localRegistrationRepository,
    inspectionSources,
    inspectionResults,
    selectedDirectories,
    matchingModule,
    syncInspectionRepoFields,
    openInspection,
    selectNewDirectories,
    inspect,
    reviewableModule,
    selectedModuleEdits,
  };
}
