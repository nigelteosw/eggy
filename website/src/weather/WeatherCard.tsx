import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { MenuIcon } from "../components/ui/icons";
import { SessionExpiredError, getWeather, type WeatherOutlook, type WeatherReport } from "../lib/api";
import { timeAgo } from "../lib/time";
import { errorMessage } from "../lib/utils";
import { ReadingIcon, WeatherGlyph } from "./WeatherGlyph";
import {
  clockTime,
  dayLine,
  glyphFor,
  isNightAt,
  outlookLabel,
  periodIsNight,
  rangeBar,
  shown,
  skyFor,
  umbrellaNote,
  windText,
} from "./weather";

// Live weather on Home: what it is like now, whether to take an umbrella, and
// the next few days. The report is the deployment's, shared by every account,
// and the server caches it, so polling here costs upstream nothing extra.
const POLL_MS = 5 * 60 * 1000;

// Home's greeting and status line live in the card's top row, so the sky is
// the first thing on the page rather than something under a heading.
export type WeatherHeading = { title: string; description?: ReactNode; onOpenSidebar?: () => void };

export function WeatherCard({ onSessionExpired, heading }: { onSessionExpired: () => void; heading?: WeatherHeading }) {
  const [report, setReport] = useState<WeatherReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [now, setNow] = useState(() => new Date());
  const loadedAt = useRef(0);

  const load = useCallback(
    (refresh: boolean) => {
      setRefreshing(true);
      getWeather(refresh)
        .then((next) => {
          setReport(next);
          setError(null);
          loadedAt.current = Date.now();
        })
        .catch((err) => {
          if (err instanceof SessionExpiredError) return onSessionExpired();
          setError(errorMessage(err, "The weather could not load"));
        })
        .finally(() => {
          setRefreshing(false);
          setNow(new Date());
        });
    },
    [onSessionExpired],
  );

  useEffect(() => {
    load(false);
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") load(false);
    }, POLL_MS);
    // Coming back to a tab left open overnight shows this morning's sky, not
    // last night's.
    const onVisible = () => {
      if (document.visibilityState === "visible" && Date.now() - loadedAt.current > 2 * 60 * 1000) load(false);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [load]);

  if (!report) {
    return (
      <section aria-label="Weather" data-sky={isNightAt(now) ? "night" : "day"} className="weather-sky flex min-h-[148px] flex-col justify-between gap-6 rounded-card p-5">
        {heading && <Greeting heading={heading} />}
        {error ? (
          <p className="text-small" role="alert">
            {error}{" "}
            <button type="button" onClick={() => load(true)} className="font-semibold underline underline-offset-2">
              Try again
            </button>
          </p>
        ) : (
          <p className="weather-soft text-small">Looking at the sky…</p>
        )}
      </section>
    );
  }
  return <WeatherView report={report} now={now} refreshing={refreshing} onRefresh={() => load(true)} heading={heading} />;
}

