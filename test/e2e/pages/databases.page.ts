import { expect, type Page } from "@playwright/test";

/**
 * Page Object for the OpenEverest instance list (/databases), its per-row
 * status and the delete action.
 */
export class DatabasesPage {
  constructor(private readonly page: Page) {}

  async open(): Promise<void> {
    await this.page.goto("/databases");
  }

  status(name: string) {
    return this.page.getByTestId(`${name}-status`);
  }

  /** Rendered once the list has loaded: provider tiles when empty, else the create button. */
  loaded() {
    return this.page
      .locator('[data-testid^="provider-tile-"]')
      .or(this.page.getByTestId("add-db-cluster-button"))
      .first();
  }

  /** Polls the list until the instance reports the given phase. */
  async waitForPhase(name: string, phase: string, timeoutMs: number): Promise<void> {
    await this.open();
    await expect(this.status(name)).toHaveText(phase, { timeout: timeoutMs });
  }

  async delete(name: string): Promise<void> {
    await this.open();
    const row = this.page.getByRole("row").filter({ has: this.status(name) });
    await row.getByTestId("actions-menu-button").click();
    await this.page.getByTestId(`${name}-delete`).click();

    const dialog = this.page.getByRole("dialog");
    await dialog.getByLabel("Name").fill(name);
    await dialog.getByTestId("form-dialog-delete").click();
    await expect(this.status(name)).toHaveCount(0, { timeout: 5 * 60 * 1_000 });
  }
}
