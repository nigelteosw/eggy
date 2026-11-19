import { Suspense, lazy, type ReactNode } from "react";
import { FinancePage } from "./finance/FinancePage";
import { FileCodeIcon, TerminalIcon, WalletIcon } from "../components/ui/icons";
import { AgentRunnerPage } from "./agent-runner/AgentRunnerPage";
// The terminal page carries xterm.js, the largest thing in the panel, so it is
// fetched only when someone opens a running terminal extension.
const TerminalPage = lazy(() => import("./terminal/TerminalPage").then((m) => ({ default: m.TerminalPage })));

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
  // onOpenSidebar puts the phone's menu button in the panel's own heading row.
  Panel: (props: { onSessionExpired: () => void; onClose: () => void; onOpenSidebar?: () => void }) => ReactNode;
};

export const EXTENSIONS: ExtensionDef[] = [
  {
    id: "finance",
    label: "Finance",
    icon: <WalletIcon className="h-4 w-4" />,
    feature: "finance",
    Panel: ({ onSessionExpired, onClose, onOpenSidebar }) => <FinancePage onClose={onClose} onOpenSidebar={onOpenSidebar} onSessionExpired={onSessionExpired} />,
  },
  {
    id: "agent_runner",
    label: "Agent Runner",
    icon: <FileCodeIcon className="h-4 w-4" />,
    feature: "agent_runner",
    Panel: ({ onSessionExpired, onClose, onOpenSidebar }) => <AgentRunnerPage onClose={onClose} onOpenSidebar={onOpenSidebar} onSessionExpired={onSessionExpired} />,
  },
  {
    id: "terminal",
    label: "Terminal",
    icon: <TerminalIcon className="h-4 w-4" />,
    feature: "terminal",
    Panel: ({ onSessionExpired, onClose, onOpenSidebar }) => (
      <Suspense fallback={<p className="p-6 text-small text-neutral-700">Loading...</p>}>
        <TerminalPage onClose={onClose} onOpenSidebar={onOpenSidebar} onSessionExpired={onSessionExpired} />
      </Suspense>
    ),
  },
];

export function runningExtensions(features: string[]): ExtensionDef[] {
  return EXTENSIONS.filter((extension) => features.includes(extension.feature));
}
