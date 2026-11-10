import { VIEW_PATHS } from "../src/app/routing";
import { TAB_IDS } from "../src/customize/CustomizePage";

// auditPages is every screen a phone reaches by address: each rail view, and
// each Customize tab. Derived from the routes rather than listed, so a new
// screen is measured without anyone remembering to add it.
export function auditPages(): string[] {
  const views = Object.values(VIEW_PATHS);
  const tabs = TAB_IDS.filter((tab) => tab !== "skills").map((tab) => `${VIEW_PATHS.customize}?tab=${tab}`);
  return [...views, ...tabs];
}
