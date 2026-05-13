/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
  theme: {
    extend: {
      colors: {
        // Electric blue matching the dog's glowing eyes & globe
        brand: {
          50:  "#eff6ff",
          300: "#93c5fd",
          400: "#60a5fa",
          500: "#2563eb",   // primary — royal/electric blue
          600: "#1d4ed8",   // hover
          700: "#1e40af",   // pressed
        },
        // Dark navy-blue surfaces (more blue-tinted than before)
        surface: {
          900: "#06080f",   // app background
          800: "#090d1a",   // slightly lighter
          700: "#0d1225",   // cards base
          600: "#111830",   // elevated cards
          500: "#16203a",   // highest elevation
        },
      },
      animation: {
        "spin-slow":  "spin 3s linear infinite",
        ripple:       "ripple 1.5s linear infinite",
        "fade-in":    "fadeIn 0.3s ease-out",
        "slide-up":   "slideUp 0.3s ease-out",
      },
      keyframes: {
        ripple: {
          "0%":   { transform: "scale(0.8)", opacity: "1" },
          "100%": { transform: "scale(2.4)", opacity: "0" },
        },
        fadeIn: {
          from: { opacity: "0" },
          to:   { opacity: "1" },
        },
        slideUp: {
          from: { opacity: "0", transform: "translateY(12px)" },
          to:   { opacity: "1", transform: "translateY(0)" },
        },
      },
      fontFamily: {
        sans: ["Inter", "system-ui", "sans-serif"],
      },
    },
  },
  plugins: [],
};
