import { expect, test } from "@playwright/test";
import { dismissWelcomeDialog, getCredentials } from "../helpers/auth";
import { kubectlDeleteInstance, kubectlEnabled, kubectlJsonPath } from "../helpers/kube";
import { DatabaseFormPage, type InstanceResources } from "../pages/database-form.page";
import { DatabasesPage } from "../pages/databases.page";
import { LoginPage } from "../pages/login.page";

// Namespace prepared for databases before the run (see .github/workflows/e2e.yaml).
const namespace = process.env.E2E_NAMESPACE ?? "e2e-ui";

// Every value differs from the topology defaults (3 nodes, CPU 1, memory 4Gi,
// disk 25Gi) and requests differ from limits, so each one shows that its field
// was submitted. The fractional CPU request guards #99: it must reach the API as
// a quantity string, which Kubernetes stores as 500m.
const resources: InstanceResources = {
  nodes: 1,
  cpuRequest: "0.5",
  cpuLimit: "2",
  memoryRequestGi: 1,
  memoryLimitGi: 2,
  diskGi: 1,
};

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
  const loginForm = page.getByLabel("Username");
  await expect(loginForm.or(new DatabasesPage(page).loaded()).first()).toBeVisible();
  if (await loginForm.isVisible()) {
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
    await form.configureAndCreate(resources);
  });

  await test.step("instance becomes Ready", async () => {
    await databases.waitForPhase(name, "Ready", 10 * 60 * 1_000);
  });

  // Reported as skipped rather than passed when kubectl is unavailable.
  const clusterStep = kubectlEnabled ? test.step : test.step.skip;
  await clusterStep("provider created the CloudNativePG cluster from the form values", async () => {
    const cluster = (jsonPath: string) => kubectlJsonPath(namespace, `cluster/${name}`, jsonPath);
    expect(cluster("{.spec.instances}")).toBe(String(resources.nodes));
    expect(cluster("{.spec.resources.requests.cpu}")).toBe("500m");
    expect(cluster("{.spec.resources.limits.cpu}")).toBe(resources.cpuLimit);
    expect(cluster("{.spec.resources.requests.memory}")).toBe(`${resources.memoryRequestGi}Gi`);
    expect(cluster("{.spec.resources.limits.memory}")).toBe(`${resources.memoryLimitGi}Gi`);
    expect(cluster("{.spec.storage.size}")).toBe(`${resources.diskGi}Gi`);
    expect(cluster("{.status.readyInstances}")).toBe(String(resources.nodes));
  });

  await test.step("delete through the UI", async () => {
    await databases.delete(name);
    createdInstance = undefined;
  });
});