// The presentational half, so the card can be rendered in a test without a server.
export function WeatherView({
  report,
  now,
  refreshing = false,
  onRefresh,
  heading,
}: {
  report: WeatherReport;
  now: Date;
  refreshing?: boolean;
  onRefresh?: () => void;
  heading?: WeatherHeading;
}) {
  const night = isNightAt(now);
  const current = report.now;
  const headline = current?.forecast || report.today?.forecast || "";
  const glyph = glyphFor(headline, night);
  const note = umbrellaNote(report);
  const temperature = current?.temperature;
  const outlook = report.outlook ?? [];
  const [next, ...later] = outlook;
  const refresh = onRefresh && (
    <button
      type="button"
      onClick={onRefresh}
      disabled={refreshing}
      aria-label="Refresh the weather"
      title="Refresh"
      className="weather-glass flex h-11 w-11 shrink-0 items-center justify-center rounded-full transition-transform active:scale-95 disabled:opacity-70"
    >
      <ReadingIcon kind="refresh" className={refreshing ? "motion-safe:animate-spin" : undefined} />
    </button>
  );

  return (
    <section aria-labelledby="home-weather" data-sky={skyFor(glyph, night)} className="weather-sky rounded-card p-3 sm:p-4 xl:p-5">
      {heading && (
        <div className="px-1 pb-4 pt-1 sm:pb-5">
          <Greeting heading={heading} action={refresh} />
        </div>
      )}
      <div className="grid gap-3 md:grid-cols-[minmax(0,1.08fr)_minmax(0,1fr)] md:gap-4 xl:gap-5">
        <div className="flex min-w-0 flex-col gap-3 px-1 pt-1">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <h2 id="home-weather" className="flex items-center gap-1 text-body font-semibold">
                <ReadingIcon kind="pin" className="h-4 w-4 shrink-0" />
                <span className="truncate">{report.area}</span>
              </h2>
              <p className="weather-soft mt-0.5 text-caption">
                {dayLine(now)} · {clockTime(now)}
              </p>
            </div>
            {!heading && refresh}
          </div>

          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0">
              <p className="weather-figure" aria-label={temperature === undefined ? "Temperature unavailable" : `${Math.round(temperature)} degrees`}>
                {temperature === undefined ? "–" : Math.round(temperature)}
                <span className="align-top">°</span>
              </p>
              {headline && <p className="mt-2 text-title font-semibold tracking-tight">{shown(headline)}</p>}
              {report.today && (
                <p className="weather-soft mt-0.5 text-small tabular-nums">
                  H {report.today.high}° · L {report.today.low}°
                </p>
              )}
            </div>
            <WeatherGlyph glyph={glyph} size={136} animated className="-mr-2 shrink-0 sm:h-[156px] sm:w-[156px] xl:h-[184px] xl:w-[184px]" />
          </div>

          <ul className="mt-auto grid grid-cols-3 gap-2" aria-label="Readings">
            <Reading
              icon="wind"
              label="Wind"
              value={report.today ? windText(report.today.wind_low, report.today.wind_high) : "–"}
              detail={report.today?.wind_direction && report.today.wind_direction !== "VARIABLE" ? report.today.wind_direction : "Variable"}
            />
            <Reading
              icon="humidity"
              label="Humidity"
              value={current?.humidity !== undefined ? `${Math.round(current.humidity)}%` : report.today ? `${report.today.humidity_low}–${report.today.humidity_high}%` : "–"}
              detail={current?.humidity !== undefined ? "Right now" : "Today"}
            />
            <Reading icon="clock" label="Next 2 hours" value={shown(current?.forecast) || "–"} detail={current?.valid_text} />
          </ul>

          {note && (
            <p className="weather-glass-strong flex items-start gap-2.5 rounded-control px-3 py-2.5 text-small" role="status">
              <ReadingIcon kind="umbrella" className="mt-px shrink-0" />
              <span>
                <span className="font-semibold">Take an umbrella.</span> <span className="weather-soft">{note}</span>
              </span>
            </p>
          )}
        </div>

        <div className="weather-glass-strong flex min-w-0 flex-col gap-4 rounded-card p-4">
          {report.periods && report.periods.length > 0 && (
            <div>
              <h3 className="text-small font-semibold">Today</h3>
              <ul className="mt-3 grid grid-cols-3 gap-2">
                {report.periods.slice(0, 3).map((period) => (
                  <li key={period.start} className="flex flex-col items-center gap-1.5 text-center" title={period.text}>
                    <span className="weather-glass flex h-14 w-14 items-center justify-center rounded-full">
                      <WeatherGlyph glyph={glyphFor(period.forecast, periodIsNight(period))} size={40} />
                    </span>
                    <span className="text-caption font-semibold">{period.label}</span>
                    <span className="weather-soft line-clamp-2 text-caption">{shown(period.forecast)}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {next && <NextDay day={next} label={outlookLabel(next, now)} />}

          {later.length > 0 && (
            <ul className="flex flex-col" aria-label="Coming days">
              {later.map((day) => (
                <OutlookRow key={day.date} day={day} days={outlook} />
              ))}
            </ul>
          )}

          {!report.periods?.length && !outlook.length && <p className="weather-soft text-small">The forecast is not available right now.</p>}
        </div>
      </div>

      <p className="weather-soft mt-3 px-1 text-caption">
        {report.stale ? `data.gov.sg is not answering; showing the reading from ${clockTime(new Date(report.fetched_at))}.` : `Updated ${timeAgo(report.fetched_at, now.getTime())}`}
        {current?.station ? ` · ${current.station} station` : ""} · data.gov.sg
      </p>
    </section>
  );
}

function Greeting({ heading, action }: { heading: WeatherHeading; action?: ReactNode }) {
  return (
    <header className="flex flex-col gap-1.5">
      <div className="flex min-h-11 items-start gap-2">
        {heading.onOpenSidebar && (
          <button
            type="button"
            onClick={heading.onOpenSidebar}
            aria-label="Open sidebar"
            title="Open sidebar"
            className="weather-glass -ml-1 flex h-11 w-11 shrink-0 items-center justify-center rounded-full md:hidden"
          >
            <MenuIcon />
          </button>
        )}
        <h1 className="min-w-0 flex-1 self-center font-voice text-display font-normal tracking-tight">{heading.title}</h1>
        {action}
      </div>
      {heading.description && <p className="weather-soft max-w-2xl text-small leading-6">{heading.description}</p>}
    </header>
  );
}

function Reading({ icon, label, value, detail }: { icon: "wind" | "humidity" | "clock"; label: string; value: string; detail?: string }) {
  return (
    <li className="weather-glass flex min-w-0 flex-col items-center gap-1 rounded-card px-1.5 py-3 text-center">
      <ReadingIcon kind={icon} className="weather-soft" />
      <span className="weather-soft text-caption">{label}</span>
      <span className="line-clamp-2 text-small font-semibold tabular-nums">{value}</span>
      {detail && <span className="weather-soft hidden truncate text-caption sm:block">{detail}</span>}
    </li>
  );
}

// Tomorrow gets the highlighted bar, as the reference apps give it: it is the
// day someone opening Eggy in the evening is actually planning.
function NextDay({ day, label }: { day: WeatherOutlook; label: string }) {
  return (
    <div className="weather-accent flex items-center gap-3 rounded-card py-2.5 pl-2.5 pr-4">
      <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-white/90">
        <WeatherGlyph glyph={glyphFor(day.forecast, false)} size={32} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-small font-semibold">{label}</span>
        <span className="line-clamp-2 block text-caption opacity-90">{day.summary || shown(day.forecast)}</span>
      </span>
      <span className="shrink-0 text-small font-semibold tabular-nums">
        <span aria-label={`high ${day.high} degrees`}>↑{day.high}°</span> <span aria-label={`low ${day.low} degrees`}>↓{day.low}°</span>
      </span>
    </div>
  );
}

function OutlookRow({ day, days }: { day: WeatherOutlook; days: WeatherOutlook[] }) {
  const bar = rangeBar(day, days);
  return (
    <li className="grid grid-cols-[4.75rem_32px_minmax(0,1fr)] items-center gap-2 py-1.5" title={day.summary || day.forecast}>
      <span className="text-small font-medium">{day.day}</span>
      <WeatherGlyph glyph={glyphFor(day.forecast, false)} size={30} />
      <span className="flex items-center gap-2 text-small tabular-nums">
        <span className="weather-soft w-7 text-right">{day.low}°</span>
        <span className="weather-bar-track relative h-1.5 flex-1 rounded-full">
          <span className="weather-bar absolute inset-y-0 rounded-full" style={{ left: `${bar.left * 100}%`, width: `${bar.width * 100}%` }} />
        </span>
        <span className="w-7 font-semibold">{day.high}°</span>
      </span>
    </li>
  );
}
