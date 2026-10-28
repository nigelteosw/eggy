import { expect, test } from "bun:test";
import { PROVIDER_PRESETS, presetById } from "../src/lib/providers";

// A preset fills the form, so each one must name a real key variable and an
// address people actually use. A key is never a preset's value.
test("every preset names a key variable and a secure provider address", () => {
  for (const preset of PROVIDER_PRESETS) {
    expect(preset.apiKeyEnv).toMatch(/^[A-Z][A-Z0-9_]*_API_KEY$/);
    expect(preset.baseUrl.startsWith("https://")).toBe(true);
    expect(preset.label.length).toBeGreaterThan(0);
  }
});

test("a preset can be looked up by its id, and an unknown id is not a preset", () => {
  expect(presetById("openai")?.modelId).toBe("gpt-4o");
  expect(presetById("nope")).toBeUndefined();
});
