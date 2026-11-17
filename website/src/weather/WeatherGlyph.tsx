import { useId } from "react";
import type { Glyph } from "./weather";

// The weather pictures: soft, lit shapes rather than line icons, so the card
// reads as a sky and not a settings row. Their colours are the things drawn --
// sun, cloud, rain -- not the panel's, so they stay the same in every theme,
// the way a photograph would. Gradients are in user space so overlapping cloud
// puffs share one fill and show no seams.

export function WeatherGlyph({ glyph, size = 96, className }: { glyph: Glyph; size?: number; className?: string }) {
  const id = useId().replace(/:/g, "");
  const cloudFill = glyph === "storm" ? `url(#${id}-storm)` : `url(#${id}-cloud)`;
  const lifted = glyph === "rain" || glyph === "storm";
  return (
    <svg viewBox="0 0 96 96" width={size} height={size} className={className} aria-hidden="true" focusable="false">
      <defs>
        <radialGradient id={`${id}-sun`} cx="0.42" cy="0.38" r="0.7">
          <stop offset="0" stopColor="#FFF4B8" />
          <stop offset="0.45" stopColor="#FFCF3F" />
          <stop offset="1" stopColor="#F59A0C" />
        </radialGradient>
        <linearGradient id={`${id}-moon`} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#FFE9A3" />
          <stop offset="1" stopColor="#F2AE2E" />
        </linearGradient>
        <linearGradient id={`${id}-cloud`} gradientUnits="userSpaceOnUse" x1="40" y1="26" x2="56" y2="76">
          <stop offset="0" stopColor="#FFFFFF" />
          <stop offset="0.6" stopColor="#F1F5FA" />
          <stop offset="1" stopColor="#D4DEEA" />
        </linearGradient>
        <linearGradient id={`${id}-storm`} gradientUnits="userSpaceOnUse" x1="40" y1="26" x2="56" y2="76">
          <stop offset="0" stopColor="#E8EDF4" />
          <stop offset="0.6" stopColor="#B9C4D3" />
          <stop offset="1" stopColor="#8E9BAE" />
        </linearGradient>
        <linearGradient id={`${id}-drop`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#8FD0FF" />
          <stop offset="1" stopColor="#2E7FE6" />
        </linearGradient>
        <filter id={`${id}-shade`} x="-20%" y="-20%" width="140%" height="150%">
          <feDropShadow dx="0" dy="3" stdDeviation="3" floodColor="#1E3A5F" floodOpacity="0.2" />
        </filter>
      </defs>

      {glyph === "sun" && <Sun id={id} cx={48} cy={48} scale={1} />}
      {glyph === "moon" && <Moon id={id} cx={48} cy={48} scale={1} />}
      {glyph === "partly-day" && <Sun id={id} cx={60} cy={36} scale={0.66} />}
      {glyph === "partly-night" && <Moon id={id} cx={60} cy={34} scale={0.7} />}
      {glyph === "haze" && (
        <>
          <g opacity="0.75">
            <Sun id={id} cx={48} cy={40} scale={0.72} />
          </g>
          <g stroke="#B8C4D2" strokeWidth="5" strokeLinecap="round">
            <line x1="18" y1="64" x2="62" y2="64" />
            <line x1="30" y1="74" x2="78" y2="74" />
            <line x1="22" y1="84" x2="56" y2="84" />
          </g>
        </>
      )}
      {glyph === "wind" && (
        <g fill="none" stroke="#C9D5E3" strokeWidth="5" strokeLinecap="round" filter={`url(#${id}-shade)`}>
          <path d="M14 40h44a10 10 0 1 0-10-10" />
          <path d="M14 54h58a10 10 0 1 1-10 10" />
          <path d="M22 68h22" />
        </g>
      )}
      {(glyph === "cloud" || glyph === "partly-day" || glyph === "partly-night" || lifted) && (
        <g transform={glyph === "cloud" ? "translate(0 2)" : lifted ? "translate(0 -10)" : "translate(-6 8)"} filter={`url(#${id}-shade)`} fill={cloudFill}>
          <circle cx="34" cy="56" r="14" />
          <circle cx="51" cy="45" r="20" />
          <circle cx="68" cy="56" r="14" />
          <rect x="20" y="54" width="62" height="20" rx="10" />
        </g>
      )}
      {glyph === "rain" && (
        <g fill={`url(#${id}-drop)`}>
          <Drop x={34} y={74} />
          <Drop x={50} y={80} />
          <Drop x={66} y={74} />
        </g>
      )}
      {glyph === "storm" && (
        <>
          <path d="M52 58 40 77h9l-5 15 17-22h-9l5-12z" fill="#FFC83D" stroke="#F2A10C" strokeWidth="1.5" strokeLinejoin="round" />
          <g fill={`url(#${id}-drop)`}>
            <Drop x={30} y={74} />
            <Drop x={70} y={76} />
          </g>
        </>
      )}
    </svg>
  );
}

function Sun({ id, cx, cy, scale }: { id: string; cx: number; cy: number; scale: number }) {
  const rays = Array.from({ length: 8 }, (_, index) => (index * Math.PI) / 4);
  return (
    <g transform={`translate(${cx} ${cy}) scale(${scale})`}>
      <g stroke="#FFC23A" strokeWidth="5" strokeLinecap="round">
        {rays.map((angle) => (
          <line key={angle} x1={Math.cos(angle) * 29} y1={Math.sin(angle) * 29} x2={Math.cos(angle) * 37} y2={Math.sin(angle) * 37} />
        ))}
      </g>
      <circle r="21" fill={`url(#${id}-sun)`} />
    </g>
  );
}

function Moon({ id, cx, cy, scale }: { id: string; cx: number; cy: number; scale: number }) {
  return (
    <g transform={`translate(${cx} ${cy}) scale(${scale})`}>
      <mask id={`${id}-crescent`}>
        <circle r="24" fill="#fff" />
        <circle cx="13" cy="-11" r="20" fill="#000" />
      </mask>
      <circle r="24" fill={`url(#${id}-moon)`} mask={`url(#${id}-crescent)`} />
    </g>
  );
}

function Drop({ x, y }: { x: number; y: number }) {
  return <path d={`M${x} ${y}c0 0-5 6.5-5 10a5 5 0 0 0 10 0c0-3.5-5-10-5-10z`} />;
}

// Line icons for the readings, drawn in the text colour around them.
export function ReadingIcon({ kind, className }: { kind: "wind" | "humidity" | "clock" | "umbrella" | "pin" | "refresh"; className?: string }) {
  const common = { fill: "none", stroke: "currentColor", strokeWidth: 1.8, strokeLinecap: "round" as const, strokeLinejoin: "round" as const };
  return (
    <svg viewBox="0 0 24 24" width="20" height="20" className={className} aria-hidden="true" focusable="false">
      {kind === "wind" && (
        <g {...common}>
          <path d="M3 9h11a3 3 0 1 0-3-3" />
          <path d="M3 13h15a3 3 0 1 1-3 3" />
          <path d="M3 17h6" />
        </g>
      )}
      {kind === "humidity" && <path {...common} d="M12 3.5s-6 6.6-6 10.8a6 6 0 0 0 12 0C18 10.1 12 3.5 12 3.5z" />}
      {kind === "clock" && (
        <g {...common}>
          <circle cx="12" cy="12" r="8.5" />
          <path d="M12 7.5V12l3 2" />
        </g>
      )}
      {kind === "umbrella" && (
        <g {...common}>
          <path d="M3.5 12a8.5 8.5 0 0 1 17 0z" />
          <path d="M12 12v6.5a2 2 0 0 1-4 0" />
          <path d="M12 3.5V2.5" />
        </g>
      )}
      {kind === "pin" && (
        <g {...common}>
          <path d="M12 21s-6.5-5.6-6.5-11a6.5 6.5 0 0 1 13 0c0 5.4-6.5 11-6.5 11z" />
          <circle cx="12" cy="10" r="2.3" />
        </g>
      )}
      {kind === "refresh" && (
        <g {...common}>
          <path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3" />
          <path d="M19.5 4.5v4h-4" />
        </g>
      )}
    </svg>
  );
}
