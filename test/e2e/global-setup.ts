import { chromium, type FullConfig } from "@playwright/test";
import { authenticateAndSave } from "./helpers/auth";

/**
 * Playwright global setup.
 *
 * Launches a throwaway browser, logs in to the Everest UI, and saves the
 * resulting browser storage state to `.auth/user.json`.  All subsequent test
 * workers reuse this state so they start already-authenticated.
 */
async function globalSetup(config: FullConfig): Promise<void> {
  const baseURL =
    config.projects[0]?.use?.baseURL ?? "http://localhost:8080";

  const browser = await chromium.launch();
  const context = await browser.newContext({ baseURL });
  const page = await context.newPage();

  try {
    await authenticateAndSave(page);
  } finally {
    await browser.close();
  }
}

export default globalSetup;
