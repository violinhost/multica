import { createStore } from "zustand/vanilla";
import { useStore } from "zustand";

interface ConfigState {
  cdnDomain: string;
  // True when cdnDomain serves private content via time-bounded signed URLs
  // (CloudFront signing enabled server-side). Renderers must not treat a raw
  // storage URL on that domain as a loadable media source (MUL-3254).
  cdnSigned: boolean;
  allowSignup: boolean;
  oidcIssuerURL: string;
  oidcClientID: string;
  oidcAuthorizationEndpoint: string;
  oidcEndSessionEndpoint: string;
  oidcRedirectURI: string;
  googleClientId: string;
  daemonServerUrl: string;
  daemonAppUrl: string;
  // Self-host gate (#3433): when true, every "Create workspace" affordance
  // must be hidden. Defaults to false so unknown / older servers behave like
  // the managed-cloud case.
  workspaceCreationDisabled: boolean;
  // upstream v0.4.31: VCS integration section visibility.
  vcsIntegrationAvailable: boolean;
  // upstream #7113: worktree save gate — absent on older servers.
  localWorktreeSupported: boolean;
  featureFlags: Record<string, boolean>;
  // The running API build version, surfaced in the Help popover so
  // self-hosted operators can confirm what's deployed. Empty for dev builds
  // or servers older than this feature.
  serverVersion: string;
  setCdnConfig: (config: { cdnDomain: string; cdnSigned?: boolean }) => void;
  setAuthConfig: (config: {
    allowSignup: boolean;
    oidcIssuerURL?: string;
    oidcClientID?: string;
    oidcAuthorizationEndpoint?: string;
    oidcEndSessionEndpoint?: string;
    oidcRedirectURI?: string;
    googleClientId?: string;
    workspaceCreationDisabled?: boolean;
    vcsIntegrationAvailable?: boolean;
  }) => void;
  setDaemonConfig: (config: {
    daemonServerUrl?: string;
    daemonAppUrl?: string;
  }) => void;
  setFeatureFlags: (flags?: Record<string, boolean>) => void;
  setServerVersion: (version?: string) => void;
  setLocalWorktreeSupported: (supported?: boolean) => void;
}

export const configStore = createStore<ConfigState>((set) => ({
  cdnDomain: "",
  cdnSigned: false,
  allowSignup: true,
  oidcIssuerURL: "",
  oidcClientID: "",
  oidcAuthorizationEndpoint: "",
  oidcEndSessionEndpoint: "",
  oidcRedirectURI: "",
  googleClientId: "",
  daemonServerUrl: "",
  daemonAppUrl: "",
  workspaceCreationDisabled: false,
  vcsIntegrationAvailable: false,
  localWorktreeSupported: false,
  featureFlags: {},
  serverVersion: "",
  setCdnConfig: ({ cdnDomain, cdnSigned = false }) => set({ cdnDomain, cdnSigned }),
  setAuthConfig: ({
    allowSignup,
    oidcIssuerURL = "",
    oidcClientID = "",
    oidcAuthorizationEndpoint = "",
    oidcEndSessionEndpoint = "",
    oidcRedirectURI = "",
    googleClientId = "",
    workspaceCreationDisabled = false,
    vcsIntegrationAvailable = false,
  }) =>
    set({
      allowSignup,
      oidcIssuerURL,
      oidcClientID,
      oidcAuthorizationEndpoint,
      oidcEndSessionEndpoint,
      oidcRedirectURI,
      googleClientId,
      workspaceCreationDisabled, vcsIntegrationAvailable,
    }),
  setDaemonConfig: ({ daemonServerUrl = "", daemonAppUrl = "" }) =>
    set({ daemonServerUrl, daemonAppUrl }),
  setFeatureFlags: (flags = {}) => set({ featureFlags: { ...flags } }),
  setServerVersion: (version = "") => set({ serverVersion: version }),
  // upstream #7113: absent on servers predating the worktree save gate,
  // which is exactly when the client must not offer the mode.
  setLocalWorktreeSupported: (supported?: boolean) =>
    set({ localWorktreeSupported: supported === true }),
}));

export function useConfigStore(): ConfigState;
export function useConfigStore<T>(selector: (state: ConfigState) => T): T;
export function useConfigStore<T>(selector?: (state: ConfigState) => T) {
  return useStore(configStore, selector as (state: ConfigState) => T);
}

export function featureFlagEnabled(
  flags: Readonly<Record<string, boolean>> | undefined,
  key: string,
  defaultValue = false,
): boolean {
  return flags?.[key] ?? defaultValue;
}

export function useFeatureEnabled(key: string, defaultValue = false): boolean {
  return useConfigStore((state) =>
    featureFlagEnabled(state.featureFlags, key, defaultValue),
  );
}
