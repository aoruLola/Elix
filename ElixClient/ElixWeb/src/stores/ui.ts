import { defineStore } from "pinia";
import { ref } from "vue";

export const useUiStore = defineStore("ui", () => {
  const globalFilter = ref("");

  function setGlobalFilter(value: string) {
    globalFilter.value = value;
  }

  return {
    globalFilter,
    setGlobalFilter,
  };
});
