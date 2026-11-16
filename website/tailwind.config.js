// Every colour lives in internal/themes/themes.json, which the server embeds
// too. Its first theme is the default: its palettes become :root and .dark
// here, and a look the account picks is painted over them at runtime.
const themes = require("../internal/themes/themes.json");
const palette = (tokens) => Object.fromEntries(Object.entries(tokens).map(([name, value]) => [`--${name}`, value]));

/** @type {import('tailwindcss').Config} */
export default {
  darkMode: ["class"],
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    // Five sizes and a field size, replacing Tailwind's scale rather than
    // extending it, so a size outside the set does not exist to reach for.
    // Field is 16 px because a phone zooms on focus below it; it is for form
    // controls only. tests/type-scale.test.ts holds the set, and src/lib/utils
    // tells tailwind-merge these are sizes.
    fontSize: {
      caption: ["12px", { lineHeight: "16px" }],
      small: ["13px", { lineHeight: "18px" }],
      body: ["15px", { lineHeight: "22px" }],
      field: ["16px", { lineHeight: "24px" }],
      title: ["18px", { lineHeight: "24px" }],
      display: ["24px", { lineHeight: "30px" }],
    },
    // Three radii from the review -- 10 px controls, 16 px cards and dialogs,
    // full for pills -- and mark, 4 px, for glyph-sized things such as inline
    // code and legend swatches, which a 10 px corner would turn into circles.
    // Replaced rather than extended; tests/radius.test.ts holds the set.
    borderRadius: {
      none: "0px",
      mark: "4px",
      control: "10px",
      card: "16px",
      full: "9999px",
      // Corners nest concentrically: an outer radius is the inner one plus
      // the gap between them. card is control + 6 (p-1.5), the usual menu and
      // segmented-control padding. shell is a round control's radius plus its
      // gap, for a bar that holds pill buttons at its edge -- the composer,
      // whose 36 px buttons (44 on touch) sit 7 px in. Set in index.css.
      shell: "var(--shell-radius)",
    },
    extend: {
      fontFamily: {
        // Geist everywhere: panel, sign-in and docs share one face.
        // Geist unless the account's look picks another face; the stack
        // lives in index.css so a look can swap it.
        sans: ["var(--font-sans)"],
        brand: ["Geist", "ui-sans-serif", "system-ui", "sans-serif"],
        // Eggy's voice, for the greeting, the sign-in line and empty states
        // only (components/ui/voice.tsx). Never in a control.
        voice: ["Fraunces", "ui-serif", "Georgia", "serif"],
        mono: ["var(--font-mono)"],
      },
      colors: {
        // Hairlines are the border ink at low alpha, so a divider blends into
        // whatever it sits on -- page, card or fill -- instead of being a grey
        // that matches only one of them. --border-alpha is in index.css.
        border: "oklch(var(--border) / calc(<alpha-value> * var(--border-alpha)))",
        input: "oklch(var(--input) / <alpha-value>)",
        ring: "oklch(var(--ring) / <alpha-value>)",
        background: "oklch(var(--background) / <alpha-value>)",
        foreground: "oklch(var(--foreground) / <alpha-value>)",
        primary: { DEFAULT: "oklch(var(--primary) / <alpha-value>)", foreground: "oklch(var(--primary-foreground) / <alpha-value>)" },
        secondary: { DEFAULT: "oklch(var(--secondary) / <alpha-value>)", foreground: "oklch(var(--secondary-foreground) / <alpha-value>)" },
        muted: { DEFAULT: "oklch(var(--muted) / <alpha-value>)", foreground: "oklch(var(--muted-foreground) / <alpha-value>)" },
        accent: {
          DEFAULT: "oklch(var(--accent) / <alpha-value>)",
          foreground: "oklch(var(--accent-foreground) / <alpha-value>)",
          100: "oklch(var(--accent-100) / <alpha-value>)",
          200: "oklch(var(--accent-200) / <alpha-value>)",
          300: "oklch(var(--accent-300) / <alpha-value>)",
          400: "oklch(var(--accent-400) / <alpha-value>)",
          500: "oklch(var(--accent-500) / <alpha-value>)",
          600: "oklch(var(--accent-600) / <alpha-value>)",
          700: "oklch(var(--accent-700) / <alpha-value>)",
          800: "oklch(var(--accent-800) / <alpha-value>)",
          900: "oklch(var(--accent-900) / <alpha-value>)",
        },
        neutral: {
          100: "oklch(var(--neutral-100) / <alpha-value>)",
          200: "oklch(var(--neutral-200) / <alpha-value>)",
          300: "oklch(var(--neutral-300) / <alpha-value>)",
          400: "oklch(var(--neutral-400) / <alpha-value>)",
          500: "oklch(var(--neutral-500) / <alpha-value>)",
          600: "oklch(var(--neutral-600) / <alpha-value>)",
          700: "oklch(var(--neutral-700) / <alpha-value>)",
          800: "oklch(var(--neutral-800) / <alpha-value>)",
          900: "oklch(var(--neutral-900) / <alpha-value>)",
        },
        surface: "oklch(var(--surface) / <alpha-value>)",
        card: { DEFAULT: "oklch(var(--card) / <alpha-value>)", foreground: "oklch(var(--card-foreground) / <alpha-value>)" },
        tomato: { DEFAULT: "oklch(var(--tomato) / <alpha-value>)", soft: "oklch(var(--tomato-soft) / <alpha-value>)" },
        "on-tomato": "oklch(var(--on-tomato) / <alpha-value>)",
        chive: "oklch(var(--chive) / <alpha-value>)",
        yolk: { soft: "oklch(var(--yolk-soft) / <alpha-value>)", ink: "oklch(var(--yolk-ink) / <alpha-value>)" },
      },
      boxShadow: {
        subtle: "0 1px 2px 0 rgb(43 38 32 / 0.04), 0 1px 3px 0 rgb(43 38 32 / 0.06)",
        card: "0 1px 2px 0 rgb(43 38 32 / 0.03), 0 4px 16px -4px rgb(43 38 32 / 0.08)",
        lift: "0 2px 4px -1px rgb(43 38 32 / 0.06), 0 12px 32px -8px rgb(43 38 32 / 0.14)",
      },
      keyframes: {
        "fade-in-up": {
          from: { opacity: "0", transform: "translateY(6px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        blink: {
          "0%, 80%, 100%": { opacity: "0.25", transform: "translateY(0)" },
          "40%": { opacity: "1", transform: "translateY(-2px)" },
        },
      },
      animation: {
        "fade-in-up": "fade-in-up 0.22s cubic-bezier(0.22, 1, 0.36, 1)",
        blink: "blink 1.2s ease-in-out infinite",
      },
    },
  },
  plugins: [
    require("@tailwindcss/typography"),
    require("tailwindcss-animate"),
    ({ addBase }) => addBase({ ":root": palette(themes[0].light), ".dark": palette(themes[0].dark) }),
  ],
};
