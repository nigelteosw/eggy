import type { ReactNode } from "react";
import { FinancePage } from "./FinancePage";
import { WalletIcon } from "./components/ui/icons";

// The feature plugins the web panel can open. Each one is listed under the
// rail's Plugins menu and opens as a window over the current view. A plugin
// is listed only while its feature is running, so a disabled one leaves no
// trace in the panel.
export type PluginDef = {
  id: string;
  label: string;
  icon: ReactNode;
  feature: string;
  Panel: (props: { onSessionExpired: () => void }) => ReactNode;
};

export const PLUGINS: PluginDef[] = [
  {
    id: "finance",
    label: "Finance",
    icon: <WalletIcon className="h-4 w-4" />,
    feature: "finance",
    Panel: ({ onSessionExpired }) => <FinancePage embedded onSessionExpired={onSessionExpired} />,
  },
];

export function runningPlugins(features: string[]): PluginDef[] {
  return PLUGINS.filter((plugin) => features.includes(plugin.feature));
}
