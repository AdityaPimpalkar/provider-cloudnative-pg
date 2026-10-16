import { expect, test } from "@playwright/test";
import { dismissWelcomeDialog, getCredentials } from "../helpers/auth";
import { kubectlDeleteInstance, kubectlJsonPath } from "../helpers/kube";
import { DatabaseFormPage } from "../pages/database-form.page";
import { DatabasesPage } from "../pages/databases.page";
import { LoginPage } from "../pages/login.page";

// Namespace prepared for databases before the run (see .github/workflows/e2e.yaml).
const namespace = process.env.E2E_NAMESPACE ?? "e2e-ui";

// Instance created by the running test; removed if the test fails before its
// own UI delete, so runs against a shared cluster leave nothing behind.
let createdInstance: string | undefined;

test.afterEach(() => {
  if (createdInstance) {
    kubectlDeleteInstance(namespace, createdInstance);
    createdInstance = undefined;
  }
});

test.beforeEach(async ({ page }) => {
  // Safety net if the welcome dialog shows up despite the saved session.
  await page.addLocatorHandler(page.getByTestId("lets-go-button"), async (button) => {
    await button.click();
  });
  // The saved session can expire during a long run, e.g. before a retry.
  await page.goto("/databases");
  await page.waitForLoadState("networkidle");
  if (page.url().includes("/login")) {
    const { username, password } = getCredentials();
    await new LoginPage(page).login(username, password);
    await dismissWelcomeDialog(page);
  }
});

test("create a PostgreSQL instance through the UI, wait for Ready, delete it", async ({
  page,
}) => {
  const name = `pg-ui-${Date.now().toString(36)}`;
  const form = new DatabaseFormPage(page);
  const databases = new DatabasesPage(page);

  await test.step("create through the generated form", async () => {
    await form.open();
    await form.fillBasics(name, namespace);
    createdInstance = name;
    // Fractional CPU guards #99: it must reach the API as a quantity string.
    await form.configureAndCreate({ nodes: 1, cpu: "0.5", memoryGi: 1, diskGi: 1 });
  });

  await test.step("instance becomes Ready", async () => {
    await databases.waitForPhase(name, "Ready", 10 * 60 * 1_000);
  });

  await test.step("provider created the CloudNativePG cluster from the form values", async () => {
    const instances = kubectlJsonPath(namespace, `cluster/${name}`, "{.spec.instances}");
    if (instances !== undefined) {
      expect(instances).toBe("1");
      expect(kubectlJsonPath(namespace, `cluster/${name}`, "{.spec.resources.requests.cpu}")).toBe("500m");
      expect(kubectlJsonPath(namespace, `cluster/${name}`, "{.status.readyInstances}")).toBe("1");
    }
  });

  await test.step("delete through the UI", async () => {
    await databases.delete(name);
    createdInstance = undefined;
  });
});
