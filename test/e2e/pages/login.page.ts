import type { Page } from "@playwright/test";

/**
 * Page Object for the Everest login page.
 *
 * Encapsulates the login flow so that tests never deal with raw selectors for
 * the authentication form.
 */
export class LoginPage {
  constructor(private readonly page: Page) {}

  /** Navigate to the login page; filling the form waits for it to render. */
  async goto(): Promise<void> {
    await this.page.goto("/login");
  }

  /** Fill in the username field. */
  async fillUsername(username: string): Promise<void> {
    await this.page.getByLabel("Username").fill(username);
  }

  /** Fill in the password field. */
  async fillPassword(password: string): Promise<void> {
    await this.page.getByLabel("Password").fill(password);
  }

  /** Click the submit / login button. */
  async submit(): Promise<void> {
    await this.page.getByRole("button", { name: /log in|sign in/i }).click();
  }

  /**
   * Perform a full login and wait for navigation away from the login page.
   * This is the primary helper most tests should call.
   */
  async login(username: string, password: string): Promise<void> {
    await this.goto();
    await this.fillUsername(username);
    await this.fillPassword(password);
    await this.submit();

    // Wait until we've navigated away from the login page.
    await this.page.waitForURL((url) => !url.pathname.includes("/login"), {
      timeout: 30_000,
    });
  }
}
