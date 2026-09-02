// Inline stroke icons, matching the ones already hand-written in App.tsx.
// A dependency for a dozen 24x24 paths would be the largest package in the
// bundle after React.
import type { ReactNode } from "react";

type IconProps = { className?: string };

function Icon({ className, children }: IconProps & { children: ReactNode }) {
  return (
    <svg
      viewBox="0 0 20 20"
      className={className ?? "h-4 w-4"}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

export const ChevronLeftIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12.5 4.5 7 10l5.5 5.5" />
  </Icon>
);

export const PanelIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="2.5" y="3.5" width="15" height="13" rx="2" />
    <path d="M7.5 3.5v13" />
  </Icon>
);

export const CpuIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="6" y="6" width="8" height="8" rx="1.5" />
    <path d="M10 2.5v3.5M10 14v3.5M2.5 10H6M14 10h3.5M4.5 4.5 6 6M15.5 4.5 14 6M4.5 15.5 6 14M15.5 15.5 14 14" />
  </Icon>
);

export const PlugIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M7 2.5v4M13 2.5v4M4.5 6.5h11v3a5.5 5.5 0 0 1-11 0Z" />
    <path d="M10 15v2.5" />
  </Icon>
);

export const GoogleIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="10" cy="10" r="7" />
    <path d="M10 6.5h3.5v3.5a3.5 3.5 0 1 1-1-2.5" />
  </Icon>
);

export const WrenchIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M13.2 3.4a4 4 0 0 0-5 5L3.5 13.1a1.6 1.6 0 0 0 2.3 2.3l4.7-4.7a4 4 0 0 0 5-5l-2.3 2.3-2-2Z" />
  </Icon>
);

export const ClockIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="10" cy="10" r="7" />
    <path d="M10 6v4.2l2.6 1.6" />
  </Icon>
);

export const CheckShieldIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M10 2.5 16 5v4.5c0 3.6-2.4 6.7-6 8-3.6-1.3-6-4.4-6-8V5Z" />
    <path d="M7.5 10 9.3 12l3.4-3.6" />
  </Icon>
);

export const FileCodeIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M11.5 2.5H5.5a1.5 1.5 0 0 0-1.5 1.5v12a1.5 1.5 0 0 0 1.5 1.5h9a1.5 1.5 0 0 0 1.5-1.5V7Z" />
    <path d="M11.5 2.5V7H16" />
    <path d="m8.6 10.6-1.4 1.4 1.4 1.4M11.4 10.6l1.4 1.4-1.4 1.4" />
  </Icon>
);

export const PaletteIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M10 2.8a7.2 7.2 0 0 0 0 14.4c.9 0 1.5-.7 1.5-1.5 0-.4-.2-.8-.4-1-.3-.3-.4-.6-.4-1 0-.8.7-1.5 1.5-1.5h1.3a3.7 3.7 0 0 0 3.7-3.7c0-3.1-3.2-5.7-7.2-5.7Z" />
    <circle cx="6.6" cy="9.2" r="0.9" fill="currentColor" stroke="none" />
    <circle cx="9.6" cy="6.4" r="0.9" fill="currentColor" stroke="none" />
    <circle cx="13.2" cy="7.6" r="0.9" fill="currentColor" stroke="none" />
  </Icon>
);

export const ChevronDownIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="m6 8 4 4 4-4" />
  </Icon>
);

export const PlusIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M10 5v10M5 10h10" />
  </Icon>
);

export const SettingsIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="10" cy="10" r="2.6" />
    <path d="M10 2.6v1.6M10 15.8v1.6M17.4 10h-1.6M4.2 10H2.6M15.2 4.8l-1.1 1.1M5.9 14.1l-1.1 1.1M15.2 15.2l-1.1-1.1M5.9 5.9 4.8 4.8" />
  </Icon>
);

// The reasoning-effort control: a dial, because the levels are ordered.
export const GaugeIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3.5 14a7 7 0 1 1 13 0" />
    <path d="m10 11.5 3-3" />
  </Icon>
);

// The approval control. A closed shackle for a gate that asks; ArrowUpIcon
// aside, this is the only icon in the composer row that changes meaning with
// its label, so the label carries the mode and this only says "approvals".
export const LockIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="4" y="8.5" width="12" height="8" rx="2" />
    <path d="M7 8.5V6a3 3 0 0 1 6 0v2.5" />
  </Icon>
);

export const ArrowUpIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M10 15.5v-11M5.5 9 10 4.5 14.5 9" />
  </Icon>
);

export const LogoutIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M8 17H4.5A1.5 1.5 0 0 1 3 15.5v-11A1.5 1.5 0 0 1 4.5 3H8" />
    <path d="M13 13.5 16.5 10 13 6.5M16.5 10H7.5" />
  </Icon>
);

// The traces destination: a step sequence, which is what a trace is.
export const TraceIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3 15.5h3.5v-4H10v-4h3.5v-3" />
    <circle cx="16" cy="4.5" r="1.5" />
    <circle cx="3" cy="15.5" r="1.5" />
  </Icon>
);
