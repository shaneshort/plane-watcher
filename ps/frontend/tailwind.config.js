/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "../internal/web/static/**/*.html",
    "../internal/web/templates/**/*.html",
    "./src/**/*.{css,html,js}",
    "./prototypes/**/*.{html,js,json}"
  ],
  theme: {
    extend: {
      colors: {
        surface: {
          DEFAULT: "rgb(var(--color-surface-1) / <alpha-value>)",
          0: "rgb(var(--color-surface-0) / <alpha-value>)",
          1: "rgb(var(--color-surface-1) / <alpha-value>)",
          2: "rgb(var(--color-surface-2) / <alpha-value>)",
          3: "rgb(var(--color-surface-3) / <alpha-value>)"
        },
        text: {
          primary: "rgb(var(--color-text-primary) / <alpha-value>)",
          secondary: "rgb(var(--color-text-secondary) / <alpha-value>)",
          disabled: "rgb(var(--color-text-disabled) / <alpha-value>)"
        },
        brand: {
          DEFAULT: "rgb(var(--color-brand) / <alpha-value>)",
          soft: "rgb(var(--color-brand-soft) / <alpha-value>)"
        },
        accent: "rgb(var(--color-accent) / <alpha-value>)",
        success: "rgb(var(--color-success) / <alpha-value>)",
        warning: "rgb(var(--color-warning) / <alpha-value>)",
        danger: "rgb(var(--color-danger) / <alpha-value>)",
        border: {
          DEFAULT: "rgb(var(--color-border-subtle) / <alpha-value>)",
          strong: "rgb(var(--color-border-strong) / <alpha-value>)"
        },
        focus: "rgb(var(--color-focus) / <alpha-value>)"
      },
      fontFamily: {
        sans: [
          "\"IBM Plex Sans\"",
          "\"Segoe UI\"",
          "system-ui",
          "sans-serif"
        ],
        mono: [
          "\"IBM Plex Mono\"",
          "\"SFMono-Regular\"",
          "Consolas",
          "monospace"
        ]
      }
    }
  },
  plugins: []
}
