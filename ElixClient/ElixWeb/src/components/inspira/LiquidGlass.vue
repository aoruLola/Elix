<script setup lang="ts">
import type { HTMLAttributes } from "vue";
import { computed, onMounted, onUnmounted, reactive, ref } from "vue";

interface Props {
  radius?: number;
  border?: number;
  lightness?: number;
  displace?: number;
  backdropBlur?: number;
  blend?: string;
  xChannel?: "R" | "G" | "B";
  yChannel?: "R" | "G" | "B";
  alpha?: number;
  blur?: number;
  rOffset?: number;
  gOffset?: number;
  bOffset?: number;
  scale?: number;
  frost?: number;
  class?: HTMLAttributes["class"];
  containerClass?: HTMLAttributes["class"];
}

const props = withDefaults(defineProps<Props>(), {
  radius: 16,
  border: 0.06,
  lightness: 42,
  blend: "normal",
  xChannel: "R",
  yChannel: "B",
  alpha: 0.8,
  blur: 9,
  rOffset: 0,
  gOffset: 4,
  bOffset: 9,
  scale: -64,
  frost: 0.04,
  displace: 0.08,
  backdropBlur: 8,
});

const liquidGlassRoot = ref<HTMLElement | null>(null);
const dimensions = reactive({
  width: 0,
  height: 0,
});
const filterId = `displacementFilter_${Math.random().toString(36).slice(2, 10)}`;

let observer: ResizeObserver | null = null;

const baseStyle = computed(() => {
  const filterValue = `url(#${filterId}) blur(${props.backdropBlur}px)`;
  return {
    "--frost": props.frost,
    "border-radius": `${props.radius}px`,
    "backdrop-filter": filterValue,
    "-webkit-backdrop-filter": filterValue,
  };
});

const displacementImage = computed(() => {
  const border = Math.min(dimensions.width, dimensions.height) * (props.border * 0.5);
  const yBorder = Math.min(dimensions.width, dimensions.height) * (props.border * 0.5);

  return `
    <svg viewBox="0 0 ${dimensions.width} ${dimensions.height}" xmlns="http://www.w3.org/2000/svg">
      <defs>
        <linearGradient id="red" x1="100%" y1="0%" x2="0%" y2="0%">
          <stop offset="0%" stop-color="#0000"/>
          <stop offset="100%" stop-color="red"/>
        </linearGradient>
        <linearGradient id="blue" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="#0000"/>
          <stop offset="100%" stop-color="blue"/>
        </linearGradient>
      </defs>
      <rect x="0" y="0" width="${dimensions.width}" height="${dimensions.height}" fill="black"></rect>
      <rect x="0" y="0" width="${dimensions.width}" height="${dimensions.height}" rx="${props.radius}" fill="url(#red)" />
      <rect x="0" y="0" width="${dimensions.width}" height="${dimensions.height}" rx="${props.radius}" fill="url(#blue)" style="mix-blend-mode: ${props.blend}" />
      <rect
        x="${border}"
        y="${yBorder}"
        width="${dimensions.width - border * 2}"
        height="${dimensions.height - border * 2}"
        rx="${props.radius}"
        fill="hsl(0 0% ${props.lightness}% / ${props.alpha})"
        style="filter:blur(${props.blur}px)"
      />
    </svg>
  `;
});

const displacementDataUri = computed(() => {
  const encoded = encodeURIComponent(displacementImage.value);
  return `data:image/svg+xml,${encoded}`;
});

onMounted(() => {
  if (!liquidGlassRoot.value) return;

  observer = new ResizeObserver((entries) => {
    const entry = entries[0];
    if (!entry) return;

    let width = 0;
    let height = 0;

    if (entry.borderBoxSize && entry.borderBoxSize.length) {
      width = entry.borderBoxSize[0]!.inlineSize;
      height = entry.borderBoxSize[0]!.blockSize;
    } else if (entry.contentRect) {
      width = entry.contentRect.width;
      height = entry.contentRect.height;
    }

    dimensions.width = width;
    dimensions.height = height;
  });

  observer.observe(liquidGlassRoot.value);
});

