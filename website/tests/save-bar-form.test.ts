import { expect, test } from "bun:test";
import { Window } from "happy-dom";
import { createElement, act } from "react";
import { createRoot } from "react-dom/client";
import { SaveBar } from "../src/components/ui/save-bar";

// Without its own dirty flag the bar watches the form it sits in: an edit
// shows it, its button submits the form, and the submit hides it again.
test("a save bar inside a form appears on an edit and submits that form", async () => {
  const window = new Window();
  const globals = { window, document: window.document, Element: window.Element, IS_REACT_ACT_ENVIRONMENT: true };
  const previous = new Map(Object.keys(globals).map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  for (const [key, value] of Object.entries(globals)) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  let submitted = 0;
  const bar = () => Array.from(container.querySelectorAll("button")).find((button) => button.textContent === "Save provider");
  try {
    await act(async () =>
      root.render(
        createElement(
          "form",
          { onSubmit: (event: Event) => (event.preventDefault(), submitted++) },
          createElement("input", { name: "url" }),
          createElement(SaveBar, { label: "Save provider" }),
        ),
      ),
    );
    expect(bar()).toBeUndefined();
    await act(async () => {
      container.querySelector("input")!.dispatchEvent(new window.Event("input", { bubbles: true }));
    });
    expect(bar()?.getAttribute("type")).toBe("submit");
    await act(async () => {
      container.querySelector("form")!.dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(submitted).toBe(1);
    expect(bar()).toBeUndefined();
  } finally {
    await act(async () => root.unmount());
    for (const [key, descriptor] of previous) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else delete (globalThis as Record<string, unknown>)[key];
    }
  }
});
