import { expect, test } from "bun:test";
import { Window } from "happy-dom";
import { createElement, act } from "react";
import { createRoot } from "react-dom/client";
import { ChatPage } from "../src/ChatPage";

test("reply waits for a settled selection and stays hidden throughout dragging", async () => {
  const window = new Window();
  const globals = { window, document: window.document, Element: window.Element, IS_REACT_ACT_ENVIRONMENT: true };
  const previous = new Map(Object.keys(globals).map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  for (const [key, value] of Object.entries(globals)) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  const reply = () => Array.from(container.querySelectorAll("button")).find(button => button.textContent?.trim() === "Reply");
  const settle = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 180)); });
  try {
    await act(async () => root.render(createElement(ChatPage, { threadId: null, title: "New chat", sidebarOpen: true, onSessionExpired() {} })));
    const paragraph = container.querySelector("p")!;
    const range = document.createRange();
    range.selectNodeContents(paragraph);
    Object.assign(range, { getClientRects: () => [{ top: 100, left: 40 }] });
    await act(async () => {
      paragraph.dispatchEvent(new window.Event("pointerdown", { bubbles: true }));
      document.getSelection()!.addRange(range);
      document.dispatchEvent(new window.Event("selectionchange"));
    });
    expect(reply()).toBeUndefined();
    await settle();
    expect(reply()).toBeUndefined();
    await act(async () => document.dispatchEvent(new window.Event("pointerup")));
    expect(reply()).toBeUndefined();
    await settle();
    expect(reply()).toBeDefined();
    expect(reply()!.className).not.toContain("animate-fade-in-up");
    await act(async () => document.getSelection()!.removeAllRanges());
    expect(reply()).toBeUndefined();
    await settle();
    expect(reply()).toBeUndefined();
    await act(async () => document.getSelection()!.addRange(range));
    await settle();
    const button = reply()!;
    await act(async () => {
      button.dispatchEvent(new window.Event("pointerdown", { bubbles: true }));
      button.dispatchEvent(new window.MouseEvent("mousedown", { bubbles: true, cancelable: true }));
      button.dispatchEvent(new window.Event("pointerup", { bubbles: true }));
      button.click();
    });
    expect(container.querySelector("textarea")?.placeholder).toBe("Reply to the quoted text...");
    expect(reply()).toBeUndefined();
  } finally {
    await act(async () => root.unmount());
    await window.happyDOM.close();
    for (const [key, descriptor] of previous) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else Reflect.deleteProperty(globalThis, key);
    }
  }
});
