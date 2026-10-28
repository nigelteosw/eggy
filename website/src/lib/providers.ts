// The model providers a person most often connects, filled in for them so the
// setup asks "which provider" rather than "what is the base URL". Each preset
// names the environment variable the key lives in: the key itself is never typed
// into setup.
export type ProviderPreset = {
  id: string;
  label: string;
  providerName: string;
  baseUrl: string;
  apiKeyEnv: string;
  modelAlias: string;
  modelId: string;
};

export const PROVIDER_PRESETS: ProviderPreset[] = [
  {
    id: "deepseek",
    label: "DeepSeek",
    providerName: "deepseek",
    baseUrl: "https://api.deepseek.com",
    apiKeyEnv: "DEEPSEEK_API_KEY",
    modelAlias: "deepseek-pro",
    modelId: "deepseek-v4-pro",
  },
  {
    id: "openai",
    label: "OpenAI",
    providerName: "openai",
    baseUrl: "https://api.openai.com/v1",
    apiKeyEnv: "OPENAI_API_KEY",
    modelAlias: "openai-main",
    modelId: "gpt-4o",
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    providerName: "openrouter",
    baseUrl: "https://openrouter.ai/api/v1",
    apiKeyEnv: "OPENROUTER_API_KEY",
    modelAlias: "openrouter-main",
    modelId: "openai/gpt-4o-mini",
  },
];

export function presetById(id: string): ProviderPreset | undefined {
  return PROVIDER_PRESETS.find((preset) => preset.id === id);
}
