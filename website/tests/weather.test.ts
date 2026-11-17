import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { WeatherReport } from "../src/lib/api";
import { WeatherView } from "../src/weather/WeatherCard";
import { glyphFor, isNightAt, outlookLabel, rangeBar, skyFor, umbrellaNote } from "../src/weather/weather";

// 2026-11-17 14:42 in Singapore.
const afternoon = new Date("2026-11-17T06:42:00Z");

const report: WeatherReport = {
  area: "Bedok",
  region: "east",
  fetched_at: "2026-11-17T06:40:00Z",
  now: { temperature: 31.6, humidity: 64.2, station: "East Coast Parkway", forecast: "Partly Cloudy (Day)", valid_text: "2 pm to 4 pm" },
  today: { forecast: "Thundery Showers", low: 25, high: 33, humidity_low: 60, humidity_high: 95, wind_low: 5, wind_high: 15, wind_direction: "VARIABLE" },
  periods: [
    { label: "Now", text: "Midday to 6 pm 17 Nov", start: "2026-11-17T12:00:00+08:00", end: "2026-11-17T18:00:00+08:00", forecast: "Partly Cloudy (Day)" },
    { label: "Tonight", text: "6 pm 17 Nov to 6 am 18 Nov", start: "2026-11-17T18:00:00+08:00", end: "2026-11-18T06:00:00+08:00", forecast: "Thundery Showers" },
  ],
  outlook: [
    { day: "Wednesday", date: "2026-11-18", forecast: "Thundery Showers", summary: "Afternoon thundery showers", low: 24, high: 33 },
    { day: "Thursday", date: "2026-11-19", forecast: "Fair (Day)", low: 26, high: 34 },
    { day: "Friday", date: "2026-11-20", forecast: "Showers", low: 24, high: 31 },
  ],
};

test("NEA's forecast words become one picture, with thunder ahead of rain ahead of wind", () => {
  expect(glyphFor("Heavy Thundery Showers with Gusty Winds", false)).toBe("storm");
  expect(glyphFor("Passing Showers", false)).toBe("rain");
  expect(glyphFor("Partly Cloudy (Night)", false)).toBe("partly-night");
  expect(glyphFor("Partly Cloudy (Day)", true)).toBe("partly-day");
  expect(glyphFor("Fair", true)).toBe("moon");
  expect(glyphFor("Hazy", false)).toBe("haze");
  expect(glyphFor("Cloudy", false)).toBe("cloud");
  expect(skyFor("storm", true)).toBe("storm");
  expect(skyFor("partly-night", true)).toBe("night");
  expect(skyFor("cloud", false)).toBe("overcast");
});

test("night is read in Singapore time, wherever the browser is", () => {
  expect(isNightAt(afternoon)).toBe(false);
  expect(isNightAt(new Date("2026-11-17T12:30:00Z"))).toBe(true); // 20:30 SGT
});

test("the umbrella note speaks only when rain is coming, and says when", () => {
  expect(umbrellaNote(report)).toBe("Thundery Showers tonight.");
  expect(umbrellaNote({ ...report, now: { forecast: "Light Rain", valid_text: "2 pm to 4 pm" } })).toBe("Light Rain in Bedok until 4 pm.");
  expect(umbrellaNote({ ...report, periods: [report.periods![0]] })).toBeNull();
});

test("tomorrow is called tomorrow, and range bars share one scale", () => {
  expect(outlookLabel(report.outlook![0], afternoon)).toBe("Tomorrow");
  expect(outlookLabel(report.outlook![1], afternoon)).toBe("Thursday");
  const bar = rangeBar(report.outlook![1], report.outlook!);
  expect(bar.left).toBe(0.2);
  expect(bar.width).toBe(0.8);
});

test("the card shows the reading, the day's range, the umbrella and the days ahead", () => {
  const html = renderToStaticMarkup(createElement(WeatherView, { report, now: afternoon, onRefresh: () => {} }));
  expect(html).toContain('data-sky="day"');
  expect(html).toContain('aria-label="32 degrees"');
  expect(html).toContain("H 33° · L 25°");
  expect(html).toContain("64%");
  expect(html).toContain("5–15 km/h");
  expect(html).toContain("Take an umbrella.");
  expect(html).toContain("Tomorrow");
  expect(html).toContain("Afternoon thundery showers");
  expect(html).toContain("Thursday");
  expect(html).toContain("East Coast Parkway station");
  expect(html).toContain('aria-label="Refresh the weather"');
});

test("a stale report says so instead of claiming to be live", () => {
  const html = renderToStaticMarkup(createElement(WeatherView, { report: { ...report, stale: true }, now: afternoon }));
  expect(html).toContain("data.gov.sg is not answering");
  expect(html).not.toContain("Updated");
});
