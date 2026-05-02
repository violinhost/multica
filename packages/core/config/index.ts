import { createStore } from "zustand/vanilla";
import { useStore } from "zustand";

interface ConfigState {
  cdnDomain: string;
  allowSignup: boolean;
  oidcIssuerURL: string;
  oidcClientID: string;
  oidcAuthorizationEndpoint: string;
  setCdnDomain: (domain: string) => void;
  setAuthConfig: (config: {
    allowSignup: boolean;
    oidcIssuerURL?: string;
    oidcClientID?: string;
    oidcAuthorizationEndpoint?: string;
  }) => void;
}

export const configStore = createStore<ConfigState>((set) => ({
  cdnDomain: "",
  allowSignup: true,
  oidcIssuerURL: "",
  oidcClientID: "",
  oidcAuthorizationEndpoint: "",
  setCdnDomain: (domain) => set({ cdnDomain: domain }),
  setAuthConfig: ({
    allowSignup,
    oidcIssuerURL = "",
    oidcClientID = "",
    oidcAuthorizationEndpoint = "",
  }) =>
    set({
      allowSignup,
      oidcIssuerURL,
      oidcClientID,
      oidcAuthorizationEndpoint,
    }),
}));

export function useConfigStore(): ConfigState;
export function useConfigStore<T>(selector: (state: ConfigState) => T): T;
export function useConfigStore<T>(selector?: (state: ConfigState) => T) {
  return useStore(configStore, selector as (state: ConfigState) => T);
}
