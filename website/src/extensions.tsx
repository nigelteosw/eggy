import type { ReactNode } from "react";
import { FinancePage } from "./FinancePage";
import { WalletIcon } from "./components/ui/icons";

// The feature extensions the web panel can open. Each one is listed under the
// rail's Extensions menu and opens as a window over the current view. An extension
// is listed only while its feature is running, so a disabled one leaves no
// trace in the panel.
export type ExtensionDef = {
  id: string;
  label: string;
  icon: ReactNode;
  feature: string;
  // onClose returns to the view underneath; a panel draws its own heading and
  // the close control, the way the other pages do.
  Panel: (props: { onSessionExpired: () => void; onClose: () => void }) => ReactNode;
};

export const EXTENSIONS: ExtensionDef[] = [
  {
    id: "finance",
    label: "Finance",
    icon: <WalletIcon className="h-4 w-4" />,
    feature: "finance",
    Panel: ({ onSessionExpired, onClose }) => <FinancePage onClose={onClose} onSessionExpired={onSessionExpired} />,
  },
];

export function runningExtensions(features: string[]): ExtensionDef[] {
  return EXTENSIONS.filter((extension) => features.includes(extension.feature));
}
