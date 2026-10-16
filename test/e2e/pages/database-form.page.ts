import { expect, type Page } from "@playwright/test";

/** Resource values small enough for a CI k3d cluster. */
export interface InstanceResources {
  nodes: number;
  cpu: number;
  memoryGi: number;
  diskGi: number;
}

/**
 * Page Object for the OpenEverest instance creation form (/databases/new).
 *
 * The first step holds the namespace and name. "Configure more options" walks
 * the provider's generated uiSchema sections, one step each. Generated fields
 * are rendered with their spec path as the input name, which gives selectors
 * that follow the provider's topology.yaml rather than display labels.
 */
export class DatabaseFormPage {
  constructor(private readonly page: Page) {}

  private field(path: string) {
    return this.page.locator(`input[name="${path}"]`);
  }

  async open(provider = "provider-cloudnative-pg"): Promise<void> {
    await this.page.goto("/databases");
    // With no instances the list shows provider tiles; otherwise the table
    // toolbar has a create button that opens the form directly when only one
    // provider is installed.
    await this.page
      .getByTestId(`provider-tile-${provider}`)
      .or(this.page.getByTestId("add-db-cluster-button"))
      .click();
    await this.page.waitForURL("**/databases/new");
    await expect(this.page.getByText("Create an instance")).toBeVisible();
  }

  async fillBasics(name: string, namespace: string): Promise<void> {
    const namespaceInput = this.page.getByRole("combobox", { name: "Namespace" });
    await namespaceInput.click();
    await namespaceInput.fill(namespace);
    await this.page.getByRole("option", { name: namespace, exact: true }).click();
    await this.field("dbName").fill(name);
  }

  /** Walks every generated step, setting resources, then submits. */
  async configureAndCreate(resources: InstanceResources): Promise<void> {
    await this.page.getByRole("button", { name: "Configure more options" }).click();

    const submit = this.page.getByTestId("db-wizard-submit-button");
    const next = this.page.getByTestId("db-wizard-continue-button");
    for (let step = 0; step < 15; step++) {
      await expect(submit.or(next)).toBeVisible();
      if (await this.field("spec.components.engine.replicas").isVisible()) {
        await this.setResources(resources);
      }
      if (await submit.isVisible()) {
        await expect(submit).toBeEnabled();
        await submit.click();
        return;
      }
      await expect(next).toBeEnabled();
      await next.click();
    }
    throw new Error("instance form did not reach its last step");
  }

  private async setResources(r: InstanceResources): Promise<void> {
    const values: Record<string, number> = {
      "spec.components.engine.replicas": r.nodes,
      "spec.components.engine.resources.requests.cpu": r.cpu,
      "spec.components.engine.resources.limits.cpu": r.cpu,
      "spec.components.engine.resources.requests.memory": r.memoryGi,
      "spec.components.engine.resources.limits.memory": r.memoryGi,
      "spec.components.engine.storage.size": r.diskGi,
    };
    for (const [path, value] of Object.entries(values)) {
      await this.field(path).fill(String(value));
    }
  }
}
