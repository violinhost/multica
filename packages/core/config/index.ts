import { createStore } from "zustand/vanilla";
import { useStore } from "zustand";

interface ConfigState {
  cdnDomain: string;
  allowSignup: boolean;
  oidcIssuerURL: string;
  oidcClientID: string;
  oidcAuthorizationEndpoint: string;
  oidcEndSessionEndpoint: string;
  oidcRedirectURI: string;
  setCdnDomain: (domain: string) => void;
  setAuthConfig: (config: {
    allowSignup: boolean;
    oidcIssuerURL?: string;
    oidcClientID?: string;
    oidcAuthorizationEndpoint?: string;
    oidcEndSessionEndpoint?: string;
    oidcRedirectURI?: string;
  }) => void;
}

export const configStore = createStore<ConfigState>((set) => ({
  cdnDomain: "",
  allowSignup: true,
  oidcIssuerURL: "",
  oidcClientID: "",
  oidcAuthorizationEndpoint: "",
  oidcEndSessionEndpoint: "",
  oidcRedirectURI: "",
  setCdnDomain: (domain) => set({ cdnDomain: domain }),
  setAuthConfig: ({
    allowSignup,
    oidcIssuerURL = "",
    oidcClientID = "",
    oidcAuthorizationEndpoint = "",
    oidcEndSessionEndpoint = "",
    oidcRedirectURI = "",
  }) =>
    set({
      allowSignup,
      oidcIssuerURL,
      oidcClientID,
      oidcAuthorizationEndpoint,
      oidcEndSessionEndpoint,
      oidcRedirectURI,
    }),
}));

export function useConfigStore(): ConfigState;
export function useConfigStore<T>(selector: (state: ConfigState) => T): T;
export function useConfigStore<T>(selector?: (state: ConfigState) => T) {
  return useStore(configStore, selector as (state: ConfigState) => T);
}
