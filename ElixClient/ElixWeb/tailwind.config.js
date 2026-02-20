import { heroui } from "@heroui/react";

/** @type {import('tailwindcss').Config} */
export default {
    content: [
        "./index.html",
        "./src/**/*.{js,ts,jsx,tsx}",
        "./node_modules/@heroui/theme/dist/**/*.{js,ts,jsx,tsx}",
    ],
    theme: {
        extend: {
            fontFamily: {
                sans: [
                    '"MiSans"',
                    "ui-sans-serif",
                    "system-ui",
                    "-apple-system",
                    "BlinkMacSystemFont",
                    "Segoe UI",
                    "Roboto",
                    "Helvetica Neue",
                    "Arial",
                    "sans-serif",
                ],
            },
            colors: {
                primary: "#ffffff",
                "bg-neutral": "#f3f4f6", // 浅灰 背景色 (基于 light 模式的主基调)
                "border-neutral": "#e5e7eb", // 低对比灰 边框
                "text-primary": "#1f2937", // 深灰
                "text-secondary": "#4b5563", // 中灰
                accent: "#002EA6", // 克莱因蓝
            },
            spacing: {
                // 覆盖为 4pt Grid
                1: "4px",
                2: "8px",
                3: "12px",
                4: "16px",
                5: "20px",
                6: "24px",
                7: "28px",
                8: "32px",
                9: "36px",
                10: "40px",
                12: "48px",
            },
            borderRadius: {
                micro: "12px",
                card: "24px",
                full: "9999px",
            },
            boxShadow: {
                level1: "0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -1px rgba(0, 0, 0, 0.03)",
                level2: "0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05)",
            },
            fontSize: {
                // Typography System
                caption: ["12px", "1.6"],
                "body-small": ["14px", "1.6"],
                "body-large": ["16px", "1.6"],
                h3: ["20px", "1.4"],
                h2: ["24px", "1.4"],
                h1: ["32px", "1.4"],
            },
        },
    },
    darkMode: "class",
    plugins: [heroui()],
};