onUnmounted(() => {
  observer?.disconnect();
});
</script>

<template>
  <div
    ref="liquidGlassRoot"
    class="effect"
    :class="[props.containerClass]"
    :style="baseStyle"
  >
    <div
      class="slot-container"
      :class="[props.class]"
    >
      <slot />
    </div>

    <svg
      class="filter"
      xmlns="http://www.w3.org/2000/svg"
    >
      <defs>
        <filter
          :id="filterId"
          color-interpolation-filters="sRGB"
        >
          <feImage
            x="0"
            y="0"
            width="100%"
            height="100%"
            :href="displacementDataUri"
            result="map"
          />
          <feDisplacementMap
            id="redchannel"
            in="SourceGraphic"
            in2="map"
            :xChannelSelector="xChannel"
            :yChannelSelector="yChannel"
            :scale="scale + rOffset"
            result="dispRed"
          />
          <feColorMatrix
            in="dispRed"
            type="matrix"
            values="1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1 0"
            result="red"
          />
          <feDisplacementMap
            id="greenchannel"
            in="SourceGraphic"
            in2="map"
            :xChannelSelector="xChannel"
            :yChannelSelector="yChannel"
            :scale="scale + gOffset"
            result="dispGreen"
          />
          <feColorMatrix
            in="dispGreen"
            type="matrix"
            values="0 0 0 0 0 0 1 0 0 0 0 0 0 0 0 0 0 0 1 0"
            result="green"
          />
          <feDisplacementMap
            id="bluechannel"
            in="SourceGraphic"
            in2="map"
            :xChannelSelector="xChannel"
            :yChannelSelector="yChannel"
            :scale="scale + bOffset"
            result="dispBlue"
          />
          <feColorMatrix
            in="dispBlue"
            type="matrix"
            values="0 0 0 0 0 0 0 0 0 0 0 0 1 0 0 0 0 0 1 0"
            result="blue"
          />
          <feBlend
            in="red"
            in2="green"
            mode="screen"
            result="rg"
          />
          <feBlend
            in="rg"
            in2="blue"
            mode="screen"
            result="output"
          />
          <feGaussianBlur :stdDeviation="displace" />
        </filter>
      </defs>
    </svg>
  </div>
</template>

<style scoped>
.effect {
  position: relative;
  display: block;
  width: 100%;
  height: 100%;
  opacity: 1;
  isolation: isolate;
  overflow: hidden;
  border-radius: inherit;
  background: light-dark(
    hsl(0 0% 100% / calc(var(--frost, 0) * 0.96)),
    hsl(222 30% 12% / calc(var(--frost, 0) * 1.25))
  );
  box-shadow:
    inset 0 0 0 0.5px rgba(255, 255, 255, 0.26),
    inset 0 1px 0 rgba(255, 255, 255, 0.2),
    0 8px 24px rgba(1, 5, 16, 0.26);
}

.effect::before {
  content: "";
  position: absolute;
  inset: 0;
  border-radius: inherit;
  pointer-events: none;
  border: 1px solid rgba(255, 255, 255, 0.2);
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.14), rgba(255, 255, 255, 0.03));
  z-index: 2;
}

.effect::after {
  content: "";
  position: absolute;
  left: 12%;
  right: 12%;
  top: 1px;
  height: 24%;
  border-radius: 9999px;
  pointer-events: none;
  background: linear-gradient(to bottom, rgba(255, 255, 255, 0.3), rgba(255, 255, 255, 0));
  filter: blur(1px);
  opacity: 0.5;
  z-index: 2;
}

.slot-container {
  position: relative;
  z-index: 3;
  width: 100%;
  height: 100%;
  overflow: hidden;
  border-radius: inherit;
}
</style>
