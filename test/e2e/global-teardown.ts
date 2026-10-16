import fs from "node:fs";
import path from "node:path";

/**
 * Playwright global teardown.
 *
 * Clean up any persistent artifacts created during the test run (e.g. the
 * saved auth state).  Extend this function when you add test resources that
 * need cluster-level cleanup.
 */
async function globalTeardown(): Promise<void> {
  const authDir = path.join(__dirname, ".auth");
  if (fs.existsSync(authDir)) {
    fs.rmSync(authDir, { recursive: true, force: true });
  }
}

export default globalTeardown;
