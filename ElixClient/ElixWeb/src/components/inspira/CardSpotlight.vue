<script setup lang="ts">
import { computed, ref } from "vue";

const x = ref(50);
const y = ref(50);
const hovering = ref(false);

function onPointerMove(event: PointerEvent) {
  const target = event.currentTarget as HTMLElement;
  const rect = target.getBoundingClientRect();
  x.value = ((event.clientX - rect.left) / rect.width) * 100;
  y.value = ((event.clientY - rect.top) / rect.height) * 100;
  hovering.value = true;
}

function onPointerLeave() {
  hovering.value = false;
}

const overlayStyle = computed(() => ({
  background: `radial-gradient(420px circle at ${x.value}% ${y.value}%, rgba(56, 189, 248, 0.2), transparent 40%)`,
  opacity: hovering.value ? 1 : 0,
}));
</script>

<template>
  <div
    class="group relative rounded-2xl border border-white/10 bg-slate-950/55 shadow-soft backdrop-blur-sm transition"
    @pointermove="onPointerMove"
    @pointerleave="onPointerLeave"
  >
    <div
      class="pointer-events-none absolute inset-0 rounded-2xl transition-opacity duration-300"
      :style="overlayStyle"
    />
    <div class="relative z-[1]">
      <slot />
    </div>
  </div>
</template>
