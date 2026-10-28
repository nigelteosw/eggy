// Whether a key press in the message box sends the message. On a keyboard
// Enter sends and Shift+Enter starts a new line. A touch keyboard has no Shift
// to give, so there Enter starts a new line and the send button is the way to
// send; otherwise a phone user could never write two lines.
export function enterSendsMessage(key: string, shiftKey: boolean, touchKeyboard: boolean): boolean {
  return key === "Enter" && !shiftKey && !touchKeyboard;
}

// Whether the primary input is a finger rather than a pointer. The media query
// is the one the platform offers for this; it is false where matchMedia is
// unavailable, which keeps desktop behaviour.
export function usesTouchKeyboard(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;
}
