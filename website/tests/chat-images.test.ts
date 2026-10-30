import { expect, test } from "bun:test";
import { withImages } from "../src/chat/images";

test("an image marker becomes a Markdown image served by the panel", () => {
  expect(withImages("Here is your cat.\n\n[image:0a1b2c]")).toBe("Here is your cat.\n\n![Generated image](/api/images/0a1b2c)");
});

// Only the panel's own hex ids are markers: text that merely looks similar is
// left as written, so a message cannot be made to point the browser elsewhere.
test("anything that is not a stored image id is left alone", () => {
  expect(withImages("[image generated, not kept]")).toBe("[image generated, not kept]");
  expect(withImages("[image:../../api/restart]")).toBe("[image:../../api/restart]");
});
