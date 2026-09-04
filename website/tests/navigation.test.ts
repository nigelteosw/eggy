import { expect, test } from "bun:test";
import { pathForView, viewForPath } from "../src/routing";

test("maps settings and traces to browser paths", () => {
  expect(pathForView("chat")).toBe("/");
  expect(pathForView("config")).toBe("/settings");
  expect(pathForView("traces")).toBe("/traces");
});

test("maps browser paths back to app views", () => {
  expect(viewForPath("/")).toBe("chat");
  expect(viewForPath("/settings")).toBe("config");
  expect(viewForPath("/settings/")).toBe("config");
  expect(viewForPath("/traces")).toBe("traces");
  expect(viewForPath("/unknown-page")).toBe("chat");
});
