import { chromium } from "playwright";

const browser = await chromium.launch({ headless: true });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
const page = await ctx.newPage();

// --------------------------------------------------------------------------
// Test 1: redirect-only main path → /login should auto-redirect to Authentik
// --------------------------------------------------------------------------
console.log("=== Test 1: /login auto-redirects to Authentik ===");
const navP = page
  .waitForURL(/auth\.velafi\.ai/, { timeout: 15000 })
  .catch(() => null);
await page.goto("http://localhost:13000/login");
await navP;
const url = page.url();
const onAuthentik = /auth\.velafi\.ai/.test(url);
console.log(`Auto-redirected to Authentik: ${onAuthentik ? "YES" : "NO"}`);
if (!onAuthentik) {
  console.log(`Stayed on: ${url}`);
}

// --------------------------------------------------------------------------
// Test 2: backend email gate (LOGIN_METHODS=oidc → /auth/send-code 403)
// --------------------------------------------------------------------------
console.log("\n=== Test 2: backend /auth/send-code returns 403 ===");
const sendCodeResp = await page.request.post(
  "http://localhost:18080/auth/send-code",
  { data: { email: "test@velafi.com" } },
);
console.log(`POST /auth/send-code → ${sendCodeResp.status()}`);
const verifyResp = await page.request.post(
  "http://localhost:18080/auth/verify-code",
  { data: { email: "test@velafi.com", code: "000000" } },
);
console.log(`POST /auth/verify-code → ${verifyResp.status()}`);

// --------------------------------------------------------------------------
// Test 3: ?force=email rescue path renders email form
// --------------------------------------------------------------------------
console.log("\n=== Test 3: /login?force=email renders email form ===");
const page2 = await ctx.newPage();
await page2.goto("http://localhost:13000/login?force=email", {
  waitUntil: "networkidle",
});
await page2.waitForTimeout(800);
const hasEmailLabel = await page2.locator('label:has-text("Email")').count();
const hasContinue = await page2
  .locator('button:has-text("Continue")')
  .count();
console.log(`Email label rendered: ${hasEmailLabel === 1 ? "YES" : "NO"}`);
console.log(`Continue button rendered: ${hasContinue === 1 ? "YES" : "NO"}`);

// --------------------------------------------------------------------------
// Test 4: ?cli_callback no-session → redirects to OIDC with cli_callback in state
// --------------------------------------------------------------------------
console.log("\n=== Test 4: /login?cli_callback= no-session redirects to OIDC ===");
const page3 = await ctx.newPage();
const navP3 = page3
  .waitForURL(/auth\.velafi\.ai/, { timeout: 15000 })
  .catch(() => null);
await page3.goto(
  "http://localhost:13000/login?cli_callback=http://localhost:9876/cb&cli_state=test-csrf",
);
await navP3;
const u3 = page3.url();
const cliPreserved = u3.includes("cli_callback") && u3.includes("test-csrf");
console.log(
  `Redirected to Authentik with cli_callback preserved: ${
    /auth\.velafi\.ai/.test(u3) && cliPreserved ? "YES" : "NO"
  }`,
);

await browser.close();
console.log("\n=== Summary ===");
console.log(
  `Test 1 (auto-redirect): ${onAuthentik ? "✓" : "✗"}`,
);
console.log(
  `Test 2 (email gate 403): ${
    sendCodeResp.status() === 403 && verifyResp.status() === 403 ? "✓" : "✗"
  }`,
);
console.log(
  `Test 3 (force=email): ${
    hasEmailLabel === 1 && hasContinue === 1 ? "✓" : "✗"
  }`,
);
console.log(
  `Test 4 (cli redirect): ${
    /auth\.velafi\.ai/.test(u3) && cliPreserved ? "✓" : "✗"
  }`,
);
