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
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        primary: { DEFAULT: "hsl(var(--primary))", foreground: "hsl(var(--primary-foreground))" },
        secondary: { DEFAULT: "hsl(var(--secondary))", foreground: "hsl(var(--secondary-foreground))" },
        muted: { DEFAULT: "hsl(var(--muted))", foreground: "hsl(var(--muted-foreground))" },
        accent: {
          DEFAULT: "hsl(var(--accent))",
          foreground: "hsl(var(--accent-foreground))",
          100: "hsl(var(--accent-100))",
          200: "hsl(var(--accent-200))",
          300: "hsl(var(--accent-300))",
          400: "hsl(var(--accent-400))",
          500: "hsl(var(--accent-500))",
          600: "hsl(var(--accent-600))",
          700: "hsl(var(--accent-700))",
          800: "hsl(var(--accent-800))",
          900: "hsl(var(--accent-900))",
        },
        neutral: {
          100: "hsl(var(--neutral-100))",
          200: "hsl(var(--neutral-200))",
          300: "hsl(var(--neutral-300))",
          400: "hsl(var(--neutral-400))",
          500: "hsl(var(--neutral-500))",
          600: "hsl(var(--neutral-600))",
          700: "hsl(var(--neutral-700))",
          800: "hsl(var(--neutral-800))",
          900: "hsl(var(--neutral-900))",
        },
        surface: "hsl(var(--surface))",
        card: { DEFAULT: "hsl(var(--card))", foreground: "hsl(var(--card-foreground))" },
        tomato: { DEFAULT: "hsl(var(--tomato))", soft: "hsl(var(--tomato-soft))" },
        "on-tomato": "hsl(var(--on-tomato))",
        chive: "hsl(var(--chive))",
        yolk: { soft: "hsl(var(--yolk-soft))", ink: "hsl(var(--yolk-ink))" },
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
  plugins: [require("@tailwindcss/typography"), require("tailwindcss-animate")],
};
