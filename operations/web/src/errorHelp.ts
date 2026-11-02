import { reactive } from "vue";

// The same reference dialog opens above the current page, preserving its draft.
export const errorHelp = reactive({ open: false, code: "" });
export function showErrorHelp(code: string) {
  errorHelp.code = code.trim();
  errorHelp.open = true;
}
