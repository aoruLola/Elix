<template>
  <div class="pointer-events-none absolute inset-0 overflow-hidden">
    <div class="absolute inset-0 bg-[#05070f]" />
    <div class="absolute inset-0 bg-grid-slate-200 opacity-10 [mask-image:radial-gradient(ellipse_at_center,black_25%,transparent_75%)]" />
    <div
      class="absolute inset-0 opacity-80"
      :style="{
        background:
          'radial-gradient(45% 40% at 18% 15%, rgba(56, 189, 248, 0.18), transparent), radial-gradient(35% 35% at 82% 0%, rgba(99, 102, 241, 0.22), transparent), radial-gradient(40% 38% at 50% 100%, rgba(14, 165, 233, 0.1), transparent)',
      }"
    />
  </div>
</template>
