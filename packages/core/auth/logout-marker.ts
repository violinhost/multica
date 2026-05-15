export const LOGOUT_IN_PROGRESS_KEY = "multica_logout_in_progress";
const LOGOUT_IN_PROGRESS_TTL_MS = 15_000;

export function markLogoutInProgress(now = Date.now()): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      LOGOUT_IN_PROGRESS_KEY,
      String(now + LOGOUT_IN_PROGRESS_TTL_MS),
    );
  } catch {
    // Best effort only.
  }
}

export function clearLogoutInProgress(): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(LOGOUT_IN_PROGRESS_KEY);
  } catch {
    // Best effort only.
  }
}

export function isLogoutInProgress(now = Date.now()): boolean {
  if (typeof window === "undefined") return false;
  try {
    const raw = window.sessionStorage.getItem(LOGOUT_IN_PROGRESS_KEY);
    if (!raw) return false;
    const expiresAt = Number(raw);
    if (!Number.isFinite(expiresAt) || expiresAt <= now) {
      window.sessionStorage.removeItem(LOGOUT_IN_PROGRESS_KEY);
      return false;
    }
    return true;
  } catch {
    return false;
  }
}
