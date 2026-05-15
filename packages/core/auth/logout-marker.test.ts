import { describe, expect, it, beforeEach, vi } from "vitest";
import {
  LOGOUT_IN_PROGRESS_KEY,
  clearLogoutInProgress,
  isLogoutInProgress,
  markLogoutInProgress,
} from "./logout-marker";

describe("logout-marker", () => {
  beforeEach(() => {
    vi.stubGlobal("window", {
      sessionStorage: {
        data: new Map<string, string>(),
        getItem(key: string) {
          return this.data.has(key) ? this.data.get(key)! : null;
        },
        setItem(key: string, value: string) {
          this.data.set(key, value);
        },
        removeItem(key: string) {
          this.data.delete(key);
        },
        clear() {
          this.data.clear();
        },
      },
    });
  });

  it("marks logout in progress and reports true before expiry", () => {
    markLogoutInProgress(1_000);
    expect(window.sessionStorage.getItem(LOGOUT_IN_PROGRESS_KEY)).toBe("16000");
    expect(isLogoutInProgress(15_999)).toBe(true);
  });

  it("expires stale markers and clears them", () => {
    window.sessionStorage.setItem(LOGOUT_IN_PROGRESS_KEY, "1000");
    expect(isLogoutInProgress(1_001)).toBe(false);
    expect(window.sessionStorage.getItem(LOGOUT_IN_PROGRESS_KEY)).toBeNull();
  });

  it("clear removes the marker", () => {
    markLogoutInProgress(1_000);
    clearLogoutInProgress();
    expect(isLogoutInProgress(1_500)).toBe(false);
  });
});
