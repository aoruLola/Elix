import { setupInspiraUI } from "@inspira-ui/plugins";

/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}",
    "./node_modules/primevue/**/*.{vue,js,ts,jsx,tsx}",
  ],
  darkMode: "class",
  theme: {
    extend: {
      borderRadius: {
        soft: "1rem",
      },
      boxShadow: {
        soft: "0 12px 35px rgba(2, 6, 23, 0.35)",
        glow: "0 0 0 1px rgba(148, 163, 184, 0.12), 0 16px 40px rgba(15, 23, 42, 0.4)",
      },
    },
  },
  plugins: [setupInspiraUI],
};
