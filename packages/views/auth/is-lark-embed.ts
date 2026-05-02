"use client";

import { useEffect, useState } from "react";

// Velafi: detect when multica is running inside a Lark client webview.
//
// Lark desktop client (Lark / Feishu / TikTok-family for Lark) and the
// mobile client embed multica via a Chromium-based webview when launched
// from the workspace H5 / sidebar slot. The User-Agent contains "Lark"
// or "Feishu" in those contexts.
//
// We use this to suppress UI that would break the embed experience —
// e.g. the logout dropdown item, which would jump to the Authentik
// invalidation flow inside the webview and leave the user staring at
// Authentik's sign-in page (or a blank webview after IDP redirect).
// Logout in Lark embed mode is meaningless: closing the Lark app or
// signing out of Lark itself is the right action.

function detect(): boolean {
  if (typeof navigator === "undefined") return false;
  return /\b(Lark|Feishu|ByteDance)\b/i.test(navigator.userAgent);
}

// Plain function — synchronous, browser-only. Safe to call from
// non-React contexts (utilities, route handlers running in browser).
export function isLarkEmbed(): boolean {
  return detect();
}

// React hook — defers detection to useEffect so SSR / first render
// returns a stable value (false). After hydration, flips to true if
// the user agent matches. This avoids hydration mismatch warnings
// when Next.js renders the initial HTML on the server.
export function useIsLarkEmbed(): boolean {
  const [embed, setEmbed] = useState(false);
  useEffect(() => {
    setEmbed(detect());
  }, []);
  return embed;
}
